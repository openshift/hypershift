//go:build e2ev2

/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package tests

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"

	cpomanifests "github.com/openshift/hypershift/pkg/manifests/cpo"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	"k8s.io/utils/ptr"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// RegisterEtcdPeerCertTests registers all etcd peer-cert SAN test cases.
func RegisterEtcdPeerCertTests(getTestCtx internal.TestContextGetter) {
	EtcdPeerCertExactSANsTest(getTestCtx)
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:EtcdResilience] Etcd Peer Cert",
	Label("etcd-peer-cert", internal.InformingLabel), func() {
		var testCtx *internal.TestContext

		BeforeEach(func() {
			testCtx = internal.GetTestContext()
			Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
		})

		RegisterEtcdPeerCertTests(func() *internal.TestContext { return testCtx })
	})

// EtcdPeerCertExactSANsTest verifies the etcd peer certificate carries an EXACT
// per-member discovery SAN (etcd-<ordinal>.etcd-discovery.<ns>.svc and
// .svc.cluster.local) for every member ordinal, in addition to the wildcard
// SANs. These exact names are what makes etcd's peer-TLS forward-lookup fallback
// (client/pkg/transport/listener_tls.go isHostInDNS) accept a peer when reverse
// DNS of the dialer's pod IP returns the ambiguous etcd-client PTR first; the
// fallback iterates only exact (non-wildcard) SANs, so wildcard-only certs leave
// it inert and peers intermittently reject each other (OCPBUGS-76530).
//
// It also asserts loopback lives in the cert's IP SANs, not its DNS SANs
// (x509-correct placement).
func EtcdPeerCertExactSANsTest(getTestCtx internal.TestContextGetter) {
	It("should include exact per-member discovery SANs on the peer certificate", func() {
		testCtx := getTestCtx()
		ctx := testCtx.Context
		cpNamespace := testCtx.ControlPlaneNamespace

		// Derive the member count from the live etcd StatefulSet so the
		// assertion tracks SingleReplica (1) and HighlyAvailable (3) alike.
		etcdSts, _ := getEtcdStsAndPods(ctx, testCtx.MgmtClient, cpNamespace)
		replicas := ptr.Deref(etcdSts.Spec.Replicas, 0)
		Expect(replicas).To(BeNumerically(">", 0), "etcd StatefulSet should have a positive replica count")

		cert := getEtcdPeerCert(Default, ctx, testCtx.MgmtClient, cpNamespace)

		// Wildcard SANs remain (base).
		Expect(cert.DNSNames).To(ContainElements(
			fmt.Sprintf("*.etcd-discovery.%s.svc", cpNamespace),
			fmt.Sprintf("*.etcd-discovery.%s.svc.cluster.local", cpNamespace),
		), "peer cert should retain the wildcard discovery SANs")

		// Exact per-member SANs for every live ordinal (the fix).
		var wantExact []interface{}
		for i := int32(0); i < replicas; i++ {
			wantExact = append(wantExact,
				fmt.Sprintf("etcd-%d.etcd-discovery.%s.svc", i, cpNamespace),
				fmt.Sprintf("etcd-%d.etcd-discovery.%s.svc.cluster.local", i, cpNamespace),
			)
		}
		Expect(cert.DNSNames).To(ContainElements(wantExact...),
			"peer cert must carry an exact per-member discovery SAN for every ordinal 0..%d so etcd's forward-lookup fallback can match peers (OCPBUGS-76530)", replicas-1)

		// Loopback belongs in IP SANs, not DNS SANs.
		Expect(cert.IPAddresses).To(ContainElement(matchIP("127.0.0.1")),
			"loopback 127.0.0.1 should be an IP SAN")
		Expect(cert.IPAddresses).To(ContainElement(matchIP("::1")),
			"loopback ::1 should be an IP SAN")
		Expect(cert.DNSNames).NotTo(ContainElement("127.0.0.1"),
			"loopback must not appear in DNS SANs")
		Expect(cert.DNSNames).NotTo(ContainElement("::1"),
			"loopback must not appear in DNS SANs")
	})
}

// getEtcdPeerCert fetches and parses peer.crt from the etcd-peer-tls secret.
func getEtcdPeerCert(g Gomega, ctx context.Context, client crclient.Client, cpNamespace string) *x509.Certificate {
	GinkgoHelper()

	secret := cpomanifests.EtcdPeerSecret(cpNamespace)
	g.Expect(client.Get(ctx, crclient.ObjectKeyFromObject(secret), secret)).To(Succeed(), "failed to get etcd-peer-tls secret")

	pemData, ok := secret.Data["peer.crt"]
	g.Expect(ok).To(BeTrue(), "etcd-peer-tls secret should contain peer.crt")

	block, _ := pem.Decode(pemData)
	g.Expect(block).NotTo(BeNil(), "peer.crt should be PEM-decodable")

	cert, err := x509.ParseCertificate(block.Bytes)
	g.Expect(err).NotTo(HaveOccurred(), "peer.crt should parse as an x509 certificate")
	return cert
}

// matchIP returns a Gomega matcher that compares net.IP values by equality,
// so "127.0.0.1" matches regardless of 4-byte vs 16-byte representation.
func matchIP(ip string) types.GomegaMatcher {
	want := net.ParseIP(ip)
	return WithTransform(func(got net.IP) bool { return got.Equal(want) }, BeTrue())
}
