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
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func RegisterHostedClusterIngressTests(getTestCtx internal.TestContextGetter) {
	ValidateIngressOperatorConfigurationTest(getTestCtx)
	ServiceProviderDefaultIngressServingCertificateLifecycleTest(getTestCtx)
}

func ValidateIngressOperatorConfigurationTest(getTestCtx internal.TestContextGetter) {
	When("hosted cluster has IngressOperator EndpointPublishingStrategy configured", func() {
		It("should reflect the custom strategy in the hosted cluster IngressController", func() {
			tc := getTestCtx()
			if e2eutil.IsLessThan(e2eutil.Version421) {
				Skip("Ingress operator configuration requires version >= 4.21")
			}
			hc := tc.GetHostedCluster()

			if hc.Spec.OperatorConfiguration == nil ||
				hc.Spec.OperatorConfiguration.IngressOperator == nil ||
				hc.Spec.OperatorConfiguration.IngressOperator.EndpointPublishingStrategy == nil {
				Skip("HostedCluster does not have IngressOperator EndpointPublishingStrategy configured")
			}

			expectedStrategy := hc.Spec.OperatorConfiguration.IngressOperator.EndpointPublishingStrategy

			tc.ValidateHostedClusterClient()
			hcClient := tc.GetHostedClusterClient()

			Eventually(func(g Gomega) {
				ic := &operatorv1.IngressController{}
				g.Expect(hcClient.Get(tc.Context, types.NamespacedName{
					Namespace: "openshift-ingress-operator",
					Name:      "default",
				}, ic)).To(Succeed(), "failed to get IngressController default in hosted cluster")

				g.Expect(ic.Spec.EndpointPublishingStrategy).NotTo(BeNil(),
					"IngressController EndpointPublishingStrategy should be set")
				g.Expect(ic.Spec.EndpointPublishingStrategy.Type).To(Equal(expectedStrategy.Type),
					fmt.Sprintf("expected EndpointPublishingStrategy type %s, got %s", expectedStrategy.Type, ic.Spec.EndpointPublishingStrategy.Type))
				if expectedStrategy.LoadBalancer != nil {
					g.Expect(ic.Spec.EndpointPublishingStrategy.LoadBalancer).NotTo(BeNil(),
						"IngressController LoadBalancer configuration should be set")
					g.Expect(ic.Spec.EndpointPublishingStrategy.LoadBalancer.Scope).To(Equal(expectedStrategy.LoadBalancer.Scope),
						fmt.Sprintf("expected LoadBalancer scope %s, got %s", expectedStrategy.LoadBalancer.Scope, ic.Spec.EndpointPublishingStrategy.LoadBalancer.Scope))
				}
			}, 5*time.Minute, 10*time.Second).Should(Succeed())
		})
	})
}

