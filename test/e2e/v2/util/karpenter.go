//go:build e2ev2

package util

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	awskarpenterv1 "github.com/aws/karpenter-provider-aws/pkg/apis/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	karpenterv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	"github.com/onsi/ginkgo/v2"
)

// karpenterE2ETestNodeTaint keeps platform pods off NodePools created by BaseNodePool.
var karpenterE2ETestNodeTaint = corev1.Taint{
	Key:    "hypershift.io/test",
	Value:  "karpenter-test",
	Effect: corev1.TaintEffectNoSchedule,
}

// BaseNodePool returns a NodePool template used across Karpenter e2e tests.
// Nodes carry a test taint (karpenterE2ETestNodeTaint) so platform DaemonSet pods
// and other workloads without a matching toleration will not schedule on them.
// Any pod that needs to run on these nodes must tolerate this taint, either
// explicitly or via a wildcard toleration (TolerationOpExists).
func BaseNodePool(name, nodeClassName string) *karpenterv1.NodePool {
	return &karpenterv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: karpenterv1.NodePoolSpec{
			Disruption: karpenterv1.Disruption{
				// 60s mitigates the risk of consolidation from racing with drift
				// the default setting of 0s can cause replacement nodes to be deleted
				// before the drift drain begins.
				ConsolidateAfter: karpenterv1.MustParseNillableDuration("60s"),
			},
			Template: karpenterv1.NodeClaimTemplate{
				ObjectMeta: karpenterv1.ObjectMeta{
					Labels: map[string]string{
						"hypershift.openshift.io/nodepool-globalps-enabled": "true",
					},
				},
				Spec: karpenterv1.NodeClaimTemplateSpec{
					Requirements: []karpenterv1.NodeSelectorRequirementWithMinValues{
						{Key: "node.kubernetes.io/instance-type", Operator: corev1.NodeSelectorOpIn, Values: []string{"t3.xlarge"}},
						{Key: karpenterv1.CapacityTypeLabelKey, Operator: corev1.NodeSelectorOpIn, Values: []string{karpenterv1.CapacityTypeOnDemand}},
					},
					NodeClassRef: &karpenterv1.NodeClassReference{
						Group: "karpenter.k8s.aws",
						Kind:  "EC2NodeClass",
						Name:  nodeClassName,
					},
					Taints: []corev1.Taint{karpenterE2ETestNodeTaint},
				},
			},
		},
	}
}

// TestWorkload returns a sleep workload Deployment for Karpenter e2e tests.
// The pod template includes a toleration for BaseNodePool's test taint so
// the workload can schedule on Karpenter-provisioned nodes.
func TestWorkload(name string, replicas int32, nodeSelector map[string]string) *appsv1.Deployment {
	return TestWorkloadWithImage(name, replicas, nodeSelector, "quay.io/openshift/origin-pod:4.22.0")
}

// TestWorkloadWithImage returns a sleep workload Deployment using the given container image.
func TestWorkloadWithImage(name string, replicas int32, nodeSelector map[string]string, image string) *appsv1.Deployment {
	appLabel := map[string]string{"app": name}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(replicas),
			Selector: &metav1.LabelSelector{MatchLabels: appLabel},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: appLabel},
				Spec: corev1.PodSpec{
					Affinity: &corev1.Affinity{
						PodAntiAffinity: &corev1.PodAntiAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{{
								LabelSelector: &metav1.LabelSelector{MatchLabels: appLabel},
								TopologyKey:   "kubernetes.io/hostname",
							}},
						},
					},
					SecurityContext: &corev1.PodSecurityContext{
						RunAsUser:  ptr.To(int64(1000)),
						RunAsGroup: ptr.To(int64(3000)),
						FSGroup:    ptr.To(int64(2000)),
					},
					Containers: []corev1.Container{{
						Name:  name,
						Image: image,
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("250m"),
								corev1.ResourceMemory: resource.MustParse("256M"),
							},
						},
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: ptr.To(false),
						},
						Command: []string{"/bin/sh", "-c", "sleep infinity"},
					}},
					NodeSelector: nodeSelector,
					Tolerations: []corev1.Toleration{{
						Key:      karpenterE2ETestNodeTaint.Key,
						Operator: corev1.TolerationOpEqual,
						Value:    karpenterE2ETestNodeTaint.Value,
						Effect:   karpenterE2ETestNodeTaint.Effect,
					}},
				},
			},
		},
	}
}

