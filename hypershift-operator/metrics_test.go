package main

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/zapr"
	"github.com/google/go-cmp/cmp"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hypershift/api"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"go.uber.org/zap/zaptest"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestMetrics(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name          string
		updateHistory []configv1.UpdateHistory
		conditions    []metav1.Condition
		expected      []*dto.MetricFamily
	}{
		{
			name: "Cluster rollout duration is reported",
			updateHistory: []configv1.UpdateHistory{{
				CompletionTime: &metav1.Time{Time: time.Time{}.Add(2 * time.Hour)},
			}},
			expected: []*dto.MetricFamily{{
				Name: pointer.String("hypershift_cluster_initial_rollout_duration_seconds"),
				Help: pointer.String("Time in seconds it took from initial cluster creation and rollout of initial version"),
				Type: func() *dto.MetricType { v := dto.MetricType(1); return &v }(),
				Metric: []*dto.Metric{{
					Label: []*dto.LabelPair{{Name: pointer.String("name"), Value: pointer.String("/hc")}},
					Gauge: &dto.Gauge{Value: pointer.Float64(3600)},
				}},
			}},
		},
		{
			name:     "Cluster didn't finish updating, no metric",
			expected: []*dto.MetricFamily{},
		},
		{
			name: "Multiple versions, the oldest one is used",
			updateHistory: []configv1.UpdateHistory{
				{
					CompletionTime: &metav1.Time{Time: time.Time{}.Add(3 * time.Hour)},
				},
				{
					CompletionTime: &metav1.Time{Time: time.Time{}.Add(2 * time.Hour)},
				},
			},
			expected: []*dto.MetricFamily{{
				Name: pointer.String("hypershift_cluster_initial_rollout_duration_seconds"),
				Help: pointer.String("Time in seconds it took from initial cluster creation and rollout of initial version"),
				Type: func() *dto.MetricType { v := dto.MetricType(1); return &v }(),
				Metric: []*dto.Metric{{
					Label: []*dto.LabelPair{{Name: pointer.String("name"), Value: pointer.String("/hc")}},
					Gauge: &dto.Gauge{Value: pointer.Float64(3600)},
				}},
			}},
		},
		{
			name: "HostedClusterAvailable is false, Cluster available duration is not reported",
			conditions: []metav1.Condition{
				{
					Type:   string(hyperv1.HostedClusterAvailable),
					Status: metav1.ConditionFalse,
				},
			},
			expected: []*dto.MetricFamily{},
		},
		{
			name: "HostedClusterAvailable is true, Cluster available duration is reported",
			conditions: []metav1.Condition{
				{
					Type:               string(hyperv1.HostedClusterAvailable),
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.Time{Time: time.Time{}.Add(2 * time.Hour)},
				},
			},
			expected: []*dto.MetricFamily{{
				Name: pointer.String("hypershift_cluster_available_duration_seconds"),
				Help: pointer.String("Time in seconds it took from initial cluster creation to HostedClusterAvailable condition becoming true"),
				Type: func() *dto.MetricType { v := dto.MetricType(1); return &v }(),
				Metric: []*dto.Metric{{
					Label: []*dto.LabelPair{{Name: pointer.String("name"), Value: pointer.String("/hc")}},
					Gauge: &dto.Gauge{Value: pointer.Float64(3600)},
				}},
			}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cluster := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "hc",
					CreationTimestamp: metav1.Time{Time: time.Time{}.Add(time.Hour)},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						History: tc.updateHistory,
					},
					Conditions: tc.conditions,
				},
			}
			client := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(cluster).Build()

			metrics := newMetrics(client, zapr.NewLogger(zaptest.NewLogger(t)))
			if err := metrics.collect(context.Background()); err != nil {
				t.Fatalf("failed to collect metrics: %v", err)
			}

			// The following somewhat mirrors github.com/prometheus/client_golang/prometheus/testutil.CollectAndCompare.
			// The testutil parses a text metric and seems to drop the HELP text, which results in the comparison always
			// failing.
			reg := prometheus.NewPedanticRegistry()
			if err := reg.Register(metrics.clusterCreationTime); err != nil {
				t.Fatalf("registering creationTime collector failed: %v", err)
			}
			if err := reg.Register(metrics.clusterAvailableTime); err != nil {
				t.Fatalf("registering availableTIme collector failed: %v", err)
			}
			result, err := reg.Gather()
			if err != nil {
				t.Fatalf("gathering metrics failed: %v", err)
			}
			if diff := cmp.Diff(result, tc.expected); diff != "" {
				t.Errorf("result differs from actual: %s", diff)
			}
		})
	}
}
