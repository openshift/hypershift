// Package etcdsanfix implements the `etcd-sanfix` break-glass subcommand: a guided,
// SRE-gated recovery for a hosted etcd cluster stuck on the peer-cert SAN bug. It
// installs a per-member-SAN peer cert (reusing the in-tree pki reconciler) so peer
// TLS recovers via hot-reload, re-admits the removed member, and drives a
// detect-and-retry loop, with a human gate at every state change. See the plan note
// hcp-etcd-sanfix plan.md for the full design and the honest invariants.
package etcdsanfix

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"go.etcd.io/etcd/api/v3/etcdserverpb"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type options struct {
	namespace          string
	etcdClientCertFile string
	etcdClientKeyFile  string
	etcdCAFile         string
	etcdEndpoint       string
	member             string
	hostedCluster      string
	pauseWindow        time.Duration
	maxRetries         int
	skipPause          bool
	skipBounce         bool
	skipUnpause        bool
	local              bool

	// populated at runtime by setupKube / etcdTLSFromSecrets
	kube       crclient.Client
	restConfig *rest.Config
	etcdTLS    *tls.Config
}

// NewCommand returns the etcd-sanfix parent command: the guided break-glass recovery
// (heal) plus the read-only inspection and before/after checks an SRE runs around it.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "etcd-sanfix",
		Short: "Break-glass tooling for a hosted etcd stuck on the peer-cert SAN bug",
		Long: "Break-glass tooling for a hosted etcd stuck on the peer-cert SAN bug (OCPBUGS-76530).\n" +
			"heal drives the guided, SRE-gated recovery; cert, health, preflight and verify are\n" +
			"read-only and safe to run any time. See hcp-etcd-sanfix plan.md.",
		SilenceUsage: true,
	}
	cmd.AddCommand(
		newHealCommand(),
		newCertCommand(),
		newHealthCommand(),
		newPreflightCommand(),
		newVerifyCommand(),
	)
	return cmd
}

// newClientOptions returns options pre-filled with the in-pod cert-file defaults that
// every subcommand shares.
func newClientOptions() *options {
	return &options{
		etcdClientCertFile: "/etc/etcd/tls/client/etcd-client.crt",
		etcdClientKeyFile:  "/etc/etcd/tls/client/etcd-client.key",
		etcdCAFile:         "/etc/etcd/tls/etcd-ca/ca.crt",
	}
}

// addNamespaceFlag registers --namespace and marks it required. Every subcommand needs it.
func addNamespaceFlag(cmd *cobra.Command, o *options) {
	cmd.Flags().StringVar(&o.namespace, "namespace", "", "hosted control plane namespace (required)")
	if err := cmd.MarkFlagRequired("namespace"); err != nil {
		panic(err)
	}
}

// addEtcdClientFlags registers the flags a subcommand needs to talk to the hosted etcd
// directly. The cert subcommand omits these, since it reads the secret via the kube API.
func addEtcdClientFlags(cmd *cobra.Command, o *options) {
	f := cmd.Flags()
	f.StringVar(&o.etcdClientCertFile, "etcd-client-cert", o.etcdClientCertFile, "etcd client cert file")
	f.StringVar(&o.etcdClientKeyFile, "etcd-client-key", o.etcdClientKeyFile, "etcd client key file")
	f.StringVar(&o.etcdCAFile, "etcd-ca-cert", o.etcdCAFile, "etcd CA cert file")
	f.StringVar(&o.etcdEndpoint, "etcd-endpoint", "", "override etcd endpoint (e.g. a port-forward); default https://etcd-client.<ns>.svc:2379")
	f.BoolVar(&o.local, "local", false, "force out-of-cluster mode: port-forward to etcd pods and read certs from secrets, even if in-pod cert files exist")
}

