//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	awskarpenterv1 "github.com/aws/karpenter-provider-aws/pkg/apis/v1"
	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/releaseinfo"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	karpenterv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/yaml"
)

func TestKarpenter(t *testing.T) {
	e2eutil.AtLeast(t, e2eutil.Version419)
	if os.Getenv("TECH_PREVIEW_NO_UPGRADE") != "true" {
		t.Skipf("Only tested when CI sets TECH_PREVIEW_NO_UPGRADE=true and the Hypershift Operator is installed with --tech-preview-no-upgrade")
	}
	if globalOpts.Platform != hyperv1.AWSPlatform {
		t.Skip("test only supported on platform AWS")
	}
	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)
	clusterOpts.AWSPlatform.AutoNode = true
	clusterOpts.ControlPlaneAvailabilityPolicy = string(hyperv1.HighlyAvailable)
	clusterOpts.ReleaseImage = globalOpts.PreviousReleaseImage

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		guestClient := e2eutil.WaitForGuestClient(t, ctx, mgtClient, hostedCluster)

		// Unmarshal Karpenter NodePool.
		karpenterNodePool := &unstructured.Unstructured{}
		yamlFile, err := content.ReadFile(fmt.Sprintf("assets/%s", "karpenter-nodepool.yaml"))
		g.Expect(err).NotTo(HaveOccurred())
		err = yaml.Unmarshal(yamlFile, karpenterNodePool)
		g.Expect(err).NotTo(HaveOccurred())

		// Unmarshal workloads.
		workLoads := &unstructured.Unstructured{}
		yamlFile, err = content.ReadFile(fmt.Sprintf("assets/%s", "karpenter-workloads.yaml"))
		g.Expect(err).NotTo(HaveOccurred())
		err = yaml.Unmarshal(yamlFile, workLoads)
		g.Expect(err).NotTo(HaveOccurred())

		nodeLabels := map[string]string{
			"node.kubernetes.io/instance-type": "t3.large",
			"karpenter.sh/nodepool":            karpenterNodePool.GetName(),
		}

		t.Run("Control plane upgrade and Karpenter Drift", func(t *testing.T) {
			g := NewWithT(t)

			t.Logf("Starting Karpenter control plane upgrade. FromImage: %s, toImage: %s", globalOpts.PreviousReleaseImage, globalOpts.LatestReleaseImage)

			// Lookup os and kubelet versions of the latestReleaseImage
			releaseProvider := &releaseinfo.RegistryClientProvider{}
			pullSecret, err := os.ReadFile(clusterOpts.PullSecretFile)
			g.Expect(err).NotTo(HaveOccurred())

			latestReleaseImage, err := releaseProvider.Lookup(ctx, globalOpts.LatestReleaseImage, pullSecret)
			g.Expect(err).NotTo(HaveOccurred())
			releaseImageComponentVersions, err := latestReleaseImage.ComponentVersions()
			g.Expect(err).NotTo(HaveOccurred())

			expectedRHCOSVersion := releaseImageComponentVersions["machine-os"]
			g.Expect(expectedRHCOSVersion).NotTo(BeEmpty())
			expectedKubeletVersion := releaseImageComponentVersions["kubernetes"]
			g.Expect(expectedKubeletVersion).NotTo(BeEmpty())

			replicas := 1
			workLoads.Object["spec"].(map[string]interface{})["replicas"] = replicas

			// Apply both Karpenter NodePool and workloads.
			defer guestClient.Delete(ctx, karpenterNodePool)
			defer guestClient.Delete(ctx, workLoads)
			g.Expect(guestClient.Create(ctx, karpenterNodePool)).To(Succeed())
			t.Logf("Created Karpenter NodePool")
			g.Expect(guestClient.Create(ctx, workLoads)).To(Succeed())
			t.Logf("Created workloads")

			// Wait for Nodes, NodeClaims and Pods to be ready.
			nodes := e2eutil.WaitForReadyNodesByLabels(t, ctx, guestClient, hostedCluster.Spec.Platform.Type, int32(replicas), nodeLabels)
			nodeClaims := waitForReadyNodeClaims(t, ctx, guestClient, len(nodes))
			waitForReadyKarpenterPods(t, ctx, guestClient, nodes, replicas)

			// Update hosted control plane to induce Drift
			t.Logf("Updating cluster image. Image: %s", globalOpts.LatestReleaseImage)
			err = e2eutil.UpdateObject(t, ctx, mgtClient, hostedCluster, func(obj *hyperv1.HostedCluster) {
				obj.Spec.Release.Image = globalOpts.LatestReleaseImage
				if obj.Annotations == nil {
					obj.Annotations = make(map[string]string)
				}
				obj.Annotations[hyperv1.ForceUpgradeToAnnotation] = globalOpts.LatestReleaseImage
				if globalOpts.DisablePKIReconciliation {
					obj.Annotations[hyperv1.DisablePKIReconciliationAnnotation] = "true"
				}
			})
			g.Expect(err).NotTo(HaveOccurred(), "failed update hostedcluster image")

			// Check that the NodeClaim(s) actually Drift
			driftChan := make(chan struct{})
			go func() {
				defer close(driftChan)
				for _, nodeClaim := range nodeClaims.Items {
					waitForNodeClaimDrifted(t, ctx, guestClient, &nodeClaim)
				}
			}()

			// Wait for the new rollout to be complete
			e2eutil.WaitForImageRollout(t, ctx, mgtClient, hostedCluster)
			err = mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
			g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")

			// Ensure Karpenter Drift behaviour
			t.Logf("Waiting for Karpenter Nodes to drift and come up")
			<-driftChan

			nodes = e2eutil.WaitForNReadyNodesWithOptions(t, ctx, guestClient, int32(replicas), hyperv1.AWSPlatform, "",
				e2eutil.WithClientOptions(
					crclient.MatchingLabelsSelector{Selector: labels.SelectorFromSet(labels.Set(nodeLabels))},
				),
				e2eutil.WithPredicates(
					e2eutil.ConditionPredicate[*corev1.Node](e2eutil.Condition{
						Type:   string(corev1.NodeReady),
						Status: metav1.ConditionTrue,
					}),
					e2eutil.Predicate[*corev1.Node](func(node *corev1.Node) (done bool, reasons string, err error) {
						// the actual OS version is at the end of the node's OSImage field
						fullOSImageString := node.Status.NodeInfo.OSImage
						parts := strings.Split(fullOSImageString, " ")
						if len(parts) <= 1 {
							return false, "", fmt.Errorf("unexpected OSImage format: %s", fullOSImageString)
						}
						rawVersion := parts[len(parts)-2]
						if rawVersion != expectedRHCOSVersion {
							return false, fmt.Sprintf("expected %s, got %s", expectedRHCOSVersion, rawVersion), nil
						}

						// the node's KubeletVersion field is prefixed, but the releaseImageComponent version is not
						rawKubeletVersion := strings.TrimPrefix(node.Status.NodeInfo.KubeletVersion, "v")
						if rawKubeletVersion != expectedKubeletVersion {
							return false, fmt.Sprintf("expected %s, got %s", expectedKubeletVersion, rawKubeletVersion), nil
						}
						correctMachineOSVersionMessage := fmt.Sprintf("correct machineOS: wanted %s, got %s", expectedRHCOSVersion, rawVersion)
						correctK8sVersionMessage := fmt.Sprintf("correct kube: wanted %s, got %s", expectedKubeletVersion, rawKubeletVersion)
						return true, fmt.Sprintf("%s, %s", correctMachineOSVersionMessage, correctK8sVersionMessage), nil
					}),
				),
			)

			t.Logf("Waiting for Karpenter pods to schedule on the new node")
			waitForReadyKarpenterPods(t, ctx, guestClient, nodes, replicas)

			// Test we can delete both Karpenter NodePool and workloads.
			g.Expect(guestClient.Delete(ctx, karpenterNodePool)).To(Succeed())
			t.Logf("Deleted Karpenter NodePool")
			g.Expect(guestClient.Delete(ctx, workLoads)).To(Succeed())
			t.Logf("Delete workloads")

			// Wait for Karpenter Nodes to go away.
			t.Logf("Waiting for Karpenter Nodes to disappear")
			_ = e2eutil.WaitForReadyNodesByLabels(t, ctx, guestClient, hostedCluster.Spec.Platform.Type, 0, nodeLabels)
		})

		t.Run("Test basic provisioning and deprovising", func(t *testing.T) {
			// Test that we can provision as many nodes as needed (in this case, we need 3 nodes for 3 replicas)
			replicas := 3
			workLoads.Object["spec"].(map[string]interface{})["replicas"] = replicas
			workLoads.Object["spec"].(map[string]interface{})["containers"] = []interface{}{
				map[string]interface{}{
					"resources": map[string]interface{}{
						"requests": map[string]interface{}{
							"cpu":    "1", // set to 1 CPU since t3.large has 2 vCPUs and cannot fit more than 1 replica
							"memory": "256M",
						},
					},
				},
			}
			workLoads.SetResourceVersion("")
			karpenterNodePool.SetResourceVersion("")

			// Leave dangling resources, and hope the teardown is not blocked, else the test will fail.
			g.Expect(guestClient.Create(ctx, karpenterNodePool)).To(Succeed())
			t.Logf("Created Karpenter NodePool")
			g.Expect(guestClient.Create(ctx, workLoads)).To(Succeed())
			t.Logf("Created workloads")

			_ = e2eutil.WaitForReadyNodesByLabels(t, ctx, guestClient, hostedCluster.Spec.Platform.Type, int32(replicas), nodeLabels)

			ec2NodeClassList := &awskarpenterv1.EC2NodeClassList{}
			g.Expect(guestClient.List(ctx, ec2NodeClassList)).To(Succeed())
			g.Expect(ec2NodeClassList.Items).ToNot(BeEmpty())

			ec2NodeClass := ec2NodeClassList.Items[0]
			g.Expect(guestClient.Delete(ctx, &ec2NodeClass)).To(MatchError(ContainSubstring("EC2NodeClass resource can't be created/updated/deleted directly, please use OpenshiftEC2NodeClass resource instead")))
		})

		// TODO(alberto): increase coverage:
		// - Karpenter operator plumbing, e.g:
		// -- validate the CRDs are installed
		// -- validate the default class is created and has expected values
		// -- validate admin can't modify fields owned by the service, e.g. ami.
		// - Karpenter functionality:
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "karpenter", globalOpts.ServiceAccountSigningKey)
}

