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

	operatorv1 "github.com/openshift/api/operator/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	cpomanifests "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"
	v2util "github.com/openshift/hypershift/test/e2e/v2/util"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// canaryURL returns the health-check URL for the openshift-ingress canary route
// on the given ingress domain.
func canaryURL(ingressDomain string) string {
	return fmt.Sprintf("https://canary-openshift-ingress-canary.%s/healthz", ingressDomain)
}

// newTLSClient builds an HTTP client that trusts only the provided CA bundle.
func newTLSClient(caBundle []byte) (*http.Client, error) {
	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(caBundle) {
		return nil, fmt.Errorf("failed to parse CA bundle")
	}
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:    certPool,
				MinVersion: tls.VersionTLS12,
			},
		},
		Timeout: 30 * time.Second,
	}, nil
}

// ingressDomainForHostedCluster derives the apps ingress domain for the hosted
// cluster, honoring an explicitly configured ingress domain (AppsDomain over
// Domain, matching globalconfig.IngressDomain) and falling back to
// apps.<base-domain> when neither is configured.
func ingressDomainForHostedCluster(hc *hyperv1.HostedCluster) string {
	if hc.Spec.Configuration != nil && hc.Spec.Configuration.Ingress != nil {
		if len(hc.Spec.Configuration.Ingress.AppsDomain) > 0 {
			return hc.Spec.Configuration.Ingress.AppsDomain
		}
		if len(hc.Spec.Configuration.Ingress.Domain) > 0 {
			return hc.Spec.Configuration.Ingress.Domain
		}
	}
	if hc.Spec.DNS.BaseDomainPrefix != nil && *hc.Spec.DNS.BaseDomainPrefix != "" {
		return fmt.Sprintf("apps.%s.%s", *hc.Spec.DNS.BaseDomainPrefix, hc.Spec.DNS.BaseDomain)
	}
	if hc.Spec.DNS.BaseDomainPrefix != nil && *hc.Spec.DNS.BaseDomainPrefix == "" {
		return fmt.Sprintf("apps.%s", hc.Spec.DNS.BaseDomain)
	}
	return fmt.Sprintf("apps.%s.%s", hc.Name, hc.Spec.DNS.BaseDomain)
}

func RegisterHostedClusterIngressTests(getTestCtx internal.TestContextGetter) {
	ValidateIngressOperatorConfigurationTest(getTestCtx)
	ServiceProviderDefaultIngressServingCertificateLifecycleTest(getTestCtx)
}