// NewEC2Client returns an EC2 API client for guest-infra credentials.
func NewEC2Client(ctx context.Context, awsCredsFile, region string) *ec2.Client {
	awsSession := awsutil.NewSession(ctx, "hypershift-e2e", awsCredsFile, "", "", region)
	awsConfig := awsutil.NewConfig()
	return ec2.NewFromConfig(*awsSession, func(o *ec2.Options) {
		o.Retryer = awsConfig()
	})
}

// ExpectedPlatformTags returns non-restricted AWS resource tags from the HostedCluster spec.
func ExpectedPlatformTags(g Gomega, hc *hyperv1.HostedCluster) map[string]string {
	g.Expect(hc).NotTo(BeNil(), "HostedCluster must not be nil")
	tags := make(map[string]string)
	if hc.Spec.Platform.AWS == nil {
		return tags
	}
	for _, tag := range hc.Spec.Platform.AWS.ResourceTags {
		restricted := false
		for _, pattern := range awskarpenterv1.RestrictedTagPatterns {
			if pattern.MatchString(tag.Key) {
				restricted = true
				break
			}
		}
		if !restricted {
			tags[tag.Key] = tag.Value
		}
	}
	return tags
}

// DescribeEC2Instance loads the EC2 instance for a Kubernetes Node provider ID.
func DescribeEC2Instance(g Gomega, ctx context.Context, ec2client *ec2.Client, node corev1.Node) (ec2types.Instance, string) {
	g.Expect(ec2client).NotTo(BeNil(), "EC2 client must not be nil")
	providerID := node.Spec.ProviderID
	g.Expect(providerID).NotTo(BeEmpty(), "node should have a providerID")

	parts := strings.Split(providerID, "/")
	g.Expect(parts).To(HaveLen(5), "providerID should have 5 parts")
	instanceID := parts[4]

	result, err := ec2client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{instanceID},
	})
	g.Expect(err).NotTo(HaveOccurred(), "failed to describe EC2 instance %s", instanceID)
	g.Expect(result.Reservations).NotTo(BeEmpty(), "expected at least one reservation")
	g.Expect(result.Reservations[0].Instances).NotTo(BeEmpty(), "expected at least one instance")
	return result.Reservations[0].Instances[0], instanceID
}

// WaitForAutoNodeStatusVCPUs polls until HostedCluster.Status.AutoNode.VCPUs converges to expected.
// This checks only the status field (Karpenter-only vCPUs), not the billing metric.
func WaitForAutoNodeStatusVCPUs(g Gomega, ctx context.Context, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster, expected int32) {
	ginkgo.GinkgoHelper()
	g.Expect(hostedCluster).NotTo(BeNil(), "HostedCluster must not be nil")
	ginkgo.GinkgoWriter.Printf("Validating AutoNode.VCPUs converges to %d\n", expected)
	objective := fmt.Sprintf("HostedCluster %s/%s AutoNode.VCPUs=%d", hostedCluster.Namespace, hostedCluster.Name, expected)
	g.Eventually(func(pollG Gomega, pollCtx context.Context) {
		hc := &hyperv1.HostedCluster{}
		pollG.Expect(mgtClient.Get(pollCtx, crclient.ObjectKeyFromObject(hostedCluster), hc)).To(Succeed(), "%s: failed to get HostedCluster", objective)
		if hc.Status.AutoNode.VCPUs == nil {
			pollG.Expect(hc.Status.AutoNode.VCPUs).NotTo(BeNil(), "%s: AutoNode.VCPUs is nil", objective)
			return
		}
		actual := *hc.Status.AutoNode.VCPUs
		pollG.Expect(actual).To(Equal(expected), "%s: AutoNode.VCPUs=%d, want %d", objective, actual, expected)
	}).
		WithContext(ctx).
		WithTimeout(1 * time.Minute).
		WithPolling(3 * time.Second).
		Should(Succeed())
}

