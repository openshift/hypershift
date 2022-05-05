package main

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/zapr"
	"github.com/google/go-cmp/cmp"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hypershift/api"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"go.uber.org/zap/zaptest"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilpointer "k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestMetrics(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name          string
		updateHistory []configv1.UpdateHistory
		expected      []*dto.MetricFamily
	}{
		{
			name: "Cluster is reported",
			updateHistory: []configv1.UpdateHistory{{
				CompletionTime: &metav1.Time{Time: time.Time{}.Add(time.Hour)},
			}},
			expected: []*dto.MetricFamily{{
				Name: utilpointer.StringPtr("hypershift_cluster_initial_rollout_duration_seconds"),
				Help: utilpointer.StringPtr("Time in seconds it took from initial cluster creation and rollout of initial version"),
				Type: func() *dto.MetricType { v := dto.MetricType(1); return &v }(),
				Metric: []*dto.Metric{{
					Label: []*dto.LabelPair{{Name: utilpointer.StringPtr("name"), Value: utilpointer.StringPtr("/hc")}},
					Gauge: &dto.Gauge{Value: utilpointer.Float64Ptr(3600)},
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
					CompletionTime: &metav1.Time{Time: time.Time{}.Add(2 * time.Hour)},
				},
				{
					CompletionTime: &metav1.Time{Time: time.Time{}.Add(time.Hour)},
				},
			},
			expected: []*dto.MetricFamily{{
				Name: utilpointer.StringPtr("hypershift_cluster_initial_rollout_duration_seconds"),
				Help: utilpointer.StringPtr("Time in seconds it took from initial cluster creation and rollout of initial version"),
				Type: func() *dto.MetricType { v := dto.MetricType(1); return &v }(),
				Metric: []*dto.Metric{{
					Label: []*dto.LabelPair{{Name: utilpointer.StringPtr("name"), Value: utilpointer.StringPtr("/hc")}},
					Gauge: &dto.Gauge{Value: utilpointer.Float64Ptr(3600)},
				}},
			}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cluster := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "hc",
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						History: tc.updateHistory,
					},
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
				t.Fatalf("registering collector failed: %v", err)
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
