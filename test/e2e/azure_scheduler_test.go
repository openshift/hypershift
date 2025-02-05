//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	schedulingv1alpha1 "github.com/openshift/hypershift/api/scheduling/v1alpha1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedclustersizing"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"
	"reflect"
	"sort"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// TestAzureScheduler tests the Azure scheduler and depends:
//   - the HypershiftOperator running with size tagging enabled.
//   - the HostedCluster is running on Azure.
//   - the HostedCluster has a NodePool with 2 replicas.
//   - the NodePool is using Standard_D4s_v3 VMs.
func TestAzureScheduler(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)

	if globalOpts.Platform != "Azure" {
		t.Skip("Skipping test because it requires Azure")
	}

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		guestClient := e2eutil.WaitForGuestClient(t, ctx, mgtClient, hostedCluster)
		numNodes := clusterOpts.NodePoolReplicas
		_ = e2eutil.WaitForNReadyNodes(t, ctx, guestClient, numNodes, hostedCluster.Spec.Platform.Type)

		hcAnnotations := map[string]string{
			"resource-request-override.hypershift.openshift.io/control-plane-operator.control-plane-operator": "cpu=300m",
		}

		controlPlaneNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)

		updateClusterSizingConfig(ctx, t, g, mgtClient)
		checkHCSizeAndAnnotations(ctx, t, mgtClient, hostedCluster, "small", nil)
		scaleNodePool(ctx, t, g, mgtClient, guestClient, hostedCluster)
		checkHCSizeAndAnnotations(ctx, t, mgtClient, hostedCluster, "medium", hcAnnotations)
		checkCPOPodRescheduled(ctx, t, mgtClient, controlPlaneNamespace)
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, globalOpts.ServiceAccountSigningKey)
}

func updateClusterSizingConfig(ctx context.Context, t *testing.T, g Gomega, mgtClient crclient.Client) {
	// Get the default ClusterSizingConfig
	defaultClusterSizingConfig := hostedclustersizing.DefaultSizingConfig()
	err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(defaultClusterSizingConfig), defaultClusterSizingConfig)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get clusterSizingConfig")

	// Update the default ClusterSizingConfig
	originalClusterSizingConfig := defaultClusterSizingConfig.DeepCopy()
	defaultClusterSizingConfig.Spec = schedulingv1alpha1.ClusterSizingConfigurationSpec{
		Sizes: []schedulingv1alpha1.SizeConfiguration{
			{
				Name: "small",
				Criteria: schedulingv1alpha1.NodeCountCriteria{
					From: 0,
					To:   ptr.To(uint32(2)),
				},
			},
			{
				Name: "medium",
				Criteria: schedulingv1alpha1.NodeCountCriteria{
					From: 3,
					To:   ptr.To(uint32(4)),
				},
				Effects: &schedulingv1alpha1.Effects{
					ResourceRequests: []schedulingv1alpha1.ResourceRequest{
						{
							DeploymentName: "kube-apiserver",
							ContainerName:  "kube-apiserver",
							CPU:            resource.NewMilliQuantity(300, resource.DecimalSI),
						},
						{
							DeploymentName: "control-plane-operator",
							ContainerName:  "control-plane-operator",
							CPU:            resource.NewMilliQuantity(300, resource.DecimalSI),
						},
					},
				},
			},
			{
				Name: "large",
				Criteria: schedulingv1alpha1.NodeCountCriteria{
					From: 5,
				},
			},
		},
	}

	err = mgtClient.Patch(ctx, defaultClusterSizingConfig, crclient.MergeFrom(originalClusterSizingConfig))
	g.Expect(err).NotTo(HaveOccurred(), "failed to update clusterSizingConfig")
	t.Logf("Updated clusterSizingConfig.")
}

func checkHCSizeLabel(ctx context.Context, t *testing.T, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	// Check that the HostedCluster size label is small
	e2eutil.EventuallyObject(t, ctx, "HostedCluster size is set to small",
		func(ctx context.Context) (*hyperv1.HostedCluster, error) {
			hc := &hyperv1.HostedCluster{}
			err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hc)
			return hc, err
		},
		[]e2eutil.Predicate[*hyperv1.HostedCluster]{
			func(hostedCluster *hyperv1.HostedCluster) (done bool, reasons string, err error) {
				want, got := "small", hostedCluster.Labels[hyperv1.HostedClusterSizeLabel]
				return want == got, fmt.Sprintf("expected HostedCluster size label to be %q, got %q", want, got), nil
			},
		}, e2eutil.WithTimeout(1*time.Minute), e2eutil.WithInterval(5*time.Second),
	)
}