// newHealCommand runs the guided, SRE-gated recovery (the former root behaviour).
func newHealCommand() *cobra.Command {
	opts := newClientOptions()
	opts.pauseWindow = 2 * time.Hour
	opts.maxRetries = 5
	cmd := &cobra.Command{
		Use:          "heal",
		Short:        "Run the guided, SRE-gated recovery (pause, bridge cert, re-admit, unpause)",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
			defer cancel()
			return run(ctx, opts)
		},
	}
	addNamespaceFlag(cmd, opts)
	addEtcdClientFlags(cmd, opts)
	f := cmd.Flags()
	f.StringVar(&opts.member, "member", "", "stuck member name (default: auto-detect the missing one)")
	f.StringVar(&opts.hostedCluster, "hosted-cluster", "", "<namespace>/<name> of the HostedCluster to pause (required unless --skip-pause)")
	f.DurationVar(&opts.pauseWindow, "pause-window", opts.pauseWindow, "how long to pause reconciliation")
	f.IntVar(&opts.maxRetries, "max-retries", opts.maxRetries, "max detect-and-retry passes for the member re-admit")
	f.BoolVar(&opts.skipPause, "skip-pause", false, "skip pausing (you paused the HostedCluster by hand)")
	f.BoolVar(&opts.skipBounce, "skip-bounce", false, "skip deleting the stuck pod (you will bounce it by hand)")
	f.BoolVar(&opts.skipUnpause, "skip-unpause", false, "skip unpausing at the end")
	return cmd
}

func run(ctx context.Context, o *options) error {
	if err := o.setupKube(); err != nil {
		return err
	}
	c := o.kube
	in, out := os.Stdin, os.Stdout
	fmt.Fprintf(out, "etcd-sanfix: break-glass recovery for namespace %q (mode: %s)\n", o.namespace, o.modeLabel())

	// PHASE 0 - preflight
	members, err := o.memberList(ctx)
	if err != nil {
		return fmt.Errorf("preflight: etcd member list failed: %w", err)
	}
	stuck := o.member
	if stuck == "" {
		stuck = detectMissingMember(members)
	}
	renderStep(out, step{
		title: "PHASE 0 - preflight",
		observed: []string{
			fmt.Sprintf("registered members: %s", memberNames(members)),
			fmt.Sprintf("target stuck member: %q", stuck),
		},
		read:   fmt.Sprintf("%d registered members; will re-admit %q", len(members), stuck),
		verify: []string{o.etcdctlHint("member list -w table"), o.etcdctlHint("endpoint health --cluster -w table")},
	})
	if len(members) < 2 {
		return fmt.Errorf("abort: fewer than 2 members registered (%d); not a safe recovery state", len(members))
	}
	if stuck == "" {
		return fmt.Errorf("abort: could not determine the stuck member; pass --member")
	}

	// PHASE 1 - pause (skippable)
	if o.skipPause {
		fmt.Fprintln(out, "\nPHASE 1 - pause: skipped (--skip-pause). Ensure the HostedCluster is paused before continuing.")
	} else {
		hcNS, hcName, ok := splitNN(o.hostedCluster)
		if !ok {
			return fmt.Errorf("abort: --hosted-cluster <namespace>/<name> required to pause (or use --skip-pause)")
		}
		until := ptr.To(time.Now().Add(o.pauseWindow).UTC().Format(time.RFC3339))
		proceed, err := confirm(in, out, step{
			title:       "PHASE 1 - pause the HostedCluster",
			action:      fmt.Sprintf("set hostedcluster %s/%s spec.pausedUntil=%s", hcNS, hcName, *until),
			consequence: "the CPO stops reconciling; it will not revert the cert or roll etcd while paused.",
			verify:      []string{fmt.Sprintf("oc get hostedcluster %s -n %s -o jsonpath='{.spec.pausedUntil}'", hcName, hcNS)},
			token:       "PAUSE",
		})
		if err != nil {
			return err
		}
		if !proceed {
			return fmt.Errorf("aborted at pause")
		}
		if err := setHostedClusterPause(ctx, c, hcNS, hcName, until); err != nil {
			return fmt.Errorf("pause: %w", err)
		}
		fmt.Fprintln(out, "paused.")
	}

	// PHASE 2 - install the bridge cert (core)
	proceed, err := confirm(in, out, step{
		title:       "PHASE 2 - install the per-member-SAN bridge cert",
		action:      "mint + patch etcd-peer-tls (per-member SANs, signed by the etcd-signer CA) via the in-tree pki reconciler",
		consequence: "etcd-0/1 hot-reload the cert on new peer handshakes; no restart. Reverted by the CPO at unpause.",
		verify: []string{
			fmt.Sprintf("oc get secret etcd-peer-tls -n %s -o jsonpath='{.data.peer\\.crt}' | base64 -d | openssl x509 -noout -text | grep -A1 'Subject Alternative Name'", o.namespace),
		},
		token: "PATCH-CERT",
	})
	if err != nil {
		return err
	}
	if !proceed {
		return fmt.Errorf("aborted at cert")
	}
	if err := reconcilePeerCertBridge(ctx, c, o.namespace); err != nil {
		return fmt.Errorf("cert: %w", err)
	}
	fmt.Fprintln(out, "peer cert patched; waiting for the kubelet to propagate it and etcd to hot-reload...")

	// PHASE 3 - confirm peering recovered (observe-gated)
	fmt.Fprintln(out, "\nPHASE 3 - confirm peering recovered (observe)")
	if err := o.waitHealthy(ctx, out, len(members), 3*time.Minute); err != nil {
		return fmt.Errorf("hot-reload confirm: %w", err)
	}

	// PHASE 4 + 5 - re-admit + bounce + detect-and-retry
	if err := o.healMember(ctx, c, in, out, stuck); err != nil {
		return err
	}

	// PHASE 6 - confirm recovery
	fmt.Fprintln(out, "\nPHASE 6 - confirm all members healthy (observe)")
	if err := o.waitHealthy(ctx, out, len(members)+1, 5*time.Minute); err != nil {
		return fmt.Errorf("recovery confirm: %w", err)
	}
	fmt.Fprintln(out, "cluster healthy.")

	// PHASE 7 - unpause (skippable)
	if o.skipUnpause {
		fmt.Fprintln(out, "\nPHASE 7 - unpause: skipped. Clear spec.pausedUntil when ready; expect a one-time safe etcd roll.")
		return nil
	}
	hcNS, hcName, ok := splitNN(o.hostedCluster)
	if !ok {
		fmt.Fprintln(out, "\nPHASE 7 - unpause: no --hosted-cluster set; clear spec.pausedUntil by hand.")
		return nil
	}
	proceed, err = confirm(in, out, step{
		title:       "PHASE 7 - unpause the HostedCluster",
		action:      fmt.Sprintf("clear hostedcluster %s/%s spec.pausedUntil", hcNS, hcName),
		consequence: "the CPO resumes, reverts the bridge cert, and rolls etcd one pod at a time (safe at 3 healthy).",
		token:       "UNPAUSE",
	})
	if err != nil {
		return err
	}
	if !proceed {
		fmt.Fprintln(out, "left paused; clear spec.pausedUntil when ready.")
		return nil
	}
	if err := setHostedClusterPause(ctx, c, hcNS, hcName, nil); err != nil {
		return fmt.Errorf("unpause: %w", err)
	}
	fmt.Fprintln(out, "unpaused. Watch the one-at-a-time etcd roll complete.")
	return nil
}

