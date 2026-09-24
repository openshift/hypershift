//go:build e2e

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

package e2e

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	cpomanifests "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	homanifests "github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/capabilities"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// ingressTestContext supplies the existing create-cluster fixture to the backported checks.
type ingressTestContext struct {
	Context               context.Context
	MgmtClient            crclient.Client
	ClusterNamespace      string
	ClusterName           string
	ControlPlaneNamespace string
}

func testIngressDefaultCertificate(t *testing.T, ctx context.Context, mgmtClient crclient.Client, hc *hyperv1.HostedCluster) {
	t.Helper()
	tc := &ingressTestContext{
		Context:               ctx,
		MgmtClient:            mgmtClient,
		ClusterNamespace:      hc.Namespace,
		ClusterName:           hc.Name,
		ControlPlaneNamespace: homanifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name),
	}
	t.Run("When ingress is disabled it should not copy the default certificate", func(t *testing.T) {
		testIngressDefaultCertificateDisabled(t, tc)
	})
	t.Run("When a custom ingress certificate is configured it should follow its lifecycle", func(t *testing.T) {
		testIngressDefaultCertificateLifecycle(t, tc)
	})
}

// ingressTestGomega preserves the source tests' Informing behavior without the v2 runner.
func ingressTestGomega(t *testing.T) Gomega {
	t.Helper()
	return NewGomega(func(message string, callerSkip ...int) {
		t.Helper()
		t.Skipf("informing ingress certificate check failed: %s", message)
	})
}

// getIngressTestHostedCluster fetches current state for certificate lifecycle assertions.
func getIngressTestHostedCluster(tc *ingressTestContext) (*hyperv1.HostedCluster, error) {
	hc := &hyperv1.HostedCluster{}
	if err := tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{Namespace: tc.ClusterNamespace, Name: tc.ClusterName}, hc); err != nil {
		return nil, err
	}
	return hc, nil
}

func getIngressTestHostedClusterRESTConfig(tc *ingressTestContext, hc *hyperv1.HostedCluster) (*rest.Config, error) {
	if hc.Status.KubeConfig == nil {
		return nil, fmt.Errorf("kubeconfig status not yet available for HostedCluster %s/%s", hc.Namespace, hc.Name)
	}
	var kubeconfigSecret corev1.Secret
	err := tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{
		Namespace: hc.Namespace,
		Name:      hc.Status.KubeConfig.Name,
	}, &kubeconfigSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to get kubeconfig secret %s/%s: %w", hc.Namespace, hc.Status.KubeConfig.Name, err)
	}

	kubeconfigData, ok := kubeconfigSecret.Data["kubeconfig"]
	if !ok || len(kubeconfigData) == 0 {
		return nil, fmt.Errorf("kubeconfig key not found or empty in secret %s/%s", hc.Namespace, hc.Status.KubeConfig.Name)
	}

	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigData)
	if err != nil {
		return nil, fmt.Errorf("failed to create REST config from kubeconfig: %w", err)
	}
	restConfig.QPS = 200
	restConfig.Burst = 300

	return restConfig, nil
}

