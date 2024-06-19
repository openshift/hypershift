//go:build integration

package integration

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	schedulingv1alpha1 "github.com/openshift/hypershift/api/scheduling/v1alpha1"
	schedulingv1alpha1applyconfigurations "github.com/openshift/hypershift/client/applyconfiguration/scheduling/v1alpha1"
	"github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/integration/framework"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	corev1applyconfigurations "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/client-go/kubernetes"
)

func withPlaceholders(num int) func(in *schedulingv1alpha1applyconfigurations.SizeConfigurationApplyConfiguration) *schedulingv1alpha1applyconfigurations.SizeConfigurationApplyConfiguration {
	return func(in *schedulingv1alpha1applyconfigurations.SizeConfigurationApplyConfiguration) *schedulingv1alpha1applyconfigurations.SizeConfigurationApplyConfiguration {
		return in.WithManagement(schedulingv1alpha1applyconfigurations.Management().WithPlaceholders(num))
	}
}
func TestPlaceholders(t *testing.T) {
	framework.RunHyperShiftOperatorTest(testContext, log, globalOpts, framework.HostedClusterOptions{
		DebugDeployments: []string{
			"control-plane-operator",
			"ignition-server",
			"hosted-cluster-config-operator",
			"control-plane-pki-operator",
		}, // turn off all the child components, we are only exercising HO here
	}, t, func(t *testing.T, testCtx *framework.ManagementTestContext) {
		// TODO: when/if we ever run all of these in parallel, this will collide with the hosted cluster sizing test on apply
		// since we will overwrite the small size, and we can't just add our own size as it needs to be self-consistent
		t.Log("setting the cluster sizing configuration to have 1 small placeholder")
		config, err := testCtx.MgmtCluster.HyperShiftClient.SchedulingV1alpha1().ClusterSizingConfigurations().Apply(testContext,
			schedulingv1alpha1applyconfigurations.ClusterSizingConfiguration("cluster").WithSpec(
				schedulingv1alpha1applyconfigurations.ClusterSizingConfigurationSpec().
					WithSizes(sizes(withPlaceholders(1))...).
					WithTransitionDelay(shortTransitionDelay).
					WithConcurrency(highConcurrency),
			),
			metav1.ApplyOptions{FieldManager: "e2e-test", Force: true},
		)
		if err != nil {
			t.Fatalf("could not update cluster sizing config: %v", err)
		}

		waitForPlaceholders(testContext, t, testCtx.MgmtCluster.KubeClient, config, []string{})

		t.Log("adding labels to the kind node to drive the paired-nodes behavior")
		nodes, err := testCtx.MgmtCluster.KubeClient.CoreV1().Nodes().List(testContext, metav1.ListOptions{})
		if err != nil {
			t.Fatalf("failed to list nodes: %v", err)
		}
		if len(nodes.Items) == 0 {
			t.Fatal("test needs at least one node to run")
		}
		if _, err := testCtx.MgmtCluster.KubeClient.CoreV1().Nodes().Apply(testContext, corev1applyconfigurations.Node(nodes.Items[0].Name).WithLabels(map[string]string{
			"hypershift.openshift.io/cluster-name":        "test",
			"hypershift.openshift.io/cluster-namespace":   "whatever",
			"osd-fleet-manager.openshift.io/paired-nodes": "first",
		}), metav1.ApplyOptions{FieldManager: "e2e-test"}); err != nil {
			t.Fatalf("failed to apply labels to node: %v", err)
		}

		waitForPlaceholders(testContext, t, testCtx.MgmtCluster.KubeClient, config, []string{"first"})

		t.Log("removing labels to the kind node to drive the paired-nodes removal behavior")
		if _, err := testCtx.MgmtCluster.KubeClient.CoreV1().Nodes().Apply(testContext, corev1applyconfigurations.Node(nodes.Items[0].Name).WithLabels(map[string]string{}), metav1.ApplyOptions{FieldManager: "e2e-test"}); err != nil {
			t.Fatalf("failed to remove labels from node: %v", err)
		}

		waitForPlaceholders(testContext, t, testCtx.MgmtCluster.KubeClient, config, []string{})

		t.Log("reinstating labels to the kind node to drive the paired-nodes behavior")
		if _, err := testCtx.MgmtCluster.KubeClient.CoreV1().Nodes().Apply(testContext, corev1applyconfigurations.Node(nodes.Items[0].Name).WithLabels(map[string]string{
			"hypershift.openshift.io/cluster-name":        "test",
			"hypershift.openshift.io/cluster-namespace":   "whatever",
			"osd-fleet-manager.openshift.io/paired-nodes": "first",
		}), metav1.ApplyOptions{FieldManager: "e2e-test"}); err != nil {
			t.Fatalf("failed to apply labels to node: %v", err)
		}

		waitForPlaceholders(testContext, t, testCtx.MgmtCluster.KubeClient, config, []string{"first"})

		t.Log("setting the cluster sizing configuration to have 3 small placeholders, to cause scale up")
		config, err = testCtx.MgmtCluster.HyperShiftClient.SchedulingV1alpha1().ClusterSizingConfigurations().Apply(testContext,
			schedulingv1alpha1applyconfigurations.ClusterSizingConfiguration("cluster").WithSpec(
				schedulingv1alpha1applyconfigurations.ClusterSizingConfigurationSpec().
					WithSizes(sizes(withPlaceholders(3))...).
					WithTransitionDelay(shortTransitionDelay).
					WithConcurrency(highConcurrency),
			),
			metav1.ApplyOptions{FieldManager: "e2e-test", Force: true},
		)
		if err != nil {
			t.Fatalf("could not update cluster sizing config: %v", err)
		}

		waitForPlaceholders(testContext, t, testCtx.MgmtCluster.KubeClient, config, []string{"first"})

		t.Log("deleting a placeholder and waiting for self-healing")
		if err := testCtx.MgmtCluster.KubeClient.AppsV1().Deployments("request-serving-node-placeholders").Delete(testContext, "placeholder-small-0", metav1.DeleteOptions{}); err != nil {
			t.Fatalf("couldn't delete deployment request-serving-node-placeholders/placeholder-small-0: %v", err)
		}

		waitForPlaceholders(testContext, t, testCtx.MgmtCluster.KubeClient, config, []string{"first"})

		t.Log("setting the cluster sizing configuration to have 2 small placeholders, to cause scale down")
		config, err = testCtx.MgmtCluster.HyperShiftClient.SchedulingV1alpha1().ClusterSizingConfigurations().Apply(testContext,
			schedulingv1alpha1applyconfigurations.ClusterSizingConfiguration("cluster").WithSpec(
				schedulingv1alpha1applyconfigurations.ClusterSizingConfigurationSpec().
					WithSizes(sizes(withPlaceholders(2))...).
					WithTransitionDelay(shortTransitionDelay).
					WithConcurrency(highConcurrency),
			),
			metav1.ApplyOptions{FieldManager: "e2e-test", Force: true},
		)
		if err != nil {
			t.Fatalf("could not update cluster sizing config: %v", err)
		}

		waitForPlaceholders(testContext, t, testCtx.MgmtCluster.KubeClient, config, []string{"first"})

	})
}

