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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	cpconst "github.com/openshift/hypershift/pkg/controlplane"
	"github.com/openshift/hypershift/support/imageregistry"
	"github.com/openshift/hypershift/support/netutil"
	hyperutil "github.com/openshift/hypershift/support/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/blang/semver"
)

// RegisterHostedClusterHealthTests registers all hosted cluster health test specs.
func RegisterHostedClusterHealthTests(getTestCtx internal.TestContextGetter) {
	ValidateHostedClusterConditionsTest(getTestCtx)
	ValidateHostedClusterNodesTest(getTestCtx)
	ValidateControlPlaneVersionSteadyStateTest(getTestCtx)
	EnsureCAPIFinalizersTest(getTestCtx)
	EnsureFeatureGateStatusTest(getTestCtx)
	EnsurePayloadArchSetCorrectlyTest(getTestCtx)
	ValidateConfigurationStatusTest(getTestCtx)
	ValidateControlPlaneEndpointTest(getTestCtx)
}

func ValidateControlPlaneEndpointTest(getTestCtx internal.TestContextGetter) {
	It("should publish the configured API hostname rather than an internal placeholder", Label("hosted-cluster-health-management"), func() {
		tc := getTestCtx()
		hc, err := tc.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
		strategy := netutil.ServicePublishingStrategyByTypeByHC(hc, hyperv1.APIServer)
		Expect(strategy).NotTo(BeNil())
		Expect(hc.Status.ControlPlaneEndpoint.Host).NotTo(BeEmpty())
		if strategy.Route != nil && strategy.Route.Hostname != "" {
			Expect(hc.Status.ControlPlaneEndpoint.Host).To(Equal(strategy.Route.Hostname))
		} else {
			Expect(hc.Status.ControlPlaneEndpoint.Host).NotTo(ContainSubstring("hypershift.local"))
		}
	})
	It("should configure AWS ingress with an NLB", Label("hosted-cluster-health-management"), func() {
		tc := getTestCtx()
		tc.SkipIfNotPlatform(hyperv1.AWSPlatform)
		hc, err := tc.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
		Expect(hc.Spec.Configuration).NotTo(BeNil())
		Expect(hc.Spec.Configuration.Ingress).NotTo(BeNil())
		Expect(hc.Spec.Configuration.Ingress.LoadBalancer.Platform.AWS).NotTo(BeNil())
		Expect(hc.Spec.Configuration.Ingress.LoadBalancer.Platform.AWS.Type).To(Equal(configv1.NLB))
	})
}

func ValidateControlPlaneVersionSteadyStateTest(getTestCtx internal.TestContextGetter) {
	It("should report a completed control plane version for the desired release image", Label("hosted-cluster-health-management"), func() {
		tc := getTestCtx()
		applicable, err := healthControlPlaneVersionApplicable(tc)
		Expect(err).NotTo(HaveOccurred(), "determine independent control plane version applicability")
		if !applicable {
			Skip("control plane version status is only available from release 4.22")
		}
		timeout, err := healthControlPlaneVersionTimeout(tc.ExpectedNodePools)
		Expect(err).NotTo(HaveOccurred(), "determine control plane version assertion budget from configured worker intent")
		Expect(waitForHealthControlPlaneVersion(tc, timeout, 10*time.Second)).To(Succeed())
	})
}

func ValidateHostedClusterNodesTest(getTestCtx internal.TestContextGetter) {
	It("should match NodePool replica counts and have only ready public nodes", Label("hosted-cluster-health-management"), func() {
		tc := getTestCtx()
		hostedCluster, err := tc.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
		pools := &hyperv1.NodePoolList{}
		Expect(tc.MgmtClient.List(tc.Context, pools, crclient.InNamespace(hostedCluster.Namespace))).To(Succeed())
		nodes := &corev1.NodeList{}
		if netutil.IsPublicHC(hostedCluster) {
			hostedClient, err := tc.GetHostedClusterClient(hostedCluster)
			Expect(err).NotTo(HaveOccurred())
			Expect(hostedClient.List(tc.Context, nodes)).To(Succeed())
		}
		Expect(healthNodePoolCounts(hostedCluster, pools.Items, nodes.Items)).To(Succeed())
	})
}