func scaleNodePool(ctx context.Context, t *testing.T, g Gomega, mgtClient, guestClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	// Get associated NodePool
	nodepools := &hyperv1.NodePoolList{}
	if err := mgtClient.List(ctx, nodepools, crclient.InNamespace(hostedCluster.Namespace)); err != nil {
		t.Fatalf("failed to list nodepools in namespace %s: %v", hostedCluster.Namespace, err)
	}
	if len(nodepools.Items) != 1 {
		t.Fatalf("expected exactly one nodepool, got %d", len(nodepools.Items))
	}
	nodepool := &nodepools.Items[0]

	err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(nodepool), nodepool)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get nodepool")

	// Scale the NodePool to medium
	originalNodePool := nodepool.DeepCopy()
	nodepool.Spec.Replicas = ptr.To[int32](3)
	err = mgtClient.Patch(ctx, nodepool, crclient.MergeFrom(originalNodePool))
	g.Expect(err).NotTo(HaveOccurred(), "failed to update NodePool")
	t.Logf("Scaled Nodepool. Namespace: %s, name: %s, replicas: %v", nodepool.Namespace, nodepool.Name, nodepool.Spec.Replicas)

	// Wait for the NodePool to scale
	numNodes := *nodepool.Spec.Replicas
	_ = e2eutil.WaitForNReadyNodes(t, ctx, guestClient, numNodes, hostedCluster.Spec.Platform.Type)
}

func checkHCSizeAndAnnotations(ctx context.Context, t *testing.T, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster, size string, annotations map[string]string) {
	e2eutil.EventuallyObject(t, ctx, "HostedCluster size label and annotations updated",
		func(ctx context.Context) (*hyperv1.HostedCluster, error) {
			hc := &hyperv1.HostedCluster{}
			err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hc)
			return hc, err
		},
		[]e2eutil.Predicate[*hyperv1.HostedCluster]{
			func(hostedCluster *hyperv1.HostedCluster) (done bool, reasons string, err error) {
				got := hostedCluster.Labels[hyperv1.HostedClusterSizeLabel]
				return size == got, fmt.Sprintf("expected HostedCluster size label to be %q, got %q", size, got), nil
			},
			func(hostedCluster *hyperv1.HostedCluster) (done bool, reasons string, err error) {
				for k, v := range annotations {
					if got, ok := hostedCluster.Annotations[k]; !ok || got != v {
						return false, fmt.Sprintf("expected annotation %q to be %q, got %q", k, v, got), nil
					}
				}
				return true, "", nil
			},
		}, e2eutil.WithTimeout(5*time.Minute), e2eutil.WithInterval(5*time.Second),
	)
}

func checkCPOPodRescheduled(ctx context.Context, t *testing.T, mgtClient crclient.Client, controlPlaneNamespace string) {
	e2eutil.EventuallyObject(t, ctx, "control-plane-operator pod is running with expected resource request",
		func(ctx context.Context) (*corev1.Pod, error) {
			podList := &corev1.PodList{}
			err := mgtClient.List(ctx, podList, crclient.InNamespace(controlPlaneNamespace), crclient.MatchingLabels{"app": "control-plane-operator"})
			if err != nil {
				return nil, err
			}

			if len(podList.Items) == 0 {
				return nil, fmt.Errorf("no pods found for control-plane-operator")
			}

			sort.Slice(podList.Items, func(i, j int) bool {
				return podList.Items[i].CreationTimestamp.After(podList.Items[j].CreationTimestamp.Time)
			})

			return &podList.Items[0], nil
		},
		[]e2eutil.Predicate[*corev1.Pod]{
			func(pod *corev1.Pod) (done bool, reasons string, err error) {
				if pod.Status.Phase == corev1.PodRunning {
					return true, "pod is running", nil
				}
				return false, fmt.Sprintf("expected pod to be running, but it is in phase: %s", pod.Status.Phase), nil
			},
			func(pod *corev1.Pod) (done bool, reasons string, err error) {
				want, got := resource.MustParse("300m"), pod.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU]
				return reflect.DeepEqual(want, got), fmt.Sprintf("expected "+
					"control-plane-operator cpu request to be %s, got %s", want.String(), got.String()), nil
			},
		}, e2eutil.WithTimeout(3*time.Minute), e2eutil.WithInterval(5*time.Second),
	)
}