// WaitForAutoNodeStatusVCPUsStable asserts AutoNode.VCPUs stays at expected for duration.
func WaitForAutoNodeStatusVCPUsStable(g Gomega, ctx context.Context, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster, expected int32, duration time.Duration) {
	ginkgo.GinkgoHelper()
	g.Expect(hostedCluster).NotTo(BeNil(), "HostedCluster must not be nil")
	g.Consistently(func(pollG Gomega) {
		hc := &hyperv1.HostedCluster{}
		pollG.Expect(mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hc)).To(Succeed())
		pollG.Expect(hc.Status.AutoNode.VCPUs).NotTo(BeNil(), "AutoNode.VCPUs became nil")
		pollG.Expect(*hc.Status.AutoNode.VCPUs).To(Equal(expected))
	}).WithTimeout(duration).WithPolling(2 * time.Second).Should(Succeed())
}

// CreateKarpenterNodePoolAndWorkload creates a NodePool and workload Deployment in the hosted cluster.
func CreateKarpenterNodePoolAndWorkload(
	g Gomega,
	ctx context.Context,
	hcClient crclient.Client,
	platform hyperv1.PlatformType,
	nodePool *karpenterv1.NodePool,
	workload *appsv1.Deployment,
	nodeLabels map[string]string,
	skipCleanup bool,
) {
	ginkgo.GinkgoHelper()
	g.Expect(nodePool).NotTo(BeNil(), "NodePool must not be nil")
	g.Expect(workload).NotTo(BeNil(), "workload Deployment must not be nil")
	ginkgo.By(fmt.Sprintf("Creating Karpenter NodePool %q and workload deployment %q", nodePool.Name, workload.Name))
	g.Expect(hcClient.Create(ctx, nodePool)).To(Succeed())
	if !skipCleanup {
		ginkgo.DeferCleanup(func() {
			if err := hcClient.Delete(ctx, nodePool); err != nil {
				if apierrors.IsNotFound(err) {
					return
				}
				Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete NodePool %s", nodePool.Name)
			}
			waitForReadyNodeCountByLabels(Default, ctx, hcClient, platform, 0, nodeLabels)
		})
	}
	WaitForKarpenterNodePoolReady(g, ctx, hcClient, nodePool)
	g.Expect(hcClient.Create(ctx, workload)).To(Succeed())
	if !skipCleanup {
		ginkgo.DeferCleanup(func() {
			if err := hcClient.Delete(ctx, workload); err != nil && !apierrors.IsNotFound(err) {
				Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete Deployment %s", workload.Name)
			}
		})
	}
}

// WaitForKarpenterNodePoolReady waits until validation and NodeClass readiness conditions are True.
func WaitForKarpenterNodePoolReady(g Gomega, ctx context.Context, hcClient crclient.Client, nodePool *karpenterv1.NodePool) {
	ginkgo.GinkgoHelper()
	g.Expect(nodePool).NotTo(BeNil(), "NodePool must not be nil")
	ginkgo.By(fmt.Sprintf("Waiting for Karpenter NodePool %q to be ready", nodePool.Name))
	g.Eventually(func(pollG Gomega, pollCtx context.Context) {
		np := &karpenterv1.NodePool{}
		pollG.Expect(hcClient.Get(pollCtx, crclient.ObjectKeyFromObject(nodePool), np)).To(Succeed())
		for _, want := range []string{karpenterv1.ConditionTypeValidationSucceeded, karpenterv1.ConditionTypeNodeClassReady} {
			var found bool
			for _, c := range np.Status.Conditions {
				if c.Type == want {
					found = true
					pollG.Expect(c.Status).To(Equal(metav1.ConditionTrue), "NodePool %s condition %s", np.Name, want)
				}
			}
			pollG.Expect(found).To(BeTrue(), "NodePool %s missing condition %s", np.Name, want)
		}
	}).
		WithContext(ctx).
		WithTimeout(5 * time.Minute).
		WithPolling(3 * time.Second).
		Should(Succeed())
}