func waitForReadyKarpenterPods(t *testing.T, ctx context.Context, client crclient.Client, nodes []corev1.Node, n int) []corev1.Pod {
	pods := &corev1.PodList{}
	waitTimeout := 10 * time.Minute
	e2eutil.EventuallyObjects(t, ctx, fmt.Sprintf("Pods to be scheduled on provisioned Karpenter nodes"),
		func(ctx context.Context) ([]*corev1.Pod, error) {
			err := client.List(ctx, pods, crclient.InNamespace("default"))
			items := make([]*corev1.Pod, len(pods.Items))
			for i := range pods.Items {
				items[i] = &pods.Items[i]
			}
			return items, err
		},
		[]e2eutil.Predicate[[]*corev1.Pod]{
			func(pods []*corev1.Pod) (done bool, reasons string, err error) {
				want, got := int(n), len(pods)
				return want == got, fmt.Sprintf("expected %d nodes, got %d", want, got), nil
			},
		},
		[]e2eutil.Predicate[*corev1.Pod]{
			e2eutil.ConditionPredicate[*corev1.Pod](e2eutil.Condition{
				Type:   string(corev1.PodScheduled),
				Status: metav1.ConditionTrue,
			}),
			e2eutil.Predicate[*corev1.Pod](func(pod *corev1.Pod) (done bool, reasons string, err error) {
				nodeName := pod.Spec.NodeName
				for _, node := range getNodeNames(nodes) {
					if nodeName == node {
						return true, fmt.Sprintf("correctly scheduled on one of the specified nodes %s", nodeName), nil
					}
				}
				return false, fmt.Sprintf("expected at least one of the nodes %v, got %s", getNodeNames(nodes), nodeName), nil
			}),
		},
		e2eutil.WithTimeout(waitTimeout),
	)
	return pods.Items
}