func ValidateHostedClusterConfiguredWorkersTest(getTestCtx internal.TestContextGetter) {
	It("should create exactly the configured initial NodePools and their ready workers", func() {
		tc := getTestCtx()
		Expect(tc).NotTo(BeNil())
		Expect(healthConfiguredNodePoolIntent(tc.ExpectedNodePools)).To(Succeed())
		var hostedCluster *hyperv1.HostedCluster
		Eventually(func() error {
			if err := healthInitialAPIReady(tc); err != nil {
				return err
			}
			current, err := tc.GetHostedCluster()
			hostedCluster = current
			return err
		}).WithContext(tc.Context).WithTimeout(10 * time.Minute).WithPolling(3 * time.Second).Should(Succeed())
		workerTimeout := 30 * time.Minute
		if netutil.IsPublicHC(hostedCluster) {
			workerTimeout = 45 * time.Minute
		}
		Eventually(func() error {
			return healthInitialConfiguredWorkersReady(tc)
		}).WithContext(tc.Context).WithTimeout(workerTimeout).WithPolling(3 * time.Second).Should(Succeed())
		Eventually(func() error {
			current, err := tc.GetHostedCluster()
			if err != nil {
				return err
			}
			return healthInitialDataPlaneCompleted(current, tc.ExpectedNodePools)
		}).WithContext(tc.Context).WithTimeout(30 * time.Minute).WithPolling(3 * time.Second).Should(Succeed())
	})
}

func ValidateHostedClusterConditionsTest(getTestCtx internal.TestContextGetter) {
	When("hosted cluster is operational", func() {
		It("should have all expected conditions with correct status", Label("hosted-cluster-health-management"), func() {
			tc := getTestCtx()
			version, hasWorkers, timeout, err := healthConditionInputs(tc)
			Expect(err).NotTo(HaveOccurred(), "determine originating release and worker intent for condition assertions")
			Eventually(healthConditionsCheck(tc, version, hasWorkers)).WithContext(tc.Context).WithTimeout(timeout).WithPolling(10 * time.Second).Should(Succeed())
		})
	})
}

func EnsureCAPIFinalizersTest(getTestCtx internal.TestContextGetter) {
	When("CAPI components are deployed", func() {
		It("should have component finalizers on all CAPI deployments", Label("hosted-cluster-health-management"), func() {
			tc := getTestCtx()
			tc.SkipIfVersionBelow(semver.MustParse("4.22.0"))
			Expect(cpconst.CAPIComponents).NotTo(BeEmpty(),
				"expected CAPI components to be defined in HostedControlPlaneConfiguration")
			for _, name := range cpconst.CAPIComponents {
				deployment := &appsv1.Deployment{}
				Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{
					Name:      name,
					Namespace: tc.ControlPlaneNamespace,
				}, deployment)).To(Succeed(), "failed to get CAPI deployment %s", name)
				Expect(controllerutil.ContainsFinalizer(deployment, cpconst.ControlPlaneComponentFinalizer)).To(BeTrue(),
					"CAPI deployment %s should have finalizer %s", name, cpconst.ControlPlaneComponentFinalizer)
			}
		})
	})
}

