//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/releaseinfo"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/blang/semver"
)

// NodePoolMajorVersionUpgradeTest validates that upgrading a NodePool across
// major/minor OCP versions (e.g. 4.x to 5.0) correctly updates
// status.osImageStream to the version-derived RHEL stream (rhel-10 for OCP 5.0+).
type NodePoolMajorVersionUpgradeTest struct {
	DummyInfraSetup
	ctx        context.Context
	mgmtClient crclient.Client

	hostedCluster        *hyperv1.HostedCluster
	hostedClusterClient  crclient.Client
	clusterOpts          e2eutil.PlatformAgnosticOptions
	previousReleaseImage string
	latestReleaseImage   string
}

func NewNodePoolMajorVersionUpgradeTest(ctx context.Context, mgmtClient crclient.Client, hostedCluster *hyperv1.HostedCluster,
	hcClient crclient.Client, clusterOpts e2eutil.PlatformAgnosticOptions, previousReleaseImage, latestReleaseImage string) *NodePoolMajorVersionUpgradeTest {
	return &NodePoolMajorVersionUpgradeTest{
		ctx:                  ctx,
		hostedCluster:        hostedCluster,
		hostedClusterClient:  hcClient,
		clusterOpts:          clusterOpts,
		mgmtClient:           mgmtClient,
		previousReleaseImage: previousReleaseImage,
		latestReleaseImage:   latestReleaseImage,
	}
}

func (ru *NodePoolMajorVersionUpgradeTest) Setup(t *testing.T) {
	t.Log("starting test NodePoolMajorVersionUpgradeTest")
}

func (ru *NodePoolMajorVersionUpgradeTest) BuildNodePoolManifest(defaultNodepool hyperv1.NodePool) (*hyperv1.NodePool, error) {
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ru.hostedCluster.Name + "-" + "test-majorversionupgrade",
			Namespace: ru.hostedCluster.Namespace,
		},
	}
	defaultNodepool.Spec.DeepCopyInto(&nodePool.Spec)

	nodePool.Spec.Replicas = &oneReplicas
	nodePool.Spec.Management.Replace = &hyperv1.ReplaceUpgrade{
		Strategy: hyperv1.UpgradeStrategyRollingUpdate,
		RollingUpdate: &hyperv1.RollingUpdate{
			MaxUnavailable: func(v intstr.IntOrString) *intstr.IntOrString { return &v }(intstr.FromInt(0)),
			MaxSurge:       func(v intstr.IntOrString) *intstr.IntOrString { return &v }(intstr.FromInt(int(oneReplicas))),
		},
	}

	nodePool.Spec.Release.Image = ru.previousReleaseImage

	return nodePool, nil
}

