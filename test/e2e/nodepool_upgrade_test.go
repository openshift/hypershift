//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"io"
	"os"
	"testing"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool"
	"github.com/openshift/hypershift/support/releaseinfo"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	syncedLabelsKey   = "e2e.propagate.validation"
	syncedLabelsValue = "true"
)

type NodePoolUpgradeTest struct {
	ctx        context.Context
	mgmtClient crclient.Client

	hostedCluster        *hyperv1.HostedCluster
	hostedClusterClient  crclient.Client
	clusterOpts          core.CreateOptions
	upgradeType          hyperv1.UpgradeType
	previousReleaseImage string
	latestReleaseImage   string
}
type NodePoolInPlaceUpgradeTestManifest struct {
	hostedCluster        *hyperv1.HostedCluster
	previousReleaseImage string
	latestReleaseImage   string
}

func NewNodePoolInPlaceUpgradeTestManifest(hostedCluster *hyperv1.HostedCluster, previousReleaseImage, latestReleaseImage string) *NodePoolInPlaceUpgradeTestManifest {
	return &NodePoolInPlaceUpgradeTestManifest{
		hostedCluster:        hostedCluster,
		previousReleaseImage: previousReleaseImage,
		latestReleaseImage:   latestReleaseImage,
	}
}

func (ipu *NodePoolInPlaceUpgradeTestManifest) BuildNodePoolManifest(defaultNodepool hyperv1.NodePool) (*hyperv1.NodePool, error) {
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ipu.hostedCluster.Name + "-" + "test-inplaceupgrade",
			Namespace: ipu.hostedCluster.Namespace,
		},
	}
	defaultNodepool.Spec.DeepCopyInto(&nodePool.Spec)

	// Propagate Labels and Taints
	syncedLabels := map[string]string{
		syncedLabelsKey: syncedLabelsValue,
	}
	syncedTaints := []hyperv1.Taint{
		{
			Key:    "foo",
			Value:  "bar",
			Effect: corev1.TaintEffectPreferNoSchedule,
		},
	}

	nodePool.Spec.Replicas = &oneReplicas
	nodePool.Spec.NodeLabels = syncedLabels
	nodePool.Spec.Taints = syncedTaints
	nodePool.Spec.Release.Image = ipu.previousReleaseImage
	nodePool.Spec.Management.UpgradeType = hyperv1.UpgradeTypeInPlace

	return nodePool, nil
}

func NewNodePoolUpgradeTest(ctx context.Context, mgmtClient crclient.Client, hostedCluster *hyperv1.HostedCluster,
	hcClient crclient.Client, clusterOpts core.CreateOptions, previousReleaseImage, latestReleaseImage string) *NodePoolUpgradeTest {
	return &NodePoolUpgradeTest{
		ctx:                  ctx,
		hostedCluster:        hostedCluster,
		hostedClusterClient:  hcClient,
		clusterOpts:          clusterOpts,
		mgmtClient:           mgmtClient,
		upgradeType:          hyperv1.UpgradeTypeReplace,
		previousReleaseImage: previousReleaseImage,
		latestReleaseImage:   latestReleaseImage,
	}
}

func (ru *NodePoolUpgradeTest) Setup(t *testing.T) {}

func (ru *NodePoolUpgradeTest) BuildNodePoolManifest(defaultNodepool hyperv1.NodePool) (*hyperv1.NodePool, error) {
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ru.hostedCluster.Name + "-" + "test-replaceupgrade",
			Namespace: ru.hostedCluster.Namespace,
		},
	}
	defaultNodepool.Spec.DeepCopyInto(&nodePool.Spec)

	// One replica and Replace Upgrade
	nodePool.Spec.Replicas = &oneReplicas
	nodePool.Spec.Management.Replace = &hyperv1.ReplaceUpgrade{
		Strategy: hyperv1.UpgradeStrategyRollingUpdate,
		RollingUpdate: &hyperv1.RollingUpdate{
			MaxUnavailable: func(v intstr.IntOrString) *intstr.IntOrString { return &v }(intstr.FromInt(0)),
			MaxSurge:       func(v intstr.IntOrString) *intstr.IntOrString { return &v }(intstr.FromInt(int(oneReplicas))),
		},
	}

	// Propagate Labels and Taints
	syncedLabels := map[string]string{
		syncedLabelsKey: syncedLabelsValue,
	}
	syncedTaints := []hyperv1.Taint{
		{
			Key:    "foo",
			Value:  "bar",
			Effect: corev1.TaintEffectPreferNoSchedule,
		},
	}
	nodePool.Spec.NodeLabels = syncedLabels
	nodePool.Spec.Taints = syncedTaints

	// Using default security group is main use case for OCM.
	nodePool.Spec.Platform.AWS.SecurityGroups = nil

	// Setting initial release image
	nodePool.Spec.Release.Image = ru.previousReleaseImage

	return nodePool, nil
}

func (ru *NodePoolUpgradeTest) Run(t *testing.T, nodePool hyperv1.NodePool, nodes []corev1.Node) {
	g := NewWithT(t)

	ctx := ru.ctx

	// Grab release info
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

	t.Logf("Validating all Nodes have the synced labels and taints")
	e2eutil.EnsureNodesLabelsAndTaints(t, nodePool, nodes)

	// Get fresh NodePool.
	freshNodePool := &hyperv1.NodePool{}
	err = ru.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(&nodePool), freshNodePool)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get NodePool")
	g.Expect(freshNodePool.Status.Version).To(Equal(previousReleaseInfo.ObjectMeta.Name), fmt.Sprintf("wrong previous release version: Previous: %s Nodepool current: %s", previousReleaseInfo.Version(), freshNodePool.Spec.Release.Image))

	// Get fresh HostedCluster.
	freshHC := &hyperv1.HostedCluster{}
	err = ru.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(ru.hostedCluster), freshHC)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get HostedCluster")

	t.Logf("Validating default security group conditions")
	// Validate default security group conditions
	hcCondition := meta.FindStatusCondition(freshHC.Status.Conditions, string(hyperv1.AWSDefaultSecurityGroupCreated))
	g.Expect(hcCondition.Status).To(BeEquivalentTo(metav1.ConditionTrue))

	nodePoolCondition := nodepool.FindStatusCondition(freshNodePool.Status.Conditions, hyperv1.NodePoolAWSSecurityGroupAvailableConditionType)
	g.Expect(nodePoolCondition.Status).To(BeEquivalentTo(metav1.ConditionTrue))

	// Update NodePool images to the latest.
	t.Logf("Updating NodePool image. Image: %s", ru.latestReleaseImage)
	original := freshNodePool.DeepCopy()
	freshNodePool.Spec.Release.Image = ru.latestReleaseImage
	err = ru.mgmtClient.Patch(ctx, freshNodePool, crclient.MergeFrom(original))
	g.Expect(err).NotTo(HaveOccurred(), "failed update NodePool image")

	// final checks
	e2eutil.WaitForNodePoolVersion(t, ctx, ru.mgmtClient, freshNodePool, latestReleaseInfo.Version())
	e2eutil.WaitForNodePoolConditionsNotToBePresent(t, ctx, ru.mgmtClient, freshNodePool, hyperv1.NodePoolUpdatingVersionConditionType)
	nodesFromNodePool := e2eutil.WaitForNReadyNodesByNodePool(t, ctx, ru.hostedClusterClient, *freshNodePool.Spec.Replicas, ru.hostedCluster.Spec.Platform.Type, freshNodePool.Name)
	g.Expect(freshNodePool.Status.Replicas).To(BeEquivalentTo(len(nodesFromNodePool)))
}
