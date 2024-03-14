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
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedclustersizing"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/test/integration/framework"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
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
		sizes := []*schedulingv1alpha1applyconfigurations.SizeConfigurationApplyConfiguration{
			schedulingv1alpha1applyconfigurations.SizeConfiguration().
				WithName("small").
				WithCriteria(
					schedulingv1alpha1applyconfigurations.NodeCountCriteria().
						WithFrom(0).
						WithTo(10),
				),
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
		t.Log("setting the cluster sizing configuration")
		if _, err := testCtx.MgmtCluster.HyperShiftClient.SchedulingV1alpha1().ClusterSizingConfigurations().Apply(testContext,
			schedulingv1alpha1applyconfigurations.ClusterSizingConfiguration("cluster").WithSpec(
				schedulingv1alpha1applyconfigurations.ClusterSizingConfigurationSpec().
					WithSizes(sizes...).
					WithTransitionDelay( // set the transition delays super short so we DON'T hit them
						schedulingv1alpha1applyconfigurations.TransitionDelayConfiguration().
							WithDecrease(metav1.Duration{Duration: time.Second}).
							WithIncrease(metav1.Duration{Duration: time.Second}),
					).
					WithConcurrency( // set the sliding window and limit so we do NOT hit them
						schedulingv1alpha1applyconfigurations.ConcurrencyConfiguration().
							WithSlidingWindow(metav1.Duration{Duration: time.Second}).
							WithLimit(1000),
					),
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
					WithSizes(sizes...).
					WithTransitionDelay( // set the transition delays super long so we hit them
						schedulingv1alpha1applyconfigurations.TransitionDelayConfiguration().
							WithDecrease(metav1.Duration{Duration: time.Hour}).
							WithIncrease(metav1.Duration{Duration: time.Hour}),
					).
					WithConcurrency( // set the sliding window and limit so we do NOT hit them
						schedulingv1alpha1applyconfigurations.ConcurrencyConfiguration().
							WithSlidingWindow(metav1.Duration{Duration: time.Second}).
							WithLimit(1000),
					),
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
			[]condition{
				{conditionType: hypershiftv1beta1.ClusterSizeTransitionPending, status: metav1.ConditionTrue, reason: "TransitionDelayNotElapsed"},
				{conditionType: hypershiftv1beta1.ClusterSizeTransitionRequired, status: metav1.ConditionTrue, reason: "medium"},
			}, nil,
		)

		t.Log("configuring the cluster sizing configuration to have miniscule delays")
		if _, err := testCtx.MgmtCluster.HyperShiftClient.SchedulingV1alpha1().ClusterSizingConfigurations().Apply(testContext,
			schedulingv1alpha1applyconfigurations.ClusterSizingConfiguration("cluster").WithSpec(
				schedulingv1alpha1applyconfigurations.ClusterSizingConfigurationSpec().
					WithSizes(sizes...).
					WithTransitionDelay( // set the transition delays super short so we DON'T hit them
						schedulingv1alpha1applyconfigurations.TransitionDelayConfiguration().
							WithDecrease(metav1.Duration{Duration: time.Second}).
							WithIncrease(metav1.Duration{Duration: time.Second}),
					).
					WithConcurrency( // set the sliding window and limit so we do NOT hit them
						schedulingv1alpha1applyconfigurations.ConcurrencyConfiguration().
							WithSlidingWindow(metav1.Duration{Duration: time.Second}).
							WithLimit(1000),
					),
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
					WithSizes(sizes...).
					WithTransitionDelay( // set the transition delays super short so we DON'T hit them
						schedulingv1alpha1applyconfigurations.TransitionDelayConfiguration().
							WithDecrease(metav1.Duration{Duration: time.Second}).
							WithIncrease(metav1.Duration{Duration: time.Second}),
					).
					WithConcurrency( // set the sliding window and limit so we DO hit them
						schedulingv1alpha1applyconfigurations.ConcurrencyConfiguration().
							WithSlidingWindow(metav1.Duration{Duration: time.Hour}).
							WithLimit(1),
					),
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
			[]condition{
				{conditionType: hypershiftv1beta1.ClusterSizeTransitionPending, status: metav1.ConditionTrue, reason: "ConcurrencyLimitReached"},
				{conditionType: hypershiftv1beta1.ClusterSizeTransitionRequired, status: metav1.ConditionTrue, reason: "large"},
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
					WithSizes(sizes...).
					WithTransitionDelay( // set the transition delays super short so we DON'T hit them
						schedulingv1alpha1applyconfigurations.TransitionDelayConfiguration().
							WithDecrease(metav1.Duration{Duration: time.Second}).
							WithIncrease(metav1.Duration{Duration: time.Second}),
					).
					WithConcurrency( // set the sliding window and limit so we DON'T hit them
						schedulingv1alpha1applyconfigurations.ConcurrencyConfiguration().
							WithSlidingWindow(metav1.Duration{Duration: time.Second}).
							WithLimit(1000),
					),
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
		[]condition{
			{conditionType: hypershiftv1beta1.ClusterSizeComputed, status: metav1.ConditionTrue, reason: size},
			{conditionType: hypershiftv1beta1.ClusterSizeTransitionPending, status: metav1.ConditionFalse, reason: "ClusterSizeTransitioned"},
			{conditionType: hypershiftv1beta1.ClusterSizeTransitionRequired, status: metav1.ConditionFalse, reason: hypershiftv1beta1.AsExpectedReason},
		},
		func(hostedCluster *hypershiftv1beta1.HostedCluster) (done bool, reason string, err error) {
			label, present := hostedCluster.ObjectMeta.Labels[hostedclustersizing.HostedClusterSizeLabel]
			if !present {
				return false, fmt.Sprintf("hostedCluster.metadata.labels[%s] missing", hostedclustersizing.HostedClusterSizeLabel), nil
			}
			if label != size {
				return false, fmt.Sprintf("hostedCluster.metadata.labels[%s]=%s, wanted %s", hostedclustersizing.HostedClusterSizeLabel, label, size), nil
			}
			return true, fmt.Sprintf("hostedCluster.metadata.labels[%s]=%s", hostedclustersizing.HostedClusterSizeLabel, label), nil
		},
	)
}

var relevantConditions = sets.New[string](hypershiftv1beta1.ClusterSizeTransitionPending, hypershiftv1beta1.ClusterSizeComputed, hypershiftv1beta1.ClusterSizeTransitionRequired)

type validationFunc func(*hypershiftv1beta1.HostedCluster) (done bool, reason string, err error)

type condition struct {
	conditionType string
	status        metav1.ConditionStatus
	reason        string
}

func waitForHostedClusterCondition(t *testing.T, ctx context.Context, client hypershiftclient.Interface, namespace, name string, conditions []condition, validate validationFunc) {
	var display []string
	for _, condition := range conditions {
		display = append(display, fmt.Sprintf("%s=%s: %s", condition.conditionType, condition.status, condition.reason))
	}
	intent := fmt.Sprintf("hosted cluster %s/%s to have status conditions %s", namespace, name, strings.Join(display, ", "))
	if validate != nil {
		intent += " and match validation func"
	}
	t.Logf("waiting for %s", intent)
	var lastResourceVersion string
	lastTimestamp := time.Now()
	var lastReason string
	if err := wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, 10*time.Minute, true, func(ctx context.Context) (done bool, err error) {
		hostedCluster, err := client.HypershiftV1beta1().HostedClusters(namespace).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			t.Logf("hosted cluster %s/%s does not exist yet", namespace, name)
			return false, nil
		}
		if err != nil && !apierrors.IsNotFound(err) {
			return true, err
		}
		if hostedCluster.ObjectMeta.ResourceVersion != lastResourceVersion {
			t.Logf("hosted cluster %s/%s observed at RV %s after %s", namespace, name, hostedCluster.ObjectMeta.ResourceVersion, time.Since(lastTimestamp))
			for _, condition := range hostedCluster.Status.Conditions {
				if !relevantConditions.Has(condition.Type) {
					continue
				}
				msg := fmt.Sprintf("%s=%s", condition.Type, condition.Status)
				if condition.Reason != "" {
					msg += ": " + condition.Reason
				}
				if condition.Message != "" {
					msg += " (" + condition.Message + ")"
				}
				t.Logf("hosted cluster %s/%s status: %s", namespace, name, msg)
			}
			lastResourceVersion = hostedCluster.ObjectMeta.ResourceVersion
			lastTimestamp = time.Now()
		}

		conditionsMet := true
		for _, expected := range conditions {
			var conditionMet bool
			for _, actual := range hostedCluster.Status.Conditions {
				if actual.Type == expected.conditionType &&
					actual.Status == expected.status &&
					actual.Reason == expected.reason {
					conditionMet = true
					break
				}
			}
			conditionsMet = conditionsMet && conditionMet
		}
		if conditionsMet {
			if validate != nil {
				done, validationReason, err := validate(hostedCluster)
				if validationReason != lastReason {
					t.Logf("hosted cluster %s/%s validation: %s", namespace, name, validationReason)
					lastReason = validationReason
				}
				return done, err
			} else {
				return true, nil
			}
		}

		return false, nil
	}); err != nil {
		t.Fatalf("never saw %s: %v", intent, err)
	}
}
