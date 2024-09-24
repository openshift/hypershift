//go:build integration

package integration

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hypershiftv1beta1applyconfigurations "github.com/openshift/hypershift/client/applyconfiguration/hypershift/v1beta1"
	schedulingv1alpha1applyconfigurations "github.com/openshift/hypershift/client/applyconfiguration/scheduling/v1alpha1"
	hypershiftclient "github.com/openshift/hypershift/client/clientset/clientset"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/integration/framework"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func noop(in *schedulingv1alpha1applyconfigurations.SizeConfigurationApplyConfiguration) *schedulingv1alpha1applyconfigurations.SizeConfigurationApplyConfiguration {
	return in
}

func sizes(smallSize func(*schedulingv1alpha1applyconfigurations.SizeConfigurationApplyConfiguration) *schedulingv1alpha1applyconfigurations.SizeConfigurationApplyConfiguration) []*schedulingv1alpha1applyconfigurations.SizeConfigurationApplyConfiguration {
	return []*schedulingv1alpha1applyconfigurations.SizeConfigurationApplyConfiguration{
		smallSize(schedulingv1alpha1applyconfigurations.SizeConfiguration().
			WithName("small").
			WithCriteria(
				schedulingv1alpha1applyconfigurations.NodeCountCriteria().
					WithFrom(0).
					WithTo(10),
			)),
		schedulingv1alpha1applyconfigurations.SizeConfiguration().
			WithName("medium").
			WithCriteria(
				schedulingv1alpha1applyconfigurations.NodeCountCriteria().
					WithFrom(11).
					WithTo(100),
			),
		schedulingv1alpha1applyconfigurations.SizeConfiguration().
			WithName("large").
			WithCriteria(
				schedulingv1alpha1applyconfigurations.NodeCountCriteria().
					WithFrom(101),
			),
	}
}

var (
	shortTransitionDelay = schedulingv1alpha1applyconfigurations.TransitionDelayConfiguration().
				WithDecrease(metav1.Duration{Duration: time.Second}).
				WithIncrease(metav1.Duration{Duration: time.Second})
	longTransitionDelay = schedulingv1alpha1applyconfigurations.TransitionDelayConfiguration().
				WithDecrease(metav1.Duration{Duration: time.Hour}).
				WithIncrease(metav1.Duration{Duration: time.Hour})

	highConcurrency = schedulingv1alpha1applyconfigurations.ConcurrencyConfiguration().
			WithSlidingWindow(metav1.Duration{Duration: time.Second}).
			WithLimit(1000)
	lowConcurrency = schedulingv1alpha1applyconfigurations.ConcurrencyConfiguration().
			WithSlidingWindow(metav1.Duration{Duration: time.Hour}).
			WithLimit(1)
)

