package hostedclustersizing

import (
	"context"
	"testing"
	"time"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	schedulingv1alpha1 "github.com/openshift/hypershift/api/scheduling/v1alpha1"
	hypershiftv1beta1applyconfigurations "github.com/openshift/hypershift/client/applyconfiguration/hypershift/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	metav1applyconfigurations "k8s.io/client-go/applyconfigurations/meta/v1"
	testingclock "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"go.uber.org/zap/zapcore"
)

func TestSizingController_Reconcile(t *testing.T) {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true), zap.JSONEncoder(func(o *zapcore.EncoderConfig) {
		o.EncodeTime = zapcore.RFC3339TimeEncoder
	})))
	ctx := ctrl.LoggerInto(t.Context(), ctrl.Log)

	theTime, err := time.Parse(time.RFC3339Nano, "2006-01-02T15:04:05.000000000Z")
	if err != nil {
		t.Fatalf("could not parse time: %v", err)
	}
	fakeClock := testingclock.NewFakeClock(theTime)

	validCommonConfig := &schedulingv1alpha1.ClusterSizingConfiguration{
		ObjectMeta: metav1.ObjectMeta{Generation: 1},
		Spec: schedulingv1alpha1.ClusterSizingConfigurationSpec{
			Sizes: []schedulingv1alpha1.SizeConfiguration{
				{Name: "small", Criteria: schedulingv1alpha1.NodeCountCriteria{From: 0, To: ptr.To(uint32(10))}},
				{Name: "medium", Criteria: schedulingv1alpha1.NodeCountCriteria{From: 11, To: ptr.To(uint32(100))}},
				{Name: "large", Criteria: schedulingv1alpha1.NodeCountCriteria{From: 101}},
			},
			Concurrency: schedulingv1alpha1.ConcurrencyConfiguration{
				SlidingWindow: metav1.Duration{Duration: 10 * time.Minute},
				Limit:         5,
			},
			TransitionDelay: schedulingv1alpha1.TransitionDelayConfiguration{
				Increase: metav1.Duration{Duration: 30 * time.Second},
				Decrease: metav1.Duration{Duration: 10 * time.Minute},
			},
		},
		Status: schedulingv1alpha1.ClusterSizingConfigurationStatus{
			Conditions: []metav1.Condition{{Type: schedulingv1alpha1.ClusterSizingConfigurationValidType, Status: metav1.ConditionTrue}},
		},
	}

	for _, testCase := range []struct {
		name string

		config        *schedulingv1alpha1.ClusterSizingConfiguration
		hostedCluster *hypershiftv1beta1.HostedCluster

		listHostedClusters                 func(context.Context) (*hypershiftv1beta1.HostedClusterList, error)
		hccoReportsNodeCount               func(context.Context, *hypershiftv1beta1.HostedCluster) (bool, error)
		nodePoolsForHostedCluster          func(context.Context, *hypershiftv1beta1.HostedCluster) (*hypershiftv1beta1.NodePoolList, error)
		hostedControlPlaneForHostedCluster func(context.Context, *hypershiftv1beta1.HostedCluster) (*hypershiftv1beta1.HostedControlPlane, error)

		expected    *action
		expectedErr bool
	}{
		{
			name:          "invalid config, do nothing",
			hostedCluster: &hypershiftv1beta1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "hc"}},
			config: &schedulingv1alpha1.ClusterSizingConfiguration{
				Status: schedulingv1alpha1.ClusterSizingConfigurationStatus{
					Conditions: []metav1.Condition{{Type: schedulingv1alpha1.ClusterSizingConfigurationValidType, Status: metav1.ConditionFalse}},
				},
			},
		},
		{
			name:          "deleting hosted cluster, do nothing",
			hostedCluster: &hypershiftv1beta1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "hc", DeletionTimestamp: ptr.To(metav1.NewTime(fakeClock.Now()))}},
			config:        validCommonConfig,
		},
		{
			name: "paused cluster, wait",
			hostedCluster: &hypershiftv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "hc"},
				Spec: hypershiftv1beta1.HostedClusterSpec{
					PausedUntil: ptr.To(fakeClock.Now().Add(10 * time.Minute).Format(time.RFC3339)),
				},
			},
			config:   validCommonConfig,
			expected: &action{requeueAfter: 10 * time.Minute},
		},
		{
			name:          "transition, hcco doesn't report node count",
			config:        validCommonConfig,
			hostedCluster: &hypershiftv1beta1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "hc"}},
			listHostedClusters: func(_ context.Context) (*hypershiftv1beta1.HostedClusterList, error) {
				return &hypershiftv1beta1.HostedClusterList{}, nil
			},
			hccoReportsNodeCount: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (bool, error) {
				return false, nil
			},
			nodePoolsForHostedCluster: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (*hypershiftv1beta1.NodePoolList, error) {
				return &hypershiftv1beta1.NodePoolList{Items: []hypershiftv1beta1.NodePool{
					{Spec: hypershiftv1beta1.NodePoolSpec{Replicas: ptr.To[int32](10)}},
					{Spec: hypershiftv1beta1.NodePoolSpec{Replicas: ptr.To[int32](3)}},
					{Spec: hypershiftv1beta1.NodePoolSpec{Replicas: ptr.To[int32](17)}},
				}}, nil
			},
			expected: &action{applyCfg: &hypershiftv1beta1applyconfigurations.HostedClusterApplyConfiguration{
				ObjectMetaApplyConfiguration: &metav1applyconfigurations.ObjectMetaApplyConfiguration{Namespace: ptr.To("ns"), Name: ptr.To("hc")},
				Status: &hypershiftv1beta1applyconfigurations.HostedClusterStatusApplyConfiguration{
					Conditions: []metav1applyconfigurations.ConditionApplyConfiguration{
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeComputed),
							Status:             ptr.To(metav1.ConditionTrue),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To("medium"),
							Message:            ptr.To("The HostedCluster has transitioned to a new t-shirt size."),
						},
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeTransitionPending),
							Status:             ptr.To(metav1.ConditionFalse),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To("ClusterSizeTransitioned"),
							Message:            ptr.To("The HostedCluster has transitioned to a new t-shirt size."),
						},
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeTransitionRequired),
							Status:             ptr.To(metav1.ConditionFalse),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To(hypershiftv1beta1.AsExpectedReason),
							Message:            ptr.To("The HostedCluster has transitioned to a new t-shirt size."),
						},
					},
				},
			}},
		},
		{
			name:          "transition, hcco reports node count",
			config:        validCommonConfig,
			hostedCluster: &hypershiftv1beta1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "hc"}},
			listHostedClusters: func(_ context.Context) (*hypershiftv1beta1.HostedClusterList, error) {
				return &hypershiftv1beta1.HostedClusterList{}, nil
			},
			hccoReportsNodeCount: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (bool, error) {
				return true, nil
			},
			hostedControlPlaneForHostedCluster: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (*hypershiftv1beta1.HostedControlPlane, error) {
				return &hypershiftv1beta1.HostedControlPlane{
					Status: hypershiftv1beta1.HostedControlPlaneStatus{NodeCount: ptr.To(300)},
				}, nil
			},
			expected: &action{applyCfg: &hypershiftv1beta1applyconfigurations.HostedClusterApplyConfiguration{
				ObjectMetaApplyConfiguration: &metav1applyconfigurations.ObjectMetaApplyConfiguration{Namespace: ptr.To("ns"), Name: ptr.To("hc")},
				Status: &hypershiftv1beta1applyconfigurations.HostedClusterStatusApplyConfiguration{
					Conditions: []metav1applyconfigurations.ConditionApplyConfiguration{
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeComputed),
							Status:             ptr.To(metav1.ConditionTrue),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To("large"),
							Message:            ptr.To("The HostedCluster has transitioned to a new t-shirt size."),
						},
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeTransitionPending),
							Status:             ptr.To(metav1.ConditionFalse),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To("ClusterSizeTransitioned"),
							Message:            ptr.To("The HostedCluster has transitioned to a new t-shirt size."),
						},
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeTransitionRequired),
							Status:             ptr.To(metav1.ConditionFalse),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To(hypershiftv1beta1.AsExpectedReason),
							Message:            ptr.To("The HostedCluster has transitioned to a new t-shirt size."),
						},
					},
				},
			}},
		},
		{
			name:   "pending transition, hcco doesn't report node count",
			config: validCommonConfig,
			hostedCluster: &hypershiftv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "hc"},
				Status: hypershiftv1beta1.HostedClusterStatus{Conditions: []metav1.Condition{{
					Type:               hypershiftv1beta1.ClusterSizeTransitionRequired,
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.NewTime(fakeClock.Now().Add(-1 * time.Second)),
					Reason:             "medium",
					Message:            "The HostedCluster will transition to a new t-shirt size.",
				}}},
			},
			listHostedClusters: func(_ context.Context) (*hypershiftv1beta1.HostedClusterList, error) {
				return &hypershiftv1beta1.HostedClusterList{}, nil
			},
			hccoReportsNodeCount: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (bool, error) {
				return false, nil
			},
			nodePoolsForHostedCluster: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (*hypershiftv1beta1.NodePoolList, error) {
				return &hypershiftv1beta1.NodePoolList{Items: []hypershiftv1beta1.NodePool{
					{Spec: hypershiftv1beta1.NodePoolSpec{AutoScaling: &hypershiftv1beta1.NodePoolAutoScaling{Max: 10}}, Status: hypershiftv1beta1.NodePoolStatus{Replicas: 10}},
					{Spec: hypershiftv1beta1.NodePoolSpec{AutoScaling: &hypershiftv1beta1.NodePoolAutoScaling{Max: 10}}, Status: hypershiftv1beta1.NodePoolStatus{Replicas: 3}},
					{Spec: hypershiftv1beta1.NodePoolSpec{AutoScaling: &hypershiftv1beta1.NodePoolAutoScaling{Max: 10}}, Status: hypershiftv1beta1.NodePoolStatus{Replicas: 17}},
				}}, nil
			},
			expected: &action{applyCfg: &hypershiftv1beta1applyconfigurations.HostedClusterApplyConfiguration{
				ObjectMetaApplyConfiguration: &metav1applyconfigurations.ObjectMetaApplyConfiguration{Namespace: ptr.To("ns"), Name: ptr.To("hc")},
				Status: &hypershiftv1beta1applyconfigurations.HostedClusterStatusApplyConfiguration{
					Conditions: []metav1applyconfigurations.ConditionApplyConfiguration{
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeTransitionPending),
							Status:             ptr.To(metav1.ConditionTrue),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To("TransitionDelayNotElapsed"),
							Message:            ptr.To("HostedClusters must wait at least 30s to increase in size after the cluster size changes."),
						},
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeTransitionRequired),
							Status:             ptr.To(metav1.ConditionTrue),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now().Add(-1 * time.Second))),
							Reason:             ptr.To("medium"),
							Message:            ptr.To("The HostedCluster will transition to a new t-shirt size."),
						},
					},
				},
			}, requeueAfter: 29 * time.Second},
		},
		{
			name:   "pending transition, hcco reports node count",
			config: validCommonConfig,
			hostedCluster: &hypershiftv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "hc"},
				Status: hypershiftv1beta1.HostedClusterStatus{Conditions: []metav1.Condition{{
					Type:               hypershiftv1beta1.ClusterSizeTransitionRequired,
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.NewTime(fakeClock.Now().Add(-1 * time.Second)),
					Reason:             "large",
					Message:            "The HostedCluster will transition to a new t-shirt size.",
				}}},
			},
			listHostedClusters: func(_ context.Context) (*hypershiftv1beta1.HostedClusterList, error) {
				return &hypershiftv1beta1.HostedClusterList{}, nil
			},
			hccoReportsNodeCount: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (bool, error) {
				return true, nil
			},
			hostedControlPlaneForHostedCluster: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (*hypershiftv1beta1.HostedControlPlane, error) {
				return &hypershiftv1beta1.HostedControlPlane{
					Status: hypershiftv1beta1.HostedControlPlaneStatus{NodeCount: ptr.To(300)},
				}, nil
			},
			expected: &action{applyCfg: &hypershiftv1beta1applyconfigurations.HostedClusterApplyConfiguration{
				ObjectMetaApplyConfiguration: &metav1applyconfigurations.ObjectMetaApplyConfiguration{Namespace: ptr.To("ns"), Name: ptr.To("hc")},
				Status: &hypershiftv1beta1applyconfigurations.HostedClusterStatusApplyConfiguration{
					Conditions: []metav1applyconfigurations.ConditionApplyConfiguration{
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeTransitionPending),
							Status:             ptr.To(metav1.ConditionTrue),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To("TransitionDelayNotElapsed"),
							Message:            ptr.To("HostedClusters must wait at least 30s to increase in size after the cluster size changes."),
						},
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeTransitionRequired),
							Status:             ptr.To(metav1.ConditionTrue),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now().Add(-1 * time.Second))),
							Reason:             ptr.To("large"),
							Message:            ptr.To("The HostedCluster will transition to a new t-shirt size."),
						},
					},
				},
			}, requeueAfter: 29 * time.Second},
		},
		{
			name:   "transition, previously computed, hcco reports node count",
			config: validCommonConfig,
			hostedCluster: &hypershiftv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "hc"},
				Status: hypershiftv1beta1.HostedClusterStatus{Conditions: []metav1.Condition{{
					Type:               hypershiftv1beta1.ClusterSizeTransitionRequired,
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.NewTime(fakeClock.Now().Add(-10 * time.Minute)),
					Reason:             "large",
					Message:            "The HostedCluster will transition to a new t-shirt size.",
				}}},
			},
			listHostedClusters: func(_ context.Context) (*hypershiftv1beta1.HostedClusterList, error) {
				return &hypershiftv1beta1.HostedClusterList{}, nil
			},
			hccoReportsNodeCount: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (bool, error) {
				return true, nil
			},
			hostedControlPlaneForHostedCluster: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (*hypershiftv1beta1.HostedControlPlane, error) {
				return &hypershiftv1beta1.HostedControlPlane{
					Status: hypershiftv1beta1.HostedControlPlaneStatus{NodeCount: ptr.To(300)},
				}, nil
			},
			expected: &action{applyCfg: &hypershiftv1beta1applyconfigurations.HostedClusterApplyConfiguration{
				ObjectMetaApplyConfiguration: &metav1applyconfigurations.ObjectMetaApplyConfiguration{Namespace: ptr.To("ns"), Name: ptr.To("hc")},
				Status: &hypershiftv1beta1applyconfigurations.HostedClusterStatusApplyConfiguration{
					Conditions: []metav1applyconfigurations.ConditionApplyConfiguration{
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeComputed),
							Status:             ptr.To(metav1.ConditionTrue),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To("large"),
							Message:            ptr.To("The HostedCluster has transitioned to a new t-shirt size."),
						},
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeTransitionPending),
							Status:             ptr.To(metav1.ConditionFalse),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To("ClusterSizeTransitioned"),
							Message:            ptr.To("The HostedCluster has transitioned to a new t-shirt size."),
						},
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeTransitionRequired),
							Status:             ptr.To(metav1.ConditionFalse),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To(hypershiftv1beta1.AsExpectedReason),
							Message:            ptr.To("The HostedCluster has transitioned to a new t-shirt size."),
						},
					},
				},
			}},
		},
		{
			name:   "transition, previously computed, hcco does not report node count",
			config: validCommonConfig,
			hostedCluster: &hypershiftv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "hc"},
				Status: hypershiftv1beta1.HostedClusterStatus{Conditions: []metav1.Condition{
					{
						Type:               hypershiftv1beta1.ClusterSizeTransitionRequired,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(fakeClock.Now().Add(-10 * time.Minute)),
						Reason:             "large",
						Message:            "The HostedCluster will transition to a new t-shirt size.",
					},
				}},
			},
			listHostedClusters: func(_ context.Context) (*hypershiftv1beta1.HostedClusterList, error) {
				return &hypershiftv1beta1.HostedClusterList{}, nil
			},
			hccoReportsNodeCount: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (bool, error) {
				return false, nil
			},
			nodePoolsForHostedCluster: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (*hypershiftv1beta1.NodePoolList, error) {
				return &hypershiftv1beta1.NodePoolList{Items: []hypershiftv1beta1.NodePool{
					{Spec: hypershiftv1beta1.NodePoolSpec{Replicas: ptr.To[int32](100)}},
					{Spec: hypershiftv1beta1.NodePoolSpec{Replicas: ptr.To[int32](100)}},
					{Spec: hypershiftv1beta1.NodePoolSpec{Replicas: ptr.To[int32](100)}},
				}}, nil
			},
			expected: &action{applyCfg: &hypershiftv1beta1applyconfigurations.HostedClusterApplyConfiguration{
				ObjectMetaApplyConfiguration: &metav1applyconfigurations.ObjectMetaApplyConfiguration{Namespace: ptr.To("ns"), Name: ptr.To("hc")},
				Status: &hypershiftv1beta1applyconfigurations.HostedClusterStatusApplyConfiguration{
					Conditions: []metav1applyconfigurations.ConditionApplyConfiguration{
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeComputed),
							Status:             ptr.To(metav1.ConditionTrue),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To("large"),
							Message:            ptr.To("The HostedCluster has transitioned to a new t-shirt size."),
						},
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeTransitionPending),
							Status:             ptr.To(metav1.ConditionFalse),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To("ClusterSizeTransitioned"),
							Message:            ptr.To("The HostedCluster has transitioned to a new t-shirt size."),
						},
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeTransitionRequired),
							Status:             ptr.To(metav1.ConditionFalse),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To(hypershiftv1beta1.AsExpectedReason),
							Message:            ptr.To("The HostedCluster has transitioned to a new t-shirt size."),
						},
					},
				},
			}},
		},
		{
			name:   "transition, previously computed and tagged, hcco reports node count",
			config: validCommonConfig,
			hostedCluster: &hypershiftv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "hc", Labels: map[string]string{hypershiftv1beta1.HostedClusterSizeLabel: "medium"}},
				Status: hypershiftv1beta1.HostedClusterStatus{Conditions: []metav1.Condition{{
					Type:               hypershiftv1beta1.ClusterSizeTransitionRequired,
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.NewTime(fakeClock.Now().Add(-10 * time.Minute)),
					Reason:             "large",
					Message:            "The HostedCluster will transition to a new t-shirt size.",
				}, {
					Type:               hypershiftv1beta1.ClusterSizeComputed,
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.NewTime(fakeClock.Now().Add(-20 * time.Minute)),
					Reason:             "medium",
					Message:            "The HostedCluster has transitioned to a new t-shirt size.",
				}, {
					Type:   string(hypershiftv1beta1.KubeAPIServerAvailable),
					Status: metav1.ConditionTrue,
				}}},
			},
			listHostedClusters: func(_ context.Context) (*hypershiftv1beta1.HostedClusterList, error) {
				return &hypershiftv1beta1.HostedClusterList{}, nil
			},
			hccoReportsNodeCount: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (bool, error) {
				return true, nil
			},
			hostedControlPlaneForHostedCluster: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (*hypershiftv1beta1.HostedControlPlane, error) {
				return &hypershiftv1beta1.HostedControlPlane{
					Status: hypershiftv1beta1.HostedControlPlaneStatus{NodeCount: ptr.To(300)},
				}, nil
			},
			expected: &action{applyCfg: &hypershiftv1beta1applyconfigurations.HostedClusterApplyConfiguration{
				ObjectMetaApplyConfiguration: &metav1applyconfigurations.ObjectMetaApplyConfiguration{Namespace: ptr.To("ns"), Name: ptr.To("hc")},
				Status: &hypershiftv1beta1applyconfigurations.HostedClusterStatusApplyConfiguration{
					Conditions: []metav1applyconfigurations.ConditionApplyConfiguration{
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeComputed),
							Status:             ptr.To(metav1.ConditionTrue),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To("large"),
							Message:            ptr.To("The HostedCluster has transitioned to a new t-shirt size."),
						},
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeTransitionPending),
							Status:             ptr.To(metav1.ConditionFalse),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To("ClusterSizeTransitioned"),
							Message:            ptr.To("The HostedCluster has transitioned to a new t-shirt size."),
						},
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeTransitionRequired),
							Status:             ptr.To(metav1.ConditionFalse),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To(hypershiftv1beta1.AsExpectedReason),
							Message:            ptr.To("The HostedCluster has transitioned to a new t-shirt size."),
						},
					},
				},
			}},
		},
		{
			name:   "transition, previously computed and tagged, hcco reports node count, kas unavailable",
			config: validCommonConfig,
			hostedCluster: &hypershiftv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "hc", Labels: map[string]string{hypershiftv1beta1.HostedClusterSizeLabel: "medium"}},
				Status: hypershiftv1beta1.HostedClusterStatus{Conditions: []metav1.Condition{{
					Type:               hypershiftv1beta1.ClusterSizeTransitionRequired,
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.NewTime(fakeClock.Now().Add(-10 * time.Minute)),
					Reason:             "large",
					Message:            "The HostedCluster will transition to a new t-shirt size.",
				}, {
					Type:               hypershiftv1beta1.ClusterSizeComputed,
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.NewTime(fakeClock.Now().Add(-20 * time.Minute)),
					Reason:             "medium",
					Message:            "The HostedCluster has transitioned to a new t-shirt size.",
				}, {
					Type:   string(hypershiftv1beta1.KubeAPIServerAvailable),
					Status: metav1.ConditionFalse,
				}}},
			},
			listHostedClusters: func(_ context.Context) (*hypershiftv1beta1.HostedClusterList, error) {
				return &hypershiftv1beta1.HostedClusterList{}, nil
			},
			hccoReportsNodeCount: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (bool, error) {
				return true, nil
			},
			hostedControlPlaneForHostedCluster: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (*hypershiftv1beta1.HostedControlPlane, error) {
				return &hypershiftv1beta1.HostedControlPlane{
					Status: hypershiftv1beta1.HostedControlPlaneStatus{NodeCount: ptr.To(300)},
				}, nil
			},
			expected: &action{applyCfg: &hypershiftv1beta1applyconfigurations.HostedClusterApplyConfiguration{
				ObjectMetaApplyConfiguration: &metav1applyconfigurations.ObjectMetaApplyConfiguration{Namespace: ptr.To("ns"), Name: ptr.To("hc")},
				Status: &hypershiftv1beta1applyconfigurations.HostedClusterStatusApplyConfiguration{
					Conditions: []metav1applyconfigurations.ConditionApplyConfiguration{
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeComputed),
							Status:             ptr.To(metav1.ConditionTrue),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To("large"),
							Message:            ptr.To("The HostedCluster has transitioned to a new t-shirt size."),
						},
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeTransitionPending),
							Status:             ptr.To(metav1.ConditionFalse),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To("ClusterSizeTransitioned"),
							Message:            ptr.To("The HostedCluster has transitioned to a new t-shirt size."),
						},
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeTransitionRequired),
							Status:             ptr.To(metav1.ConditionFalse),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To(hypershiftv1beta1.AsExpectedReason),
							Message:            ptr.To("The HostedCluster has transitioned to a new t-shirt size."),
						},
					},
				},
			}},
		},
		{
			name:   "transition, previously computed and tagged, hcco does not report node count, no autoscaling, kas unavailable",
			config: validCommonConfig,
			hostedCluster: &hypershiftv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "hc", Labels: map[string]string{hypershiftv1beta1.HostedClusterSizeLabel: "medium"}},
				Status: hypershiftv1beta1.HostedClusterStatus{Conditions: []metav1.Condition{{
					Type:               hypershiftv1beta1.ClusterSizeTransitionRequired,
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.NewTime(fakeClock.Now().Add(-10 * time.Minute)),
					Reason:             "large",
					Message:            "The HostedCluster will transition to a new t-shirt size.",
				}, {
					Type:               hypershiftv1beta1.ClusterSizeComputed,
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.NewTime(fakeClock.Now().Add(-20 * time.Minute)),
					Reason:             "medium",
					Message:            "The HostedCluster has transitioned to a new t-shirt size.",
				}, {
					Type:   string(hypershiftv1beta1.KubeAPIServerAvailable),
					Status: metav1.ConditionFalse,
				}}},
			},
			nodePoolsForHostedCluster: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (*hypershiftv1beta1.NodePoolList, error) {
				return &hypershiftv1beta1.NodePoolList{Items: []hypershiftv1beta1.NodePool{
					{Spec: hypershiftv1beta1.NodePoolSpec{Replicas: ptr.To[int32](100)}},
					{Spec: hypershiftv1beta1.NodePoolSpec{Replicas: ptr.To[int32](100)}},
					{Spec: hypershiftv1beta1.NodePoolSpec{Replicas: ptr.To[int32](100)}},
				}}, nil
			},
			listHostedClusters: func(_ context.Context) (*hypershiftv1beta1.HostedClusterList, error) {
				return &hypershiftv1beta1.HostedClusterList{}, nil
			},
			hccoReportsNodeCount: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (bool, error) {
				return false, nil
			},
			hostedControlPlaneForHostedCluster: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (*hypershiftv1beta1.HostedControlPlane, error) {
				return &hypershiftv1beta1.HostedControlPlane{
					Status: hypershiftv1beta1.HostedControlPlaneStatus{NodeCount: ptr.To(300)},
				}, nil
			},
			expected: &action{applyCfg: &hypershiftv1beta1applyconfigurations.HostedClusterApplyConfiguration{
				ObjectMetaApplyConfiguration: &metav1applyconfigurations.ObjectMetaApplyConfiguration{Namespace: ptr.To("ns"), Name: ptr.To("hc")},
				Status: &hypershiftv1beta1applyconfigurations.HostedClusterStatusApplyConfiguration{
					Conditions: []metav1applyconfigurations.ConditionApplyConfiguration{
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeComputed),
							Status:             ptr.To(metav1.ConditionTrue),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To("large"),
							Message:            ptr.To("The HostedCluster has transitioned to a new t-shirt size."),
						},
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeTransitionPending),
							Status:             ptr.To(metav1.ConditionFalse),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To("ClusterSizeTransitioned"),
							Message:            ptr.To("The HostedCluster has transitioned to a new t-shirt size."),
						},
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeTransitionRequired),
							Status:             ptr.To(metav1.ConditionFalse),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To(hypershiftv1beta1.AsExpectedReason),
							Message:            ptr.To("The HostedCluster has transitioned to a new t-shirt size."),
						},
					},
				},
			}},
		},
		{
			name:   "transition, previously computed and tagged, hcco does not report node count, has autoscaling, kas unavailable",
			config: validCommonConfig,
			hostedCluster: &hypershiftv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "hc", Labels: map[string]string{hypershiftv1beta1.HostedClusterSizeLabel: "medium"}},
				Status: hypershiftv1beta1.HostedClusterStatus{Conditions: []metav1.Condition{{
					Type:               hypershiftv1beta1.ClusterSizeTransitionRequired,
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.NewTime(fakeClock.Now().Add(-10 * time.Minute)),
					Reason:             "large",
					Message:            "The HostedCluster will transition to a new t-shirt size.",
				}, {
					Type:               hypershiftv1beta1.ClusterSizeComputed,
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.NewTime(fakeClock.Now().Add(-20 * time.Minute)),
					Reason:             "medium",
					Message:            "The HostedCluster has transitioned to a new t-shirt size.",
				}, {
					Type:   string(hypershiftv1beta1.KubeAPIServerAvailable),
					Status: metav1.ConditionFalse,
				}}},
			},
			nodePoolsForHostedCluster: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (*hypershiftv1beta1.NodePoolList, error) {
				return &hypershiftv1beta1.NodePoolList{Items: []hypershiftv1beta1.NodePool{
					{Spec: hypershiftv1beta1.NodePoolSpec{Replicas: ptr.To[int32](100)}},
					{Spec: hypershiftv1beta1.NodePoolSpec{Replicas: ptr.To[int32](100)}},
					{Spec: hypershiftv1beta1.NodePoolSpec{AutoScaling: &hypershiftv1beta1.NodePoolAutoScaling{Min: 1, Max: 100}}, Status: hypershiftv1beta1.NodePoolStatus{Replicas: 100}},
				}}, nil
			},
			listHostedClusters: func(_ context.Context) (*hypershiftv1beta1.HostedClusterList, error) {
				return &hypershiftv1beta1.HostedClusterList{}, nil
			},
			hccoReportsNodeCount: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (bool, error) {
				return false, nil
			},
			hostedControlPlaneForHostedCluster: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (*hypershiftv1beta1.HostedControlPlane, error) {
				return &hypershiftv1beta1.HostedControlPlane{
					Status: hypershiftv1beta1.HostedControlPlaneStatus{NodeCount: ptr.To(300)},
				}, nil
			},
			expected: nil,
		},
		{
			name:   "label, have previous condition",
			config: validCommonConfig,
			hostedCluster: &hypershiftv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "hc"},
				Status: hypershiftv1beta1.HostedClusterStatus{Conditions: []metav1.Condition{{
					Type:               hypershiftv1beta1.ClusterSizeComputed,
					Status:             metav1.ConditionTrue,
					Reason:             "large",
					Message:            "The HostedCluster has transitioned to a new t-shirt size.",
					LastTransitionTime: metav1.NewTime(fakeClock.Now()),
				}}},
			},
			listHostedClusters: func(_ context.Context) (*hypershiftv1beta1.HostedClusterList, error) {
				return &hypershiftv1beta1.HostedClusterList{}, nil
			},
			hccoReportsNodeCount: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (bool, error) {
				return true, nil
			},
			hostedControlPlaneForHostedCluster: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (*hypershiftv1beta1.HostedControlPlane, error) {
				return &hypershiftv1beta1.HostedControlPlane{
					Status: hypershiftv1beta1.HostedControlPlaneStatus{NodeCount: ptr.To(300)},
				}, nil
			},
			expected: &action{applyCfg: &hypershiftv1beta1applyconfigurations.HostedClusterApplyConfiguration{
				ObjectMetaApplyConfiguration: &metav1applyconfigurations.ObjectMetaApplyConfiguration{
					Namespace: ptr.To("ns"),
					Name:      ptr.To("hc"),
					Labels:    map[string]string{hypershiftv1beta1.HostedClusterSizeLabel: "large"},
				},
			}},
		},
		{
			name:   "label, have previous condition, even when current calculation is different",
			config: validCommonConfig,
			hostedCluster: &hypershiftv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "hc"},
				Status: hypershiftv1beta1.HostedClusterStatus{Conditions: []metav1.Condition{{
					Type:               hypershiftv1beta1.ClusterSizeComputed,
					Status:             metav1.ConditionTrue,
					Reason:             "large",
					Message:            "The HostedCluster has transitioned to a new t-shirt size.",
					LastTransitionTime: metav1.NewTime(fakeClock.Now()),
				}}},
			},
			listHostedClusters: func(_ context.Context) (*hypershiftv1beta1.HostedClusterList, error) {
				return &hypershiftv1beta1.HostedClusterList{}, nil
			},
			hccoReportsNodeCount: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (bool, error) {
				return true, nil
			},
			hostedControlPlaneForHostedCluster: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (*hypershiftv1beta1.HostedControlPlane, error) {
				return &hypershiftv1beta1.HostedControlPlane{
					Status: hypershiftv1beta1.HostedControlPlaneStatus{NodeCount: ptr.To(3)},
				}, nil
			},
			expected: &action{applyCfg: &hypershiftv1beta1applyconfigurations.HostedClusterApplyConfiguration{
				ObjectMetaApplyConfiguration: &metav1applyconfigurations.ObjectMetaApplyConfiguration{
					Namespace: ptr.To("ns"),
					Name:      ptr.To("hc"),
					Labels:    map[string]string{hypershiftv1beta1.HostedClusterSizeLabel: "large"},
				},
			}},
		},
		{
			name:   "delay due to hosted cluster delay for increase",
			config: validCommonConfig,
			hostedCluster: &hypershiftv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns", Name: "hc",
					Labels: map[string]string{hypershiftv1beta1.HostedClusterSizeLabel: "small"},
				},
				Status: hypershiftv1beta1.HostedClusterStatus{Conditions: []metav1.Condition{{
					Type:               hypershiftv1beta1.ClusterSizeComputed,
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.NewTime(fakeClock.Now().Add(-1 * time.Minute)),
					Reason:             "small",
					Message:            "The HostedCluster has transitioned to a new t-shirt size.",
				}, {
					Type:               hypershiftv1beta1.ClusterSizeTransitionRequired,
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.NewTime(fakeClock.Now().Add(-1 * time.Second)),
					Reason:             "large",
					Message:            "The HostedCluster will transition to a new t-shirt size.",
				}, {
					Type:   string(hypershiftv1beta1.KubeAPIServerAvailable),
					Status: metav1.ConditionTrue,
				}}},
			},
			listHostedClusters: func(_ context.Context) (*hypershiftv1beta1.HostedClusterList, error) {
				return &hypershiftv1beta1.HostedClusterList{}, nil
			},
			hccoReportsNodeCount: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (bool, error) {
				return true, nil
			},
			hostedControlPlaneForHostedCluster: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (*hypershiftv1beta1.HostedControlPlane, error) {
				return &hypershiftv1beta1.HostedControlPlane{
					Status: hypershiftv1beta1.HostedControlPlaneStatus{NodeCount: ptr.To(300)},
				}, nil
			},
			expected: &action{applyCfg: &hypershiftv1beta1applyconfigurations.HostedClusterApplyConfiguration{
				ObjectMetaApplyConfiguration: &metav1applyconfigurations.ObjectMetaApplyConfiguration{Namespace: ptr.To("ns"), Name: ptr.To("hc")},
				Status: &hypershiftv1beta1applyconfigurations.HostedClusterStatusApplyConfiguration{
					Conditions: []metav1applyconfigurations.ConditionApplyConfiguration{
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeComputed),
							Status:             ptr.To(metav1.ConditionTrue),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now().Add(-1 * time.Minute))),
							Reason:             ptr.To("small"),
							Message:            ptr.To("The HostedCluster has transitioned to a new t-shirt size."),
						},
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeTransitionPending),
							Status:             ptr.To(metav1.ConditionTrue),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To("TransitionDelayNotElapsed"),
							Message:            ptr.To("HostedClusters must wait at least 30s to increase in size after the cluster size changes."),
						},
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeTransitionRequired),
							Status:             ptr.To(metav1.ConditionTrue),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now().Add(-1 * time.Second))),
							Reason:             ptr.To("large"),
							Message:            ptr.To("The HostedCluster will transition to a new t-shirt size."),
						},
					},
				},
			}, requeueAfter: 29 * time.Second},
		},
		{
			name:   "delay due to hosted cluster delay for decrease",
			config: validCommonConfig,
			hostedCluster: &hypershiftv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns", Name: "hc",
					Labels: map[string]string{hypershiftv1beta1.HostedClusterSizeLabel: "large"},
				},
				Status: hypershiftv1beta1.HostedClusterStatus{Conditions: []metav1.Condition{{
					Type:               hypershiftv1beta1.ClusterSizeComputed,
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.NewTime(fakeClock.Now().Add(-1 * time.Minute)),
					Reason:             "large",
					Message:            "The HostedCluster has transitioned to a new t-shirt size.",
				}, {
					Type:               hypershiftv1beta1.ClusterSizeTransitionRequired,
					Status:             metav1.ConditionTrue,
					Reason:             "small",
					Message:            "The HostedCluster will transition to a new t-shirt size.",
					LastTransitionTime: metav1.NewTime(fakeClock.Now().Add(-1 * time.Minute)),
				}, {
					Type:   string(hypershiftv1beta1.KubeAPIServerAvailable),
					Status: metav1.ConditionTrue,
				}}},
			},
			listHostedClusters: func(_ context.Context) (*hypershiftv1beta1.HostedClusterList, error) {
				return &hypershiftv1beta1.HostedClusterList{}, nil
			},
			hccoReportsNodeCount: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (bool, error) {
				return true, nil
			},
			hostedControlPlaneForHostedCluster: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (*hypershiftv1beta1.HostedControlPlane, error) {
				return &hypershiftv1beta1.HostedControlPlane{
					Status: hypershiftv1beta1.HostedControlPlaneStatus{NodeCount: ptr.To(3)},
				}, nil
			},
			expected: &action{applyCfg: &hypershiftv1beta1applyconfigurations.HostedClusterApplyConfiguration{
				ObjectMetaApplyConfiguration: &metav1applyconfigurations.ObjectMetaApplyConfiguration{Namespace: ptr.To("ns"), Name: ptr.To("hc")},
				Status: &hypershiftv1beta1applyconfigurations.HostedClusterStatusApplyConfiguration{
					Conditions: []metav1applyconfigurations.ConditionApplyConfiguration{
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeComputed),
							Status:             ptr.To(metav1.ConditionTrue),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now().Add(-1 * time.Minute))),
							Reason:             ptr.To("large"),
							Message:            ptr.To("The HostedCluster has transitioned to a new t-shirt size."),
						},
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeTransitionPending),
							Status:             ptr.To(metav1.ConditionTrue),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To("TransitionDelayNotElapsed"),
							Message:            ptr.To("HostedClusters must wait at least 10m0s to decrease in size after the cluster size changes."),
						},
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeTransitionRequired),
							Status:             ptr.To(metav1.ConditionTrue),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now().Add(-1 * time.Minute))),
							Reason:             ptr.To("small"),
							Message:            ptr.To("The HostedCluster will transition to a new t-shirt size."),
						},
					},
				},
			}, requeueAfter: 9 * time.Minute},
		},
		{
			name:   "delay due to hosted cluster delay, update target size during delay",
			config: validCommonConfig,
			hostedCluster: &hypershiftv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns", Name: "hc",
					Labels: map[string]string{hypershiftv1beta1.HostedClusterSizeLabel: "large"},
				},
				Status: hypershiftv1beta1.HostedClusterStatus{Conditions: []metav1.Condition{{
					Type:               hypershiftv1beta1.ClusterSizeComputed,
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.NewTime(fakeClock.Now().Add(-1 * time.Minute)),
					Reason:             "large",
					Message:            "The HostedCluster has transitioned to a new t-shirt size.",
				}, {
					Type:               hypershiftv1beta1.ClusterSizeTransitionRequired,
					Status:             metav1.ConditionTrue,
					Reason:             "small",
					Message:            "The HostedCluster will transition to a new t-shirt size.",
					LastTransitionTime: metav1.NewTime(fakeClock.Now().Add(-1 * time.Minute)),
				}, {
					Type:   string(hypershiftv1beta1.KubeAPIServerAvailable),
					Status: metav1.ConditionTrue,
				}}},
			},
			listHostedClusters: func(_ context.Context) (*hypershiftv1beta1.HostedClusterList, error) {
				return &hypershiftv1beta1.HostedClusterList{}, nil
			},
			hccoReportsNodeCount: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (bool, error) {
				return true, nil
			},
			hostedControlPlaneForHostedCluster: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (*hypershiftv1beta1.HostedControlPlane, error) {
				return &hypershiftv1beta1.HostedControlPlane{
					Status: hypershiftv1beta1.HostedControlPlaneStatus{NodeCount: ptr.To(30)},
				}, nil
			},
			expected: &action{applyCfg: &hypershiftv1beta1applyconfigurations.HostedClusterApplyConfiguration{
				ObjectMetaApplyConfiguration: &metav1applyconfigurations.ObjectMetaApplyConfiguration{Namespace: ptr.To("ns"), Name: ptr.To("hc")},
				Status: &hypershiftv1beta1applyconfigurations.HostedClusterStatusApplyConfiguration{
					Conditions: []metav1applyconfigurations.ConditionApplyConfiguration{
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeComputed),
							Status:             ptr.To(metav1.ConditionTrue),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now().Add(-1 * time.Minute))),
							Reason:             ptr.To("large"),
							Message:            ptr.To("The HostedCluster has transitioned to a new t-shirt size."),
						},
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeTransitionPending),
							Status:             ptr.To(metav1.ConditionTrue),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To("TransitionDelayNotElapsed"),
							Message:            ptr.To("HostedClusters must wait at least 10m0s to decrease in size after the cluster size changes."),
						},
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeTransitionRequired),
							Status:             ptr.To(metav1.ConditionTrue),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To("medium"),
							Message:            ptr.To("The HostedCluster will transition to a new t-shirt size."),
						},
					},
				},
			}, requeueAfter: 9 * time.Minute},
		},
		{
			name:   "transition, longer than delay",
			config: validCommonConfig,
			hostedCluster: &hypershiftv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns", Name: "hc",
					Labels: map[string]string{hypershiftv1beta1.HostedClusterSizeLabel: "large"},
				},
				Status: hypershiftv1beta1.HostedClusterStatus{Conditions: []metav1.Condition{{
					Type:               hypershiftv1beta1.ClusterSizeComputed,
					Status:             metav1.ConditionTrue,
					Reason:             "large",
					Message:            "The HostedCluster has transitioned to a new t-shirt size.",
					LastTransitionTime: metav1.NewTime(fakeClock.Now().Add(-100 * time.Minute)),
				}, {
					Type:               hypershiftv1beta1.ClusterSizeTransitionRequired,
					Status:             metav1.ConditionTrue,
					Reason:             "small",
					Message:            "The HostedCluster will transition to a new t-shirt size.",
					LastTransitionTime: metav1.NewTime(fakeClock.Now().Add(-50 * time.Minute)),
				}, {
					Type:   string(hypershiftv1beta1.KubeAPIServerAvailable),
					Status: metav1.ConditionTrue,
				}}},
			},
			listHostedClusters: func(_ context.Context) (*hypershiftv1beta1.HostedClusterList, error) {
				return &hypershiftv1beta1.HostedClusterList{}, nil
			},
			hccoReportsNodeCount: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (bool, error) {
				return true, nil
			},
			hostedControlPlaneForHostedCluster: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (*hypershiftv1beta1.HostedControlPlane, error) {
				return &hypershiftv1beta1.HostedControlPlane{
					Status: hypershiftv1beta1.HostedControlPlaneStatus{NodeCount: ptr.To(3)},
				}, nil
			},
			expected: &action{applyCfg: &hypershiftv1beta1applyconfigurations.HostedClusterApplyConfiguration{
				ObjectMetaApplyConfiguration: &metav1applyconfigurations.ObjectMetaApplyConfiguration{Namespace: ptr.To("ns"), Name: ptr.To("hc")},
				Status: &hypershiftv1beta1applyconfigurations.HostedClusterStatusApplyConfiguration{
					Conditions: []metav1applyconfigurations.ConditionApplyConfiguration{
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeComputed),
							Status:             ptr.To(metav1.ConditionTrue),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To("small"),
							Message:            ptr.To("The HostedCluster has transitioned to a new t-shirt size."),
						},
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeTransitionPending),
							Status:             ptr.To(metav1.ConditionFalse),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To("ClusterSizeTransitioned"),
							Message:            ptr.To("The HostedCluster has transitioned to a new t-shirt size."),
						},
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeTransitionRequired),
							Status:             ptr.To(metav1.ConditionFalse),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To(hypershiftv1beta1.AsExpectedReason),
							Message:            ptr.To("The HostedCluster has transitioned to a new t-shirt size."),
						},
					},
				},
			}},
		},
		{
			name:   "no-op, delay already exposed in status",
			config: validCommonConfig,
			hostedCluster: &hypershiftv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns", Name: "hc",
					Labels: map[string]string{hypershiftv1beta1.HostedClusterSizeLabel: "large"},
				},
				Status: hypershiftv1beta1.HostedClusterStatus{Conditions: []metav1.Condition{{
					Type:               hypershiftv1beta1.ClusterSizeComputed,
					Status:             metav1.ConditionTrue,
					Reason:             "large",
					Message:            "The HostedCluster has transitioned to a new t-shirt size.",
					LastTransitionTime: metav1.NewTime(fakeClock.Now().Add(-1 * time.Minute)),
				}, {
					Type:               hypershiftv1beta1.ClusterSizeTransitionPending,
					Status:             metav1.ConditionTrue,
					Reason:             "TransitionDelayNotElapsed",
					Message:            "HostedClusters must wait at least 10m0s to decrease in size after the cluster size changes.",
					LastTransitionTime: metav1.NewTime(fakeClock.Now().Add(-1 * time.Second)),
				}, {
					Type:               hypershiftv1beta1.ClusterSizeTransitionRequired,
					Status:             metav1.ConditionTrue,
					Reason:             "small",
					Message:            "The HostedCluster will transition to a new t-shirt size.",
					LastTransitionTime: metav1.NewTime(fakeClock.Now().Add(-2 * time.Minute)),
				}, {
					Type:   string(hypershiftv1beta1.KubeAPIServerAvailable),
					Status: metav1.ConditionTrue,
				}}},
			},
			listHostedClusters: func(_ context.Context) (*hypershiftv1beta1.HostedClusterList, error) {
				return &hypershiftv1beta1.HostedClusterList{}, nil
			},
			hccoReportsNodeCount: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (bool, error) {
				return true, nil
			},
			hostedControlPlaneForHostedCluster: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (*hypershiftv1beta1.HostedControlPlane, error) {
				return &hypershiftv1beta1.HostedControlPlane{
					Status: hypershiftv1beta1.HostedControlPlaneStatus{NodeCount: ptr.To(3)},
				}, nil
			},
		},
		{
			name:   "delay for concurrency",
			config: validCommonConfig,
			hostedCluster: &hypershiftv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns", Name: "hc",
					Labels:      map[string]string{hypershiftv1beta1.HostedClusterSizeLabel: "large"},
					Annotations: map[string]string{hypershiftv1beta1.HostedClusterScheduledAnnotation: "true"},
				},
				Status: hypershiftv1beta1.HostedClusterStatus{Conditions: []metav1.Condition{{
					Type:               hypershiftv1beta1.ClusterSizeComputed,
					Status:             metav1.ConditionTrue,
					Reason:             "large",
					Message:            "The HostedCluster has transitioned to a new t-shirt size.",
					LastTransitionTime: metav1.NewTime(fakeClock.Now().Add(-20 * time.Minute)),
				}, {
					Type:               hypershiftv1beta1.ClusterSizeTransitionRequired,
					Status:             metav1.ConditionTrue,
					Reason:             "small",
					Message:            "The HostedCluster will transition to a new t-shirt size.",
					LastTransitionTime: metav1.NewTime(fakeClock.Now().Add(-15 * time.Minute)),
				}, {
					Type:   string(hypershiftv1beta1.KubeAPIServerAvailable),
					Status: metav1.ConditionTrue,
				}}},
			},
			listHostedClusters: func(_ context.Context) (*hypershiftv1beta1.HostedClusterList, error) {
				return &hypershiftv1beta1.HostedClusterList{Items: []hypershiftv1beta1.HostedCluster{
					hostedClusterWithTransition("first", fakeClock.Now().Add(-1*time.Minute)),
					hostedClusterWithTransition("second", fakeClock.Now().Add(-2*time.Minute)),
					hostedClusterWithTransition("third", fakeClock.Now().Add(-3*time.Minute)),
					hostedClusterWithTransition("fourth", fakeClock.Now().Add(-4*time.Minute)),
					hostedClusterWithTransition("fifth", fakeClock.Now().Add(-5*time.Minute)),
				}}, nil
			},
			hccoReportsNodeCount: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (bool, error) {
				return true, nil
			},
			hostedControlPlaneForHostedCluster: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (*hypershiftv1beta1.HostedControlPlane, error) {
				return &hypershiftv1beta1.HostedControlPlane{
					Status: hypershiftv1beta1.HostedControlPlaneStatus{NodeCount: ptr.To(3)},
				}, nil
			},
			expected: &action{applyCfg: &hypershiftv1beta1applyconfigurations.HostedClusterApplyConfiguration{
				ObjectMetaApplyConfiguration: &metav1applyconfigurations.ObjectMetaApplyConfiguration{Namespace: ptr.To("ns"), Name: ptr.To("hc")},
				Status: &hypershiftv1beta1applyconfigurations.HostedClusterStatusApplyConfiguration{
					Conditions: []metav1applyconfigurations.ConditionApplyConfiguration{
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeComputed),
							Status:             ptr.To(metav1.ConditionTrue),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now().Add(-20 * time.Minute))),
							Reason:             ptr.To("large"),
							Message:            ptr.To("The HostedCluster has transitioned to a new t-shirt size."),
						},
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeTransitionPending),
							Status:             ptr.To(metav1.ConditionTrue),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To("ConcurrencyLimitReached"),
							Message:            ptr.To("5 HostedClusters have already transitioned sizes in the last 10m0s, more time must elapse before the next transition."),
						},
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeTransitionRequired),
							Status:             ptr.To(metav1.ConditionTrue),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now().Add(-15 * time.Minute))),
							Reason:             ptr.To("small"),
							Message:            ptr.To("The HostedCluster will transition to a new t-shirt size."),
						},
					},
				},
			}, requeueAfter: 5 * time.Minute},
		},
		{
			name:   "delay existing scheduled cluster without size for concurrency",
			config: validCommonConfig,
			hostedCluster: &hypershiftv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns", Name: "hc",
					Annotations: map[string]string{hypershiftv1beta1.HostedClusterScheduledAnnotation: "true"},
				},
			},
			listHostedClusters: func(_ context.Context) (*hypershiftv1beta1.HostedClusterList, error) {
				return &hypershiftv1beta1.HostedClusterList{Items: []hypershiftv1beta1.HostedCluster{
					hostedClusterWithTransition("first", fakeClock.Now().Add(-1*time.Minute)),
					hostedClusterWithTransition("second", fakeClock.Now().Add(-2*time.Minute)),
					hostedClusterWithTransition("third", fakeClock.Now().Add(-3*time.Minute)),
					hostedClusterWithTransition("fourth", fakeClock.Now().Add(-4*time.Minute)),
					hostedClusterWithTransition("fifth", fakeClock.Now().Add(-5*time.Minute)),
				}}, nil
			},
			hccoReportsNodeCount: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (bool, error) {
				return true, nil
			},
			hostedControlPlaneForHostedCluster: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (*hypershiftv1beta1.HostedControlPlane, error) {
				return &hypershiftv1beta1.HostedControlPlane{
					Status: hypershiftv1beta1.HostedControlPlaneStatus{NodeCount: ptr.To(3)},
				}, nil
			},
			expected: &action{applyCfg: &hypershiftv1beta1applyconfigurations.HostedClusterApplyConfiguration{
				ObjectMetaApplyConfiguration: &metav1applyconfigurations.ObjectMetaApplyConfiguration{Namespace: ptr.To("ns"), Name: ptr.To("hc")},
				Status: &hypershiftv1beta1applyconfigurations.HostedClusterStatusApplyConfiguration{
					Conditions: []metav1applyconfigurations.ConditionApplyConfiguration{
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeTransitionPending),
							Status:             ptr.To(metav1.ConditionTrue),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To("ConcurrencyLimitReached"),
							Message:            ptr.To("5 HostedClusters have already transitioned sizes in the last 10m0s, more time must elapse before the next transition."),
						},
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeTransitionRequired),
							Status:             ptr.To(metav1.ConditionTrue),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To("small"),
							Message:            ptr.To("The HostedCluster will transition to a new t-shirt size."),
						},
					},
				},
			}, requeueAfter: 5 * time.Minute},
		},
		{
			name:   "delay for concurrency, no-op since condition already present",
			config: validCommonConfig,
			hostedCluster: &hypershiftv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns", Name: "hc",
					Labels:      map[string]string{hypershiftv1beta1.HostedClusterSizeLabel: "large"},
					Annotations: map[string]string{hypershiftv1beta1.HostedClusterScheduledAnnotation: "true"},
				},
				Status: hypershiftv1beta1.HostedClusterStatus{Conditions: []metav1.Condition{{
					Type:               hypershiftv1beta1.ClusterSizeComputed,
					Status:             metav1.ConditionTrue,
					Reason:             "large",
					Message:            "The HostedCluster has transitioned to a new t-shirt size.",
					LastTransitionTime: metav1.NewTime(fakeClock.Now().Add(-20 * time.Minute)),
				}, {
					Type:               hypershiftv1beta1.ClusterSizeTransitionPending,
					Status:             metav1.ConditionTrue,
					Reason:             "ConcurrencyLimitReached",
					Message:            "5 HostedClusters have already transitioned sizes in the last 10m0s, more time must elapse before the next transition.",
					LastTransitionTime: metav1.NewTime(fakeClock.Now().Add(-1 * time.Minute)),
				}, {
					Type:               hypershiftv1beta1.ClusterSizeTransitionRequired,
					Status:             metav1.ConditionTrue,
					Reason:             "small",
					Message:            "The HostedCluster will transition to a new t-shirt size.",
					LastTransitionTime: metav1.NewTime(fakeClock.Now().Add(-15 * time.Minute)),
				}, {
					Type:   string(hypershiftv1beta1.KubeAPIServerAvailable),
					Status: metav1.ConditionTrue,
				}}},
			},
			listHostedClusters: func(_ context.Context) (*hypershiftv1beta1.HostedClusterList, error) {
				return &hypershiftv1beta1.HostedClusterList{Items: []hypershiftv1beta1.HostedCluster{
					hostedClusterWithTransition("first", fakeClock.Now().Add(-1*time.Minute)),
					hostedClusterWithTransition("second", fakeClock.Now().Add(-2*time.Minute)),
					hostedClusterWithTransition("third", fakeClock.Now().Add(-3*time.Minute)),
					hostedClusterWithTransition("fourth", fakeClock.Now().Add(-4*time.Minute)),
					hostedClusterWithTransition("fifth", fakeClock.Now().Add(-5*time.Minute)),
				}}, nil
			},
			hccoReportsNodeCount: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (bool, error) {
				return true, nil
			},
			hostedControlPlaneForHostedCluster: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (*hypershiftv1beta1.HostedControlPlane, error) {
				return &hypershiftv1beta1.HostedControlPlane{
					Status: hypershiftv1beta1.HostedControlPlaneStatus{NodeCount: ptr.To(3)},
				}, nil
			},
		},
		{
			name:   "delay for concurrency, undo conditions since cluster returned to original size during delay",
			config: validCommonConfig,
			hostedCluster: &hypershiftv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns", Name: "hc",
					Labels:      map[string]string{hypershiftv1beta1.HostedClusterSizeLabel: "large"},
					Annotations: map[string]string{hypershiftv1beta1.HostedClusterScheduledAnnotation: "true"},
				},
				Status: hypershiftv1beta1.HostedClusterStatus{Conditions: []metav1.Condition{{
					Type:               hypershiftv1beta1.ClusterSizeComputed,
					Status:             metav1.ConditionTrue,
					Reason:             "large",
					Message:            "The HostedCluster has transitioned to a new t-shirt size.",
					LastTransitionTime: metav1.NewTime(fakeClock.Now().Add(-20 * time.Minute)),
				}, {
					Type:               hypershiftv1beta1.ClusterSizeTransitionPending,
					Status:             metav1.ConditionTrue,
					Reason:             "ConcurrencyLimitReached",
					Message:            "5 HostedClusters have already transitioned sizes in the last 10m0s, more time must elapse before the next transition.",
					LastTransitionTime: metav1.NewTime(fakeClock.Now().Add(-1 * time.Minute)),
				}, {
					Type:               hypershiftv1beta1.ClusterSizeTransitionRequired,
					Status:             metav1.ConditionTrue,
					Reason:             "small",
					Message:            "The HostedCluster will transition to a new t-shirt size.",
					LastTransitionTime: metav1.NewTime(fakeClock.Now().Add(-15 * time.Minute)),
				}, {
					Type:   string(hypershiftv1beta1.KubeAPIServerAvailable),
					Status: metav1.ConditionTrue,
				}}},
			},
			listHostedClusters: func(_ context.Context) (*hypershiftv1beta1.HostedClusterList, error) {
				return &hypershiftv1beta1.HostedClusterList{}, nil
			},
			hccoReportsNodeCount: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (bool, error) {
				return true, nil
			},
			hostedControlPlaneForHostedCluster: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (*hypershiftv1beta1.HostedControlPlane, error) {
				return &hypershiftv1beta1.HostedControlPlane{
					Status: hypershiftv1beta1.HostedControlPlaneStatus{NodeCount: ptr.To(300)},
				}, nil
			},
			expected: &action{applyCfg: &hypershiftv1beta1applyconfigurations.HostedClusterApplyConfiguration{
				ObjectMetaApplyConfiguration: &metav1applyconfigurations.ObjectMetaApplyConfiguration{Namespace: ptr.To("ns"), Name: ptr.To("hc")},
				Status: &hypershiftv1beta1applyconfigurations.HostedClusterStatusApplyConfiguration{
					Conditions: []metav1applyconfigurations.ConditionApplyConfiguration{
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeComputed),
							Status:             ptr.To(metav1.ConditionTrue),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now().Add(-20 * time.Minute))),
							Reason:             ptr.To("large"),
							Message:            ptr.To("The HostedCluster has transitioned to a new t-shirt size."),
						},
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeTransitionPending),
							Status:             ptr.To(metav1.ConditionFalse),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now().Add(-20 * time.Minute))),
							Reason:             ptr.To("ClusterSizeTransitioned"),
							Message:            ptr.To("The HostedCluster has transitioned to a new t-shirt size."),
						},
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeTransitionRequired),
							Status:             ptr.To(metav1.ConditionFalse),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now().Add(-20 * time.Minute))),
							Reason:             ptr.To(hypershiftv1beta1.AsExpectedReason),
							Message:            ptr.To("The HostedCluster has transitioned to a new t-shirt size."),
						},
					},
				},
			}},
		},
		{
			name:   "transition, not enough previous transitions to limit concurrency",
			config: validCommonConfig,
			hostedCluster: &hypershiftv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns", Name: "hc",
					Labels:      map[string]string{hypershiftv1beta1.HostedClusterSizeLabel: "small"},
					Annotations: map[string]string{hypershiftv1beta1.HostedClusterScheduledAnnotation: "true"},
				},
				Status: hypershiftv1beta1.HostedClusterStatus{Conditions: []metav1.Condition{{
					Type:               hypershiftv1beta1.ClusterSizeComputed,
					Status:             metav1.ConditionTrue,
					Reason:             "small",
					Message:            "The HostedCluster has transitioned to a new t-shirt size.",
					LastTransitionTime: metav1.NewTime(fakeClock.Now().Add(-15 * time.Minute)),
				}, {
					Type:               hypershiftv1beta1.ClusterSizeTransitionRequired,
					Status:             metav1.ConditionTrue,
					Reason:             "large",
					Message:            "The HostedCluster will transition to a new t-shirt size.",
					LastTransitionTime: metav1.NewTime(fakeClock.Now().Add(-1 * time.Minute)),
				}, {
					Type:   string(hypershiftv1beta1.KubeAPIServerAvailable),
					Status: metav1.ConditionTrue,
				}}},
			},
			listHostedClusters: func(_ context.Context) (*hypershiftv1beta1.HostedClusterList, error) {
				return &hypershiftv1beta1.HostedClusterList{Items: []hypershiftv1beta1.HostedCluster{
					hostedClusterWithTransition("first", fakeClock.Now().Add(-1*time.Minute)),
					hostedClusterWithTransition("second", fakeClock.Now().Add(-2*time.Minute)),
					hostedClusterWithTransition("third", fakeClock.Now().Add(-3*time.Minute)),
					hostedClusterWithTransition("fourth", fakeClock.Now().Add(-4*time.Minute)),
				}}, nil
			},
			hccoReportsNodeCount: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (bool, error) {
				return true, nil
			},
			hostedControlPlaneForHostedCluster: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (*hypershiftv1beta1.HostedControlPlane, error) {
				return &hypershiftv1beta1.HostedControlPlane{
					Status: hypershiftv1beta1.HostedControlPlaneStatus{NodeCount: ptr.To(300)},
				}, nil
			},
			expected: &action{applyCfg: &hypershiftv1beta1applyconfigurations.HostedClusterApplyConfiguration{
				ObjectMetaApplyConfiguration: &metav1applyconfigurations.ObjectMetaApplyConfiguration{Namespace: ptr.To("ns"), Name: ptr.To("hc")},
				Status: &hypershiftv1beta1applyconfigurations.HostedClusterStatusApplyConfiguration{
					Conditions: []metav1applyconfigurations.ConditionApplyConfiguration{
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeComputed),
							Status:             ptr.To(metav1.ConditionTrue),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To("large"),
							Message:            ptr.To("The HostedCluster has transitioned to a new t-shirt size."),
						},
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeTransitionPending),
							Status:             ptr.To(metav1.ConditionFalse),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To("ClusterSizeTransitioned"),
							Message:            ptr.To("The HostedCluster has transitioned to a new t-shirt size."),
						},
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeTransitionRequired),
							Status:             ptr.To(metav1.ConditionFalse),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To(hypershiftv1beta1.AsExpectedReason),
							Message:            ptr.To("The HostedCluster has transitioned to a new t-shirt size."),
						},
					},
				},
			}},
		},
		{
			name:   "transition, don't delay unscheduled cluster for concurrency",
			config: validCommonConfig,
			hostedCluster: &hypershiftv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns", Name: "hc",
					Labels: map[string]string{hypershiftv1beta1.HostedClusterSizeLabel: "large"},
				},
				Status: hypershiftv1beta1.HostedClusterStatus{Conditions: []metav1.Condition{{
					Type:               hypershiftv1beta1.ClusterSizeComputed,
					Status:             metav1.ConditionTrue,
					Reason:             "large",
					Message:            "The HostedCluster has transitioned to a new t-shirt size.",
					LastTransitionTime: metav1.NewTime(fakeClock.Now().Add(-20 * time.Minute)),
				}, {
					Type:               hypershiftv1beta1.ClusterSizeTransitionRequired,
					Status:             metav1.ConditionTrue,
					Reason:             "small",
					Message:            "The HostedCluster will transition to a new t-shirt size.",
					LastTransitionTime: metav1.NewTime(fakeClock.Now().Add(-15 * time.Minute)),
				}}},
			},
			listHostedClusters: func(_ context.Context) (*hypershiftv1beta1.HostedClusterList, error) {
				return &hypershiftv1beta1.HostedClusterList{Items: []hypershiftv1beta1.HostedCluster{
					hostedClusterWithTransition("first", fakeClock.Now().Add(-1*time.Minute)),
					hostedClusterWithTransition("second", fakeClock.Now().Add(-2*time.Minute)),
					hostedClusterWithTransition("third", fakeClock.Now().Add(-3*time.Minute)),
					hostedClusterWithTransition("fourth", fakeClock.Now().Add(-4*time.Minute)),
					hostedClusterWithTransition("fifth", fakeClock.Now().Add(-5*time.Minute)),
				}}, nil
			},
			hccoReportsNodeCount: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (bool, error) {
				return true, nil
			},
			hostedControlPlaneForHostedCluster: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (*hypershiftv1beta1.HostedControlPlane, error) {
				return &hypershiftv1beta1.HostedControlPlane{
					Status: hypershiftv1beta1.HostedControlPlaneStatus{NodeCount: ptr.To(3)},
				}, nil
			},
			expected: &action{applyCfg: &hypershiftv1beta1applyconfigurations.HostedClusterApplyConfiguration{
				ObjectMetaApplyConfiguration: &metav1applyconfigurations.ObjectMetaApplyConfiguration{Namespace: ptr.To("ns"), Name: ptr.To("hc")},
				Status: &hypershiftv1beta1applyconfigurations.HostedClusterStatusApplyConfiguration{
					Conditions: []metav1applyconfigurations.ConditionApplyConfiguration{
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeComputed),
							Status:             ptr.To(metav1.ConditionTrue),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To("small"),
							Message:            ptr.To("The HostedCluster has transitioned to a new t-shirt size."),
						},
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeTransitionPending),
							Status:             ptr.To(metav1.ConditionFalse),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To("ClusterSizeTransitioned"),
							Message:            ptr.To("The HostedCluster has transitioned to a new t-shirt size."),
						},
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeTransitionRequired),
							Status:             ptr.To(metav1.ConditionFalse),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To(hypershiftv1beta1.AsExpectedReason),
							Message:            ptr.To("The HostedCluster has transitioned to a new t-shirt size."),
						},
					},
				},
			}},
		},
		{
			name:   "transition, don't delay brand new cluster for concurrency",
			config: validCommonConfig,
			hostedCluster: &hypershiftv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns", Name: "hc",
				},
			},
			listHostedClusters: func(_ context.Context) (*hypershiftv1beta1.HostedClusterList, error) {
				return &hypershiftv1beta1.HostedClusterList{Items: []hypershiftv1beta1.HostedCluster{
					hostedClusterWithTransition("first", fakeClock.Now().Add(-1*time.Minute)),
					hostedClusterWithTransition("second", fakeClock.Now().Add(-2*time.Minute)),
					hostedClusterWithTransition("third", fakeClock.Now().Add(-3*time.Minute)),
					hostedClusterWithTransition("fourth", fakeClock.Now().Add(-4*time.Minute)),
					hostedClusterWithTransition("fifth", fakeClock.Now().Add(-5*time.Minute)),
				}}, nil
			},
			hccoReportsNodeCount: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (bool, error) {
				return true, nil
			},
			hostedControlPlaneForHostedCluster: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (*hypershiftv1beta1.HostedControlPlane, error) {
				return &hypershiftv1beta1.HostedControlPlane{
					Status: hypershiftv1beta1.HostedControlPlaneStatus{NodeCount: ptr.To(3)},
				}, nil
			},
			expected: &action{applyCfg: &hypershiftv1beta1applyconfigurations.HostedClusterApplyConfiguration{
				ObjectMetaApplyConfiguration: &metav1applyconfigurations.ObjectMetaApplyConfiguration{Namespace: ptr.To("ns"), Name: ptr.To("hc")},
				Status: &hypershiftv1beta1applyconfigurations.HostedClusterStatusApplyConfiguration{
					Conditions: []metav1applyconfigurations.ConditionApplyConfiguration{
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeComputed),
							Status:             ptr.To(metav1.ConditionTrue),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To("small"),
							Message:            ptr.To("The HostedCluster has transitioned to a new t-shirt size."),
						},
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeTransitionPending),
							Status:             ptr.To(metav1.ConditionFalse),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To("ClusterSizeTransitioned"),
							Message:            ptr.To("The HostedCluster has transitioned to a new t-shirt size."),
						},
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeTransitionRequired),
							Status:             ptr.To(metav1.ConditionFalse),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To(hypershiftv1beta1.AsExpectedReason),
							Message:            ptr.To("The HostedCluster has transitioned to a new t-shirt size."),
						},
					},
				},
			}},
		},
		{
			name:   "transition, use override size",
			config: validCommonConfig,
			hostedCluster: &hypershiftv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns", Name: "hc",
					Annotations: map[string]string{
						hypershiftv1beta1.ClusterSizeOverrideAnnotation: "large",
					},
				},
			},
			listHostedClusters: func(_ context.Context) (*hypershiftv1beta1.HostedClusterList, error) {
				return &hypershiftv1beta1.HostedClusterList{Items: []hypershiftv1beta1.HostedCluster{
					hostedClusterWithTransition("first", fakeClock.Now().Add(-1*time.Minute)),
					hostedClusterWithTransition("second", fakeClock.Now().Add(-2*time.Minute)),
					hostedClusterWithTransition("third", fakeClock.Now().Add(-3*time.Minute)),
					hostedClusterWithTransition("fourth", fakeClock.Now().Add(-4*time.Minute)),
					hostedClusterWithTransition("fifth", fakeClock.Now().Add(-5*time.Minute)),
				}}, nil
			},
			hccoReportsNodeCount: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (bool, error) {
				return true, nil
			},
			hostedControlPlaneForHostedCluster: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (*hypershiftv1beta1.HostedControlPlane, error) {
				return &hypershiftv1beta1.HostedControlPlane{
					Status: hypershiftv1beta1.HostedControlPlaneStatus{NodeCount: ptr.To(0)},
				}, nil
			},
			expected: &action{applyCfg: &hypershiftv1beta1applyconfigurations.HostedClusterApplyConfiguration{
				ObjectMetaApplyConfiguration: &metav1applyconfigurations.ObjectMetaApplyConfiguration{Namespace: ptr.To("ns"), Name: ptr.To("hc")},
				Status: &hypershiftv1beta1applyconfigurations.HostedClusterStatusApplyConfiguration{
					Conditions: []metav1applyconfigurations.ConditionApplyConfiguration{
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeComputed),
							Status:             ptr.To(metav1.ConditionTrue),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To("large"),
							Message:            ptr.To("The HostedCluster has transitioned to a new t-shirt size."),
						},
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeTransitionPending),
							Status:             ptr.To(metav1.ConditionFalse),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To("ClusterSizeTransitioned"),
							Message:            ptr.To("The HostedCluster has transitioned to a new t-shirt size."),
						},
						{
							Type:               ptr.To(hypershiftv1beta1.ClusterSizeTransitionRequired),
							Status:             ptr.To(metav1.ConditionFalse),
							LastTransitionTime: ptr.To(metav1.NewTime(fakeClock.Now())),
							Reason:             ptr.To(hypershiftv1beta1.AsExpectedReason),
							Message:            ptr.To("The HostedCluster has transitioned to a new t-shirt size."),
						},
					},
				},
			}},
		},
		{
			name:   "happy case: cluster has not changed size, already has condition and label",
			config: validCommonConfig,
			hostedCluster: &hypershiftv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns", Name: "hc",
					Labels: map[string]string{hypershiftv1beta1.HostedClusterSizeLabel: "small"},
				},
				Status: hypershiftv1beta1.HostedClusterStatus{Conditions: []metav1.Condition{{
					Type:               hypershiftv1beta1.ClusterSizeComputed,
					Status:             metav1.ConditionTrue,
					Reason:             "small",
					Message:            "The HostedCluster has transitioned to a new t-shirt size.",
					LastTransitionTime: metav1.NewTime(fakeClock.Now().Add(-1 * time.Minute)),
				}, {
					Type:               hypershiftv1beta1.ClusterSizeTransitionPending,
					Status:             metav1.ConditionFalse,
					Reason:             "ClusterSizeTransitioned",
					Message:            "The HostedCluster has transitioned to a new t-shirt size.",
					LastTransitionTime: metav1.NewTime(fakeClock.Now().Add(-1 * time.Minute)),
				}, {
					Type:               hypershiftv1beta1.ClusterSizeTransitionRequired,
					Status:             metav1.ConditionFalse,
					Reason:             hypershiftv1beta1.AsExpectedReason,
					Message:            "The HostedCluster has transitioned to a new t-shirt size.",
					LastTransitionTime: metav1.NewTime(fakeClock.Now().Add(-1 * time.Minute)),
				}, {
					Type:   string(hypershiftv1beta1.KubeAPIServerAvailable),
					Status: metav1.ConditionTrue,
				}}},
			},
			listHostedClusters: func(_ context.Context) (*hypershiftv1beta1.HostedClusterList, error) {
				return &hypershiftv1beta1.HostedClusterList{}, nil
			},
			hccoReportsNodeCount: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (bool, error) {
				return true, nil
			},
			hostedControlPlaneForHostedCluster: func(_ context.Context, _ *hypershiftv1beta1.HostedCluster) (*hypershiftv1beta1.HostedControlPlane, error) {
				return &hypershiftv1beta1.HostedControlPlane{
					Status: hypershiftv1beta1.HostedControlPlaneStatus{NodeCount: ptr.To(3)},
				}, nil
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			r := &reconciler{
				now:                                fakeClock.Now,
				listHostedClusters:                 testCase.listHostedClusters,
				hccoReportsNodeCount:               testCase.hccoReportsNodeCount,
				nodePoolsForHostedCluster:          testCase.nodePoolsForHostedCluster,
				hostedControlPlaneForHostedCluster: testCase.hostedControlPlaneForHostedCluster,
			}
			action, err := r.reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: testCase.hostedCluster.Namespace, Name: testCase.hostedCluster.Name}}, testCase.config, testCase.hostedCluster)
			if err == nil && testCase.expectedErr {
				t.Fatalf("expected an error but got none")
			}
			if err != nil && !testCase.expectedErr {
				t.Fatalf("expected no error but got one: %v", err)
			}
			if diff := cmp.Diff(action, testCase.expected, compareActions()...); diff != "" {
				t.Fatalf("got incorrect action: %v", diff)
			}
		})
	}
}

func hostedClusterWithTransition(name string, transition time.Time) hypershiftv1beta1.HostedCluster {
	return hypershiftv1beta1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns", Name: name,
			Labels: map[string]string{hypershiftv1beta1.HostedClusterSizeLabel: "large"},
		},
		Status: hypershiftv1beta1.HostedClusterStatus{Conditions: []metav1.Condition{{
			Type:               hypershiftv1beta1.ClusterSizeComputed,
			Status:             metav1.ConditionTrue,
			Reason:             "large",
			Message:            "The HostedCluster has transitioned to a new t-shirt size.",
			LastTransitionTime: metav1.NewTime(transition),
		}}},
	}
}

func compareActions() []cmp.Option {
	return []cmp.Option{
		cmp.AllowUnexported(action{}),
		cmpopts.IgnoreTypes(
			metav1applyconfigurations.TypeMetaApplyConfiguration{}, // these are entirely set by generated code
		),
		cmpopts.IgnoreFields(metav1applyconfigurations.ConditionApplyConfiguration{}, "ObservedGeneration"),
	}
}
