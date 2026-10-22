//go:build e2ev2

package tests

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/internal"
	v2util "github.com/openshift/hypershift/test/e2e/v2/util"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/blang/semver"
)

func RegisterHostedClusterNetworkConfigurationTests(getTestCtx internal.TestContextGetter) {
	It("should reconcile custom OVN internal subnets and restore the original operator configuration", func() {
		tc := getTestCtx()
		skipIfNativeOriginBelow(tc, semver.MustParse("4.20.0"))
		hc, err := tc.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
		if hc.Spec.Networking.NetworkType != hyperv1.OVNKubernetes {
			Skip("custom OVN subnet lifecycle requires OVNKubernetes")
		}
		client, err := tc.GetHostedClusterClient(hc)
		Expect(err).NotTo(HaveOccurred())
		originalConfiguration := hc.Spec.OperatorConfiguration.DeepCopy()
		originalNetwork := &operatorv1.Network{}
		Expect(client.Get(tc.Context, crclient.ObjectKey{Name: "cluster"}, originalNetwork)).To(Succeed())
		Expect(originalNetwork.Spec.DefaultNetwork.OVNKubernetesConfig).NotTo(BeNil())
		restore := func() {
			cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(tc.Context), 25*time.Minute)
			defer cancel()
			cleanupTC := *tc
			cleanupTC.Context = cleanupContext
			tc := &cleanupTC
			Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
				latest, err := tc.GetHostedCluster()
				if err != nil {
					return err
				}
				latest.Spec.OperatorConfiguration = originalConfiguration.DeepCopy()
				return tc.MgmtClient.Update(tc.Context, latest)
			})).To(Succeed())
			Eventually(func(g Gomega) {
				hcp := &hyperv1.HostedControlPlane{}
				g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{Namespace: tc.ControlPlaneNamespace, Name: hc.Name}, hcp)).To(Succeed())
				g.Expect(hcp.Spec.OperatorConfiguration).To(Equal(originalConfiguration))
			}).WithContext(cleanupContext).WithTimeout(10 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
			Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
				network := &operatorv1.Network{}
				if err := client.Get(tc.Context, crclient.ObjectKey{Name: "cluster"}, network); err != nil {
					return err
				}
				network.Spec.DefaultNetwork.OVNKubernetesConfig = originalNetwork.Spec.DefaultNetwork.OVNKubernetesConfig.DeepCopy()
				return client.Update(tc.Context, network)
			})).To(Succeed())
			Eventually(func(g Gomega) {
				network := &operatorv1.Network{}
				g.Expect(client.Get(tc.Context, crclient.ObjectKey{Name: "cluster"}, network)).To(Succeed())
				g.Expect(network.Spec.DefaultNetwork.OVNKubernetesConfig).To(Equal(originalNetwork.Spec.DefaultNetwork.OVNKubernetesConfig))
			}).WithContext(cleanupContext).WithTimeout(10*time.Minute).WithPolling(5*time.Second).Should(Succeed(), "restore original OVN subnets")
		}
		DeferCleanup(restore)
		Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
			latest, err := tc.GetHostedCluster()
			if err != nil {
				return err
			}
			if latest.Spec.OperatorConfiguration == nil {
				latest.Spec.OperatorConfiguration = &hyperv1.OperatorConfiguration{}
			}
			if latest.Spec.OperatorConfiguration.ClusterNetworkOperator == nil {
				latest.Spec.OperatorConfiguration.ClusterNetworkOperator = &hyperv1.ClusterNetworkOperatorSpec{}
			}
			if latest.Spec.OperatorConfiguration.ClusterNetworkOperator.OVNKubernetesConfig == nil {
				latest.Spec.OperatorConfiguration.ClusterNetworkOperator.OVNKubernetesConfig = &hyperv1.OVNKubernetesConfig{}
			}
			if latest.Spec.OperatorConfiguration.ClusterNetworkOperator.OVNKubernetesConfig.IPv4 == nil {
				latest.Spec.OperatorConfiguration.ClusterNetworkOperator.OVNKubernetesConfig.IPv4 = &hyperv1.OVNIPv4Config{}
			}
			latest.Spec.OperatorConfiguration.ClusterNetworkOperator.OVNKubernetesConfig.IPv4.InternalJoinSubnet = "100.99.0.0/16"
			latest.Spec.OperatorConfiguration.ClusterNetworkOperator.OVNKubernetesConfig.IPv4.InternalTransitSwitchSubnet = "100.100.0.0/16"
			return tc.MgmtClient.Update(tc.Context, latest)
		})).To(Succeed())
		Eventually(func(g Gomega) {
			hcp := &hyperv1.HostedControlPlane{}
			g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{Namespace: tc.ControlPlaneNamespace, Name: hc.Name}, hcp)).To(Succeed())
			internal.ValidateConditions(g, hcp, []v2util.Condition{
				{Type: "network.operator.openshift.io/Available", Status: metav1.ConditionTrue},
				{Type: "network.operator.openshift.io/Progressing", Status: metav1.ConditionFalse},
				{Type: "network.operator.openshift.io/Degraded", Status: metav1.ConditionFalse},
			})
			network := &operatorv1.Network{}
			g.Expect(client.Get(tc.Context, crclient.ObjectKey{Name: "cluster"}, network)).To(Succeed())
			g.Expect(network.Spec.DefaultNetwork.Type).To(Equal(operatorv1.NetworkTypeOVNKubernetes))
			g.Expect(network.Spec.DefaultNetwork.OVNKubernetesConfig).NotTo(BeNil())
			g.Expect(network.Spec.DefaultNetwork.OVNKubernetesConfig.IPv4).NotTo(BeNil())
			g.Expect(network.Spec.DefaultNetwork.OVNKubernetesConfig.IPv4.InternalJoinSubnet).To(Equal("100.99.0.0/16"))
			g.Expect(network.Spec.DefaultNetwork.OVNKubernetesConfig.IPv4.InternalTransitSwitchSubnet).To(Equal("100.100.0.0/16"))
			networkStatus := &configv1.Network{}
			g.Expect(client.Get(tc.Context, crclient.ObjectKey{Name: "cluster"}, networkStatus)).To(Succeed())
			g.Expect(networkStatus.Status.NetworkType).NotTo(BeEmpty())
			g.Expect(networkStatus.Status.ClusterNetwork).NotTo(BeEmpty())
			g.Expect(networkStatus.Status.ServiceNetwork).NotTo(BeEmpty())
			latest, err := tc.GetHostedCluster()
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(latest.Status.Version).NotTo(BeNil())
			g.Expect(latest.Status.Version.History).NotTo(BeEmpty())
			_, err = semver.Parse(latest.Status.Version.History[0].Version)
			g.Expect(err).NotTo(HaveOccurred())
			version, err := healthReleaseVersion(tc)
			g.Expect(err).NotTo(HaveOccurred())
			for condition, status := range healthExpectedConditions(latest, true, version) {
				internal.ValidateConditions(g, latest, []v2util.Condition{{Type: string(condition), Status: status}})
			}
		}, 10*time.Minute, 5*time.Second).Should(Succeed())
	})
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:NetworkConfiguration] Hosted Cluster Network Configuration Lifecycle", Label("lifecycle", "hosted-cluster-network-configuration"), Serial, func() {
	RegisterHostedClusterNetworkConfigurationTests(internal.GetTestContext)
})