func TestHostedSizingController(t *testing.T) {
	framework.RunHyperShiftOperatorTest(testContext, log, globalOpts, framework.HostedClusterOptions{
		DebugDeployments: []string{
			"control-plane-operator",
			"ignition-server",
			"hosted-cluster-config-operator",
			"control-plane-pki-operator",
		}, // turn off all the child components, so we can mess with HCP status
	}, t, func(t *testing.T, testCtx *framework.ManagementTestContext) {
		t.Log("setting the cluster sizing configuration")
		if _, err := testCtx.MgmtCluster.HyperShiftClient.SchedulingV1alpha1().ClusterSizingConfigurations().Apply(testContext,
			schedulingv1alpha1applyconfigurations.ClusterSizingConfiguration("cluster").WithSpec(
				schedulingv1alpha1applyconfigurations.ClusterSizingConfigurationSpec().
					WithSizes(sizes(noop)...).
					WithTransitionDelay(shortTransitionDelay). // set the transition delays super short so we DON'T hit them
					WithConcurrency(highConcurrency),          // set the sliding window and limit so we do NOT hit them
			),
			metav1.ApplyOptions{FieldManager: "e2e-test", Force: true},
		); err != nil {
			t.Fatalf("could not update cluster sizing config: %v", err)
		}

		hostedControlPlaneNamespace := manifests.HostedControlPlaneNamespace(testCtx.HostedCluster.Namespace, testCtx.HostedCluster.Name)
		hostedControlPlaneName := testCtx.HostedCluster.Name
		nodeCount := 1
		t.Logf("adding a small node count (%d) to the hosted control plane %s/%s", nodeCount, hostedControlPlaneNamespace, hostedControlPlaneName)
		if _, err := testCtx.MgmtCluster.HyperShiftClient.HypershiftV1beta1().HostedControlPlanes(hostedControlPlaneNamespace).ApplyStatus(testContext,
			hypershiftv1beta1applyconfigurations.HostedControlPlane(hostedControlPlaneName, hostedControlPlaneNamespace).WithStatus(
				hypershiftv1beta1applyconfigurations.HostedControlPlaneStatus().WithNodeCount(nodeCount),
			),
			metav1.ApplyOptions{FieldManager: "e2e-test"},
		); err != nil {
			t.Fatalf("failed to update hosted control plane status: %v", err)
		}

		waitForHostedClusterToHaveSize(t, testContext, testCtx.MgmtCluster.HyperShiftClient, testCtx.HostedCluster.Namespace, testCtx.HostedCluster.Name, "small")

		t.Log("configuring the cluster sizing configuration to have long delays")
		if _, err := testCtx.MgmtCluster.HyperShiftClient.SchedulingV1alpha1().ClusterSizingConfigurations().Apply(testContext,
			schedulingv1alpha1applyconfigurations.ClusterSizingConfiguration("cluster").WithSpec(
				schedulingv1alpha1applyconfigurations.ClusterSizingConfigurationSpec().
					WithSizes(sizes(noop)...).
					WithTransitionDelay(longTransitionDelay). // set the transition delays super long so we hit them
					WithConcurrency(highConcurrency),         // set the sliding window and limit so we do NOT hit them
			),
			metav1.ApplyOptions{FieldManager: "e2e-test", Force: true},
		); err != nil {
			t.Fatalf("could not update cluster sizing config: %v", err)
		}

		nodeCount = 50
		t.Logf("adding a large node count (%d) to the hosted control plane %s/%s", nodeCount, hostedControlPlaneNamespace, hostedControlPlaneName)
		if _, err := testCtx.MgmtCluster.HyperShiftClient.HypershiftV1beta1().HostedControlPlanes(hostedControlPlaneNamespace).ApplyStatus(testContext,
			hypershiftv1beta1applyconfigurations.HostedControlPlane(hostedControlPlaneName, hostedControlPlaneNamespace).WithStatus(
				hypershiftv1beta1applyconfigurations.HostedControlPlaneStatus().WithNodeCount(nodeCount),
			),
			metav1.ApplyOptions{FieldManager: "e2e-test"},
		); err != nil {
			t.Fatalf("failed to update hosted control plane status: %v", err)
		}

		t.Logf("expecting hosted cluster %s/%s to delay transition", testCtx.HostedCluster.Namespace, testCtx.HostedCluster.Name)
		waitForHostedClusterCondition(t, testContext, testCtx.MgmtCluster.HyperShiftClient,
			testCtx.HostedCluster.Namespace, testCtx.HostedCluster.Name,
			[]util.Condition{
				{Type: hypershiftv1beta1.ClusterSizeTransitionPending, Status: metav1.ConditionTrue, Reason: "TransitionDelayNotElapsed"},
				{Type: hypershiftv1beta1.ClusterSizeTransitionRequired, Status: metav1.ConditionTrue, Reason: "medium"},
			}, nil,
		)

		t.Log("configuring the cluster sizing configuration to have minuscule delays")
		if _, err := testCtx.MgmtCluster.HyperShiftClient.SchedulingV1alpha1().ClusterSizingConfigurations().Apply(testContext,
			schedulingv1alpha1applyconfigurations.ClusterSizingConfiguration("cluster").WithSpec(
				schedulingv1alpha1applyconfigurations.ClusterSizingConfigurationSpec().
					WithSizes(sizes(noop)...).
					WithTransitionDelay(shortTransitionDelay). // set the transition delays super short so we DON'T hit them
					WithConcurrency(highConcurrency),          // set the sliding window and limit so we do NOT hit them
			),
			metav1.ApplyOptions{FieldManager: "e2e-test", Force: true},
		); err != nil {
			t.Fatalf("could not update cluster sizing config: %v", err)
		}

		waitForHostedClusterToHaveSize(t, testContext, testCtx.MgmtCluster.HyperShiftClient, testCtx.HostedCluster.Namespace, testCtx.HostedCluster.Name, "medium")

		t.Log("configure the cluster sizing configuration to have no concurrency")
		if _, err := testCtx.MgmtCluster.HyperShiftClient.SchedulingV1alpha1().ClusterSizingConfigurations().Apply(testContext,
			schedulingv1alpha1applyconfigurations.ClusterSizingConfiguration("cluster").WithSpec(
				schedulingv1alpha1applyconfigurations.ClusterSizingConfigurationSpec().
					WithSizes(sizes(noop)...).
					WithTransitionDelay(shortTransitionDelay). // set the transition delays super short so we DON'T hit them
					WithConcurrency(lowConcurrency),           // set the sliding window and limit so we DO hit them
			),
			metav1.ApplyOptions{FieldManager: "e2e-test", Force: true},
		); err != nil {
			t.Fatalf("could not update cluster sizing config: %v", err)
		}

		nodeCount = 500
		t.Logf("adding a huge node count (%d) to the hosted control plane %s/%s", nodeCount, hostedControlPlaneNamespace, hostedControlPlaneName)
		if _, err := testCtx.MgmtCluster.HyperShiftClient.HypershiftV1beta1().HostedControlPlanes(hostedControlPlaneNamespace).ApplyStatus(testContext,
			hypershiftv1beta1applyconfigurations.HostedControlPlane(hostedControlPlaneName, hostedControlPlaneNamespace).WithStatus(
				hypershiftv1beta1applyconfigurations.HostedControlPlaneStatus().WithNodeCount(nodeCount),
			),
			metav1.ApplyOptions{FieldManager: "e2e-test"},
		); err != nil {
			t.Fatalf("failed to update hosted control plane status: %v", err)
		}

		t.Logf("expecting hosted cluster %s/%s to delay transition for concurrency", testCtx.HostedCluster.Namespace, testCtx.HostedCluster.Name)
		waitForHostedClusterCondition(t, testContext, testCtx.MgmtCluster.HyperShiftClient,
			testCtx.HostedCluster.Namespace, testCtx.HostedCluster.Name,
			[]util.Condition{
				{Type: hypershiftv1beta1.ClusterSizeTransitionPending, Status: metav1.ConditionTrue, Reason: "ConcurrencyLimitReached"},
				{Type: hypershiftv1beta1.ClusterSizeTransitionRequired, Status: metav1.ConditionTrue, Reason: "large"},
			}, nil,
		)

		nodeCount = 25
		t.Logf("reverting to a medium node count (%d) to the hosted control plane %s/%s", nodeCount, hostedControlPlaneNamespace, hostedControlPlaneName)
		if _, err := testCtx.MgmtCluster.HyperShiftClient.HypershiftV1beta1().HostedControlPlanes(hostedControlPlaneNamespace).ApplyStatus(testContext,
			hypershiftv1beta1applyconfigurations.HostedControlPlane(hostedControlPlaneName, hostedControlPlaneNamespace).WithStatus(
				hypershiftv1beta1applyconfigurations.HostedControlPlaneStatus().WithNodeCount(nodeCount),
			),
			metav1.ApplyOptions{FieldManager: "e2e-test"},
		); err != nil {
			t.Fatalf("failed to update hosted control plane status: %v", err)
		}

		waitForHostedClusterToHaveSize(t, testContext, testCtx.MgmtCluster.HyperShiftClient, testCtx.HostedCluster.Namespace, testCtx.HostedCluster.Name, "medium")

		nodeCount = 500
		t.Logf("adding back the huge node count (%d) to the hosted control plane %s/%s", nodeCount, hostedControlPlaneNamespace, hostedControlPlaneName)
		if _, err := testCtx.MgmtCluster.HyperShiftClient.HypershiftV1beta1().HostedControlPlanes(hostedControlPlaneNamespace).ApplyStatus(testContext,
			hypershiftv1beta1applyconfigurations.HostedControlPlane(hostedControlPlaneName, hostedControlPlaneNamespace).WithStatus(
				hypershiftv1beta1applyconfigurations.HostedControlPlaneStatus().WithNodeCount(nodeCount),
			),
			metav1.ApplyOptions{FieldManager: "e2e-test"},
		); err != nil {
			t.Fatalf("failed to update hosted control plane status: %v", err)
		}

		t.Log("configure the cluster sizing configuration to have allow lots of concurrency")
		if _, err := testCtx.MgmtCluster.HyperShiftClient.SchedulingV1alpha1().ClusterSizingConfigurations().Apply(testContext,
			schedulingv1alpha1applyconfigurations.ClusterSizingConfiguration("cluster").WithSpec(
				schedulingv1alpha1applyconfigurations.ClusterSizingConfigurationSpec().
					WithSizes(sizes(noop)...).
					WithTransitionDelay(shortTransitionDelay). // set the transition delays super short so we DON'T hit them
					WithConcurrency(highConcurrency),          // set the sliding window and limit so we DON'T hit them
			),
			metav1.ApplyOptions{FieldManager: "e2e-test", Force: true},
		); err != nil {
			t.Fatalf("could not update cluster sizing config: %v", err)
		}

		waitForHostedClusterToHaveSize(t, testContext, testCtx.MgmtCluster.HyperShiftClient, testCtx.HostedCluster.Namespace, testCtx.HostedCluster.Name, "large")
	})
}

