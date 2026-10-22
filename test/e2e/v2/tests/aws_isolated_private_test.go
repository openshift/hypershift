//go:build e2ev2

package tests

import (
	"context"
	"net"
	"net/url"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func RegisterAWSPrivateEndpointTransitionTests(getTestCtx internal.TestContextGetter) {
	It("should transition a private endpoint to public and back with updated kubeconfig or DNS", func() {
		testCtx := getTestCtx()
		testCtx.SkipIfNotPlatform(hyperv1.AWSPlatform)
		hostedCluster, err := testCtx.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
		Expect(hostedCluster.Spec.Platform.AWS).NotTo(BeNil())
		Expect(hostedCluster.Spec.Platform.AWS.EndpointAccess).To(Equal(hyperv1.Private),
			"this lifecycle test requires a dedicated, initially private hosted cluster")

		var apiPublishingStrategy *hyperv1.ServicePublishingStrategy
		for _, service := range hostedCluster.Spec.Services {
			if service.Service == hyperv1.APIServer {
				strategy := service.ServicePublishingStrategy
				apiPublishingStrategy = &strategy
				break
			}
		}
		Expect(apiPublishingStrategy).NotTo(BeNil(), "hosted cluster must publish its API server")
		expectDNSUpdate := apiPublishingStrategy.Type == hyperv1.Route
		if expectDNSUpdate {
			Expect(apiPublishingStrategy.Route).NotTo(BeNil())
			Expect(apiPublishingStrategy.Route.Hostname).NotTo(BeEmpty(), "route fixture requires an external DNS hostname")
		}

		originalAccess := hostedCluster.Spec.Platform.AWS.EndpointAccess
		originalConfig, err := testCtx.GetHostedClusterRESTConfig(hostedCluster)
		Expect(err).NotTo(HaveOccurred())
		Expect(originalConfig.Host).NotTo(BeEmpty())
		DeferCleanup(func() {
			cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(testCtx.Context), 35*time.Minute)
			defer cancel()
			cleanupTC := *testCtx
			cleanupTC.Context = cleanupContext
			testCtx := &cleanupTC
			Eventually(func(g Gomega) {
				current, getErr := testCtx.GetHostedCluster()
				if apierrors.IsNotFound(getErr) {
					return
				}
				g.Expect(getErr).NotTo(HaveOccurred())
				g.Expect(current.Spec.Platform.AWS).NotTo(BeNil())
				if current.Spec.Platform.AWS.EndpointAccess != originalAccess {
					original := current.DeepCopy()
					current.Spec.Platform.AWS.EndpointAccess = originalAccess
					g.Expect(testCtx.MgmtClient.Patch(testCtx.Context, current, crclient.MergeFrom(original))).To(Succeed())
				}
			}, 2*time.Minute, 5*time.Second).Should(Succeed(), "restore original endpoint access")
			Eventually(func(g Gomega) {
				current, getErr := testCtx.GetHostedCluster()
				if apierrors.IsNotFound(getErr) {
					return
				}
				g.Expect(getErr).NotTo(HaveOccurred())
				config, configErr := testCtx.GetHostedClusterRESTConfig(current)
				g.Expect(configErr).NotTo(HaveOccurred())
				g.Expect(config.Host).To(Equal(originalConfig.Host), "restore original kubeconfig endpoint")
				if expectDNSUpdate {
					endpoint, parseErr := url.Parse(config.Host)
					g.Expect(parseErr).NotTo(HaveOccurred())
					lookupCtx, cancel := context.WithTimeout(testCtx.Context, 10*time.Second)
					defer cancel()
					addresses, lookupErr := net.DefaultResolver.LookupIPAddr(lookupCtx, endpoint.Hostname())
					g.Expect(lookupErr).NotTo(HaveOccurred())
					g.Expect(addresses).NotTo(BeEmpty())
					for _, address := range addresses {
						g.Expect(address.IP.IsPrivate()).To(BeTrue(), "restore private DNS resolution")
					}
				}
			}, 30*time.Minute, 15*time.Second).Should(Succeed(), "wait for original endpoint visibility")
		})

		for _, endpointAccess := range []hyperv1.AWSEndpointAccessType{hyperv1.PublicAndPrivate, hyperv1.Private} {
			By("switching endpoint access to " + string(endpointAccess))
			current, getErr := testCtx.GetHostedCluster()
			Expect(getErr).NotTo(HaveOccurred())
			previousConfig, configErr := testCtx.GetHostedClusterRESTConfig(current)
			Expect(configErr).NotTo(HaveOccurred())
			Expect(previousConfig.Host).NotTo(BeEmpty())
			previousHost := previousConfig.Host
			Eventually(func(g Gomega) {
				current, getErr = testCtx.GetHostedCluster()
				g.Expect(getErr).NotTo(HaveOccurred())
				g.Expect(current.Spec.Platform.AWS).NotTo(BeNil())
				original := current.DeepCopy()
				current.Spec.Platform.AWS.EndpointAccess = endpointAccess
				g.Expect(testCtx.MgmtClient.Patch(testCtx.Context, current, crclient.MergeFrom(original))).To(Succeed())
			}, 2*time.Minute, 5*time.Second).Should(Succeed())

			Eventually(func(g Gomega) {
				current, getErr := testCtx.GetHostedCluster()
				g.Expect(getErr).NotTo(HaveOccurred())
				config, configErr := testCtx.GetHostedClusterRESTConfig(current)
				g.Expect(configErr).NotTo(HaveOccurred())
				g.Expect(config.Host).NotTo(BeEmpty())
				if !expectDNSUpdate {
					g.Expect(config.Host).NotTo(Equal(previousHost), "kubeconfig endpoint must change with endpoint visibility")
					return
				}
				g.Expect(config.Host).To(Equal(previousHost), "external DNS route hostname must remain stable")
				endpoint, parseErr := url.Parse(config.Host)
				g.Expect(parseErr).NotTo(HaveOccurred())
				g.Expect(endpoint.Hostname()).NotTo(BeEmpty())
				lookupCtx, cancel := context.WithTimeout(testCtx.Context, 10*time.Second)
				defer cancel()
				addresses, lookupErr := net.DefaultResolver.LookupIPAddr(lookupCtx, endpoint.Hostname())
				g.Expect(lookupErr).NotTo(HaveOccurred())
				g.Expect(addresses).NotTo(BeEmpty())
				for _, address := range addresses {
					g.Expect(address.IP.IsPrivate()).To(Equal(endpointAccess == hyperv1.Private),
						"all API endpoint addresses must match the requested visibility")
				}
			}, 30*time.Minute, 15*time.Second).Should(Succeed(), "endpoint access transition to %s", endpointAccess)
		}
	})
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:AWSPrivateEndpoint] AWS Private Endpoint Lifecycle",
	Label("aws-private-endpoint-transition", "AWS"), Serial, func() {
		var testCtx *internal.TestContext
		BeforeEach(func() {
			testCtx = internal.GetTestContext()
			Expect(testCtx).NotTo(BeNil())
		})
		RegisterAWSPrivateEndpointTransitionTests(func() *internal.TestContext { return testCtx })
	})