func EnsureFeatureGateStatusTest(getTestCtx internal.TestContextGetter) {
	When("hosted cluster version is completed", func() {
		It("should have feature gate status matching cluster version", func() {
			tc := getTestCtx()
			tc.SkipIfVersionBelow(semver.MustParse("4.19.0"))
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			hcClient, err := tc.GetHostedClusterClient(hc)
			Expect(err).NotTo(HaveOccurred())

			var currentVersion string
			Eventually(func(g Gomega) {
				cv := &configv1.ClusterVersion{}
				g.Expect(hcClient.Get(tc.Context, crclient.ObjectKey{Name: "version"}, cv)).To(Succeed())
				g.Expect(cv.Status.History).NotTo(BeEmpty())
				g.Expect(cv.Status.History[0].State).To(Equal(configv1.CompletedUpdate))
				currentVersion = cv.Status.History[0].Version
			}, 30*time.Minute, 30*time.Second).Should(Succeed())

			Eventually(func(g Gomega) {
				fg := &configv1.FeatureGate{}
				g.Expect(hcClient.Get(tc.Context, crclient.ObjectKey{Name: "cluster"}, fg)).To(Succeed())
				found := false
				for _, details := range fg.Status.FeatureGates {
					if details.Version == currentVersion {
						found = true
						break
					}
				}
				g.Expect(found).To(BeTrue(), "version %s not found in FeatureGate status", currentVersion)
			}, 10*time.Minute, 10*time.Second).Should(Succeed())
		})
	})
}

func EnsurePayloadArchSetCorrectlyTest(getTestCtx internal.TestContextGetter) {
	When("hosted cluster has a release image", func() {
		It("should set payload arch status correctly", Label("hosted-cluster-health-management"), func() {
			tc := getTestCtx()
			hostedCluster, err := getTestCtx().GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())

			imageMetadataProvider := &imageregistry.RegistryClientImageMetadataProvider{}
			Eventually(func(g Gomega) {
				hc := &hyperv1.HostedCluster{}
				g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(hostedCluster), hc)).To(Succeed())
				g.Expect(hc.Status.PayloadArch).NotTo(BeEmpty(), "PayloadArch should be set")
				payloadArch, err := hyperutil.DetermineHostedClusterPayloadArch(tc.Context, tc.MgmtClient, hc, imageMetadataProvider)
				g.Expect(err).NotTo(HaveOccurred(), "failed to determine payload arch")
				g.Expect(payloadArch).To(Equal(hc.Status.PayloadArch))
			}, 30*time.Minute, time.Minute).Should(Succeed())
		})
	})
}

func ValidateConfigurationStatusTest(getTestCtx internal.TestContextGetter) {
	When("hosted cluster authentication is configured", func() {
		It("should propagate configuration status consistently", Label("hosted-cluster-authentication-status"), func() {
			tc := getTestCtx()
			hostedCluster, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			tc.SkipIfVersionBelow(semver.MustParse("4.21.0"))

			hcClient, err := tc.GetHostedClusterClient(hostedCluster)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func(g Gomega) {
				var hostedClusterAuth configv1.Authentication
				g.Expect(hcClient.Get(tc.Context, crclient.ObjectKey{Name: "cluster"}, &hostedClusterAuth)).To(Succeed())

				var hcp hyperv1.HostedControlPlane
				g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{
					Name:      hostedCluster.Name,
					Namespace: tc.ControlPlaneNamespace,
				}, &hcp)).To(Succeed())
				g.Expect(hcp.Status.Configuration).NotTo(BeNil(), "HCP configuration status not set")

				var hc hyperv1.HostedCluster
				g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(hostedCluster), &hc)).To(Succeed())
				g.Expect(hc.Status.Configuration).NotTo(BeNil(), "HC configuration status not set")

				g.Expect(hcp.Status.Configuration.Authentication).To(Equal(hostedClusterAuth.Status),
					"HCP authentication status should match hosted cluster Authentication resource")
				g.Expect(hc.Status.Configuration.Authentication).To(Equal(hostedClusterAuth.Status),
					"HC authentication status should match hosted cluster Authentication resource")
				g.Expect(hcp.Status.Configuration.Authentication).To(Equal(hc.Status.Configuration.Authentication),
					"HCP and HC authentication status should be consistent")
			}, 10*time.Minute, 10*time.Second).Should(Succeed())
		})
	})
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:Health] Hosted Cluster Health", Label("hosted-cluster-health"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
	})

	RegisterHostedClusterHealthTests(func() *internal.TestContext { return testCtx })
})

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:Health] Hosted Cluster Configured Workers", Label("hosted-cluster-health-configured-workers"), func() {
	ValidateHostedClusterConfiguredWorkersTest(internal.GetTestContext)
})
