package manifests

import (
	"testing"

	. "github.com/onsi/gomega"
)

// TestGMPCollectorSecretNames ties the collector's Secret access to the
// constructors of the Secrets the staged PodMonitorings reference. The generator
// test TestGeneratedTLSSecretsHaveProducers allows a staged TLS reference to
// name only these Secrets, so the Role grants access to every reference that
// can pass it, and never to a CA signer Secret that holds a private key.
//
//nolint:hypershiftlinter // testfuncstructure: GMPCollectorSecretNames is a list, not a function
func TestGMPCollectorSecretNames(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)
	const ns = "hcp-ns"

	g.Expect(GMPCollectorSecretNames).To(ConsistOf(
		RootCACertSecret(ns).Name,
		EtcdCACertSecret(ns).Name,
		MetricsClientCertSecret(ns).Name,
		EtcdMetricsClientSecret(ns).Name,
	))
	g.Expect(GMPCollectorSecretNames).ToNot(ContainElements(
		RootCASecret(ns).Name,
		EtcdSignerSecret(ns).Name,
	), "signer Secrets hold CA private keys")
}
