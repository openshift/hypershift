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

		// Apply both Karpenter NodePool and workloads.
		defer guestClient.Delete(ctx, karpenterNodePool)
		defer guestClient.Delete(ctx, workLoads)
		g.Expect(guestClient.Create(ctx, karpenterNodePool)).To(Succeed())
		t.Logf("Created Karpenter NodePool")
		g.Expect(guestClient.Create(ctx, workLoads)).To(Succeed())
		t.Logf("Created workloads")

		// Wait for Karpenter Nodes.
		nodeLabels := map[string]string{
			"node.kubernetes.io/instance-type": "t3.large",
			"karpenter.sh/nodepool":            karpenterNodePool.GetName(),
		}

		t.Logf("Waiting for Karpenter Nodes to come up")
		_ = e2eutil.WaitForReadyNodesByLabels(t, ctx, guestClient, hostedCluster.Spec.Platform.Type, 3, nodeLabels)

		// Delete both Karpenter NodePool and workloads.
		g.Expect(guestClient.Delete(ctx, karpenterNodePool)).To(Succeed())
		t.Logf("Deleted Karpenter NodePool")
		g.Expect(guestClient.Delete(ctx, workLoads)).To(Succeed())
		t.Logf("Delete workloads")

		// Wait for Karpenter Nodes to go away.
		_ = e2eutil.WaitForReadyNodesByLabels(t, ctx, guestClient, hostedCluster.Spec.Platform.Type, 0, nodeLabels)
		t.Logf("Waiting for Karpenter Nodes to disappear")

		karpenterNodePool.SetResourceVersion("")
		workLoads.SetResourceVersion("")

		// Leave dangling resources, and hope the teardown is not blocked, else the test will fail.
		g.Expect(guestClient.Create(ctx, karpenterNodePool)).To(Succeed())
		t.Logf("Created Karpenter NodePool")
		g.Expect(guestClient.Create(ctx, workLoads)).To(Succeed())
		t.Logf("Created workloads")

		t.Logf("Waiting for Karpenter Nodes to come up")
		_ = e2eutil.WaitForReadyNodesByLabels(t, ctx, guestClient, hostedCluster.Spec.Platform.Type, 3, nodeLabels)

		ec2NodeClassList := &awskarpenterv1.EC2NodeClassList{}
		g.Expect(guestClient.List(ctx, ec2NodeClassList)).To(Succeed())
		g.Expect(ec2NodeClassList.Items).ToNot(BeEmpty())

		ec2NodeClass := ec2NodeClassList.Items[0]
		g.Expect(guestClient.Delete(ctx, &ec2NodeClass)).To(MatchError(ContainSubstring("EC2NodeClass resource can't be created/updated/deleted directly, please use OpenshiftEC2NodeClass resource instead")))

		// TODO(alberto): increase coverage:
		// - Karpenter operator plumbing, e.g:
		// -- validate the CRDs are installed
		// -- validate the default class is created and has expected values
		// -- validate admin can't modify fields owned by the service, e.g. ami.
		// - Karpenter functionality:
		// -- Drift and Upgrades
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "karpenter", globalOpts.ServiceAccountSigningKey)
}

func TestKarpenterControlPlaneUpgrade(t *testing.T) {
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

	t.Logf("Starting Karpenter control plane upgrade test. FromImage: %s, toImage: %s", globalOpts.PreviousReleaseImage, globalOpts.LatestReleaseImage)

	clusterOpts := globalOpts.DefaultClusterOptions(t)
	clusterOpts.ReleaseImage = globalOpts.PreviousReleaseImage
	clusterOpts.ControlPlaneAvailabilityPolicy = string(hyperv1.HighlyAvailable)
	clusterOpts.AWSPlatform.AutoNode = true
	clusterOpts.NodePoolReplicas = 1 // No need for additional hyperv1.NodePool nodes

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		guestClient := e2eutil.WaitForGuestClient(t, ctx, mgtClient, hostedCluster)

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

		replicas := 1
		workLoads.Object["spec"].(map[string]interface{})["replicas"] = replicas

		// Apply both Karpenter NodePool and workloads.
		defer guestClient.Delete(ctx, karpenterNodePool)
		defer guestClient.Delete(ctx, workLoads)
		g.Expect(guestClient.Create(ctx, karpenterNodePool)).To(Succeed())
		t.Logf("Created Karpenter NodePool")
		g.Expect(guestClient.Create(ctx, workLoads)).To(Succeed())
		t.Logf("Created workloads")

		// Wait for Karpenter Nodes.
		nodeLabels := map[string]string{
			"node.kubernetes.io/instance-type": "t3.large",
			"karpenter.sh/nodepool":            karpenterNodePool.GetName(),
		}

		t.Logf("Waiting for Karpenter Nodes to come up")
		nodes := e2eutil.WaitForReadyNodesByLabels(t, ctx, guestClient, hostedCluster.Spec.Platform.Type, int32(replicas), nodeLabels)

		t.Logf("Waiting for workloads to be scheduled")
		waitForReadyKarpenterPods(t, ctx, guestClient, nodes, replicas)

		// Update hosted control plane to induce drift
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

		// Wait for the new rollout to be complete
		e2eutil.WaitForImageRollout(t, ctx, mgtClient, hostedCluster)
		err = mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")

		// Ensure karpenter drift behaviour
		t.Logf("Waiting for Karpenter Nodes to drift and come up")
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
					if len(parts) == 0 {
						return false, "", fmt.Errorf("unexpected OSImage format: %s", fullOSImageString)
					}
					rawVersion := parts[len(parts)-1]
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
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "karpenter", globalOpts.ServiceAccountSigningKey)
}

func waitForReadyKarpenterPods(t *testing.T, ctx context.Context, client crclient.Client, nodes []corev1.Node, n int) []corev1.Pod {
	pods := &corev1.PodList{}
	waitTimeout := 10 * time.Minute

	e2eutil.EventuallyObjects(t, ctx, fmt.Sprintf("Pods to be rescheduled on the new karpenter nodes"),
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

// getNodeNames returns the names of the nodes in the list
func getNodeNames(nodes []corev1.Node) []string {
	nodeNames := make([]string, len(nodes))
	for i, node := range nodes {
		nodeNames[i] = node.Name
	}
	return nodeNames
}
