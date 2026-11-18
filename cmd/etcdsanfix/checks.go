package etcdsanfix

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// check is one line of a preflight/verify report.
type check struct {
	name   string
	status string // PASS, WARN, FAIL, INFO
	detail string
}

func newPreflightCommand() *cobra.Command {
	opts := newClientOptions()
	cmd := &cobra.Command{
		Use:          "preflight",
		Short:        "Read-only before-checks: confirm the SAN-bug situation and that healing is safe",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
			defer cancel()
			if err := opts.setupKube(); err != nil {
				return err
			}
			return runPreflight(ctx, opts.kube, os.Stdout, opts)
		},
	}
	addNamespaceFlag(cmd, opts)
	addEtcdClientFlags(cmd, opts)
	cmd.Flags().StringVar(&opts.member, "member", "", "expected stuck member (default: auto-detect the missing one)")
	return cmd
}

func newVerifyCommand() *cobra.Command {
	opts := newClientOptions()
	cmd := &cobra.Command{
		Use:          "verify",
		Short:        "Read-only after-checks: confirm all members are back, healthy, and fault-tolerant",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
			defer cancel()
			if err := opts.setupKube(); err != nil {
				return err
			}
			return runVerify(ctx, opts.kube, os.Stdout, opts)
		},
	}
	addNamespaceFlag(cmd, opts)
	addEtcdClientFlags(cmd, opts)
	return cmd
}

func runPreflight(ctx context.Context, c crclient.Client, out io.Writer, o *options) error {
	var checks []check
	fmt.Fprintf(out, "mode: %s\n", o.modeLabel())

	hs, alarms, err := o.gatherHealth(ctx)
	if err != nil {
		checks = append(checks, check{"etcd reachable", "FAIL", err.Error()})
		return reportChecks(out, "preflight", checks, "NO-GO")
	}
	checks = append(checks, check{"etcd reachable", "PASS", fmt.Sprintf("%d members registered", len(hs))})

	healthyVoters := 0
	for _, m := range hs {
		if m.healthy && !m.isLearner {
			healthyVoters++
		}
	}
	if healthyVoters >= 2 {
		checks = append(checks, check{"quorum safe (>=2 healthy voters)", "PASS", fmt.Sprintf("%d healthy voters", healthyVoters)})
	} else {
		checks = append(checks, check{"quorum safe (>=2 healthy voters)", "FAIL", fmt.Sprintf("only %d healthy voters; healing is unsafe", healthyVoters)})
	}

	stuck := o.member
	if stuck == "" {
		members, _ := o.memberList(ctx)
		stuck = detectMissingMember(members)
	}
	if stuck != "" {
		checks = append(checks, check{"a member needs re-admit", "PASS", fmt.Sprintf("target: %s", stuck)})
	} else {
		checks = append(checks, check{"a member needs re-admit", "WARN", "all of etcd-0/1/2 are registered; maybe nothing to heal"})
	}

	if cert, cerr := loadPeerCert(ctx, c, o.namespace); cerr != nil {
		checks = append(checks, check{"peer cert readable", "FAIL", cerr.Error()})
	} else {
		status, detail := certVerdict(classifyCert(cert, o.namespace, expectedReplicas(ctx, c, o.namespace)))
		switch status {
		case "WILDCARD-ONLY":
			checks = append(checks, check{"peer cert is the bug (wildcard-only)", "PASS", detail})
		case "BRIDGED":
			checks = append(checks, check{"peer cert already bridged", "WARN", "cert already has per-member SANs; heal will re-apply it idempotently"})
		default:
			checks = append(checks, check{"peer cert SANs", "WARN", detail})
		}
	}

	checks = append(checks, check{"peer-TLS rejection logs", "INFO",
		fmt.Sprintf("confirm by hand: oc logs -n %s etcd-0 -c etcd | grep 'does not match any of DNSNames'", o.namespace)})

	if len(alarms) > 0 {
		checks = append(checks, check{"no etcd alarms", "WARN", fmt.Sprintf("%d alarm(s) present; investigate before healing", len(alarms))})
	}

	return reportChecks(out, "preflight", checks, verdict(checks, "GO", "NO-GO"))
}

func runVerify(ctx context.Context, c crclient.Client, out io.Writer, o *options) error {
	var checks []check
	fmt.Fprintf(out, "mode: %s\n", o.modeLabel())
	replicas := expectedReplicas(ctx, c, o.namespace)

	hs, alarms, err := o.gatherHealth(ctx)
	if err != nil {
		checks = append(checks, check{"etcd reachable", "FAIL", err.Error()})
		return reportChecks(out, "verify", checks, "NOT-RECOVERED")
	}

	started, healthy, learners := 0, 0, 0
	for _, m := range hs {
		if m.started {
			started++
		}
		if m.healthy {
			healthy++
		}
		if m.isLearner {
			learners++
		}
	}

	checks = append(checks, boolCheck("all members registered", int32(len(hs)) >= replicas,
		fmt.Sprintf("%d of %d", len(hs), replicas)))
	checks = append(checks, boolCheck("all members started", int32(started) >= replicas,
		fmt.Sprintf("%d of %d started", started, replicas)))
	checks = append(checks, boolCheck("all members healthy", int32(healthy) >= replicas,
		fmt.Sprintf("%d of %d healthy", healthy, replicas)))

	if learners == 0 {
		checks = append(checks, check{"no learners pending promotion", "PASS", ""})
	} else {
		checks = append(checks, check{"no learners pending promotion", "WARN", fmt.Sprintf("%d learner(s) still promoting", learners)})
	}

	quorum := len(hs)/2 + 1
	checks = append(checks, boolCheck("fault tolerant", healthy > quorum,
		fmt.Sprintf("healthy=%d quorum=%d", healthy, quorum)))
	checks = append(checks, boolCheck("no etcd alarms", len(alarms) == 0,
		fmt.Sprintf("%d alarm(s)", len(alarms))))

	return reportChecks(out, "verify", checks, verdict(checks, "RECOVERED", "NOT-RECOVERED"))
}

func boolCheck(name string, ok bool, detail string) check {
	if ok {
		return check{name, "PASS", detail}
	}
	return check{name, "FAIL", detail}
}

// verdict is pass unless any check FAILed.
func verdict(checks []check, pass, fail string) string {
	for _, ch := range checks {
		if ch.status == "FAIL" {
			return fail
		}
	}
	return pass
}

// reportChecks prints the checklist and verdict, and returns a non-zero error for a
// failing verdict so the subcommand can gate a script.
func reportChecks(out io.Writer, title string, checks []check, result string) error {
	fmt.Fprintf(out, "\n=== %s ===\n", title)
	for _, ch := range checks {
		fmt.Fprintf(out, "[%-5s] %s\n", ch.status, ch.name)
		if ch.detail != "" {
			fmt.Fprintf(out, "         %s\n", ch.detail)
		}
	}
	fmt.Fprintf(out, "\nVERDICT: %s\n", result)
	if result == "NO-GO" || result == "NOT-RECOVERED" {
		return fmt.Errorf("%s: %s", title, result)
	}
	return nil
}