// healMember runs phase 4 (re-admit) and phase 5 (bounce), in a detect-and-retry loop,
// because the reset-member script's own re-add can flap and re-arm the crash loop.
func (o *options) healMember(ctx context.Context, c crclient.Client, in io.Reader, out io.Writer, stuck string) error {
	for attempt := 1; attempt <= o.maxRetries; attempt++ {
		members, err := o.memberList(ctx)
		if err != nil {
			return err
		}

		if !hasMember(members, stuck) {
			proceed, err := confirm(in, out, step{
				title:       fmt.Sprintf("PHASE 4 - re-admit %s (attempt %d/%d)", stuck, attempt, o.maxRetries),
				action:      fmt.Sprintf("etcdctl member add %s", stuck),
				consequence: "adds the member as unstarted (the trigger for reset-member); quorum unchanged.",
				verify:      []string{o.etcdctlHint("member list -w table")},
				token:       "MEMBER-ADD",
			})
			if err != nil {
				return err
			}
			if !proceed {
				return fmt.Errorf("aborted at member add")
			}
			id, err := o.memberAdd(ctx, stuck)
			if err != nil {
				fmt.Fprintf(out, "  member add refused (%v); waiting for peering to settle, then retrying\n", err)
				if werr := o.waitHealthy(ctx, out, len(members), 3*time.Minute); werr != nil {
					return werr
				}
				continue
			}
			fmt.Fprintf(out, "  added %s as member %x\n", stuck, id)
		} else {
			fmt.Fprintf(out, "  %s already registered; proceeding to bounce\n", stuck)
		}

		if o.skipBounce {
			fmt.Fprintf(out, "\nPHASE 5 - bounce: skipped. Delete pod %s -n %s by hand, then confirm to re-check.\n", stuck, o.namespace)
			if _, err := confirm(in, out, step{title: "waiting for you to bounce the pod", token: "DONE"}); err != nil {
				return err
			}
		} else {
			proceed, err := confirm(in, out, step{
				title:       fmt.Sprintf("PHASE 5 - bounce %s", stuck),
				action:      fmt.Sprintf("oc delete pod %s -n %s", stuck, o.namespace),
				consequence: "reset-member re-runs: removes + re-adds the member and sets marker=existing, so it boots existing.",
				token:       "BOUNCE",
			})
			if err != nil {
				return err
			}
			if !proceed {
				return fmt.Errorf("aborted at bounce")
			}
			if err := deletePod(ctx, c, o.namespace, stuck); err != nil {
				return fmt.Errorf("delete pod: %w", err)
			}
		}

		fmt.Fprintf(out, "  waiting to see if %s rejoins and syncs...\n", stuck)
		if o.memberHealedWithin(ctx, stuck, 3*time.Minute) {
			fmt.Fprintf(out, "  %s rejoined and is healthy\n", stuck)
			return nil
		}
		fmt.Fprintf(out, "  %s did not heal (likely the reset-member re-add flapped); retrying\n", stuck)
	}
	return fmt.Errorf("member %s did not heal after %d attempts; cluster left paused, investigate", stuck, o.maxRetries)
}

