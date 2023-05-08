package hostedcluster

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	platformaws "github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/internal/platform/aws"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
)

func TestMetrics(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name          string
		updateHistory []configv1.UpdateHistory
		conditions    []metav1.Condition
		expected      []*dto.MetricFamily
		annotations   map[string]string
	}{
		{
			name: "Cluster rollout duration is reported",
			updateHistory: []configv1.UpdateHistory{{
				CompletionTime: &metav1.Time{Time: time.Time{}.Add(2 * time.Hour)},
			}},
			expected: []*dto.MetricFamily{{
				Name: pointer.String(InitialRolloutDurationName),
				Help: pointer.String("Time in seconds it took from initial cluster creation and rollout of initial version"),
				Type: func() *dto.MetricType { v := dto.MetricType(1); return &v }(),
				Metric: []*dto.Metric{{
					Label: []*dto.LabelPair{
						{
							Name: pointer.String("name"), Value: pointer.String("hc"),
						},
						{
							Name: pointer.String("namespace"), Value: pointer.String("any"),
						},
					},
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
				Name: pointer.String(InitialRolloutDurationName),
				Help: pointer.String("Time in seconds it took from initial cluster creation and rollout of initial version"),
				Type: func() *dto.MetricType { v := dto.MetricType(1); return &v }(),
				Metric: []*dto.Metric{{
					Label: []*dto.LabelPair{
						{
							Name: pointer.String("name"), Value: pointer.String("hc"),
						},
						{
							Name: pointer.String("namespace"), Value: pointer.String("any"),
						},
					},
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
				Name: pointer.String(AvailableDurationName),
				Help: pointer.String("Time in seconds it took from initial cluster creation to HostedClusterAvailable condition becoming true"),
				Type: func() *dto.MetricType { v := dto.MetricType(1); return &v }(),
				Metric: []*dto.Metric{{
					Label: []*dto.LabelPair{
						{
							Name: pointer.String("name"), Value: pointer.String("hc"),
						},
						{
							Name: pointer.String("namespace"), Value: pointer.String("any"),
						},
					},
					Gauge: &dto.Gauge{Value: pointer.Float64(3600)},
				}},
			}},
		},
		{
			name: "Force skipping deletion aws cloud resources by broken OIDC, SkippedCloudResourcesDeletion should be reported",
			conditions: []metav1.Condition{
				{
					Type:   string(hyperv1.ValidAWSIdentityProvider),
					Status: metav1.ConditionFalse,
				},
			},
			expected: []*dto.MetricFamily{{
				Name: pointer.String(platformaws.SkippedCloudResourcesDeletionName),
				Help: pointer.String("Indicates the operator will skip the aws resources deletion"),
				Type: func() *dto.MetricType { v := dto.MetricType(1); return &v }(),
				Metric: []*dto.Metric{{
					Label: []*dto.LabelPair{
						{
							Name: pointer.String("name"), Value: pointer.String("hc"),
						},
						{
							Name: pointer.String("namespace"), Value: pointer.String("any"),
						},
					},
					Gauge: &dto.Gauge{Value: pointer.Float64(1)},
				}},
			}},
		},
		{
			name:        "Force skipping deletion aws cloud resources metric by annotation, SkippedCloudResourcesDeletion should be reported",
			annotations: map[string]string{hyperv1.CleanupCloudResourcesAnnotation: "true"},
			expected: []*dto.MetricFamily{{
				Name: pointer.String(platformaws.SkippedCloudResourcesDeletionName),
				Help: pointer.String("Indicates the operator will skip the aws resources deletion"),
				Type: func() *dto.MetricType { v := dto.MetricType(1); return &v }(),
				Metric: []*dto.Metric{{
					Label: []*dto.LabelPair{
						{
							Name: pointer.String("name"), Value: pointer.String("hc"),
						},
						{
							Name: pointer.String("namespace"), Value: pointer.String("any"),
						},
					},
					Gauge: &dto.Gauge{Value: pointer.Float64(1)},
				}},
			}},
		},
		{
			name:        "In a usual cluster teardown, SkippedCloudResourcesDeletion should not be reported",
			annotations: map[string]string{},
			conditions:  []metav1.Condition{},
			expected:    []*dto.MetricFamily{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cluster := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "hc",
					Namespace:         "any",
					Annotations:       tc.annotations,
					CreationTimestamp: metav1.Time{Time: time.Time{}.Add(time.Hour)},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						History: tc.updateHistory,
					},
					Conditions: tc.conditions,
				},
			}

			// The following somewhat mirrors github.com/prometheus/client_golang/prometheus/testutil.CollectAndCompare.
			// The testutil parses a text metric and seems to drop the HELP text, which results in the comparison always
			// failing.
			hostedClusterInitialRolloutDuration.Reset()
			hostedClusterAvailableDuration.Reset()
			platformaws.SkippedCloudResourcesDeletion.Reset()
			reg := prometheus.NewPedanticRegistry()

			// Capture metrics.
			if err := reg.Register(hostedClusterInitialRolloutDuration); err != nil {
				t.Fatalf("registering creationTime collector failed: %v", err)
			}
			if err := reg.Register(hostedClusterAvailableDuration); err != nil {
				t.Fatalf("registering availableTIme collector failed: %v", err)
			}
			if err := reg.Register(platformaws.SkippedCloudResourcesDeletion); err != nil {
				t.Fatalf("registering SkippedCloudResourcesDeletion collector failed: %v", err)
			}
			availableTime := clusterAvailableTime(cluster)
			if availableTime != nil {
				hostedClusterAvailableDuration.WithLabelValues(cluster.Namespace, cluster.Name).Set(*availableTime)
			}

			versionRolloutTime := clusterVersionRolloutTime(cluster)
			if versionRolloutTime != nil {
				hostedClusterInitialRolloutDuration.WithLabelValues(cluster.Namespace, cluster.Name).Set(*versionRolloutTime)
			}

			for _, condition := range cluster.Status.Conditions {
				if condition.Type == string(hyperv1.ValidAWSIdentityProvider) && condition.Status == metav1.ConditionFalse {
					platformaws.SkippedCloudResourcesDeletion.WithLabelValues(cluster.Namespace, cluster.Name).Set(float64(1))
				}
			}

			if cluster.Annotations[hyperv1.CleanupCloudResourcesAnnotation] == "true" {
				platformaws.SkippedCloudResourcesDeletion.WithLabelValues(cluster.Namespace, cluster.Name).Set(float64(1))
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

func TestClusterAvailableTime(t *testing.T) {
	testCases := []struct {
		name     string
		hc       *hyperv1.HostedCluster
		expected *float64
	}{
		{
			name: "When HostedCluster has been available it should return nil",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						HasBeenAvailableAnnotation: "true",
					},
				},
			},
			expected: nil,
		},
	}

	g := NewGomegaWithT(t)
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g.Expect(clusterAvailableTime(tc.hc)).To(BeEquivalentTo(tc.expected))
		})
	}
}