func ServiceProviderDefaultIngressServingCertificateLifecycleTest(getTestCtx internal.TestContextGetter) {
	When("a custom default ingress certificate is configured", Ordered, func() {
		const certSecretName = "e2e-custom-ingress-cert"

		var tc *internal.TestContext
		var hcClient crclient.Client
		var ingressDomain string
		var certPEM, keyPEM []byte

		BeforeAll(func() {
			tc = getTestCtx()
			tc.ValidateHostedClusterClient()
			hcClient = tc.GetHostedClusterClient()

			hc := tc.GetHostedCluster()
			ingressDomain = fmt.Sprintf("apps.%s.%s", hc.Name, hc.Spec.DNS.BaseDomain)
			if hc.Spec.DNS.BaseDomainPrefix != nil && *hc.Spec.DNS.BaseDomainPrefix != "" {
				ingressDomain = fmt.Sprintf("apps.%s.%s", *hc.Spec.DNS.BaseDomainPrefix, hc.Spec.DNS.BaseDomain)
			} else if hc.Spec.DNS.BaseDomainPrefix != nil && *hc.Spec.DNS.BaseDomainPrefix == "" {
				ingressDomain = fmt.Sprintf("apps.%s", hc.Spec.DNS.BaseDomain)
			}

			var err error
			certPEM, keyPEM, err = e2eutil.GenerateCustomCertificate(
				[]string{fmt.Sprintf("*.%s", ingressDomain)},
				24*time.Hour,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to generate custom ingress certificate")
		})

		AfterAll(func() {
			if tc == nil {
				return
			}
			By("Removing defaultCertificate from HostedCluster")
			hc := tc.GetHostedCluster()
			err := e2eutil.UpdateObject(GinkgoTB(), tc.Context, tc.MgmtClient, hc, func(obj *hyperv1.HostedCluster) {
				if obj.Spec.OperatorConfiguration != nil && obj.Spec.OperatorConfiguration.IngressOperator != nil {
					obj.Spec.OperatorConfiguration.IngressOperator.DefaultCertificate = hyperv1.IngressDefaultCertificateReference{}
				}
			})
			if err != nil && !apierrors.IsNotFound(err) {
				GinkgoWriter.Printf("WARNING: failed to clear defaultCertificate: %v\n", err)
			}

			By("Deleting the custom cert secret")
			certSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      certSecretName,
					Namespace: tc.ClusterNamespace,
				},
			}
			err = tc.MgmtClient.Delete(tc.Context, certSecret)
			if err != nil && !apierrors.IsNotFound(err) {
				GinkgoWriter.Printf("WARNING: failed to delete cert secret: %v\n", err)
			}
		})

		It("should create the cert secret and set defaultCertificate on the HostedCluster", func() {
			By("Creating the TLS secret in the HostedCluster namespace")
			certSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      certSecretName,
					Namespace: tc.ClusterNamespace,
				},
				Type: corev1.SecretTypeTLS,
				Data: map[string][]byte{
					corev1.TLSCertKey:       certPEM,
					corev1.TLSPrivateKeyKey: keyPEM,
				},
			}
			err := tc.MgmtClient.Create(tc.Context, certSecret)
			if apierrors.IsAlreadyExists(err) {
				existing := &corev1.Secret{}
				Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(certSecret), existing)).To(Succeed())
				existing.Data = certSecret.Data
				existing.Type = certSecret.Type
				Expect(tc.MgmtClient.Update(tc.Context, existing)).To(Succeed())
			} else {
				Expect(err).NotTo(HaveOccurred(), "failed to create custom cert secret")
			}

			By("Setting defaultCertificate on the HostedCluster")
			hc := tc.GetHostedCluster()
			Expect(e2eutil.UpdateObject(GinkgoTB(), tc.Context, tc.MgmtClient, hc, func(obj *hyperv1.HostedCluster) {
				if obj.Spec.OperatorConfiguration == nil {
					obj.Spec.OperatorConfiguration = &hyperv1.OperatorConfiguration{}
				}
				if obj.Spec.OperatorConfiguration.IngressOperator == nil {
					obj.Spec.OperatorConfiguration.IngressOperator = &hyperv1.IngressOperatorSpec{}
				}
				obj.Spec.OperatorConfiguration.IngressOperator.DefaultCertificate = hyperv1.IngressDefaultCertificateReference{
					Name: certSecretName,
				}
			})).To(Succeed(), "failed to set defaultCertificate on HostedCluster")
		})

		It("should propagate the custom cert data to the hosted cluster's default-ingress-cert secret", func() {
			Eventually(func(g Gomega) {
				guestSecret := &corev1.Secret{}
				ref := manifests.IngressDefaultIngressControllerCert()
				g.Expect(hcClient.Get(tc.Context, types.NamespacedName{
					Namespace: ref.Namespace,
					Name:      ref.Name,
				}, guestSecret)).To(Succeed())

				g.Expect(guestSecret.Data[corev1.TLSCertKey]).To(Equal(certPEM),
					"guest cluster cert should match the user-provided cert")
				g.Expect(guestSecret.Data[corev1.TLSPrivateKeyKey]).To(Equal(keyPEM),
					"guest cluster key should match the user-provided key")
			}, 5*time.Minute, 10*time.Second).Should(Succeed())
		})

		It("should populate the observed-default-ingress-cert ConfigMap in the control plane namespace with the custom cert's CA", func() {
			Eventually(func(g Gomega) {
				cm := &corev1.ConfigMap{}
				g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{
					Namespace: tc.ControlPlaneNamespace,
					Name:      "observed-default-ingress-cert",
				}, cm)).To(Succeed(), "observed-default-ingress-cert ConfigMap should exist in control plane namespace")

				caData, ok := cm.Data["ca.crt"]
				g.Expect(ok).To(BeTrue(), "observed-default-ingress-cert should have ca.crt key")
				g.Expect(caData).NotTo(BeEmpty(), "ca.crt should not be empty")

				certPool := x509.NewCertPool()
				g.Expect(certPool.AppendCertsFromPEM([]byte(caData))).To(BeTrue(),
					"ca.crt should contain valid PEM certificate data")
			}, 10*time.Minute, 15*time.Second).Should(Succeed())
		})

		It("should serve a route with the custom cert verifiable by the CA from the management cluster", func() {
			By("Reading the observed CA from the management cluster")
			var caBundle []byte
			Eventually(func(g Gomega) {
				cm := &corev1.ConfigMap{}
				g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{
					Namespace: tc.ControlPlaneNamespace,
					Name:      "observed-default-ingress-cert",
				}, cm)).To(Succeed())
				caData, ok := cm.Data["ca.crt"]
				g.Expect(ok).To(BeTrue())
				caBundle = []byte(caData)
			}, 2*time.Minute, 10*time.Second).Should(Succeed())

			certPool := x509.NewCertPool()
			Expect(certPool.AppendCertsFromPEM(caBundle)).To(BeTrue(), "failed to parse observed CA bundle")

			httpClient := &http.Client{
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{
						RootCAs:    certPool,
						MinVersion: tls.VersionTLS12,
					},
				},
				Timeout: 30 * time.Second,
			}

			By(fmt.Sprintf("Verifying TLS handshake against https://canary-openshift-ingress-canary.%s/healthz", ingressDomain))
			canaryURL := fmt.Sprintf("https://canary-openshift-ingress-canary.%s/healthz", ingressDomain)
			Eventually(func(g Gomega) {
				req, err := http.NewRequestWithContext(tc.Context, http.MethodGet, canaryURL, nil)
				g.Expect(err).NotTo(HaveOccurred())
				resp, err := httpClient.Do(req)
				g.Expect(err).NotTo(HaveOccurred(), "TLS handshake should succeed using the observed CA from the management cluster")
				defer resp.Body.Close()
				g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
			}, 5*time.Minute, 10*time.Second).Should(Succeed())
		})

		It("should propagate rotated certificate data when the source secret is updated", func() {
			By("Generating a new certificate for rotation")
			newCertPEM, newKeyPEM, err := e2eutil.GenerateCustomCertificate(
				[]string{fmt.Sprintf("*.%s", ingressDomain)},
				24*time.Hour,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to generate rotated certificate")
			Expect(newCertPEM).NotTo(Equal(certPEM), "rotated cert should differ from original")

			By("Updating the source secret in the HostedCluster namespace")
			Expect(e2eutil.UpdateObject(GinkgoTB(), tc.Context, tc.MgmtClient, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      certSecretName,
					Namespace: tc.ClusterNamespace,
				},
			}, func(obj *corev1.Secret) {
				obj.Data[corev1.TLSCertKey] = newCertPEM
				obj.Data[corev1.TLSPrivateKeyKey] = newKeyPEM
			})).To(Succeed(), "failed to update cert secret for rotation")

			By("Verifying the rotated cert appears in the guest cluster")
			Eventually(func(g Gomega) {
				guestSecret := &corev1.Secret{}
				ref := manifests.IngressDefaultIngressControllerCert()
				g.Expect(hcClient.Get(tc.Context, types.NamespacedName{
					Namespace: ref.Namespace,
					Name:      ref.Name,
				}, guestSecret)).To(Succeed())

				g.Expect(bytes.Equal(guestSecret.Data[corev1.TLSCertKey], newCertPEM)).To(BeTrue(),
					"guest cluster cert should match the rotated cert")
				g.Expect(bytes.Equal(guestSecret.Data[corev1.TLSPrivateKeyKey], newKeyPEM)).To(BeTrue(),
					"guest cluster key should match the rotated key")
			}, 5*time.Minute, 10*time.Second).Should(Succeed())

			certPEM = newCertPEM
			keyPEM = newKeyPEM

			By("Verifying the observed CA in the management cluster updates for the rotated cert")
			var rotatedCABundle []byte
			Eventually(func(g Gomega) {
				cm := &corev1.ConfigMap{}
				g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{
					Namespace: tc.ControlPlaneNamespace,
					Name:      "observed-default-ingress-cert",
				}, cm)).To(Succeed())
				caData, ok := cm.Data["ca.crt"]
				g.Expect(ok).To(BeTrue(), "observed-default-ingress-cert should have ca.crt key")
				g.Expect(caData).NotTo(BeEmpty())

				certPool := x509.NewCertPool()
				g.Expect(certPool.AppendCertsFromPEM([]byte(caData))).To(BeTrue(),
					"ca.crt should contain valid PEM certificate data")
				rotatedCABundle = []byte(caData)
			}, 10*time.Minute, 15*time.Second).Should(Succeed())

			By("Verifying TLS handshake succeeds with the rotated CA from the management cluster")
			certPool := x509.NewCertPool()
			Expect(certPool.AppendCertsFromPEM(rotatedCABundle)).To(BeTrue())
			httpClient := &http.Client{
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{
						RootCAs:    certPool,
						MinVersion: tls.VersionTLS12,
					},
				},
				Timeout: 30 * time.Second,
			}
			canaryURL := fmt.Sprintf("https://canary-openshift-ingress-canary.%s/healthz", ingressDomain)
			Eventually(func(g Gomega) {
				req, err := http.NewRequestWithContext(tc.Context, http.MethodGet, canaryURL, nil)
				g.Expect(err).NotTo(HaveOccurred())
				resp, err := httpClient.Do(req)
				g.Expect(err).NotTo(HaveOccurred(), "TLS handshake should succeed with rotated CA from management cluster")
				defer resp.Body.Close()
				g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
			}, 5*time.Minute, 10*time.Second).Should(Succeed())
		})
	})
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:Ingress] Hosted Cluster Ingress", Label("hosted-cluster-ingress"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")

		testCtx.ValidateHostedCluster()
	})

	RegisterHostedClusterIngressTests(func() *internal.TestContext { return testCtx })
})
