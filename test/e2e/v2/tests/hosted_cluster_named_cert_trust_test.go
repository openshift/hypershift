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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/certs"
	v2util "github.com/openshift/hypershift/test/e2e/v2/internal"
	testutil "github.com/openshift/hypershift/test/e2e/v2/util"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const namedCertSecretName = "e2e-named-cert-trust"

// RegisterNamedCertTrustTests registers tests that validate named certificate
// CA propagation into the root-ca trust bundle.
func RegisterNamedCertTrustTests(getTestCtx v2util.TestContextGetter) {
	EnsureNamedCertCATrustBundleTest(getTestCtx)
}

// EnsureNamedCertCATrustBundleTest verifies that when a named certificate
// signed by an external CA is configured, the CA appears in the root-ca
// ConfigMap in the control plane namespace and in kube-root-ca.crt in the
// hosted cluster.
func EnsureNamedCertCATrustBundleTest(getTestCtx v2util.TestContextGetter) {
	When("a named certificate signed by an external CA is configured", func() {
		It("should include the external CA in the root-ca and kube-root-ca.crt trust bundles", Label(v2util.InformingLabel), func() {
			tc := getTestCtx()
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())

			// Generate a CA + leaf cert signed by that CA.
			kasHost := hc.Status.ControlPlaneEndpoint.Host
			Expect(kasHost).NotTo(BeEmpty(), "HostedCluster should have a control plane endpoint host")

			caPEM, chainPEM, keyPEM, err := testutil.GenerateCASignedCertificate(
				[]string{kasHost}, 24*time.Hour,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to generate CA-signed certificate")

			// Create the TLS Secret with the chain in tls.crt.
			certSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      namedCertSecretName,
					Namespace: hc.Namespace,
				},
				Type: corev1.SecretTypeTLS,
				Data: map[string][]byte{
					corev1.TLSCertKey:       chainPEM,
					corev1.TLSPrivateKeyKey: keyPEM,
				},
			}
			Expect(tc.MgmtClient.Create(tc.Context, certSecret)).To(Succeed(),
				"failed to create named certificate secret")
			DeferCleanup(func() {
				err := tc.MgmtClient.Delete(tc.Context, certSecret)
				if err != nil && !apierrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete named cert secret")
				}
			})

			// Patch the HostedCluster to add the named certificate.
			originalNamedCerts := getNamedCerts(hc)
			err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
				latest := &hyperv1.HostedCluster{}
				if err := tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(hc), latest); err != nil {
					return err
				}
				if latest.Spec.Configuration == nil {
					latest.Spec.Configuration = &hyperv1.ClusterConfiguration{}
				}
				if latest.Spec.Configuration.APIServer == nil {
					latest.Spec.Configuration.APIServer = &configv1.APIServerSpec{}
				}
				latest.Spec.Configuration.APIServer.ServingCerts.NamedCertificates = append(
					latest.Spec.Configuration.APIServer.ServingCerts.NamedCertificates,
					configv1.APIServerNamedServingCert{
						Names:              []string{kasHost},
						ServingCertificate: configv1.SecretNameReference{Name: namedCertSecretName},
					},
				)
				return tc.MgmtClient.Update(tc.Context, latest)
			})
			Expect(err).NotTo(HaveOccurred(), "failed to add named certificate to HostedCluster")
			DeferCleanup(func() {
				err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
					latest := &hyperv1.HostedCluster{}
					if err := tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(hc), latest); err != nil {
						return err
					}
					if latest.Spec.Configuration != nil && latest.Spec.Configuration.APIServer != nil {
						latest.Spec.Configuration.APIServer.ServingCerts.NamedCertificates = originalNamedCerts
					}
					return tc.MgmtClient.Update(tc.Context, latest)
				})
				if err != nil && !apierrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred(), "cleanup: failed to restore named certificates")
				}
			})

			caString := string(caPEM)

			// Verify the root-ca ConfigMap in the control plane namespace
			// contains the external CA certificate.
			Eventually(func(g Gomega) {
				rootCA := &corev1.ConfigMap{}
				g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{
					Namespace: tc.ControlPlaneNamespace,
					Name:      "root-ca",
				}, rootCA)).To(Succeed())
				g.Expect(rootCA.Data).To(HaveKey(certs.CASignerCertMapKey))
				g.Expect(rootCA.Data[certs.CASignerCertMapKey]).To(ContainSubstring(caString),
					"root-ca ConfigMap should contain the external CA certificate")
			}).WithContext(tc.Context).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

			// Verify the kube-root-ca.crt ConfigMap in the hosted cluster
			// contains the external CA certificate.
			hcClient, err := tc.GetHostedClusterClient(hc)
			Expect(err).NotTo(HaveOccurred(), "failed to get hosted cluster client")

			Eventually(func(g Gomega) {
				kubeRootCA := &corev1.ConfigMap{}
				g.Expect(hcClient.Get(tc.Context, crclient.ObjectKey{
					Namespace: "default",
					Name:      "kube-root-ca.crt",
				}, kubeRootCA)).To(Succeed())
				g.Expect(kubeRootCA.Data).To(HaveKey("ca.crt"))
				g.Expect(kubeRootCA.Data["ca.crt"]).To(
					satisfyCAPresent(caString),
					"kube-root-ca.crt in hosted cluster should contain the external CA certificate",
				)
			}).WithContext(tc.Context).WithTimeout(5 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
		})
	})
}

// satisfyCAPresent returns a Gomega matcher that checks if a PEM bundle
// contains the given CA certificate string.
func satisfyCAPresent(caPEM string) OmegaMatcher {
	return ContainSubstring(strings.TrimSpace(caPEM))
}

// getNamedCerts returns a copy of the current named certificates from an HC.
func getNamedCerts(hc *hyperv1.HostedCluster) []configv1.APIServerNamedServingCert {
	if hc.Spec.Configuration == nil || hc.Spec.Configuration.APIServer == nil {
		return nil
	}
	orig := hc.Spec.Configuration.APIServer.ServingCerts.NamedCertificates
	result := make([]configv1.APIServerNamedServingCert, len(orig))
	copy(result, orig)
	return result
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:NamedCertTrust] Hosted Cluster Named Certificate Trust", Label("hosted-cluster-named-cert-trust"), func() {
	var testCtx *v2util.TestContext

	BeforeEach(func() {
		testCtx = v2util.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
	})

	RegisterNamedCertTrustTests(func() *v2util.TestContext { return testCtx })
})
