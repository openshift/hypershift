//go:build e2e
// +build e2e

package ginkgo

import (
	"context"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/framework"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	integrationframework "github.com/openshift/hypershift/test/integration/framework"
	"k8s.io/client-go/tools/clientcmd"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// TestCreateCluster implements a test that creates a cluster with the code under test
// vs upgrading to the code under test as TestUpgradeControlPlane does.
//
// This is the Ginkgo-enabled version of test/e2e/create_cluster_test.go::TestCreateCluster
// The main differences from the original:
// 1. Uses Ginkgo's Describe/It structure instead of func TestCreateCluster(t *testing.T)
// 2. Uses framework.NewHypershiftTest instead of e2eutil.NewHypershiftTest (Ginkgo-enabled framework)
// 3. Removes t.Parallel() (Ginkgo handles parallelism)
// 4. Uses GinkgoWriter.Printf instead of t.Logf for some logging
var _ = Describe("CreateCluster", func() {

	It("should create and validate a hypershift cluster", func(ctx SpecContext) {
		ctx, cancel := context.WithCancel(testContext)
		defer cancel()

		clusterOpts := globalOpts.DefaultClusterOptions(GinkgoT())
		zones := strings.Split(globalOpts.ConfigurableClusterOptions.Zone.String(), ",")
		if len(zones) >= 3 {
			// CreateCluster also tests multi-zone workers work properly if a sufficient number of zones are configured
			GinkgoWriter.Printf("Sufficient zones available for InfrastructureAvailabilityPolicy HighlyAvailable\n")
			clusterOpts.AWSPlatform.Zones = zones
			clusterOpts.InfrastructureAvailabilityPolicy = string(hyperv1.HighlyAvailable)
			clusterOpts.NodePoolReplicas = 1
		}
		if !e2eutil.IsLessThan(e2eutil.Version418) {
			clusterOpts.FeatureSet = string(configv1.TechPreviewNoUpgrade)
		}

		clusterOpts.PodsLabels = map[string]string{
			"hypershift-e2e-test-label": "test",
		}
		clusterOpts.Tolerations = []string{"key=hypershift-e2e-test-toleration,operator=Equal,value=true,effect=NoSchedule"}

		framework.NewHypershiftTest(GinkgoT(), ctx, func(t GinkgoTInterface, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
			// Sanity check the cluster by waiting for the nodes to report ready
			guestClient := e2eutil.WaitForGuestClient(t, ctx, mgtClient, hostedCluster)

			By("fetching mgmt kubeconfig")
			mgmtCfg, err := e2eutil.GetConfig()
			g.Expect(err).NotTo(HaveOccurred(), "couldn't get mgmt kubeconfig")
			mgmtCfg.QPS = -1
			mgmtCfg.Burst = -1

			mgmtClients, err := integrationframework.NewClients(mgmtCfg)
			g.Expect(err).NotTo(HaveOccurred(), "couldn't create mgmt clients")

			guestKubeConfigSecretData := e2eutil.WaitForGuestKubeConfig(t, ctx, mgtClient, hostedCluster)

			guestConfig, err := clientcmd.RESTConfigFromKubeConfig(guestKubeConfigSecretData)
			g.Expect(err).NotTo(HaveOccurred(), "couldn't load guest kubeconfig")
			guestConfig.QPS = -1
			guestConfig.Burst = -1

			guestClients, err := integrationframework.NewClients(guestConfig)
			g.Expect(err).NotTo(HaveOccurred(), "couldn't create guest clients")

			By("validating control plane PKI operator break glass credentials")
			framework.RunTestControlPlanePKIOperatorBreakGlassCredentials(testContext, hostedCluster, mgmtClients, guestClients)

			By("ensuring API UX")
			framework.EnsureAPIUX(ctx, mgtClient, hostedCluster)

			By("ensuring custom labels")
			framework.EnsureCustomLabels(ctx, mgtClient, hostedCluster)

			By("ensuring custom tolerations")
			framework.EnsureCustomTolerations(ctx, mgtClient, hostedCluster)

			By("ensuring app label")
			framework.EnsureAppLabel(ctx, mgtClient, hostedCluster)

			By("ensuring feature gate status")
			framework.EnsureFeatureGateStatus(ctx, guestClient)

			// ensure KAS DNS name is configured with a KAS Serving cert
			By("ensuring KubeAPI DNS name custom cert")
			framework.EnsureKubeAPIDNSNameCustomCert(ctx, mgtClient, hostedCluster)

			By("ensuring default security group tags")
			framework.EnsureDefaultSecurityGroupTags(ctx, mgtClient, hostedCluster, clusterOpts)

			if globalOpts.Platform == hyperv1.AzurePlatform {
				By("ensuring KubeAPIServer allowed CIDRs (Azure)")
				framework.EnsureKubeAPIServerAllowedCIDRs(ctx, mgtClient, guestConfig, hostedCluster)
			}

			By("ensuring global pull secret")
			framework.EnsureGlobalPullSecret(ctx, mgtClient, hostedCluster)
		}).
			Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "create-cluster", globalOpts.ServiceAccountSigningKey)

	}, SpecTimeout(90*time.Minute))
})
