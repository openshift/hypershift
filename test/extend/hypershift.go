package extend

import (
	"context"
	"github.com/go-logr/logr"
	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"
	. "github.com/openshift/hypershift/test/extend/util"
	ctrl "sigs.k8s.io/controller-runtime"
	crcclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = g.Describe("[sig-hypershift] Hypershift", func() {
	var (
		client              crcclient.Client
		hostedClusterConfig *HostedClusterConfig
		logger              logr.Logger
	)
	g.BeforeEach(func(testContext context.Context) {
		logger = NewLogger()
		ctx := ctrl.LoggerInto(testContext, logger)
		var err error
		client, err = GetClient()
		o.Expect(err).NotTo(o.HaveOccurred(), "Failed to get client")
		hostedClusterConfig, err = ValidHypershiftAndGetGuestKubeConf(ctx, client)
		o.Expect(err).NotTo(o.HaveOccurred(), "Failed validating hosted cluster kubeconfig")
		operators, err := GetHypershiftOperators(ctx, client)
		o.Expect(err).NotTo(o.HaveOccurred(), "Failed getting hypershift operators")
		o.Expect(operators).NotTo(o.BeEmpty(), "No hypershift operators found")
		logger.Info("HostedCluster platform", "platform", hostedClusterConfig.Platform)
	})
	// author: heli@redhat.com
	g.It("ROSA-OSD_CCS-HyperShiftMGMT-Critical-42855-Check Status Conditions for HostedControlPlane", func(testContext context.Context) {
		ctx := ctrl.LoggerInto(testContext, logger)
		client, err := GetClient()
		o.Expect(err).NotTo(o.HaveOccurred())
		rc, err := CheckHCConditions(ctx, client, hostedClusterConfig.Namespace, hostedClusterConfig.Name)
		if err != nil {
			logger.Error(err, "Error checking hc conditions")
			o.Expect(err).NotTo(o.HaveOccurred())
		}
		o.Expect(rc).Should(o.BeTrue())
		logger.Info("HostedCluster condition check passed", "name", hostedClusterConfig.Name)
		operatorNS, err := GetHyperShiftOperatorNamespace(ctx, client)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(operatorNS).NotTo(o.BeEmpty())
		hostedclusterNS, err := GetHostedClusterNamespace(ctx, client)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(hostedclusterNS).NotTo(o.BeEmpty())

		guestClient, err := GetClientWithConfig(hostedClusterConfig.Kubeconfig)
		o.Expect(err).NotTo(o.HaveOccurred())
		cv, err := GetHostedClusterVersion(ctx, guestClient, hostedClusterConfig.Namespace, hostedClusterConfig.Name)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(cv.Major).To(o.BeEquivalentTo(4))
	})
})