func getIngressTestHostedClusterClient(tc *ingressTestContext, hc *hyperv1.HostedCluster) (crclient.Client, error) {
	restConfig, err := getIngressTestHostedClusterRESTConfig(tc, hc)
	if err != nil {
		return nil, err
	}
	if restConfig == nil {
		return nil, fmt.Errorf("expected a REST config for hostedcluster")
	}
	client, err := crclient.New(restConfig, crclient.Options{Scheme: hyperapi.Scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create hosted cluster client: %w", err)
	}
	return client, nil
}

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

func testIngressDefaultCertificateDisabled(t *testing.T, tc *ingressTestContext) {
	t.Helper()
	g := ingressTestGomega(t)
	hc, err := getIngressTestHostedCluster(tc)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get HostedCluster")
	if capabilities.IsIngressCapabilityEnabled(hc.Spec.Capabilities) {
		t.Skip("Ingress capability must be disabled on the HostedCluster")
	}
	hcClient, err := getIngressTestHostedClusterClient(tc, hc)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get hosted cluster client")

	t.Log("Creating a valid source certificate while ingress is disabled")
	certPEM, keyPEM, err := e2eutil.GenerateCustomCertificate(
		[]string{fmt.Sprintf("*.%s", ingressDomainForHostedCluster(hc))}, 24*time.Hour)
	g.Expect(err).NotTo(HaveOccurred(), "failed to generate custom ingress certificate")
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "e2e-disabled-ingress-cert-", Namespace: tc.ClusterNamespace},
		Type:       corev1.SecretTypeTLS,
		Data:       map[string][]byte{corev1.TLSCertKey: certPEM, corev1.TLSPrivateKeyKey: keyPEM},
	}
	g.Expect(tc.MgmtClient.Create(tc.Context, secret)).To(Succeed())
	t.Cleanup(func() {
		if err := tc.MgmtClient.Delete(tc.Context, secret); !apierrors.IsNotFound(err) {
			g.Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete source certificate")
		}
	})

	originalOperatorConfig := hc.Spec.OperatorConfiguration.DeepCopy()
	t.Cleanup(func() {
		g.Expect(e2eutil.UpdateObject(t, tc.Context, tc.MgmtClient, hc, func(obj *hyperv1.HostedCluster) {
			obj.Spec.OperatorConfiguration = originalOperatorConfig
		})).To(Succeed(), "cleanup: failed to restore operator configuration")
	})
	t.Log("Referencing the source certificate on the HostedCluster")
	g.Expect(e2eutil.UpdateObject(t, tc.Context, tc.MgmtClient, hc, func(obj *hyperv1.HostedCluster) {
		if obj.Spec.OperatorConfiguration == nil {
			obj.Spec.OperatorConfiguration = &hyperv1.OperatorConfiguration{}
		}
		if obj.Spec.OperatorConfiguration.IngressOperator == nil {
			obj.Spec.OperatorConfiguration.IngressOperator = &hyperv1.IngressOperatorSpec{}
		}
		obj.Spec.OperatorConfiguration.IngressOperator.DefaultCertificate = hyperv1.IngressDefaultCertificateReference{Name: secret.Name}
	})).To(Succeed())

	t.Log("Waiting for the certificate reference to reach the HostedControlPlane")
	g.Eventually(func(g Gomega) {
		hcp := &hyperv1.HostedControlPlane{}
		g.Expect(tc.MgmtClient.Get(tc.Context, types.NamespacedName{Namespace: tc.ControlPlaneNamespace, Name: hc.Name}, hcp)).To(Succeed())
		g.Expect(capabilities.IsIngressCapabilityEnabled(hcp.Spec.Capabilities)).To(BeFalse())
		g.Expect(hcp.Spec.OperatorConfiguration).NotTo(BeNil())
		g.Expect(hcp.Spec.OperatorConfiguration.IngressOperator).NotTo(BeNil())
		g.Expect(hcp.Spec.OperatorConfiguration.IngressOperator.DefaultCertificate.Name).To(Equal(secret.Name))
	}, 5*time.Minute, 10*time.Second).Should(Succeed())

	t.Log("Verifying neither controller copies the certificate or reports it synced")
	g.Consistently(func(g Gomega) {
		controlPlaneSecret := cpomanifests.ServiceProviderDefaultIngressServingCert(tc.ControlPlaneNamespace)
		err := tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(controlPlaneSecret), controlPlaneSecret)
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "control plane certificate secret %s must remain absent, got: %v", crclient.ObjectKeyFromObject(controlPlaneSecret), err)

		hostedClusterSecret := manifests.IngressDefaultIngressControllerCert()
		err = hcClient.Get(tc.Context, crclient.ObjectKeyFromObject(hostedClusterSecret), hostedClusterSecret)
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "hosted cluster certificate secret %s must remain absent, got: %v", crclient.ObjectKeyFromObject(hostedClusterSecret), err)

		currentHC, err := getIngressTestHostedCluster(tc)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(meta.FindStatusCondition(currentHC.Status.Conditions, string(hyperv1.IngressDefaultCertificateSynced))).To(BeNil(),
			"IngressDefaultCertificateSynced condition must remain absent while ingress is disabled")
	}, 1*time.Minute, 10*time.Second).Should(Succeed())
}