func ValidateIngressOperatorConfigurationTest(getTestCtx internal.TestContextGetter) {
	When("hosted cluster has IngressOperator EndpointPublishingStrategy configured", func() {
		It("should reflect the custom strategy in the hosted cluster IngressController", func() {
			tc := getTestCtx()
			tc.SkipIfVersionBelow(e2eutil.Version421)
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())

			if hc.Spec.OperatorConfiguration == nil ||
				hc.Spec.OperatorConfiguration.IngressOperator == nil ||
				hc.Spec.OperatorConfiguration.IngressOperator.EndpointPublishingStrategy == nil {
				Skip("HostedCluster does not have IngressOperator EndpointPublishingStrategy configured")
			}

			expectedStrategy := hc.Spec.OperatorConfiguration.IngressOperator.EndpointPublishingStrategy

			hcClient, err := tc.GetHostedClusterClient(hc)
			Expect(err).NotTo(HaveOccurred())

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
		var originalDefaultCert hyperv1.IngressDefaultCertificateReference

		BeforeAll(func() {
			tc = getTestCtx()

			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred(), "failed to get HostedCluster")

			// TODO: Remove this skip once the default ingress endpoint is reachable
			// from the build farm during testing. The certificate propagation checks
			// work over the guest API, but the TLS handshake steps dial the ingress
			// canary route directly, which is not routable from CI on Azure today.
			if hc.Spec.Platform.Type == hyperv1.AzurePlatform {
				Skip("skipped on Azure until the default ingress endpoint is reachable from the build farm during testing")
			}

			// Capture the original defaultCertificate so AfterAll can restore it
			// rather than unconditionally clearing a value the cluster arrived with.
			if hc.Spec.OperatorConfiguration != nil && hc.Spec.OperatorConfiguration.IngressOperator != nil {
				originalDefaultCert = hc.Spec.OperatorConfiguration.IngressOperator.DefaultCertificate
			}

			hcClient, err = tc.GetHostedClusterClient(hc)
			Expect(err).NotTo(HaveOccurred(), "failed to get hosted cluster client")

			ingressDomain = ingressDomainForHostedCluster(hc)

			certPEM, keyPEM, err = v2util.GenerateCustomCertificate(
				[]string{fmt.Sprintf("*.%s", ingressDomain)},
				24*time.Hour,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to generate custom ingress certificate")
		})

		AfterAll(func() {
			if tc == nil {
				return
			}
			By("Restoring the original defaultCertificate on the HostedCluster")
			hc, err := tc.GetHostedCluster()
			if err != nil {
				GinkgoWriter.Printf("WARNING: failed to get HostedCluster for cleanup: %v\n", err)
			} else {
				err = e2eutil.UpdateObject(GinkgoTB(), tc.Context, tc.MgmtClient, hc, func(obj *hyperv1.HostedCluster) {
					if originalDefaultCert.Name != "" {
						if obj.Spec.OperatorConfiguration == nil {
							obj.Spec.OperatorConfiguration = &hyperv1.OperatorConfiguration{}
						}
						if obj.Spec.OperatorConfiguration.IngressOperator == nil {
							obj.Spec.OperatorConfiguration.IngressOperator = &hyperv1.IngressOperatorSpec{}
						}
						obj.Spec.OperatorConfiguration.IngressOperator.DefaultCertificate = originalDefaultCert
					} else if obj.Spec.OperatorConfiguration != nil && obj.Spec.OperatorConfiguration.IngressOperator != nil {
						obj.Spec.OperatorConfiguration.IngressOperator.DefaultCertificate = hyperv1.IngressDefaultCertificateReference{}
					}
				})
				if err != nil && !apierrors.IsNotFound(err) {
					GinkgoWriter.Printf("WARNING: failed to restore defaultCertificate: %v\n", err)
				}
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
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred(), "failed to get HostedCluster")
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
				hostedClusterSecret := &corev1.Secret{}
				ref := manifests.IngressDefaultIngressControllerCert()
				g.Expect(hcClient.Get(tc.Context, types.NamespacedName{
					Namespace: ref.Namespace,
					Name:      ref.Name,
				}, hostedClusterSecret)).To(Succeed())

				g.Expect(hostedClusterSecret.Data[corev1.TLSCertKey]).To(Equal(certPEM),
					"hosted cluster cert should match the user-provided cert")
				g.Expect(hostedClusterSecret.Data[corev1.TLSPrivateKeyKey]).To(Equal(keyPEM),
					"hosted cluster key should match the user-provided key")
			}, 5*time.Minute, 10*time.Second).Should(Succeed())
		})

		It("should report the IngressDefaultCertificateSynced condition as True on the HostedCluster", func() {
			Eventually(func(g Gomega) {
				hc, err := tc.GetHostedCluster()
				g.Expect(err).NotTo(HaveOccurred())
				cond := meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.IngressDefaultCertificateSynced))
				g.Expect(cond).NotTo(BeNil(), "IngressDefaultCertificateSynced condition should be set")
				g.Expect(cond.Status).To(Equal(metav1.ConditionTrue),
					fmt.Sprintf("expected IngressDefaultCertificateSynced=True, got %s (%s: %s)", cond.Status, cond.Reason, cond.Message))
				g.Expect(cond.Reason).To(Equal(hyperv1.AsExpectedReason))
			}, 5*time.Minute, 10*time.Second).Should(Succeed())
		})

		It("should populate the observed-default-ingress-cert ConfigMap in the control plane namespace with the custom cert's CA", func() {
			Eventually(func(g Gomega) {
				cm := cpomanifests.IngressObservedDefaultIngressCertCA(tc.ControlPlaneNamespace)
				g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(cm), cm)).To(Succeed(), "observed-default-ingress-cert ConfigMap should exist in control plane namespace")

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
				cm := cpomanifests.IngressObservedDefaultIngressCertCA(tc.ControlPlaneNamespace)
				g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(cm), cm)).To(Succeed())
				caData, ok := cm.Data["ca.crt"]
				g.Expect(ok).To(BeTrue())
				caBundle = []byte(caData)
			}, 2*time.Minute, 10*time.Second).Should(Succeed())

			httpClient, err := newTLSClient(caBundle)
			Expect(err).NotTo(HaveOccurred(), "failed to parse observed CA bundle")

			url := canaryURL(ingressDomain)
			By("Verifying TLS handshake against " + url)
			Eventually(func(g Gomega) {
				req, err := http.NewRequestWithContext(tc.Context, http.MethodGet, url, nil)
				g.Expect(err).NotTo(HaveOccurred())
				resp, err := httpClient.Do(req)
				g.Expect(err).NotTo(HaveOccurred(), "TLS handshake should succeed using the observed CA from the management cluster")
				defer resp.Body.Close()
				g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
			}, 5*time.Minute, 10*time.Second).Should(Succeed())
		})

		It("should propagate rotated certificate data when the source secret is updated", func() {
			By("Generating a new certificate for rotation")
			newCertPEM, newKeyPEM, err := v2util.GenerateCustomCertificate(
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

			By("Verifying the rotated cert appears in the hosted cluster")
			Eventually(func(g Gomega) {
				hostedClusterSecret := &corev1.Secret{}
				ref := manifests.IngressDefaultIngressControllerCert()
				g.Expect(hcClient.Get(tc.Context, types.NamespacedName{
					Namespace: ref.Namespace,
					Name:      ref.Name,
				}, hostedClusterSecret)).To(Succeed())

				g.Expect(bytes.Equal(hostedClusterSecret.Data[corev1.TLSCertKey], newCertPEM)).To(BeTrue(),
					"hosted cluster cert should match the rotated cert")
				g.Expect(bytes.Equal(hostedClusterSecret.Data[corev1.TLSPrivateKeyKey], newKeyPEM)).To(BeTrue(),
					"hosted cluster key should match the rotated key")
			}, 5*time.Minute, 10*time.Second).Should(Succeed())

			certPEM = newCertPEM
			keyPEM = newKeyPEM

			By("Verifying the observed CA in the management cluster updates for the rotated cert")
			var rotatedCABundle []byte
			Eventually(func(g Gomega) {
				cm := cpomanifests.IngressObservedDefaultIngressCertCA(tc.ControlPlaneNamespace)
				g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(cm), cm)).To(Succeed())
				caData, ok := cm.Data["ca.crt"]
				g.Expect(ok).To(BeTrue(), "observed-default-ingress-cert should have ca.crt key")
				g.Expect(caData).NotTo(BeEmpty())

				certPool := x509.NewCertPool()
				g.Expect(certPool.AppendCertsFromPEM([]byte(caData))).To(BeTrue(),
					"ca.crt should contain valid PEM certificate data")
				rotatedCABundle = []byte(caData)
			}, 10*time.Minute, 15*time.Second).Should(Succeed())

			By("Verifying TLS handshake succeeds with the rotated CA from the management cluster")
			httpClient, err := newTLSClient(rotatedCABundle)
			Expect(err).NotTo(HaveOccurred(), "failed to parse rotated CA bundle")
			url := canaryURL(ingressDomain)
			Eventually(func(g Gomega) {
				req, err := http.NewRequestWithContext(tc.Context, http.MethodGet, url, nil)
				g.Expect(err).NotTo(HaveOccurred())
				resp, err := httpClient.Do(req)
				g.Expect(err).NotTo(HaveOccurred(), "TLS handshake should succeed with rotated CA from management cluster")
				defer resp.Body.Close()
				g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
			}, 5*time.Minute, 10*time.Second).Should(Succeed())
		})

		It("should report InvalidCertificateSecret and preserve the served certificate when the source secret is missing tls.key", func() {
			const badSecretName = "e2e-custom-ingress-cert-invalid"
			ref := manifests.IngressDefaultIngressControllerCert()

			By("Capturing the certificate currently served in the hosted cluster")
			var servedCert []byte
			Eventually(func(g Gomega) {
				hostedClusterSecret := &corev1.Secret{}
				g.Expect(hcClient.Get(tc.Context, types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}, hostedClusterSecret)).To(Succeed())
				g.Expect(hostedClusterSecret.Data[corev1.TLSCertKey]).NotTo(BeEmpty())
				servedCert = append([]byte(nil), hostedClusterSecret.Data[corev1.TLSCertKey]...)
			}, 2*time.Minute, 10*time.Second).Should(Succeed())

			By("Creating a malformed Opaque source secret that is missing tls.key")
			badSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: badSecretName, Namespace: tc.ClusterNamespace},
				Type:       corev1.SecretTypeOpaque,
				Data:       map[string][]byte{corev1.TLSCertKey: certPEM},
			}
			Expect(tc.MgmtClient.Create(tc.Context, badSecret)).To(Succeed(), "failed to create malformed source secret")
			DeferCleanup(func() {
				if err := tc.MgmtClient.Delete(tc.Context, badSecret); err != nil && !apierrors.IsNotFound(err) {
					GinkgoWriter.Printf("WARNING: failed to delete malformed source secret: %v\n", err)
				}
			})

			By("Pointing defaultCertificate at the malformed secret")
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			Expect(e2eutil.UpdateObject(GinkgoTB(), tc.Context, tc.MgmtClient, hc, func(obj *hyperv1.HostedCluster) {
				obj.Spec.OperatorConfiguration.IngressOperator.DefaultCertificate = hyperv1.IngressDefaultCertificateReference{Name: badSecretName}
			})).To(Succeed())

			By("Verifying the HostedCluster reports IngressDefaultCertificateSynced=False with reason InvalidCertificateSecret")
			Eventually(func(g Gomega) {
				hc, err := tc.GetHostedCluster()
				g.Expect(err).NotTo(HaveOccurred())
				cond := meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.IngressDefaultCertificateSynced))
				g.Expect(cond).NotTo(BeNil(), "IngressDefaultCertificateSynced condition should be set")
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse),
					fmt.Sprintf("expected IngressDefaultCertificateSynced=False, got %s (%s: %s)", cond.Status, cond.Reason, cond.Message))
				g.Expect(cond.Reason).To(Equal(hyperv1.IngressDefaultCertificateInvalidReason))
			}, 5*time.Minute, 10*time.Second).Should(Succeed())

			By("Verifying the previously served certificate is preserved while the source is invalid")
			Consistently(func(g Gomega) {
				hostedClusterSecret := &corev1.Secret{}
				g.Expect(hcClient.Get(tc.Context, types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}, hostedClusterSecret)).To(Succeed())
				g.Expect(bytes.Equal(hostedClusterSecret.Data[corev1.TLSCertKey], servedCert)).To(BeTrue(),
					"the previously served certificate should remain in place while the source secret is invalid")
			}, 1*time.Minute, 10*time.Second).Should(Succeed())

			By("Restoring defaultCertificate to the valid source secret")
			hc, err = tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			Expect(e2eutil.UpdateObject(GinkgoTB(), tc.Context, tc.MgmtClient, hc, func(obj *hyperv1.HostedCluster) {
				obj.Spec.OperatorConfiguration.IngressOperator.DefaultCertificate = hyperv1.IngressDefaultCertificateReference{Name: certSecretName}
			})).To(Succeed())
		})

		It("should preserve the last synced certificate and report SecretNotFound when the source secret is deleted", func() {
			ref := manifests.IngressDefaultIngressControllerCert()

			By("Capturing the certificate currently served in the hosted cluster")
			var lastCert []byte
			Eventually(func(g Gomega) {
				hostedClusterSecret := &corev1.Secret{}
				g.Expect(hcClient.Get(tc.Context, types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}, hostedClusterSecret)).To(Succeed())
				g.Expect(hostedClusterSecret.Data[corev1.TLSCertKey]).NotTo(BeEmpty())
				lastCert = append([]byte(nil), hostedClusterSecret.Data[corev1.TLSCertKey]...)
			}, 2*time.Minute, 10*time.Second).Should(Succeed())

			By("Deleting the source secret while defaultCertificate is still set")
			Expect(tc.MgmtClient.Delete(tc.Context, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: certSecretName, Namespace: tc.ClusterNamespace},
			})).To(Succeed(), "failed to delete source cert secret")

			By("Verifying the HostedCluster reports IngressDefaultCertificateSynced=False with reason SecretNotFound")
			Eventually(func(g Gomega) {
				hc, err := tc.GetHostedCluster()
				g.Expect(err).NotTo(HaveOccurred())
				cond := meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.IngressDefaultCertificateSynced))
				g.Expect(cond).NotTo(BeNil(), "IngressDefaultCertificateSynced condition should be set")
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse),
					fmt.Sprintf("expected IngressDefaultCertificateSynced=False, got %s (%s: %s)", cond.Status, cond.Reason, cond.Message))
				g.Expect(cond.Reason).To(Equal(hyperv1.SecretNotFoundReason))
			}, 5*time.Minute, 10*time.Second).Should(Succeed())

			By("Verifying the previously synced certificate remains in place in the hosted cluster")
			Consistently(func(g Gomega) {
				hostedClusterSecret := &corev1.Secret{}
				g.Expect(hcClient.Get(tc.Context, types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}, hostedClusterSecret)).To(Succeed())
				g.Expect(bytes.Equal(hostedClusterSecret.Data[corev1.TLSCertKey], lastCert)).To(BeTrue(),
					"the last synced certificate should remain in place after the source secret is deleted")
			}, 1*time.Minute, 10*time.Second).Should(Succeed())
		})

		It("should revert to the generated wildcard certificate when defaultCertificate is cleared", func() {
			ref := manifests.IngressDefaultIngressControllerCert()

			By("Capturing the custom certificate currently served")
			var customCert []byte
			Eventually(func(g Gomega) {
				hostedClusterSecret := &corev1.Secret{}
				g.Expect(hcClient.Get(tc.Context, types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}, hostedClusterSecret)).To(Succeed())
				g.Expect(hostedClusterSecret.Data[corev1.TLSCertKey]).NotTo(BeEmpty())
				customCert = append([]byte(nil), hostedClusterSecret.Data[corev1.TLSCertKey]...)
			}, 2*time.Minute, 10*time.Second).Should(Succeed())

			By("Clearing defaultCertificate on the HostedCluster")
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			Expect(e2eutil.UpdateObject(GinkgoTB(), tc.Context, tc.MgmtClient, hc, func(obj *hyperv1.HostedCluster) {
				if obj.Spec.OperatorConfiguration != nil && obj.Spec.OperatorConfiguration.IngressOperator != nil {
					obj.Spec.OperatorConfiguration.IngressOperator.DefaultCertificate = hyperv1.IngressDefaultCertificateReference{}
				}
			})).To(Succeed(), "failed to clear defaultCertificate on HostedCluster")

			By("Verifying the hosted cluster reverts to the generated wildcard certificate")
			Eventually(func(g Gomega) {
				hostedClusterSecret := &corev1.Secret{}
				g.Expect(hcClient.Get(tc.Context, types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}, hostedClusterSecret)).To(Succeed())
				g.Expect(hostedClusterSecret.Data[corev1.TLSCertKey]).NotTo(BeEmpty(),
					"a generated wildcard certificate should be present after clearing defaultCertificate")
				g.Expect(bytes.Equal(hostedClusterSecret.Data[corev1.TLSCertKey], customCert)).To(BeFalse(),
					"default-ingress-cert should no longer contain the custom certificate after clearing defaultCertificate")
			}, 10*time.Minute, 15*time.Second).Should(Succeed())

			By("Verifying the IngressDefaultCertificateSynced condition is cleared")
			Eventually(func(g Gomega) {
				hc, err := tc.GetHostedCluster()
				g.Expect(err).NotTo(HaveOccurred())
				cond := meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.IngressDefaultCertificateSynced))
				g.Expect(cond).To(BeNil(), "IngressDefaultCertificateSynced condition should be removed once defaultCertificate is cleared")
			}, 5*time.Minute, 10*time.Second).Should(Succeed())
		})
	})
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:Ingress] Hosted Cluster Ingress", Label("hosted-cluster-ingress"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
	})

	RegisterHostedClusterIngressTests(func() *internal.TestContext { return testCtx })
})