func waitForReadyNodeClaims(t *testing.T, ctx context.Context, client crclient.Client, n int) *karpenterv1.NodeClaimList {
	nodeClaims := &karpenterv1.NodeClaimList{}
	waitTimeout := 5 * time.Minute
	e2eutil.EventuallyObjects(t, ctx, fmt.Sprintf("NodeClaims to be ready"),
		func(ctx context.Context) ([]*karpenterv1.NodeClaim, error) {
			err := client.List(ctx, nodeClaims)
			if err != nil {
				return nil, err
			}
			items := make([]*karpenterv1.NodeClaim, 0)
			for i := range nodeClaims.Items {
				items = append(items, &nodeClaims.Items[i])
			}
			return items, nil
		},
		[]e2eutil.Predicate[[]*karpenterv1.NodeClaim]{
			func(claims []*karpenterv1.NodeClaim) (done bool, reasons string, err error) {
				want, got := n, len(claims)
				return want == got, fmt.Sprintf("expected %d NodeClaims, got %d", want, got), nil
			},
		},
		[]e2eutil.Predicate[*karpenterv1.NodeClaim]{
			func(claim *karpenterv1.NodeClaim) (done bool, reasons string, err error) {
				hasLaunched := false
				hasRegistered := false
				hasInitialized := false

				for _, condition := range claim.Status.Conditions {
					if condition.Type == karpenterv1.ConditionTypeLaunched && condition.Status == metav1.ConditionTrue {
						hasLaunched = true
					}
					if condition.Type == karpenterv1.ConditionTypeRegistered && condition.Status == metav1.ConditionTrue {
						hasRegistered = true
					}
					if condition.Type == karpenterv1.ConditionTypeInitialized && condition.Status == metav1.ConditionTrue {
						hasInitialized = true
					}
				}

				if !hasLaunched || !hasRegistered || !hasInitialized {
					return false, fmt.Sprintf("NodeClaim %s not ready: Launched=%v, Registered=%v, Initialized=%v",
						claim.Name, hasLaunched, hasRegistered, hasInitialized), nil
				}
				return true, "", nil
			},
		},
		e2eutil.WithTimeout(waitTimeout),
	)

	return nodeClaims
}

