//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"io/ioutil"
	"os"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/support/releaseinfo"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func testSetReplaceUpgradeNodePool(parentCtx context.Context, mgmtClient crclient.Client, guestCluster *hyperv1.HostedCluster, guestClient crclient.Client, clusterOpts core.CreateOptions) func(t *testing.T) {
	return func(t *testing.T) {
		g := NewWithT(t)
		ctx, cancel := context.WithCancel(parentCtx)
		originalNP := hyperv1.NodePool{}
		defer cancel()

		// List NodePools (should exists only one and without replicas)
		nodePools := &hyperv1.NodePoolList{}
		err := mgmtClient.List(ctx, nodePools, &crclient.ListOptions{
			Namespace: guestCluster.Namespace,
		})
		g.Expect(err).NotTo(HaveOccurred(), "failed getting existant nodepools")
		for _, nodePool := range nodePools.Items {
			if !strings.Contains(nodePool.Name, "-test-") {
				originalNP = nodePool
			}
		}
		g.Expect(originalNP.Name).NotTo(ContainSubstring("test"))
		awsNPInfo := originalNP.Spec.Platform.AWS

		// Look up metadata about the release images so that we can extract the version
		// information for later assertions.
		releaseInfoProvider := &releaseinfo.RegistryClientProvider{}
		pullSecretFile, err := os.Open(clusterOpts.PullSecretFile)
		g.Expect(err).NotTo(HaveOccurred(), "failed to open pull secret file")
		defer pullSecretFile.Close()
		pullSecret, err := ioutil.ReadAll(pullSecretFile)
		g.Expect(err).NotTo(HaveOccurred(), "failed to read pull secret file")
		previousReleaseInfo, err := releaseInfoProvider.Lookup(ctx, PreviousReleaseImage, pullSecret)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get release info for previous image")
		latestReleaseInfo, err := releaseInfoProvider.Lookup(ctx, LatestReleaseImage, pullSecret)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get release info for latest image")

		// Define a new Nodepool
		nodePool := &hyperv1.NodePool{
			TypeMeta: metav1.TypeMeta{
				Kind:       "NodePool",
				APIVersion: hyperv1.GroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      guestCluster.Name + "-" + "test-upgrade-replace",
				Namespace: guestCluster.Namespace,
			},
			Spec: hyperv1.NodePoolSpec{
				Management: hyperv1.NodePoolManagement{
					UpgradeType: hyperv1.UpgradeTypeReplace,
					Replace: &hyperv1.ReplaceUpgrade{
						Strategy: hyperv1.UpgradeStrategyRollingUpdate,
						RollingUpdate: &hyperv1.RollingUpdate{
							MaxUnavailable: func(v intstr.IntOrString) *intstr.IntOrString { return &v }(intstr.FromInt(0)),
							MaxSurge:       func(v intstr.IntOrString) *intstr.IntOrString { return &v }(intstr.FromInt(3)),
						},
					},
				},
				ClusterName: guestCluster.Name,
				Replicas:    &twoReplicas,
				Release: hyperv1.Release{
					// TODO: Change the value to inherit from CI Job
					Image: PreviousReleaseImage,
				},
				Platform: hyperv1.NodePoolPlatform{
					Type: guestCluster.Spec.Platform.Type,
					AWS:  awsNPInfo,
				},
			},
		}

		// Create NodePool for current test
		err = mgmtClient.Create(ctx, nodePool)
		if err != nil {
			if !errors.IsAlreadyExists(err) {
				t.Fatalf("failed to create nodePool %s with Autorepair function: %v", nodePool.Name, err)
			}

			// Update NodePool
			existantNodePool := &hyperv1.NodePool{}
			// grab the existant nodepool and store it in another variable
			err = mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(nodePool), existantNodePool)
			g.Expect(err).NotTo(HaveOccurred(), "failed getting existant nodepool")
			err = mgmtClient.Delete(ctx, existantNodePool)
			g.Expect(err).NotTo(HaveOccurred(), "failed to Delete the existant NodePool")
			t.Logf("waiting for NodePools in-place update with NTO-generated MachineConfig")
			err = wait.PollImmediateWithContext(ctx, 10*time.Second, 15*time.Minute, func(ctx context.Context) (bool, error) {
				if ctx.Err() != nil {
					return false, err
				}
				err = mgmtClient.Create(ctx, nodePool)
				if errors.IsAlreadyExists(err) {
					t.Logf("WARNING: NodePool still there, will retry")
					return false, nil
				}
				return true, nil
			})
			t.Logf("Nodepool Recreated")
			g.Expect(err).NotTo(HaveOccurred(), "failed to Create the NodePool")
		}

		numZones := int32(len(clusterOpts.AWSPlatform.Zones))
		if numZones <= 1 {
			clusterOpts.NodePoolReplicas = 2
		} else if numZones == 2 {
			clusterOpts.NodePoolReplicas = 1
		} else {
			clusterOpts.NodePoolReplicas = 1
		}
		numNodes := clusterOpts.NodePoolReplicas * numZones

		t.Logf("Waiting for Nodes %d\n", numNodes)
		e2eutil.WaitForNReadyNodesByNodePool(t, ctx, guestClient, numNodes, guestCluster.Spec.Platform.Type, nodePool.Name)
		t.Logf("Desired replicas available for nodePool: %v", nodePool.Name)

		t.Logf("Wait for ReleaseVersion in deployed NodePool: %v", nodePool.Name)
		e2eutil.WaitForNodePoolVersion(t, ctx, mgmtClient, nodePool, previousReleaseInfo.Version())

		// Update NodePool images to the latest.
		t.Logf("Updating NodePool image. Image: %s", LatestReleaseImage)
		np := nodePool.DeepCopy()
		nodePool.Spec.Release.Image = LatestReleaseImage
		err = mgmtClient.Patch(ctx, nodePool, crclient.MergeFrom(np))
		g.Expect(err).NotTo(HaveOccurred(), "failed update NodePool image")

		// Check the upgrade is signalled in a condition.
		err = wait.PollUntil(5*time.Second, func() (done bool, err error) {
			err = mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(nodePool), nodePool)
			g.Expect(err).NotTo(HaveOccurred(), "failed to get NodePool")

			for _, condition := range nodePool.Status.Conditions {
				if condition.Type == hyperv1.NodePoolUpdatingVersionConditionType && condition.Status == corev1.ConditionTrue {
					return true, nil
				}
			}
			return false, nil
		}, ctx.Done())
		g.Expect(err).NotTo(HaveOccurred(), "failed to find UpdatingVersionCondition condition")
		t.Log("NodePool have UpdatingVersionCondition condition")

		// Wait for at least 1 Node to be unready, so we know the process is started.
		e2eutil.WaitForNUnReadyNodesByNodePool(t, ctx, guestClient, 1, nodePool.Name)
		t.Log("Upgrade has stated as at least 1 Node to is unready")
		e2eutil.WaitForNReadyNodesByNodePool(t, ctx, guestClient, numNodes, guestCluster.Spec.Platform.Type, nodePool.Name)

		// Wait for NodePools to roll out the latest version
		e2eutil.WaitForNodePoolVersion(t, ctx, mgmtClient, nodePool, latestReleaseInfo.Version())
		e2eutil.WaitForNReadyNodesByNodePool(t, ctx, guestClient, numNodes, guestCluster.Spec.Platform.Type, nodePool.Name)

		// Validate LatestReleaseInfo
		err = mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(nodePool), nodePool)
		g.Expect(err).NotTo(HaveOccurred(), "failed getting existant nodepool")
		latestReleaseInfo, err = releaseInfoProvider.Lookup(ctx, LatestReleaseImage, pullSecret)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get release info for latest image")
		latestVersion, err := latestReleaseInfo.ComponentVersions()
		g.Expect(latestVersion["release"]).To(Equal(nodePool.Status.Version))

		// Test Finished. Scalling down the NodePool to void waste resources
		err = mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(nodePool), nodePool)
		np = nodePool.DeepCopy()
		nodePool.Spec.Replicas = &zeroReplicas
		if err := mgmtClient.Patch(ctx, nodePool, crclient.MergeFrom(np)); err != nil {
			t.Fatalf("failed to downscale nodePool %s: %v", nodePool.Name, err)
		}
	}
}
