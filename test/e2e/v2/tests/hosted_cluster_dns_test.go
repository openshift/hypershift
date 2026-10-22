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
	"fmt"
	"io"
	"net"
	"net/url"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/forwarder"
	"github.com/openshift/hypershift/support/netutil"
	"github.com/openshift/hypershift/test/e2e/v2/internal"
	v2util "github.com/openshift/hypershift/test/e2e/v2/util"

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/retry"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/blang/semver"
)

func RegisterHostedClusterDNSTests(getTestCtx internal.TestContextGetter) {
	EnsureKubeAPIDNSNameTests(getTestCtx)
}

func EnsureKubeAPIDNSNameTests(getTestCtx internal.TestContextGetter) {
	It("should serve custom DNS and internal service certificates and remove custom kubeconfigs on restoration", func() {
		tc := getTestCtx()
		skipIfNativeOriginBelow(tc, semver.MustParse("4.19.0"))
		tc.SkipIfPlatform(hyperv1.KubevirtPlatform)
		tc.SkipIfPlatform(hyperv1.OpenStackPlatform)
		hc, err := tc.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
		if !netutil.IsPublicHC(hc) {
			Skip("custom DNS lifecycle requires a public hosted cluster")
		}
		serviceDomain := internal.GetEnvVarValue("E2E_SERVICE_DOMAIN")
		if serviceDomain == "" {
			Skip("E2E_SERVICE_DOMAIN and external-dns watching control plane Services are required")
		}
		Expect(hc.Spec.KubeAPIServerDNSName).To(BeEmpty(), "custom DNS lifecycle requires a fixture without an existing custom API DNS name")
		Expect(hc.Status.CustomKubeconfig).To(BeNil())
		originalRESTConfig, err := tc.GetHostedClusterRESTConfig(hc)
		Expect(err).NotTo(HaveOccurred())
		customHost := fmt.Sprintf("api-custom-cert-%s.%s", hc.Spec.InfraID, serviceDomain)
		certificate, key, err := v2util.GenerateCustomCertificate([]string{customHost}, 24*time.Hour)
		Expect(err).NotTo(HaveOccurred())
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{GenerateName: hc.Name + "-kas-custom-cert-", Namespace: hc.Namespace},
			Type:       corev1.SecretTypeTLS,
			Data:       map[string][]byte{corev1.TLSCertKey: certificate, corev1.TLSPrivateKeyKey: key},
		}
		Expect(tc.MgmtClient.Create(tc.Context, secret)).To(Succeed())
		restorationVerified := false
		DeferCleanup(func() {
			cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(tc.Context), 2*time.Minute)
			defer cancel()
			Expect(deleteCustomDNSCertificate(cleanupContext, tc.MgmtClient, secret, restorationVerified)).To(Succeed())
		})
		originalConfiguration := hc.Spec.Configuration.DeepCopy()
		hcp := &hyperv1.HostedControlPlane{}
		var customHCSecret, customHCPSecret string
		restore := func() {
			restorationVerified = false
			cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(tc.Context), 35*time.Minute)
			defer cancel()
			cleanupTestContext := *tc
			cleanupTestContext.Context = cleanupContext
			Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
				latest, err := cleanupTestContext.GetHostedCluster()
				if err != nil {
					return err
				}
				if latest.Status.CustomKubeconfig != nil {
					customHCSecret = latest.Status.CustomKubeconfig.Name
				}
				latest.Spec.KubeAPIServerDNSName = hc.Spec.KubeAPIServerDNSName
				latest.Spec.Configuration = originalConfiguration.DeepCopy()
				return tc.MgmtClient.Update(cleanupContext, latest)
			})).To(Succeed(), "restore original API DNS and named serving certificates")
			Eventually(func(assertions Gomega) {
				latest, err := cleanupTestContext.GetHostedCluster()
				assertions.Expect(err).NotTo(HaveOccurred())
				assertions.Expect(latest.Spec.KubeAPIServerDNSName).To(Equal(hc.Spec.KubeAPIServerDNSName))
				assertions.Expect(latest.Spec.Configuration).To(Equal(originalConfiguration))
				assertions.Expect(latest.Status.CustomKubeconfig).To(BeNil())
				assertions.Expect(tc.MgmtClient.Get(cleanupContext, crclient.ObjectKey{Namespace: tc.ControlPlaneNamespace, Name: hc.Name}, hcp)).To(Succeed())
				if hcp.Status.CustomKubeconfig != nil {
					customHCPSecret = hcp.Status.CustomKubeconfig.Name
				}
				assertions.Expect(hcp.Status.CustomKubeconfig).To(BeNil())
				assertions.Expect(hcp.Spec.KubeAPIServerDNSName).To(Equal(hc.Spec.KubeAPIServerDNSName))
				if hcp.Spec.Configuration != nil && hcp.Spec.Configuration.APIServer != nil {
					for _, namedCertificate := range hcp.Spec.Configuration.APIServer.ServingCerts.NamedCertificates {
						assertions.Expect(namedCertificate.ServingCertificate.Name).NotTo(Equal(secret.Name))
						assertions.Expect(namedCertificate.Names).NotTo(ContainElement(customHost))
					}
				}
				for _, location := range []crclient.ObjectKey{{Namespace: hc.Namespace, Name: customHCSecret}, {Namespace: tc.ControlPlaneNamespace, Name: customHCPSecret}} {
					if location.Name != "" {
						assertions.Expect(apierrors.IsNotFound(tc.MgmtClient.Get(cleanupContext, location, &corev1.Secret{}))).To(BeTrue())
					}
				}
				deployment := &appsv1.Deployment{}
				assertions.Expect(tc.MgmtClient.Get(cleanupContext, crclient.ObjectKey{Namespace: tc.ControlPlaneNamespace, Name: "kube-apiserver"}, deployment)).To(Succeed())
				assertions.Expect(deployment.Spec.Replicas).NotTo(BeNil())
				assertions.Expect(*deployment.Spec.Replicas).To(BeNumerically(">", 0))
				assertions.Expect(deployment.Status.ObservedGeneration).To(BeNumerically(">=", deployment.Generation))
				assertions.Expect(deployment.Status.UpdatedReplicas).To(Equal(*deployment.Spec.Replicas))
				assertions.Expect(deployment.Status.ReadyReplicas).To(Equal(*deployment.Spec.Replicas))
				assertions.Expect(deployment.Status.AvailableReplicas).To(Equal(*deployment.Spec.Replicas))
				assertions.Expect(deployment.Status.Replicas).To(Equal(*deployment.Spec.Replicas))
				for _, volume := range deployment.Spec.Template.Spec.Volumes {
					if volume.Secret != nil {
						assertions.Expect(volume.Secret.SecretName).NotTo(Equal(secret.Name))
					}
				}
				pods := &corev1.PodList{}
				assertions.Expect(tc.MgmtClient.List(cleanupContext, pods, crclient.InNamespace(tc.ControlPlaneNamespace), crclient.MatchingLabels{"app": "kube-apiserver"})).To(Succeed())
				assertions.Expect(pods.Items).To(HaveLen(int(*deployment.Spec.Replicas)))
				for _, pod := range pods.Items {
					assertions.Expect(pod.DeletionTimestamp).To(BeNil())
					for _, volume := range pod.Spec.Volumes {
						if volume.Secret != nil {
							assertions.Expect(volume.Secret.SecretName).NotTo(Equal(secret.Name))
						}
					}
				}
				config, err := cleanupTestContext.GetHostedClusterRESTConfig(latest)
				assertions.Expect(err).NotTo(HaveOccurred())
				assertions.Expect(config.Host).To(Equal(originalRESTConfig.Host))
				assertions.Expect(config.Insecure).To(BeFalse())
				config.Timeout = 30 * time.Second
				client, err := kubernetes.NewForConfig(config)
				assertions.Expect(err).NotTo(HaveOccurred())
				review, err := client.AuthenticationV1().SelfSubjectReviews().Create(cleanupContext, &authenticationv1.SelfSubjectReview{}, metav1.CreateOptions{})
				assertions.Expect(err).NotTo(HaveOccurred(), "restored API endpoint must serve authenticated TLS requests")
				assertions.Expect(review.Status.UserInfo.Username).NotTo(BeEmpty())
			}).WithContext(cleanupContext).WithTimeout(30*time.Minute).WithPolling(5*time.Second).Should(Succeed(), "observe complete API DNS and certificate restoration before deleting the serving Secret")
			restorationVerified = true
		}
		DeferCleanup(restore)
		Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
			latest, err := tc.GetHostedCluster()
			if err != nil {
				return err
			}
			latest.Spec.KubeAPIServerDNSName = customHost
			if latest.Spec.Configuration == nil {
				latest.Spec.Configuration = &hyperv1.ClusterConfiguration{}
			}
			if latest.Spec.Configuration.APIServer == nil {
				latest.Spec.Configuration.APIServer = &configv1.APIServerSpec{}
			}
			latest.Spec.Configuration.APIServer.ServingCerts.NamedCertificates = append(latest.Spec.Configuration.APIServer.ServingCerts.NamedCertificates,
				configv1.APIServerNamedServingCert{Names: []string{customHost}, ServingCertificate: configv1.SecretNameReference{Name: secret.Name}})
			return tc.MgmtClient.Update(tc.Context, latest)
		})).To(Succeed())
		Eventually(func(g Gomega) {
			latest, err := tc.GetHostedCluster()
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(latest.Status.CustomKubeconfig).NotTo(BeNil())
			customHCSecret = latest.Status.CustomKubeconfig.Name
			g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{Namespace: tc.ControlPlaneNamespace, Name: hc.Name}, hcp)).To(Succeed())
			g.Expect(hcp.Status.CustomKubeconfig).NotTo(BeNil())
			customHCPSecret = hcp.Status.CustomKubeconfig.Name
		}, 30*time.Minute, 5*time.Second).Should(Succeed())
		Expect(hcp.Status.ControlPlaneEndpoint.Host).NotTo(BeEmpty())
		externalService := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "kas-custom-dns-", Namespace: tc.ControlPlaneNamespace,
				Annotations: map[string]string{"external-dns.alpha.kubernetes.io/hostname": customHost, "external-dns.alpha.kubernetes.io/ttl": "30"}},
			Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeExternalName, ExternalName: hcp.Status.ControlPlaneEndpoint.Host},
		}
		Expect(tc.MgmtClient.Create(tc.Context, externalService)).To(Succeed())
		DeferCleanup(func() {
			cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(tc.Context), 2*time.Minute)
			defer cancel()
			err := tc.MgmtClient.Delete(cleanupContext, externalService)
			if !apierrors.IsNotFound(err) {
				Expect(err).NotTo(HaveOccurred())
			}
		})
		Eventually(func(g Gomega) {
			addresses, err := net.DefaultResolver.LookupIPAddr(tc.Context, customHost)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(addresses).NotTo(BeEmpty())
		}, 30*time.Minute, 10*time.Second).Should(Succeed())
		Eventually(func(g Gomega) {
			deployment := &appsv1.Deployment{}
			g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{Namespace: tc.ControlPlaneNamespace, Name: "kube-apiserver"}, deployment)).To(Succeed())
			g.Expect(deployment.Status.ObservedGeneration).To(BeNumerically(">=", deployment.Generation))
			g.Expect(deployment.Spec.Replicas).NotTo(BeNil())
			g.Expect(deployment.Status.UpdatedReplicas).To(Equal(*deployment.Spec.Replicas))
			g.Expect(deployment.Status.AvailableReplicas).To(Equal(*deployment.Spec.Replicas))
		}, 30*time.Minute, 5*time.Second).Should(Succeed())
		for _, location := range []crclient.ObjectKey{{Namespace: hc.Namespace, Name: customHCSecret}, {Namespace: tc.ControlPlaneNamespace, Name: customHCPSecret}} {
			customConfigSecret := &corev1.Secret{}
			Expect(tc.MgmtClient.Get(tc.Context, location, customConfigSecret)).To(Succeed())
			config, err := clientcmd.RESTConfigFromKubeConfig(customConfigSecret.Data["kubeconfig"])
			Expect(err).NotTo(HaveOccurred())
			endpoint, err := url.Parse(config.Host)
			Expect(err).NotTo(HaveOccurred())
			Expect(endpoint.Hostname()).To(Equal(customHost))
			Expect(config.Insecure).To(BeFalse())
			Expect(config.CAData).NotTo(BeEmpty())
			config.Timeout = 10 * time.Second
			client, err := kubernetes.NewForConfig(config)
			Expect(err).NotTo(HaveOccurred())
			Eventually(func() error {
				_, err := client.AuthenticationV1().SelfSubjectReviews().Create(tc.Context, &authenticationv1.SelfSubjectReview{}, metav1.CreateOptions{})
				return err
			}, 30*time.Minute, 5*time.Second).Should(Succeed())
			infraClient, err := crclient.New(config, crclient.Options{Scheme: tc.MgmtClient.Scheme()})
			Expect(err).NotTo(HaveOccurred())
			infra := &configv1.Infrastructure{}
			Expect(infraClient.Get(tc.Context, crclient.ObjectKey{Name: "cluster"}, infra)).To(Succeed())
			version := &configv1.ClusterVersion{}
			Expect(infraClient.Get(tc.Context, crclient.ObjectKey{Name: "version"}, version)).To(Succeed())
			Expect(version.Status.History).NotTo(BeEmpty())
			infraURL, err := url.Parse(infra.Status.APIServerURL)
			Expect(err).NotTo(HaveOccurred())
			Expect(infraURL.Hostname()).To(Equal(customHost))
		}
		verifyInternalAPICertificate(tc, hc)
		restore()
	})
}

