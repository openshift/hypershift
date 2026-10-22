//go:build e2ev2

package tests

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/conditions"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/supportedversion"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/blang/semver"
)

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:NodePoolVersionSkew] AWS NodePool version skew", Serial, Label("lifecycle", "nodepool-lifecycle"), func() {
	RegisterAWSNodePoolVersionTests(internal.GetTestContext)
})

func RegisterAWSNodePoolVersionTests(getTestCtx internal.TestContextGetter) {
	for _, distance := range []int{3, 4} {
		It(fmt.Sprintf("should report the correct supported-version polarity for an N-%d NodePool", distance), Label(fmt.Sprintf("nodepool-n%d-release", distance)), func() {
			testCtx := getTestCtx()
			hc, err := testCtx.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			if hc.Spec.Platform.Type != hyperv1.AWSPlatform {
				Skip("AWS NodePool skew tests are only for AWS platform")
			}
			imageVariable := fmt.Sprintf("E2E_N%d_RELEASE_IMAGE", distance)
			image := internal.GetEnvVarValue(imageVariable)
			Expect(image).NotTo(BeEmpty(), "%s must resolve to an actual N-%d release", imageVariable, distance)
			pullSecret := &corev1.Secret{}
			Expect(testCtx.MgmtClient.Get(testCtx.Context, crclient.ObjectKey{Namespace: hc.Namespace, Name: hc.Spec.PullSecret.Name}, pullSecret)).To(Succeed())
			provider := &releaseinfo.RegistryClientProvider{}
			payload, err := provider.Lookup(testCtx.Context, image, pullSecret.Data[corev1.DockerConfigJsonKey])
			Expect(err).NotTo(HaveOccurred(), "resolve the supplied release image before claiming skew coverage")
			controlPlaneVersion, err := testCtx.GetHostedClusterVersion()
			Expect(err).NotTo(HaveOccurred())
			poolVersion, err := semver.Parse(payload.Version())
			Expect(err).NotTo(HaveOccurred())
			Expect(awsReleaseSkewMatches(controlPlaneVersion, poolVersion, distance)).To(Succeed())
			pool := createAWSNodePool(testCtx, hc, fmt.Sprintf("n%d", distance), distance != 4, func(pool *hyperv1.NodePool) {
				pool.Spec.Release.Image = image
				if pool.Spec.NodeLabels == nil {
					pool.Spec.NodeLabels = map[string]string{}
				}
				pool.Spec.NodeLabels["e2e.hypershift.openshift.io/skew"] = fmt.Sprintf("n%d", distance)
				pool.Spec.Taints = append(pool.Spec.Taints, hyperv1.Taint{Key: "e2e-skew", Value: "validation", Effect: corev1.TaintEffectPreferNoSchedule})
			})
			if distance == 4 {
				Eventually(func(g Gomega) {
					current := &hyperv1.NodePool{}
					g.Expect(testCtx.MgmtClient.Get(testCtx.Context, crclient.ObjectKeyFromObject(pool), current)).To(Succeed())
					condition := awsPoolCondition(current, hyperv1.NodePoolSupportedVersionSkewConditionType)
					g.Expect(condition).NotTo(BeNil())
					g.Expect(condition.ObservedGeneration).To(Equal(current.Generation))
					g.Expect(condition.Status).To(Equal(corev1.ConditionFalse))
					g.Expect(condition.Reason).To(Equal(hyperv1.NodePoolUnsupportedSkewReason))
					g.Expect(condition.Message).NotTo(BeEmpty())
				}).WithContext(testCtx.Context).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
				return
			}
			hcClient, err := testCtx.GetHostedClusterClient(hc)
			Expect(err).NotTo(HaveOccurred())
			expectNodePoolReleaseAndRuntime(testCtx, hc, hcClient, pool, image)
			expectAWSNodePoolSteadyState(testCtx, pool)
		})
	}
}

func expectNodePoolReleaseDistance(testCtx *internal.TestContext, hc *hyperv1.HostedCluster, image string, distance int) {
	GinkgoHelper()
	controlPlaneVersion, err := testCtx.GetHostedClusterVersion()
	Expect(err).NotTo(HaveOccurred())
	poolVersion := nodePoolReleaseVersion(testCtx, hc, image)
	Expect(awsReleaseSkewMatches(controlPlaneVersion, poolVersion, distance)).To(Succeed(), "release input must actually have N-%d Kubernetes-minor distance", distance)
}