// waitHealthy polls until at least want members are registered and all of them report
// a healthy endpoint, or the deadline passes.
func (o *options) waitHealthy(ctx context.Context, out io.Writer, want int, d time.Duration) error {
	deadline := time.Now().Add(d)
	for {
		members, err := o.memberList(ctx)
		if err == nil {
			healthy := 0
			for _, m := range members {
				if m.Name != "" && o.endpointHealthy(ctx, m.Name) {
					healthy++
				}
			}
			fmt.Fprintf(out, "  members=%d healthy=%d (want %d)\n", len(members), healthy, want)
			if len(members) >= want && healthy >= want {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("did not reach %d healthy members within %s", want, d)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Second):
		}
	}
}

func (o *options) memberHealedWithin(ctx context.Context, stuck string, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for {
		members, err := o.memberList(ctx)
		if err == nil {
			for _, m := range members {
				if m.Name == stuck && o.endpointHealthy(ctx, stuck) {
					return true
				}
			}
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(15 * time.Second):
		}
	}
}

func (o *options) etcdctlHint(args string) string {
	return fmt.Sprintf("etcdctl --cacert=%s --cert=%s --key=%s --endpoints=https://etcd-client.%s.svc:2379 %s",
		o.etcdCAFile, o.etcdClientCertFile, o.etcdClientKeyFile, o.namespace, args)
}

func detectMissingMember(members []*etcdserverpb.Member) string {
	present := sets.New[string]()
	for _, m := range members {
		present.Insert(m.Name)
	}
	for _, n := range []string{"etcd-0", "etcd-1", "etcd-2"} {
		if !present.Has(n) {
			return n
		}
	}
	return ""
}

func hasMember(members []*etcdserverpb.Member, name string) bool {
	for _, m := range members {
		if m.Name == name {
			return true
		}
		// A just-added, unstarted member has an empty Name but its peer URL carries the name.
		for _, pu := range m.PeerURLs {
			if strings.Contains(pu, name+".etcd-discovery.") {
				return true
			}
		}
	}
	return false
}

func memberNames(members []*etcdserverpb.Member) string {
	names := make([]string, 0, len(members))
	for _, m := range members {
		n := m.Name
		if n == "" {
			n = fmt.Sprintf("(unstarted %x)", m.ID)
		}
		names = append(names, n)
	}
	return strings.Join(names, ", ")
}

func splitNN(s string) (ns, name string, ok bool) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}
