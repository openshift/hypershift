//go:build e2ev2

package tests

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/test/e2e/v2/internal"
	v2util "github.com/openshift/hypershift/test/e2e/v2/util"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/blang/semver"
)

func NodePoolMajorVersionUpgradeTest(getTestCtx internal.TestContextGetter) {
	It("should upgrade a NodePool across an actual major release boundary", Label("nodepool-major-version-upgrade"), func() {
		testCtx := getTestCtx()
		hc, err := testCtx.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
		hcClient, err := testCtx.GetHostedClusterClient(hc)
		Expect(err).NotTo(HaveOccurred())
		previousImage := internal.GetEnvVarValue("E2E_PREVIOUS_RELEASE_IMAGE")
		latestImage := internal.GetEnvVarValue("E2E_LATEST_RELEASE_IMAGE")
		Expect(previousImage).NotTo(BeEmpty(), "select actual cross-major release inputs")
		Expect(latestImage).NotTo(BeEmpty(), "select actual cross-major release inputs")
		previousVersion := nodePoolReleaseVersion(testCtx, hc, previousImage)
		latestVersion := nodePoolReleaseVersion(testCtx, hc, latestImage)
		Expect(nodePoolMajorUpgradeVersions(previousVersion, latestVersion)).To(Succeed())
		template := nodePoolTemplate(testCtx.Context, testCtx.MgmtClient, hc)
		pool := buildTestNodePool(template, "major-upgrade", func(pool *hyperv1.NodePool) {
			pool.Spec.Replicas = ptr.To(int32(1))
			pool.Spec.Release.Image = previousImage
			pool.Spec.OSImageStream.Name = ""
			pool.Spec.Management.UpgradeType = hyperv1.UpgradeTypeReplace
			pool.Spec.Management.Replace = &hyperv1.ReplaceUpgrade{Strategy: hyperv1.UpgradeStrategyRollingUpdate, RollingUpdate: &hyperv1.RollingUpdate{
				MaxUnavailable: ptr.To(intstr.FromInt32(0)), MaxSurge: ptr.To(intstr.FromInt32(1)),
			}}
		})
		Expect(testCtx.MgmtClient.Create(testCtx.Context, pool)).To(Succeed())
		DeferCleanup(func() { cleanupNativeNodePool(testCtx.Context, testCtx.MgmtClient, pool) })
		nodePoolWaitForInitialState(testCtx, hc, hcClient, pool)
		initialNodes := expectNodePoolReleaseAndRuntime(testCtx, hc, hcClient, pool, previousImage)
		Expect(nodePoolUpdateObject(testCtx.Context, testCtx.MgmtClient, pool, func(current *hyperv1.NodePool) {
			current.Spec.Release.Image = latestImage
		})).To(Succeed())
		getter := func(ctx context.Context) (*hyperv1.NodePool, error) {
			current := &hyperv1.NodePool{}
			err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(pool), current)
			return current, err
		}
		for _, status := range []metav1.ConditionStatus{metav1.ConditionTrue, metav1.ConditionFalse} {
			nodePoolEventuallyObject(testCtx.Context, fmt.Sprintf("cross-major upgrade UpdatingVersion=%s", status), getter,
				[]nodePoolPredicate[*hyperv1.NodePool]{nodePoolConditionPredicate[*hyperv1.NodePool](v2util.Condition{Type: hyperv1.NodePoolUpdatingVersionConditionType, Status: status})}, nodePoolPollTimeout(nodePoolUpgradeTimeout(hc.Spec.Platform.Type)))
		}
		updatedNodes := expectNodePoolReleaseAndRuntime(testCtx, hc, hcClient, pool, latestImage)
		for _, node := range updatedNodes {
			Expect(awsNodeUIDs(initialNodes)).NotTo(ContainElement(node.UID))
		}
		expectAWSNodePoolSteadyState(testCtx, pool)
	})
}

func nodePoolReleaseVersion(testCtx *internal.TestContext, hc *hyperv1.HostedCluster, image string) semver.Version {
	GinkgoHelper()
	pullSecret := &corev1.Secret{}
	Expect(testCtx.MgmtClient.Get(testCtx.Context, crclient.ObjectKey{Namespace: hc.Namespace, Name: hc.Spec.PullSecret.Name}, pullSecret)).To(Succeed())
	provider := &releaseinfo.RegistryClientProvider{}
	payload, err := provider.Lookup(testCtx.Context, image, pullSecret.Data[corev1.DockerConfigJsonKey])
	Expect(err).NotTo(HaveOccurred())
	version, err := semver.Parse(payload.Version())
	Expect(err).NotTo(HaveOccurred())
	return version
}

func nodePoolMajorUpgradeVersions(previous, latest semver.Version) error {
	if latest.Major <= previous.Major || latest.Major < 5 {
		return fmt.Errorf("expected a major upgrade to OCP 5+, got %s -> %s", previous, latest)
	}
	return nil
}

func expectNodePoolUpgradeInputs(testCtx *internal.TestContext, hc *hyperv1.HostedCluster, previousImage, latestImage string) {
	GinkgoHelper()
	previous := nodePoolReleaseVersion(testCtx, hc, previousImage)
	latest := nodePoolReleaseVersion(testCtx, hc, latestImage)
	Expect(nodePoolUpgradeVersions(previous, latest)).To(Succeed())
}