func awsReleaseSkewMatches(controlPlaneVersion, poolVersion semver.Version, distance int) error {
	controlPlaneKube, err := supportedversion.GetKubeVersionForSupportedVersion(controlPlaneVersion)
	if err != nil {
		return err
	}
	poolKube, err := supportedversion.GetKubeVersionForSupportedVersion(poolVersion)
	if err != nil {
		return err
	}
	if controlPlaneKube.Major != poolKube.Major || int(controlPlaneKube.Minor)-int(poolKube.Minor) != distance {
		return fmt.Errorf("expected N-%d Kubernetes minor skew, got control plane %s (%s), pool %s (%s)", distance, controlPlaneVersion, controlPlaneKube, poolVersion, poolKube)
	}
	return nil
}

func expectAWSNodePoolSteadyState(testCtx *internal.TestContext, pool *hyperv1.NodePool) {
	GinkgoHelper()
	checkAggregation, err := nodePoolAggregationApplicable(testCtx)
	Expect(err).NotTo(HaveOccurred(), "resolve originating release for NodePool condition aggregation")
	Eventually(func(g Gomega) {
		current := &hyperv1.NodePool{}
		g.Expect(testCtx.MgmtClient.Get(testCtx.Context, crclient.ObjectKeyFromObject(pool), current)).To(Succeed())
		for conditionType, status := range conditions.ExpectedNodePoolConditions(current) {
			condition := awsPoolCondition(current, conditionType)
			g.Expect(condition).NotTo(BeNil(), "missing NodePool condition %s", conditionType)
			g.Expect(condition.Status).To(Equal(status), "NodePool condition %s", conditionType)
			if checkAggregation && (conditionType == hyperv1.NodePoolAllMachinesReadyConditionType || conditionType == hyperv1.NodePoolAllNodesHealthyConditionType) {
				g.Expect(condition.Reason).To(Equal(hyperv1.AsExpectedReason))
				g.Expect(condition.Message).To(Equal(hyperv1.AllIsWellMessage))
			}
		}
	}).WithContext(testCtx.Context).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
}

func nodePoolAggregationApplicable(testCtx *internal.TestContext) (bool, error) {
	return nativeOriginAtLeast(testCtx, semver.MustParse("4.23.0"))
}

func expectAWSNodePoolProvisioningConditions(testCtx *internal.TestContext, pool *hyperv1.NodePool) {
	GinkgoHelper()
	applicable, err := nodePoolProvisioningConditionsApplicable(testCtx)
	Expect(err).NotTo(HaveOccurred(), "resolve release for NodePool provisioning conditions")
	if !applicable {
		return
	}
	Eventually(func(g Gomega) {
		current := &hyperv1.NodePool{}
		g.Expect(testCtx.MgmtClient.Get(testCtx.Context, crclient.ObjectKeyFromObject(pool), current)).To(Succeed())
		condition := awsPoolCondition(current, hyperv1.NodePoolAllNodesHealthyConditionType)
		g.Expect(condition).NotTo(BeNil())
		g.Expect(condition.Status).To(Equal(corev1.ConditionFalse))
		g.Expect(condition.Message).To(ContainSubstring("machines are not healthy"))
	}).WithContext(testCtx.Context).WithTimeout(5*time.Minute).WithPolling(3*time.Second).Should(Succeed(), "provisioning must expose unhealthy machines, not silently treat missing NodeHealthy conditions as healthy")
	observed := false
	_ = wait.PollUntilContextTimeout(testCtx.Context, 3*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
		current := &hyperv1.NodePool{}
		var complete bool
		if err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(pool), current); err == nil {
			observed, complete = nodePoolProvisioningMessageState(current, hyperv1.NodePoolAllMachinesReadyConditionType, "ready")
		}
		return complete, nil
	})
	if observed {
		GinkgoWriter.Printf("CAPI bubbling confirmed for %s on NodePool %s/%s during provisioning\n", hyperv1.NodePoolAllMachinesReadyConditionType, pool.Namespace, pool.Name)
	} else {
		GinkgoWriter.Printf("AllMachinesReady was not observed as False with aggregated message during provisioning for NodePool %s/%s (CAPI provider may have set Ready condition before we could observe nil state)\n", pool.Namespace, pool.Name)
	}
}

func nodePoolProvisioningConditionsApplicable(testCtx *internal.TestContext) (bool, error) {
	return nodePoolCloudVersionApplicable(testCtx, semver.MustParse("4.23.0"))
}

func nodePoolProvisioningMessageState(pool *hyperv1.NodePool, conditionType, state string) (bool, bool) {
	condition := awsPoolCondition(pool, conditionType)
	if condition == nil {
		return false, false
	}
	observed := condition.Status == corev1.ConditionFalse && strings.Contains(condition.Message, "machines are not "+state)
	return observed, observed || condition.Status == corev1.ConditionTrue
}