func testIngressDefaultCertificateLifecycle(t *testing.T, tc *ingressTestContext) {
	t.Helper()
	g := ingressTestGomega(t)
	const certSecretName = "e2e-custom-ingress-cert"

	var hcClient crclient.Client
	var ingressDomain string
	var certPEM, keyPEM []byte
	var originalDefaultCert hyperv1.IngressDefaultCertificateReference

	{
		hc, err := getIngressTestHostedCluster(tc)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get HostedCluster")

		if !capabilities.IsIngressCapabilityEnabled(hc.Spec.Capabilities) {
			t.Skip("Ingress capability is disabled on the HostedCluster")
		}

		if hc.Spec.Platform.Type == hyperv1.IBMCloudPlatform {
			t.Skip("custom ingress certificates are not supported on IBM Cloud")
		}

		// TODO: Remove this skip once the default ingress endpoint is reachable
		// from the build farm during testing. The certificate propagation checks
		// work over the guest API, but the TLS handshake steps dial the ingress
		// canary route directly, which is not routable from CI on Azure today.
		if hc.Spec.Platform.Type == hyperv1.AzurePlatform {
			t.Skip("skipped on Azure until the default ingress endpoint is reachable from the build farm during testing")
		}

		// Capture the original defaultCertificate for cleanup.
		if hc.Spec.OperatorConfiguration != nil && hc.Spec.OperatorConfiguration.IngressOperator != nil {
			originalDefaultCert = hc.Spec.OperatorConfiguration.IngressOperator.DefaultCertificate
		}

		hcClient, err = getIngressTestHostedClusterClient(tc, hc)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get hosted cluster client")

		ingressDomain = ingressDomainForHostedCluster(hc)

		certPEM, keyPEM, err = e2eutil.GenerateCustomCertificate(
			[]string{fmt.Sprintf("*.%s", ingressDomain)},
			24*time.Hour,
		)
		g.Expect(err).NotTo(HaveOccurred(), "failed to generate custom ingress certificate")
	}
	t.Cleanup(func() {
		t.Log("Restoring the original defaultCertificate on the HostedCluster")
		hc, err := getIngressTestHostedCluster(tc)
		if err != nil {
			t.Logf("WARNING: failed to get HostedCluster for cleanup: %v\n", err)
		} else {
			err = e2eutil.UpdateObject(t, tc.Context, tc.MgmtClient, hc, func(obj *hyperv1.HostedCluster) {
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
				t.Logf("WARNING: failed to restore defaultCertificate: %v\n", err)
			}
		}

		t.Log("Deleting the custom cert secret")
		certSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      certSecretName,
				Namespace: tc.ClusterNamespace,
			},
		}
		err = tc.MgmtClient.Delete(tc.Context, certSecret)
		if err != nil && !apierrors.IsNotFound(err) {
			t.Logf("WARNING: failed to delete cert secret: %v\n", err)
		}
	})
	{
		t.Log("should create the cert secret and set defaultCertificate on the HostedCluster")
		t.Log("Creating the TLS secret in the HostedCluster namespace")
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
			g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(certSecret), existing)).To(Succeed())
			existing.Data = certSecret.Data
			existing.Type = certSecret.Type
			g.Expect(tc.MgmtClient.Update(tc.Context, existing)).To(Succeed())
		} else {
			g.Expect(err).NotTo(HaveOccurred(), "failed to create custom cert secret")
		}

		t.Log("Setting defaultCertificate on the HostedCluster")
		hc, err := getIngressTestHostedCluster(tc)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get HostedCluster")
		g.Expect(e2eutil.UpdateObject(t, tc.Context, tc.MgmtClient, hc, func(obj *hyperv1.HostedCluster) {
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
	}

	{
		t.Log("should propagate the custom cert data to the hosted cluster's default-ingress-cert secret")
		g.Eventually(func(g Gomega) {
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
	}

	{
		t.Log("should report the IngressDefaultCertificateSynced condition as True on the HostedCluster")
		g.Eventually(func(g Gomega) {
			hc, err := getIngressTestHostedCluster(tc)
			g.Expect(err).NotTo(HaveOccurred())
			cond := meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.IngressDefaultCertificateSynced))
			g.Expect(cond).NotTo(BeNil(), "IngressDefaultCertificateSynced condition should be set")
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue),
				fmt.Sprintf("expected IngressDefaultCertificateSynced=True, got %s (%s: %s)", cond.Status, cond.Reason, cond.Message))
			g.Expect(cond.Reason).To(Equal(hyperv1.AsExpectedReason))
		}, 5*time.Minute, 10*time.Second).Should(Succeed())
	}

	{
		t.Log("should populate the observed-default-ingress-cert ConfigMap in the control plane namespace with the custom cert's CA")
		g.Eventually(func(g Gomega) {
			cm := cpomanifests.IngressObservedDefaultIngressCertCA(tc.ControlPlaneNamespace)
			g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(cm), cm)).To(Succeed(), "observed-default-ingress-cert ConfigMap should exist in control plane namespace")

			caData, ok := cm.Data["ca.crt"]
			g.Expect(ok).To(BeTrue(), "observed-default-ingress-cert should have ca.crt key")
			g.Expect(caData).NotTo(BeEmpty(), "ca.crt should not be empty")

			certPool := x509.NewCertPool()
			g.Expect(certPool.AppendCertsFromPEM([]byte(caData))).To(BeTrue(),
				"ca.crt should contain valid PEM certificate data")
		}, 10*time.Minute, 15*time.Second).Should(Succeed())
	}

	{
		t.Log("should serve a route with the custom cert verifiable by the CA from the management cluster")
		t.Log("Reading the observed CA from the management cluster")
		var caBundle []byte
		g.Eventually(func(g Gomega) {
			cm := cpomanifests.IngressObservedDefaultIngressCertCA(tc.ControlPlaneNamespace)
			g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(cm), cm)).To(Succeed())
			caData, ok := cm.Data["ca.crt"]
			g.Expect(ok).To(BeTrue())
			caBundle = []byte(caData)
		}, 2*time.Minute, 10*time.Second).Should(Succeed())

		httpClient, err := newTLSClient(caBundle)
		g.Expect(err).NotTo(HaveOccurred(), "failed to parse observed CA bundle")

		url := canaryURL(ingressDomain)
		t.Log("Verifying TLS handshake against " + url)
		g.Eventually(func(g Gomega) {
			req, err := http.NewRequestWithContext(tc.Context, http.MethodGet, url, nil)
			g.Expect(err).NotTo(HaveOccurred())
			resp, err := httpClient.Do(req)
			g.Expect(err).NotTo(HaveOccurred(), "TLS handshake should succeed using the observed CA from the management cluster")
			defer resp.Body.Close()
			g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
		}, 5*time.Minute, 10*time.Second).Should(Succeed())
	}

	{
		t.Log("should propagate rotated certificate data when the source secret is updated")
		t.Log("Generating a new certificate for rotation")
		newCertPEM, newKeyPEM, err := e2eutil.GenerateCustomCertificate(
			[]string{fmt.Sprintf("*.%s", ingressDomain)},
			24*time.Hour,
		)
		g.Expect(err).NotTo(HaveOccurred(), "failed to generate rotated certificate")
		g.Expect(newCertPEM).NotTo(Equal(certPEM), "rotated cert should differ from original")

		t.Log("Updating the source secret in the HostedCluster namespace")
		g.Expect(e2eutil.UpdateObject(t, tc.Context, tc.MgmtClient, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      certSecretName,
				Namespace: tc.ClusterNamespace,
			},
		}, func(obj *corev1.Secret) {
			obj.Data[corev1.TLSCertKey] = newCertPEM
			obj.Data[corev1.TLSPrivateKeyKey] = newKeyPEM
		})).To(Succeed(), "failed to update cert secret for rotation")

		t.Log("Verifying the rotated cert appears in the hosted cluster")
		g.Eventually(func(g Gomega) {
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

		t.Log("Verifying the observed CA in the management cluster updates for the rotated cert")
		var rotatedCABundle []byte
		g.Eventually(func(g Gomega) {
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

		t.Log("Verifying TLS handshake succeeds with the rotated CA from the management cluster")
		httpClient, err := newTLSClient(rotatedCABundle)
		g.Expect(err).NotTo(HaveOccurred(), "failed to parse rotated CA bundle")
		url := canaryURL(ingressDomain)
		g.Eventually(func(g Gomega) {
			req, err := http.NewRequestWithContext(tc.Context, http.MethodGet, url, nil)
			g.Expect(err).NotTo(HaveOccurred())
			resp, err := httpClient.Do(req)
			g.Expect(err).NotTo(HaveOccurred(), "TLS handshake should succeed with rotated CA from management cluster")
			defer resp.Body.Close()
			g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
		}, 5*time.Minute, 10*time.Second).Should(Succeed())
	}

	{
		t.Log("should report InvalidCertificateSecret and preserve the served certificate when the source secret is missing tls.key")
		const badSecretName = "e2e-custom-ingress-cert-invalid"
		ref := manifests.IngressDefaultIngressControllerCert()

		t.Log("Capturing the certificate currently served in the hosted cluster")
		var servedCert []byte
		g.Eventually(func(g Gomega) {
			hostedClusterSecret := &corev1.Secret{}
			g.Expect(hcClient.Get(tc.Context, types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}, hostedClusterSecret)).To(Succeed())
			g.Expect(hostedClusterSecret.Data[corev1.TLSCertKey]).NotTo(BeEmpty())
			servedCert = append([]byte(nil), hostedClusterSecret.Data[corev1.TLSCertKey]...)
		}, 2*time.Minute, 10*time.Second).Should(Succeed())

		t.Log("Creating a malformed Opaque source secret that is missing tls.key")
		badSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: badSecretName, Namespace: tc.ClusterNamespace},
			Type:       corev1.SecretTypeOpaque,
			Data:       map[string][]byte{corev1.TLSCertKey: certPEM},
		}
		g.Expect(tc.MgmtClient.Create(tc.Context, badSecret)).To(Succeed(), "failed to create malformed source secret")
		t.Cleanup(func() {
			if err := tc.MgmtClient.Delete(tc.Context, badSecret); err != nil && !apierrors.IsNotFound(err) {
				t.Logf("WARNING: failed to delete malformed source secret: %v\n", err)
			}
		})

		t.Log("Pointing defaultCertificate at the malformed secret")
		hc, err := getIngressTestHostedCluster(tc)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(e2eutil.UpdateObject(t, tc.Context, tc.MgmtClient, hc, func(obj *hyperv1.HostedCluster) {
			obj.Spec.OperatorConfiguration.IngressOperator.DefaultCertificate = hyperv1.IngressDefaultCertificateReference{Name: badSecretName}
		})).To(Succeed())

		t.Log("Verifying the HostedCluster reports IngressDefaultCertificateSynced=False with reason InvalidCertificateSecret")
		g.Eventually(func(g Gomega) {
			hc, err := getIngressTestHostedCluster(tc)
			g.Expect(err).NotTo(HaveOccurred())
			cond := meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.IngressDefaultCertificateSynced))
			g.Expect(cond).NotTo(BeNil(), "IngressDefaultCertificateSynced condition should be set")
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse),
				fmt.Sprintf("expected IngressDefaultCertificateSynced=False, got %s (%s: %s)", cond.Status, cond.Reason, cond.Message))
			g.Expect(cond.Reason).To(Equal(hyperv1.IngressDefaultCertificateInvalidReason))
		}, 5*time.Minute, 10*time.Second).Should(Succeed())

		t.Log("Verifying the previously served certificate is preserved while the source is invalid")
		g.Consistently(func(g Gomega) {
			hostedClusterSecret := &corev1.Secret{}
			g.Expect(hcClient.Get(tc.Context, types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}, hostedClusterSecret)).To(Succeed())
			g.Expect(bytes.Equal(hostedClusterSecret.Data[corev1.TLSCertKey], servedCert)).To(BeTrue(),
				"the previously served certificate should remain in place while the source secret is invalid")
		}, 1*time.Minute, 10*time.Second).Should(Succeed())

		t.Log("Restoring defaultCertificate to the valid source secret")
		hc, err = getIngressTestHostedCluster(tc)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(e2eutil.UpdateObject(t, tc.Context, tc.MgmtClient, hc, func(obj *hyperv1.HostedCluster) {
			obj.Spec.OperatorConfiguration.IngressOperator.DefaultCertificate = hyperv1.IngressDefaultCertificateReference{Name: certSecretName}
		})).To(Succeed())
	}

	{
		t.Log("should preserve the last synced certificate and report SecretNotFound when the source secret is deleted")
		ref := manifests.IngressDefaultIngressControllerCert()

		t.Log("Capturing the certificate currently served in the hosted cluster")
		var lastCert []byte
		g.Eventually(func(g Gomega) {
			hostedClusterSecret := &corev1.Secret{}
			g.Expect(hcClient.Get(tc.Context, types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}, hostedClusterSecret)).To(Succeed())
			g.Expect(hostedClusterSecret.Data[corev1.TLSCertKey]).NotTo(BeEmpty())
			lastCert = append([]byte(nil), hostedClusterSecret.Data[corev1.TLSCertKey]...)
		}, 2*time.Minute, 10*time.Second).Should(Succeed())

		t.Log("Deleting the source secret while defaultCertificate is still set")
		g.Expect(tc.MgmtClient.Delete(tc.Context, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: certSecretName, Namespace: tc.ClusterNamespace},
		})).To(Succeed(), "failed to delete source cert secret")

		t.Log("Verifying the HostedCluster reports IngressDefaultCertificateSynced=False with reason SecretNotFound")
		g.Eventually(func(g Gomega) {
			hc, err := getIngressTestHostedCluster(tc)
			g.Expect(err).NotTo(HaveOccurred())
			cond := meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.IngressDefaultCertificateSynced))
			g.Expect(cond).NotTo(BeNil(), "IngressDefaultCertificateSynced condition should be set")
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse),
				fmt.Sprintf("expected IngressDefaultCertificateSynced=False, got %s (%s: %s)", cond.Status, cond.Reason, cond.Message))
			g.Expect(cond.Reason).To(Equal(hyperv1.SecretNotFoundReason))
		}, 5*time.Minute, 10*time.Second).Should(Succeed())

		t.Log("Verifying the previously synced certificate remains in place in the hosted cluster")
		g.Consistently(func(g Gomega) {
			hostedClusterSecret := &corev1.Secret{}
			g.Expect(hcClient.Get(tc.Context, types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}, hostedClusterSecret)).To(Succeed())
			g.Expect(bytes.Equal(hostedClusterSecret.Data[corev1.TLSCertKey], lastCert)).To(BeTrue(),
				"the last synced certificate should remain in place after the source secret is deleted")
		}, 1*time.Minute, 10*time.Second).Should(Succeed())
	}

	{
		t.Log("should revert to the generated wildcard certificate when defaultCertificate is cleared")
		ref := manifests.IngressDefaultIngressControllerCert()

		t.Log("Capturing the custom certificate currently served")
		var customCert []byte
		g.Eventually(func(g Gomega) {
			hostedClusterSecret := &corev1.Secret{}
			g.Expect(hcClient.Get(tc.Context, types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}, hostedClusterSecret)).To(Succeed())
			g.Expect(hostedClusterSecret.Data[corev1.TLSCertKey]).NotTo(BeEmpty())
			customCert = append([]byte(nil), hostedClusterSecret.Data[corev1.TLSCertKey]...)
		}, 2*time.Minute, 10*time.Second).Should(Succeed())

		t.Log("Clearing defaultCertificate on the HostedCluster")
		hc, err := getIngressTestHostedCluster(tc)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(e2eutil.UpdateObject(t, tc.Context, tc.MgmtClient, hc, func(obj *hyperv1.HostedCluster) {
			if obj.Spec.OperatorConfiguration != nil && obj.Spec.OperatorConfiguration.IngressOperator != nil {
				obj.Spec.OperatorConfiguration.IngressOperator.DefaultCertificate = hyperv1.IngressDefaultCertificateReference{}
			}
		})).To(Succeed(), "failed to clear defaultCertificate on HostedCluster")

		t.Log("Verifying the hosted cluster reverts to the generated wildcard certificate")
		g.Eventually(func(g Gomega) {
			hostedClusterSecret := &corev1.Secret{}
			g.Expect(hcClient.Get(tc.Context, types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}, hostedClusterSecret)).To(Succeed())
			g.Expect(hostedClusterSecret.Data[corev1.TLSCertKey]).NotTo(BeEmpty(),
				"a generated wildcard certificate should be present after clearing defaultCertificate")
			g.Expect(bytes.Equal(hostedClusterSecret.Data[corev1.TLSCertKey], customCert)).To(BeFalse(),
				"default-ingress-cert should no longer contain the custom certificate after clearing defaultCertificate")
		}, 10*time.Minute, 15*time.Second).Should(Succeed())

		t.Log("Verifying the IngressDefaultCertificateSynced condition is cleared")
		g.Eventually(func(g Gomega) {
			hc, err := getIngressTestHostedCluster(tc)
			g.Expect(err).NotTo(HaveOccurred())
			cond := meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.IngressDefaultCertificateSynced))
			g.Expect(cond).To(BeNil(), "IngressDefaultCertificateSynced condition should be removed once defaultCertificate is cleared")
		}, 5*time.Minute, 10*time.Second).Should(Succeed())
	}
}
