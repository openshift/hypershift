package etcdsanfix

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/pki"
	etcdv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/etcd"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/util/sets"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func newCertCommand() *cobra.Command {
	opts := newClientOptions()
	cmd := &cobra.Command{
		Use:          "cert",
		Short:        "Inspect etcd-peer-tls: SANs, expiry, and whether it is wildcard-only or bridged",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
			defer cancel()
			if err := opts.setupKube(); err != nil {
				return err
			}
			return inspectPeerCert(ctx, opts.kube, os.Stdout, opts.namespace)
		},
	}
	addNamespaceFlag(cmd, opts)
	return cmd
}

// certReport is what we can tell about the etcd-peer-tls cert from the management cluster.
type certReport struct {
	subject          string
	issuer           string
	notBefore        time.Time
	notAfter         time.Time
	dnsNames         []string
	ipAddresses      []string
	wildcards        []string
	perMember        []string
	expectReplicas   int32
	missingPerMember []string
}

// inspectPeerCert loads etcd-peer-tls, parses peer.crt, and classifies it against the
// per-member SAN shape the #10161 fix expects for this namespace.
func inspectPeerCert(ctx context.Context, c crclient.Client, out io.Writer, namespace string) error {
	cert, err := loadPeerCert(ctx, c, namespace)
	if err != nil {
		return err
	}
	replicas := expectedReplicas(ctx, c, namespace)
	renderCertReport(out, classifyCert(cert, namespace, replicas))
	return nil
}

// loadPeerCert fetches etcd-peer-tls and parses its peer.crt into an x509 certificate.
func loadPeerCert(ctx context.Context, c crclient.Client, namespace string) (*x509.Certificate, error) {
	secret := manifests.EtcdPeerSecret(namespace)
	if err := c.Get(ctx, crclient.ObjectKeyFromObject(secret), secret); err != nil {
		return nil, fmt.Errorf("get secret %s/%s: %w", namespace, secret.Name, err)
	}
	raw, ok := secret.Data[pki.EtcdPeerCrtKey]
	if !ok || len(raw) == 0 {
		return nil, fmt.Errorf("secret %s has no %q", secret.Name, pki.EtcdPeerCrtKey)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("secret %s %q is not PEM-encoded", secret.Name, pki.EtcdPeerCrtKey)
	}
	return x509.ParseCertificate(block.Bytes)
}

// expectedReplicas resolves the etcd member count from the HCP, falling back to 3 when
// the HCP cannot be read (the cert report is still useful without it).
func expectedReplicas(ctx context.Context, c crclient.Client, namespace string) int32 {
	if hcp, err := getHCP(ctx, c, namespace); err == nil {
		return etcdv2.Replicas(hcp)
	}
	return 3
}

func classifyCert(cert *x509.Certificate, namespace string, replicas int32) certReport {
	rep := certReport{
		subject:        cert.Subject.String(),
		issuer:         cert.Issuer.String(),
		notBefore:      cert.NotBefore,
		notAfter:       cert.NotAfter,
		expectReplicas: replicas,
	}
	rep.dnsNames = append(rep.dnsNames, cert.DNSNames...)
	sort.Strings(rep.dnsNames)
	for _, ip := range cert.IPAddresses {
		rep.ipAddresses = append(rep.ipAddresses, ip.String())
	}
	present := sets.New[string]()
	for _, dns := range cert.DNSNames {
		present.Insert(dns)
		switch {
		case strings.HasPrefix(dns, "*."):
			rep.wildcards = append(rep.wildcards, dns)
		case strings.HasPrefix(dns, "etcd-") && strings.Contains(dns, ".etcd-discovery."):
			rep.perMember = append(rep.perMember, dns)
		}
	}
	sort.Strings(rep.wildcards)
	sort.Strings(rep.perMember)
	// The #10161 fix adds etcd-<i>.etcd-discovery.<ns>.svc and .svc.cluster.local per member.
	for i := int32(0); i < replicas; i++ {
		for _, suffix := range []string{".svc", ".svc.cluster.local"} {
			want := fmt.Sprintf("etcd-%d.etcd-discovery.%s%s", i, namespace, suffix)
			if !present.Has(want) {
				rep.missingPerMember = append(rep.missingPerMember, want)
			}
		}
	}
	return rep
}

// certVerdict classifies the cert for the SAN bug.
func certVerdict(rep certReport) (status, detail string) {
	switch {
	case len(rep.missingPerMember) == 0 && len(rep.perMember) > 0:
		return "BRIDGED", "has the per-member etcd-<i>.etcd-discovery SANs, so isHostInDNS can admit a rejoining peer via its forward-lookup fallback."
	case len(rep.perMember) == 0:
		return "WILDCARD-ONLY", "only wildcard SANs. Vulnerable to OCPBUGS-76530: a removed member cannot rejoin. Run `etcd-sanfix heal` to install the bridge cert."
	default:
		return "PARTIAL", fmt.Sprintf("missing %d expected per-member SAN(s): %s", len(rep.missingPerMember), strings.Join(rep.missingPerMember, ", "))
	}
}

func renderCertReport(out io.Writer, rep certReport) {
	status, detail := certVerdict(rep)
	fmt.Fprintf(out, "\n=== etcd-peer-tls ===\n")
	fmt.Fprintf(out, "Subject:  %s\n", rep.subject)
	fmt.Fprintf(out, "Issuer:   %s\n", rep.issuer)
	fmt.Fprintf(out, "Validity: %s  ->  %s\n", rep.notBefore.Format(time.RFC3339), rep.notAfter.Format(time.RFC3339))
	now := time.Now()
	if now.After(rep.notAfter) {
		fmt.Fprintf(out, "          EXPIRED %s ago\n", durShort(now.Sub(rep.notAfter)))
	} else {
		fmt.Fprintf(out, "          valid for %s\n", durShort(rep.notAfter.Sub(now)))
	}
	fmt.Fprintf(out, "DNS SANs (%d):\n", len(rep.dnsNames))
	for _, d := range rep.dnsNames {
		fmt.Fprintf(out, "  %s\n", d)
	}
	if len(rep.ipAddresses) > 0 {
		fmt.Fprintf(out, "IP SANs: %s\n", strings.Join(rep.ipAddresses, ", "))
	}
	if len(rep.missingPerMember) == 0 {
		fmt.Fprintf(out, "Per-member SANs for %d replicas: all present\n", rep.expectReplicas)
	} else {
		fmt.Fprintf(out, "Per-member SANs for %d replicas: missing %s\n", rep.expectReplicas, strings.Join(rep.missingPerMember, ", "))
	}
	fmt.Fprintf(out, "\nVERDICT: %s\n  %s\n", status, detail)
}

func durShort(d time.Duration) string {
	d = d.Round(time.Hour)
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	if days > 0 {
		return fmt.Sprintf("%dd%dh", days, hours)
	}
	return fmt.Sprintf("%dh", hours)
}