func deleteCustomDNSCertificate(ctx context.Context, managementClient crclient.Client, secret *corev1.Secret, restorationVerified bool) error {
	if secret == nil {
		return fmt.Errorf("cannot delete a nil serving certificate Secret")
	}
	if !restorationVerified {
		return fmt.Errorf("retaining named serving certificate Secret %s/%s because API restoration was not verified", secret.Namespace, secret.Name)
	}
	return crclient.IgnoreNotFound(managementClient.Delete(ctx, secret))
}

func verifyInternalAPICertificate(tc *internal.TestContext, hc *hyperv1.HostedCluster) {
	secret := &corev1.Secret{}
	Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{Namespace: tc.ControlPlaneNamespace, Name: "service-network-admin-kubeconfig"}, secret)).To(Succeed())
	config, err := clientcmd.RESTConfigFromKubeConfig(secret.Data["kubeconfig"])
	Expect(err).NotTo(HaveOccurred())
	Expect(config.Insecure).To(BeFalse())
	managementConfig, err := v2util.GetConfig()
	Expect(err).NotTo(HaveOccurred())
	managementClient, err := kubernetes.NewForConfig(managementConfig)
	Expect(err).NotTo(HaveOccurred())
	var stop chan struct{}
	defer func() {
		if stop != nil {
			close(stop)
		}
	}()
	Eventually(func() error {
		if stop != nil {
			close(stop)
		}
		stop = make(chan struct{})
		pod, err := forwarder.GetRunningKubeAPIServerPod(tc.Context, tc.MgmtClient, tc.ControlPlaneNamespace)
		if err != nil {
			return err
		}
		tunnel := &forwarder.PortForwarder{Namespace: tc.ControlPlaneNamespace, PodName: pod.Name, Client: managementClient, Config: managementConfig, Out: io.Discard, ErrOut: io.Discard}
		if err := tunnel.ForwardPorts([]string{fmt.Sprintf("0:%d", netutil.KASPodPortFromHostedCluster(hc))}, stop); err != nil {
			return err
		}
		ports, err := tunnel.GetPorts()
		if err != nil {
			return err
		}
		if len(ports) == 0 {
			return fmt.Errorf("port forward returned no ports")
		}
		attempt := rest.CopyConfig(config)
		attempt.Host = "https://" + net.JoinHostPort("localhost", fmt.Sprint(ports[0].Local))
		attempt.ServerName = "kube-apiserver"
		attempt.Timeout = 10 * time.Second
		client, err := kubernetes.NewForConfig(attempt)
		if err != nil {
			return err
		}
		_, err = client.AuthenticationV1().SelfSubjectReviews().Create(tc.Context, &authenticationv1.SelfSubjectReview{}, metav1.CreateOptions{})
		return err
	}, 30*time.Minute, 5*time.Second).Should(Succeed(), "internal API service certificate must remain trusted after custom DNS configuration")
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:DNS] Hosted Cluster DNS Lifecycle", Label("hosted-cluster-dns"), Serial, func() {
	RegisterHostedClusterDNSTests(internal.GetTestContext)
})