func (ru *NodePoolMajorVersionUpgradeTest) Run(t *testing.T, nodePool hyperv1.NodePool, nodes []corev1.Node) {
	g := NewWithT(t)
	ctx := ru.ctx

	releaseInfoProvider := &releaseinfo.RegistryClientProvider{}
	pullSecretFile, err := os.Open(ru.clusterOpts.PullSecretFile)
	g.Expect(err).NotTo(HaveOccurred(), "failed to open pull secret file")
	defer pullSecretFile.Close()
	pullSecret, err := io.ReadAll(pullSecretFile)
	g.Expect(err).NotTo(HaveOccurred(), "failed to read pull secret file")
	previousReleaseInfo, err := releaseInfoProvider.Lookup(ctx, ru.previousReleaseImage, pullSecret)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get release info for previous image")
	latestReleaseInfo, err := releaseInfoProvider.Lookup(ctx, ru.latestReleaseImage, pullSecret)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get release info for latest image")

	previousVersion, err := semver.Parse(previousReleaseInfo.Version())
	g.Expect(err).NotTo(HaveOccurred(), "failed to parse previous release version")
	latestVersion, err := semver.Parse(latestReleaseInfo.Version())
	g.Expect(err).NotTo(HaveOccurred(), "failed to parse latest release version")
	if latestVersion.Major <= previousVersion.Major {
		t.Skipf("skipping major-version upgrade test: latest (%s) is not a higher major than previous (%s)",
			latestReleaseInfo.Version(), previousReleaseInfo.Version())
	}

	t.Logf("Major-version upgrade: %s -> %s", previousReleaseInfo.Version(), latestReleaseInfo.Version())

	// Verify NodePool is at the previous version before upgrade.
	e2eutil.EventuallyObject(t, ctx, fmt.Sprintf("NodePool %s/%s to have version %s", nodePool.Namespace, nodePool.Name, previousReleaseInfo.ObjectMeta.Name),
		func(ctx context.Context) (*hyperv1.NodePool, error) {
			np := &hyperv1.NodePool{}
			err := ru.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(&nodePool), np)
			return np, err
		},
		[]e2eutil.Predicate[*hyperv1.NodePool]{
			func(nodePool *hyperv1.NodePool) (done bool, reasons string, err error) {
				return nodePool.Status.Version == previousReleaseInfo.ObjectMeta.Name, fmt.Sprintf("wanted version %s, got %s", previousReleaseInfo.ObjectMeta.Name, nodePool.Status.Version), nil
			},
		},
		e2eutil.WithTimeout(10*time.Second),
	)

	// Record pre-upgrade osImageStream.
	{
		np := &hyperv1.NodePool{}
		g.Expect(ru.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(&nodePool), np)).To(Succeed())
		t.Logf("Pre-upgrade osImageStream: %q", np.Status.OSImageStream.Name)
	}

	// Upgrade to latest release.
	err = ru.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(&nodePool), &nodePool)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get NodePool")
	t.Logf("Upgrading NodePool image: %s -> %s", ru.previousReleaseImage, ru.latestReleaseImage)
	original := nodePool.DeepCopy()
	nodePool.Spec.Release.Image = ru.latestReleaseImage
	err = ru.mgmtClient.Patch(ctx, &nodePool, crclient.MergeFrom(original))
	g.Expect(err).NotTo(HaveOccurred(), "failed to update NodePool image")

	// Wait for upgrade to start.
	e2eutil.EventuallyObject(t, ctx, fmt.Sprintf("NodePool %s/%s to start the upgrade", nodePool.Namespace, nodePool.Name),
		func(ctx context.Context) (*hyperv1.NodePool, error) {
			np := &hyperv1.NodePool{}
			err := ru.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(&nodePool), np)
			return np, err
		},
		[]e2eutil.Predicate[*hyperv1.NodePool]{
			e2eutil.ConditionPredicate[*hyperv1.NodePool](e2eutil.Condition{
				Type:   hyperv1.NodePoolUpdatingVersionConditionType,
				Status: metav1.ConditionTrue,
			}),
		},
	)

	// Wait for upgrade to complete.
	upgradeTimeout := 30 * time.Minute
	switch ru.hostedCluster.Spec.Platform.Type {
	case hyperv1.AzurePlatform, hyperv1.KubevirtPlatform:
		upgradeTimeout = 45 * time.Minute
	}

	e2eutil.EventuallyObject(t, ctx, fmt.Sprintf("NodePool %s/%s to have version %s", nodePool.Namespace, nodePool.Name, latestReleaseInfo.Version()),
		func(ctx context.Context) (*hyperv1.NodePool, error) {
			np := &hyperv1.NodePool{}
			err := ru.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(&nodePool), np)
			return np, err
		},
		[]e2eutil.Predicate[*hyperv1.NodePool]{
			func(nodePool *hyperv1.NodePool) (done bool, reasons string, err error) {
				want, got := latestReleaseInfo.Version(), nodePool.Status.Version
				return want == got, fmt.Sprintf("wanted version %s, got %s", want, got), nil
			},
			e2eutil.ConditionPredicate[*hyperv1.NodePool](e2eutil.Condition{
				Type:   hyperv1.NodePoolUpdatingVersionConditionType,
				Status: metav1.ConditionFalse,
			}),
		},
		e2eutil.WithTimeout(upgradeTimeout),
	)

	newNodes := e2eutil.WaitForReadyNodesByNodePool(t, ctx, ru.hostedClusterClient, &nodePool, ru.hostedCluster.Spec.Platform.Type)
	e2eutil.EnsureNodesRuntime(t, newNodes, &nodePool)

	// Verify osImageStream is rhel-10 after major-version upgrade to OCP 5.0+.
	expectedStream := string(hyperv1.OSImageStreamRHEL10)
	t.Logf("Verifying osImageStream=%s after major-version upgrade to %s", expectedStream, latestReleaseInfo.Version())

	e2eutil.EventuallyObject(t, ctx,
		fmt.Sprintf("NodePool %s/%s status to report osImageStream=%s after major-version upgrade", nodePool.Namespace, nodePool.Name, expectedStream),
		func(ctx context.Context) (*hyperv1.NodePool, error) {
			np := &hyperv1.NodePool{}
			err := ru.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(&nodePool), np)
			return np, err
		},
		[]e2eutil.Predicate[*hyperv1.NodePool]{
			e2eutil.OSImageStreamPredicate(expectedStream),
		},
		e2eutil.WithTimeout(5*time.Minute),
		e2eutil.WithInterval(15*time.Second),
	)
}