func waitForNodeClaimDrifted(t *testing.T, ctx context.Context, client crclient.Client, nc *karpenterv1.NodeClaim) {
	waitTimeout := 5 * time.Minute
	e2eutil.EventuallyObject(t, ctx, fmt.Sprintf("NodeClaim %s to be drifted", nc.Name),
		func(ctx context.Context) (*karpenterv1.NodeClaim, error) {
			nodeClaim := &karpenterv1.NodeClaim{}
			err := client.Get(ctx, crclient.ObjectKeyFromObject(nc), nodeClaim)
			// make sure that the condition actually exists first
			if err == nil {
				haystack, err := e2eutil.Conditions(nodeClaim)
				if err != nil {
					return nil, err
				}
				for _, condition := range haystack {
					if karpenterv1.ConditionTypeDrifted == condition.Type {
						if condition.Status == metav1.ConditionTrue {
							return nodeClaim, nil
						}
						return nil, fmt.Errorf("condition %s is not True in NodeClaim %s", karpenterv1.ConditionTypeDrifted, nc.Name)
					}
				}
				return nil, fmt.Errorf("condition %s not found in NodeClaim %s", karpenterv1.ConditionTypeDrifted, nc.Name)
			} else {
				return nil, err
			}
		},
		[]e2eutil.Predicate[*karpenterv1.NodeClaim]{
			e2eutil.ConditionPredicate[*karpenterv1.NodeClaim](e2eutil.Condition{
				Type:   karpenterv1.ConditionTypeDrifted,
				Status: metav1.ConditionTrue,
			}),
		},
		e2eutil.WithTimeout(waitTimeout),
	)
}

// getNodeNames returns the names of the nodes in the list
func getNodeNames(nodes []corev1.Node) []string {
	nodeNames := make([]string, len(nodes))
	for i, node := range nodes {
		nodeNames[i] = node.Name
	}
	return nodeNames
}