func waitForPlaceholders(ctx context.Context, t *testing.T, kubeClient kubernetes.Interface, config *schedulingv1alpha1.ClusterSizingConfiguration, pairedNodes []string) {
	t.Log("waiting for placeholders to appear")
	placeholders := map[string]int{}
	for _, size := range config.Spec.Sizes {
		if size.Management != nil && size.Management.Placeholders != 0 {
			placeholders[size.Name] = size.Management.Placeholders
		}
	}
	if len(placeholders) != 1 {
		t.Fatalf("got unexpected placeholders: %v config: %#v", placeholders, config)
	}
	placeholderPresent, err := labels.NewRequirement("hypershift.openshift.io/placeholder", selection.Exists, []string{})
	if err != nil {
		t.Fatalf("could not create requirement: %v", err)
	}

	for size, count := range placeholders {
		util.EventuallyObjects(
			t, ctx, fmt.Sprintf("%d %s placeholders", count, size),
			func(ctx context.Context) ([]*appsv1.Deployment, error) {
				deployments, err := kubeClient.AppsV1().Deployments("request-serving-node-placeholders").List(ctx, metav1.ListOptions{
					LabelSelector: labels.SelectorFromSet(labels.Set{"hypershift.openshift.io/hosted-cluster-size": size}).Add(*placeholderPresent).String(),
				})
				var ptrs []*appsv1.Deployment
				for _, d := range deployments.Items {
					ptrs = append(ptrs, &d)
				}
				return ptrs, err
			},
			func(deployments []*appsv1.Deployment) (done bool, reasons []string, err error) {
				return len(deployments) == count, []string{fmt.Sprintf("expected %d %s placeholders, found %d", count, size, len(deployments))}, nil
			},
			func(deployment *appsv1.Deployment) (done bool, reasons []string, err error) {
				var expectedAffinity *corev1.NodeAffinity
				if len(pairedNodes) == 0 {
					expectedAffinity = nil
				} else {
					expectedAffinity = &corev1.NodeAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
							NodeSelectorTerms: []corev1.NodeSelectorTerm{{
								MatchExpressions: []corev1.NodeSelectorRequirement{{
									Key:      "osd-fleet-manager.openshift.io/paired-nodes",
									Operator: corev1.NodeSelectorOpNotIn,
									Values:   pairedNodes,
								}},
							}},
						},
					}
				}
				if diff := cmp.Diff(deployment.Spec.Template.Spec.Affinity.NodeAffinity, expectedAffinity); diff != "" {
					return false, []string{fmt.Sprintf("invalid node affinity: %v", diff)}, nil
				}
				return true, []string{"valid node affinity"}, nil
			},
			util.WithoutConditionDump(),
		)
	}
}