func waitForHostedClusterToHaveSize(t *testing.T, ctx context.Context, client hypershiftclient.Interface, namespace, name, size string) {
	t.Logf("expecting hosted cluster %s/%s to transition to %s size", namespace, name, size)
	waitForHostedClusterCondition(t, ctx, client,
		namespace, name,
		[]util.Condition{
			{Type: hypershiftv1beta1.ClusterSizeComputed, Status: metav1.ConditionTrue, Reason: size},
			{Type: hypershiftv1beta1.ClusterSizeTransitionPending, Status: metav1.ConditionFalse, Reason: "ClusterSizeTransitioned"},
			{Type: hypershiftv1beta1.ClusterSizeTransitionRequired, Status: metav1.ConditionFalse, Reason: hypershiftv1beta1.AsExpectedReason},
		},
		func(hostedCluster *hypershiftv1beta1.HostedCluster) (done bool, reason string, err error) {
			label, present := hostedCluster.ObjectMeta.Labels[hypershiftv1beta1.HostedClusterSizeLabel]
			if !present {
				return false, fmt.Sprintf("hostedCluster.metadata.labels[%s] missing", hypershiftv1beta1.HostedClusterSizeLabel), nil
			}
			if label != size {
				return false, fmt.Sprintf("hostedCluster.metadata.labels[%s]=%s, wanted %s", hypershiftv1beta1.HostedClusterSizeLabel, label, size), nil
			}
			return true, fmt.Sprintf("hostedCluster.metadata.labels[%s]=%s", hypershiftv1beta1.HostedClusterSizeLabel, label), nil
		},
	)
}

func waitForHostedClusterCondition(t *testing.T, ctx context.Context, client hypershiftclient.Interface, namespace, name string, conditions []util.Condition, validate util.Predicate[*hypershiftv1beta1.HostedCluster]) {
	var display []string
	for _, condition := range conditions {
		display = append(display, condition.String())
	}
	intent := fmt.Sprintf("hosted cluster %s/%s to have status conditions %s", namespace, name, strings.Join(display, ", "))
	if validate != nil {
		intent += " and match validation func"
	}

	var predicates []util.Predicate[*hypershiftv1beta1.HostedCluster]
	for _, condition := range conditions {
		predicates = append(predicates, util.ConditionPredicate[*hypershiftv1beta1.HostedCluster](condition))
	}
	if validate != nil {
		predicates = append(predicates, validate)
	}

	util.EventuallyObject(
		t, ctx, intent,
		func(ctx context.Context) (*hypershiftv1beta1.HostedCluster, error) {
			return client.HypershiftV1beta1().HostedClusters(namespace).Get(ctx, name, metav1.GetOptions{})
		},
		predicates,
		util.WithoutConditionDump(),
	)
}