// WaitForReadyKarpenterPods waits until numPods matching podLabels are running on expected nodes.
func WaitForReadyKarpenterPods(g Gomega, ctx context.Context, client crclient.Client, includedNodes, excludedNodes []corev1.Node, numPods int, podLabels map[string]string) []corev1.Pod {
	ginkgo.GinkgoHelper()
	var matchedPods []corev1.Pod

	g.Eventually(func(pollG Gomega, pollCtx context.Context) {
		pods := &corev1.PodList{}
		pollG.Expect(client.List(pollCtx, pods, crclient.InNamespace("default"), crclient.MatchingLabels(podLabels))).To(Succeed())
		pollG.Expect(pods.Items).To(HaveLen(numPods), "expected %d pods, got %d", numPods, len(pods.Items))

		for i := range pods.Items {
			pod := &pods.Items[i]
			pollG.Expect(pod.Spec.NodeName).NotTo(BeEmpty(), "pod %s is not scheduled", pod.Name)

			for _, node := range excludedNodes {
				pollG.Expect(pod.Spec.NodeName).NotTo(Equal(node.Name),
					"pod %s incorrectly scheduled on excluded node %q", pod.Name, node.Name)
			}

			if len(includedNodes) > 0 {
				onIncludedNode := false
				for _, node := range includedNodes {
					if pod.Spec.NodeName == node.Name {
						onIncludedNode = true
						break
					}
				}
				pollG.Expect(onIncludedNode).To(BeTrue(),
					"pod %s scheduled on unexpected node %s", pod.Name, pod.Spec.NodeName)
			}

			scheduled := false
			for _, condition := range pod.Status.Conditions {
				if condition.Type == corev1.PodScheduled && condition.Status == corev1.ConditionTrue {
					scheduled = true
					break
				}
			}
			pollG.Expect(scheduled).To(BeTrue(), "pod %s does not have PodScheduled=True", pod.Name)
			pollG.Expect(pod.Status.Phase).To(Equal(corev1.PodRunning), "pod %s is not running", pod.Name)

			ready := false
			for _, condition := range pod.Status.Conditions {
				if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
					ready = true
					break
				}
			}
			pollG.Expect(ready).To(BeTrue(), "pod %s does not have PodReady=True", pod.Name)
		}

		matchedPods = pods.Items
	}).
		WithContext(ctx).
		WithTimeout(20 * time.Minute).
		WithPolling(3 * time.Second).
		Should(Succeed())

	return matchedPods
}

// waitForReadyNodeCountByLabels mirrors e2eutil.WaitForReadyNodesByLabels but lives here to avoid
// an import cycle (test/e2e/util imports this package for certs).
func waitForReadyNodeCountByLabels(g Gomega, ctx context.Context, client crclient.Client, platform hyperv1.PlatformType, want int32, nodeLabels map[string]string) {
	ginkgo.GinkgoHelper()
	timeout := 45 * time.Minute
	if platform == hyperv1.PowerVSPlatform {
		timeout = 60 * time.Minute
	}
	g.Eventually(func(pollG Gomega, pollCtx context.Context) {
		nodes := &corev1.NodeList{}
		pollG.Expect(client.List(pollCtx, nodes, crclient.MatchingLabelsSelector{Selector: labels.SelectorFromSet(labels.Set(nodeLabels))})).To(Succeed())
		ready := 0
		for i := range nodes.Items {
			for _, c := range nodes.Items[i].Status.Conditions {
				if c.Type == corev1.NodeReady && c.Status == corev1.ConditionTrue {
					ready++
					break
				}
			}
		}
		pollG.Expect(ready).To(Equal(int(want)), "expected %d ready nodes with labels %v, got %d", want, nodeLabels, ready)
	}).
		WithContext(ctx).
		WithTimeout(timeout).
		WithPolling(3 * time.Second).
		Should(Succeed())
}