func nodePoolUpgradeVersions(previous, latest semver.Version) error {
	if !latest.GT(previous) {
		return fmt.Errorf("expected an actual release upgrade, got %s -> %s", previous, latest)
	}
	return nil
}

func nodePoolWaitForUpdatingVersion(testCtx *internal.TestContext, hc *hyperv1.HostedCluster, pool *hyperv1.NodePool, status metav1.ConditionStatus) {
	GinkgoHelper()
	nodePoolEventuallyObject(testCtx.Context, fmt.Sprintf("NodePool %s UpdatingVersion=%s", pool.Name, status),
		func(ctx context.Context) (*hyperv1.NodePool, error) {
			current := &hyperv1.NodePool{}
			err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(pool), current)
			return current, err
		}, []nodePoolPredicate[*hyperv1.NodePool]{nodePoolConditionPredicate[*hyperv1.NodePool](v2util.Condition{Type: hyperv1.NodePoolUpdatingVersionConditionType, Status: status})},
		nodePoolPollTimeout(nodePoolUpgradeTimeout(hc.Spec.Platform.Type)))
}

func expectNodePoolReleaseAndRuntime(testCtx *internal.TestContext, hc *hyperv1.HostedCluster, client crclient.Client, pool *hyperv1.NodePool, image string) []corev1.Node {
	GinkgoHelper()
	pullSecret := &corev1.Secret{}
	Expect(testCtx.MgmtClient.Get(testCtx.Context, crclient.ObjectKey{Namespace: hc.Namespace, Name: hc.Spec.PullSecret.Name}, pullSecret)).To(Succeed())
	provider := &releaseinfo.RegistryClientProvider{}
	payload, err := provider.Lookup(testCtx.Context, image, pullSecret.Data[corev1.DockerConfigJsonKey])
	Expect(err).NotTo(HaveOccurred())
	version, err := semver.Parse(payload.Version())
	Expect(err).NotTo(HaveOccurred())
	var current *hyperv1.NodePool
	Eventually(func(g Gomega) {
		current = &hyperv1.NodePool{}
		g.Expect(testCtx.MgmtClient.Get(testCtx.Context, crclient.ObjectKeyFromObject(pool), current)).To(Succeed())
		g.Expect(current.Status.Version).To(Equal(payload.Version()))
		g.Expect(current.Status.NodesInfo.NodeVersions).NotTo(BeEmpty())
		readyCount := int32(0)
		for _, nodeVersion := range current.Status.NodesInfo.NodeVersions {
			if nodeVersion.ReadyNodeCount != nil && *nodeVersion.ReadyNodeCount > 0 {
				g.Expect(nodeVersion.OCPVersion).To(Equal(payload.Version()))
				readyCount += *nodeVersion.ReadyNodeCount
			}
		}
		g.Expect(current.Spec.Replicas).NotTo(BeNil())
		g.Expect(readyCount).To(Equal(*current.Spec.Replicas))
		if version.Major >= 5 {
			expectedStream := current.Spec.OSImageStream.Name
			if expectedStream == "" {
				expectedStream = hyperv1.OSImageStreamRHEL10
			}
			g.Expect(current.Status.OSImageStream.Name).To(Equal(expectedStream))
		}
	}).WithContext(testCtx.Context).WithTimeout(nodePoolUpgradeTimeout(hc.Spec.Platform.Type)).WithPolling(10 * time.Second).Should(Succeed())
	nodes := nodePoolWaitForReadyNodes(testCtx.Context, client, pool, hc.Spec.Platform.Type)
	expectAWSNodePoolRuntime(testCtx, current, nodes)
	return nodes
}

func expectAWSNodePoolRuntime(testCtx *internal.TestContext, pool *hyperv1.NodePool, nodes []corev1.Node) {
	GinkgoHelper()
	runtimeApplicable, err := nodePoolRuntimeApplicable(testCtx)
	Expect(err).NotTo(HaveOccurred())
	if !runtimeApplicable {
		return
	}
	stream, err := nodePoolRuntimeStream(pool)
	Expect(err).NotTo(HaveOccurred())
	ensureNodesRuntimeV2(nodes, stream)
}

func nodePoolRuntimeApplicable(testCtx *internal.TestContext) (bool, error) {
	version, err := healthReleaseVersion(testCtx)
	if err != nil {
		return false, err
	}
	return nodePoolRuntimeChecksEnabled(version), nil
}

func nodePoolRuntimeChecksEnabled(suiteVersion semver.Version) bool {
	return suiteVersion.GTE(semver.MustParse("4.18.0"))
}

func nodePoolRuntimeStream(pool *hyperv1.NodePool) (string, error) {
	for _, stream := range []string{pool.Status.OSImageStream.Name, pool.Spec.OSImageStream.Name} {
		if stream == hyperv1.OSImageStreamRHEL9 || stream == hyperv1.OSImageStreamRHEL10 {
			return stream, nil
		}
	}
	version, err := semver.ParseTolerant(pool.Status.Version)
	if err != nil {
		return "", fmt.Errorf("invalid NodePool version %q: %w", pool.Status.Version, err)
	}
	if version.Major >= 5 {
		return hyperv1.OSImageStreamRHEL10, nil
	}
	return hyperv1.OSImageStreamRHEL9, nil
}
