package metrics

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/clock"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

var (
	now = time.Now().Truncate(time.Second)

	ignoreUnexportedDto = cmpopts.IgnoreUnexported(dto.MetricFamily{}, dto.Metric{}, dto.LabelPair{}, dto.Gauge{})
)

func createMetricValue(metricName, metricDesc string, value float64) *dto.MetricFamily {
	return &dto.MetricFamily{
		Name: ptr.To(metricName),
		Help: ptr.To(metricDesc),
		Type: func() *dto.MetricType { v := dto.MetricType(1); return &v }(),
		Metric: []*dto.Metric{{
			Label: []*dto.LabelPair{
				{
					Name: ptr.To("_id"), Value: ptr.To("id"),
				},
				{
					Name: ptr.To("name"), Value: ptr.To("hc"),
				},
				{
					Name: ptr.To("namespace"), Value: ptr.To("any"),
				},
			},
			Gauge: &dto.Gauge{Value: ptr.To[float64](value)},
		}},
	}
}

func findMetricValue(allMetricsValues *[]*dto.MetricFamily, metricName string) *dto.MetricFamily {
	if allMetricsValues != nil {
		for _, timeSeries := range *allMetricsValues {
			if timeSeries != nil && timeSeries.Name != nil && *timeSeries.Name == metricName {
				return timeSeries
			}
		}
	}

	return nil
}

func checkMetric(t *testing.T, client client.Client, clock clock.Clock, metricName string, expectedMetricValue *dto.MetricFamily) {
	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(createHostedClustersMetricsCollector(client, clock))

	result, err := reg.Gather()
	if err != nil {
		t.Fatalf("gathering metrics failed: %v", err)
	}

	if diff := cmp.Diff(findMetricValue(&result, metricName), expectedMetricValue, ignoreUnexportedDto); diff != "" {
		t.Errorf("result differs from actual: %s", diff)
	}
}

func TestReportWaitingInitialAvailabilityDuration(t *testing.T) {
	wrapExpectedValueAsMetric := func(expectedValue float64) *dto.MetricFamily {
		return createMetricValue(
			WaitingInitialAvailabilityDurationMetricName,
			waitingInitialAvailabilityDurationMetricHelp,
			expectedValue)
	}

	testCases := []struct {
		name        string
		timestamp   time.Time
		annotations map[string]string
		expected    *dto.MetricFamily
	}{
		{
			name:      "When cluster just got created, metric is reported with a value set to 0",
			timestamp: now,
			expected:  wrapExpectedValueAsMetric(0),
		},
		{
			name:      "When annotation is not set, metric reports the elapsed time since the cluster has been created",
			timestamp: now.Add(5 * time.Minute),
			expected:  wrapExpectedValueAsMetric(300),
		},
		{
			name:      "When annotation is set, metric is not reported anymore",
			timestamp: now.Add(5 * time.Minute),
			annotations: map[string]string{
				HasBeenAvailableAnnotation: "true",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			annotations := map[string]string{
				"some.key": "some.value", // We have to make sure that the map is not empty... or the map unserialized by the fake client will be the nil map which cannot be modified.
			}

			for key, value := range tc.annotations {
				annotations[key] = value
			}

			hcluster := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "hc",
					Namespace:         "any",
					CreationTimestamp: metav1.Time{Time: now},
					Annotations:       annotations,
				},
				Spec: hyperv1.HostedClusterSpec{
					ClusterID: "id",
				},
			}

			checkMetric(t,
				fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(hcluster).Build(),
				clocktesting.NewFakeClock(tc.timestamp),
				WaitingInitialAvailabilityDurationMetricName, tc.expected)
		})
	}
}

func TestReportInitialRollingOutDuration(t *testing.T) {
	wrapExpectedValueAsMetric := func(expectedValue float64) *dto.MetricFamily {
		return createMetricValue(
			InitialRollingOutDurationMetricName,
			initialRollingOutDurationMetricHelp,
			expectedValue)
	}

	testCases := []struct {
		name          string
		timestamp     time.Time
		updateHistory []configv1.UpdateHistory
		expected      *dto.MetricFamily
	}{
		{
			name:      "When cluster just got created, metric is reported with a value set to 0",
			timestamp: now,
			expected:  wrapExpectedValueAsMetric(0),
		},
		{
			name:      "When cluster is not yet provisioned, metric reports the elapsed time since the cluster has been created",
			timestamp: now.Add(30 * time.Minute),
			updateHistory: []configv1.UpdateHistory{{
				StartedTime: metav1.Time{Time: now.Add(5 * time.Minute)},
				Version:     "1.0",
			}},
			expected: wrapExpectedValueAsMetric(1800),
		},
		{
			name:      "When cluster is provisioned, metric is not reported anymore",
			timestamp: now.Add(30 * time.Minute),
			updateHistory: []configv1.UpdateHistory{{
				StartedTime:    metav1.Time{Time: now.Add(5 * time.Minute)},
				CompletionTime: &metav1.Time{Time: now.Add(30 * time.Minute)},
				Version:        "1.0",
			}},
		},
		{
			name:      "When cluster is upgrading, metric is not reported",
			timestamp: now.Add(5*time.Hour + 30*time.Minute),
			updateHistory: []configv1.UpdateHistory{
				{
					StartedTime:    metav1.Time{Time: now.Add(5 * time.Minute)},
					CompletionTime: &metav1.Time{Time: now.Add(1 * time.Hour)},
					Version:        "1.0",
				},
				{
					StartedTime: metav1.Time{Time: now.Add(5 * time.Hour)},
					Version:     "1.1",
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			hcluster := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "hc",
					Namespace:         "any",
					CreationTimestamp: metav1.Time{Time: now},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						History: tc.updateHistory,
					},
				},
				Spec: hyperv1.HostedClusterSpec{
					ClusterID: "id",
				},
			}

			checkMetric(t,
				fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(hcluster).Build(),
				clocktesting.NewFakeClock(tc.timestamp),
				InitialRollingOutDurationMetricName,
				tc.expected)
		})
	}
}

func TestReportUpgradingDuration(t *testing.T) {
	wrapExpectedValueAsMetric := func(expectedValue float64, previousVersion, newVersion string) *dto.MetricFamily {
		return &dto.MetricFamily{
			Name: ptr.To(UpgradingDurationMetricName),
			Help: ptr.To(upgradingDurationMetricHelp),
			Type: func() *dto.MetricType { v := dto.MetricType(1); return &v }(),
			Metric: []*dto.Metric{{
				Label: []*dto.LabelPair{
					{
						Name: ptr.To("_id"), Value: ptr.To("id"),
					},
					{
						Name: ptr.To("name"), Value: ptr.To("hc"),
					},
					{
						Name: ptr.To("namespace"), Value: ptr.To("any"),
					},
					{
						Name: ptr.To("new_version"), Value: ptr.To(newVersion),
					},
					{
						Name: ptr.To("previous_version"), Value: ptr.To(previousVersion),
					},
				},
				Gauge: &dto.Gauge{Value: ptr.To[float64](expectedValue)},
			}},
		}
	}

	testCases := []struct {
		name          string
		timestamp     time.Time
		updateHistory []configv1.UpdateHistory
		expected      *dto.MetricFamily
	}{
		{
			name:      "When cluster just got created, metric is not reported",
			timestamp: now,
		},
		{
			name:      "When cluster is not yet provisioned, metric is not reported",
			timestamp: now.Add(30 * time.Minute),
			updateHistory: []configv1.UpdateHistory{{
				StartedTime: metav1.Time{Time: now.Add(5 * time.Minute)},
				Version:     "1.0",
			}},
		},
		{
			name:      "When cluster is provisioned, metric is not reported",
			timestamp: now.Add(30 * time.Minute),
			updateHistory: []configv1.UpdateHistory{{
				StartedTime:    metav1.Time{Time: now.Add(5 * time.Minute)},
				CompletionTime: &metav1.Time{Time: now.Add(30 * time.Minute)},
				Version:        "1.0",
			}},
		},
		{
			name:      "When cluster is upgrading, metric reports the time since the beginning of the upgrade",
			timestamp: now.Add(5*time.Hour + 30*time.Minute),
			updateHistory: []configv1.UpdateHistory{
				{
					StartedTime:    metav1.Time{Time: now.Add(5 * time.Minute)},
					CompletionTime: &metav1.Time{Time: now.Add(1 * time.Hour)},
					Version:        "1.0",
				},
				{
					StartedTime: metav1.Time{Time: now.Add(5 * time.Hour)},
					Version:     "1.1",
				},
			},
			expected: wrapExpectedValueAsMetric(1800, "1.0", "1.1"),
		},
		{
			name:      "When cluster has upgraded, metric is not reported again",
			timestamp: now.Add(5*time.Hour + 30*time.Minute),
			updateHistory: []configv1.UpdateHistory{
				{
					StartedTime:    metav1.Time{Time: now.Add(5 * time.Minute)},
					CompletionTime: &metav1.Time{Time: now.Add(1 * time.Hour)},
					Version:        "1.0",
				},
				{
					StartedTime:    metav1.Time{Time: now.Add(5 * time.Hour)},
					CompletionTime: &metav1.Time{Time: now.Add(5*time.Hour + 30*time.Minute)},
					Version:        "1.1",
				},
			},
		},
		{
			name:      "When cluster is upgrading again, metric reports the time since the beginning of the upgrade again",
			timestamp: now.Add(12*time.Hour + 20*time.Minute),
			updateHistory: []configv1.UpdateHistory{
				{
					StartedTime:    metav1.Time{Time: now.Add(5 * time.Minute)},
					CompletionTime: &metav1.Time{Time: now.Add(1 * time.Hour)},
					Version:        "1.0",
				},
				{
					StartedTime:    metav1.Time{Time: now.Add(5 * time.Hour)},
					CompletionTime: &metav1.Time{Time: now.Add(5*time.Hour + 30*time.Minute)},
					Version:        "1.1",
				},
				{
					StartedTime: metav1.Time{Time: now.Add(12 * time.Hour)},
					Version:     "1.2",
				},
			},
			expected: wrapExpectedValueAsMetric(1200, "1.1", "1.2"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			hcluster := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "hc",
					Namespace:         "any",
					CreationTimestamp: metav1.Time{Time: now},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						History: tc.updateHistory,
					},
				},
				Spec: hyperv1.HostedClusterSpec{
					ClusterID: "id",
				},
			}

			checkMetric(t,
				fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(hcluster).Build(),
				clocktesting.NewFakeClock(tc.timestamp),
				UpgradingDurationMetricName,
				tc.expected)
		})
	}
}

func TestReportLimitedSuportEnabled(t *testing.T) {
	wrapExpectedValueAsMetric := func(expectedValue float64) *dto.MetricFamily {
		return createMetricValue(
			LimitedSupportEnabledMetricName,
			limitedSupportEnabledMetricHelp,
			expectedValue)
	}

	testCases := []struct {
		name     string
		labels   map[string]string
		expected *dto.MetricFamily
	}{
		{
			name:     "When limited support label is set, metric is reported as one",
			labels:   map[string]string{hyperv1.LimitedSupportLabel: "true"},
			expected: wrapExpectedValueAsMetric(1),
		},
		{
			name:     "When limited support label is not set, metric is reported as zero",
			labels:   map[string]string{},
			expected: wrapExpectedValueAsMetric(0),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			hcluster := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "hc",
					Namespace:         "any",
					Labels:            tc.labels,
					CreationTimestamp: metav1.Time{Time: time.Time{}.Add(time.Hour)},
				},
				Spec: hyperv1.HostedClusterSpec{
					ClusterID: "id",
				},
			}

			checkMetric(t,
				fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(hcluster).Build(),
				clock.RealClock{},
				LimitedSupportEnabledMetricName,
				tc.expected)
		})
	}
}

func TestReportSilenceAlerts(t *testing.T) {
	wrapExpectedValueAsMetric := func(expectedValue float64) *dto.MetricFamily {
		return createMetricValue(
			SilenceAlertsMetricName,
			silenceAlertsMetricHelp,
			expectedValue)
	}

	testCases := []struct {
		name     string
		labels   map[string]string
		expected *dto.MetricFamily
	}{
		{
			name:     "When silenced alerts label is set, metric is reported as one",
			labels:   map[string]string{hyperv1.SilenceClusterAlertsLabel: "true"},
			expected: wrapExpectedValueAsMetric(1),
		},
		{
			name:     "When silenced alerts label is not set, metric is reported as zero",
			labels:   map[string]string{},
			expected: wrapExpectedValueAsMetric(0),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			hcluster := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "hc",
					Namespace:         "any",
					Labels:            tc.labels,
					CreationTimestamp: metav1.Time{Time: time.Time{}.Add(time.Hour)},
				},
				Spec: hyperv1.HostedClusterSpec{
					ClusterID: "id",
				},
			}

			checkMetric(t,
				fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(hcluster).Build(),
				clock.RealClock{},
				SilenceAlertsMetricName,
				tc.expected)
		})
	}
}

func TestReportProxy(t *testing.T) {
	wrapExpectedValueAsMetric := func(expectedValue float64) *dto.MetricFamily {
		var labelValue string

		if expectedValue != 0.0 {
			labelValue = "1"
		}

		return &dto.MetricFamily{
			Name: ptr.To(ProxyMetricName),
			Help: ptr.To(proxyMetricHelp),
			Type: func() *dto.MetricType { v := dto.MetricType(1); return &v }(),
			Metric: []*dto.Metric{{
				Label: []*dto.LabelPair{
					{
						Name: ptr.To("_id"), Value: ptr.To("id"),
					},
					{
						Name: ptr.To("name"), Value: ptr.To("hc"),
					},
					{
						Name: ptr.To("namespace"), Value: ptr.To("any"),
					},
					{
						Name: ptr.To("proxy_http"), Value: ptr.To(labelValue),
					},
					{
						Name: ptr.To("proxy_https"), Value: ptr.To(labelValue),
					},
					{
						Name: ptr.To("proxy_trusted_ca"), Value: ptr.To(labelValue),
					},
				},
				Gauge: &dto.Gauge{Value: ptr.To[float64](expectedValue)},
			}},
		}
	}

	testCases := []struct {
		name        string
		clusterConf hyperv1.ClusterConfiguration
		expected    *dto.MetricFamily
	}{
		{
			name: "When proxy configuration is set, metric is reported with a value set to 1, same for the metric labels",
			clusterConf: hyperv1.ClusterConfiguration{
				Proxy: &configv1.ProxySpec{
					HTTPProxy:  "fakeProxyServer",
					HTTPSProxy: "fakeProxySecureServer",
					TrustedCA: configv1.ConfigMapNameReference{
						Name: "fakeProxyTrustedCA",
					},
				},
			},
			expected: wrapExpectedValueAsMetric(1),
		},
		{
			name:     "When Proxy configuration is not set, metric is reported with a value set to 0, metric labels are empty",
			expected: wrapExpectedValueAsMetric(0),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			hcluster := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "hc",
					Namespace:         "any",
					CreationTimestamp: metav1.Time{Time: time.Time{}.Add(time.Hour)},
				},
				Spec: hyperv1.HostedClusterSpec{
					ClusterID:     "id",
					Configuration: &tc.clusterConf,
				},
			}

			checkMetric(t,
				fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(hcluster).Build(),
				clock.RealClock{},
				ProxyMetricName,
				tc.expected)
		})
	}
}

func TestReportInvalidAwsCreds(t *testing.T) {
	wrapExpectedValueAsMetric := func(expectedValue float64) *dto.MetricFamily {
		return createMetricValue(
			InvalidAwsCredsMetricName,
			invalidAwsCredsMetricHelp,
			expectedValue)
	}

	testCases := []struct {
		name                                    string
		ValidOIDCConfigurationConditionStatus   metav1.ConditionStatus
		ValidAWSIdentityProviderConditionStatus metav1.ConditionStatus
		expected                                *dto.MetricFamily
	}{
		{
			name:                                    "When ValidOIDCConfigurationCondition status is false, metric is reported with a value set to 1",
			ValidOIDCConfigurationConditionStatus:   metav1.ConditionFalse,
			ValidAWSIdentityProviderConditionStatus: metav1.ConditionTrue,
			expected:                                wrapExpectedValueAsMetric(1),
		},
		{
			name:                                    "When ValidAWSIdentityProviderCondition status is false, metric is reported with a value set to 1",
			ValidOIDCConfigurationConditionStatus:   metav1.ConditionTrue,
			ValidAWSIdentityProviderConditionStatus: metav1.ConditionFalse,
			expected:                                wrapExpectedValueAsMetric(1),
		},
		{
			name:                                    "When both ValidAWSIdentityProviderCondition and ValidOIDCConfigurationCondition statuses is true, metric is reported with a value set to 0",
			ValidOIDCConfigurationConditionStatus:   metav1.ConditionTrue,
			ValidAWSIdentityProviderConditionStatus: metav1.ConditionTrue,
			expected:                                wrapExpectedValueAsMetric(0),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			hcluster := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hc",
					Namespace: "any",
				},
				Spec: hyperv1.HostedClusterSpec{
					ClusterID: "id",
				},
			}

			meta.SetStatusCondition(&hcluster.Status.Conditions, metav1.Condition{
				Type:   string(hyperv1.ValidOIDCConfiguration),
				Status: tc.ValidOIDCConfigurationConditionStatus,
			})
			meta.SetStatusCondition(&hcluster.Status.Conditions, metav1.Condition{
				Type:   string(hyperv1.ValidAWSIdentityProvider),
				Status: tc.ValidAWSIdentityProviderConditionStatus,
			})

			checkMetric(t,
				fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(hcluster).Build(),
				clock.RealClock{},
				InvalidAwsCredsMetricName,
				tc.expected)
		})
	}
}

func TestReportGuestCloudResourcesDeletionDuration(t *testing.T) {
	wrapExpectedValueAsMetric := func(expectedValue float64) *dto.MetricFamily {
		return createMetricValue(
			GuestCloudResourcesDeletingDurationMetricName,
			guestCloudResourcesDeletingDurationMetricHelp,
			expectedValue)
	}

	testCases := []struct {
		name       string
		timestamp  time.Time
		isDeleting bool
		conditions []metav1.Condition
		expected   *dto.MetricFamily
	}{
		{
			name:      "When cluster is not yet deleting, metric is not reported",
			timestamp: now,
		},
		{
			name:       "When cluster just started to be deleted, metric is reported with a value set to 0",
			timestamp:  now,
			isDeleting: true,
			conditions: []metav1.Condition{},
			expected:   wrapExpectedValueAsMetric(0),
		},
		{
			name:       "When destroyed condition is false, metric reports the elapsed time since the beginning of the delete",
			timestamp:  now.Add(5 * time.Minute),
			isDeleting: true,
			conditions: []metav1.Condition{
				{
					Type:               string(hyperv1.CloudResourcesDestroyed),
					Status:             metav1.ConditionFalse,
					LastTransitionTime: metav1.Time{Time: now.Add(5 * time.Minute)},
				},
			},
			expected: wrapExpectedValueAsMetric(300),
		},
		{
			name:       "When destroyed condition is true, metric is not reported anymore",
			timestamp:  now.Add(5 * time.Minute),
			isDeleting: true,
			conditions: []metav1.Condition{
				{
					Type:               string(hyperv1.CloudResourcesDestroyed),
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.Time{Time: now.Add(5 * time.Minute)},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var deletionTimestamp *metav1.Time

			if tc.isDeleting {
				deletionTimestamp = &metav1.Time{Time: now}
			}

			hcluster := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "hc",
					Namespace:         "any",
					DeletionTimestamp: deletionTimestamp,
					Finalizers:        []string{"necessary"}, // fake client needs finalizers when a deletionTimestamp is set
				},
				Status: hyperv1.HostedClusterStatus{
					Conditions: tc.conditions,
				},
				Spec: hyperv1.HostedClusterSpec{
					ClusterID: "id",
				},
			}

			checkMetric(t,
				fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(hcluster).Build(),
				clocktesting.NewFakeClock(tc.timestamp),
				GuestCloudResourcesDeletingDurationMetricName,
				tc.expected)
		})
	}
}

func TestReportDeletingDuration(t *testing.T) {
	wrapExpectedValueAsMetric := func(expectedValue float64) *dto.MetricFamily {
		return createMetricValue(
			DeletingDurationMetricName,
			deletingDurationMetricHelp,
			expectedValue)
	}

	testCases := []struct {
		name       string
		timestamp  time.Time
		isDeleting bool
		isDeleted  bool
		expected   *dto.MetricFamily
	}{
		{
			name:      "When cluster is not yet deleting, metric is not reported",
			timestamp: now,
		},
		{
			name:       "When cluster just started to be deleted, metric is reported with a value set to 0",
			timestamp:  now,
			isDeleting: true,
			expected:   wrapExpectedValueAsMetric(0),
		},
		{
			name:       "When cluster is not yet deleted, metric reports the elapsed time since the beginning of the delete",
			timestamp:  now.Add(10 * time.Minute),
			isDeleting: true,
			expected:   wrapExpectedValueAsMetric(600),
		},
		{
			name:      "When cluster is deleted, metric is not reported anymore",
			timestamp: now,
			isDeleted: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			clientBuilder := fake.NewClientBuilder().WithScheme(api.Scheme)

			if !tc.isDeleted {
				var deletionTimestamp *metav1.Time

				if tc.isDeleting {
					deletionTimestamp = &metav1.Time{Time: now}
				}

				clientBuilder = clientBuilder.WithObjects(&hyperv1.HostedCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "hc",
						Namespace:         "any",
						DeletionTimestamp: deletionTimestamp,
						Finalizers:        []string{"necessary"}, // fake client needs finalizers when a deletionTimestamp is set
					},
					Spec: hyperv1.HostedClusterSpec{
						ClusterID: "id",
					},
				})
			}

			checkMetric(t,
				clientBuilder.Build(),
				clocktesting.NewFakeClock(tc.timestamp),
				DeletingDurationMetricName,
				tc.expected)
		})
	}
}

func TestReportEtcdManualInterventionRequired(t *testing.T) {
	wrapExpectedValueAsMetric := func(expectedValue float64) *dto.MetricFamily {
		return &dto.MetricFamily{
			Name: ptr.To(EtcdManualInterventionRequiredMetricName),
			Help: ptr.To(etcdManualInterventionRequiredMetricHelp),
			Type: func() *dto.MetricType { v := dto.MetricType(1); return &v }(),
			Metric: []*dto.Metric{{
				Label: []*dto.LabelPair{
					{Name: ptr.To("_id"), Value: ptr.To("id")},
					{Name: ptr.To("environment"), Value: ptr.To("")},
					{Name: ptr.To("internal_id"), Value: ptr.To("")},
					{Name: ptr.To("name"), Value: ptr.To("hc")},
					{Name: ptr.To("namespace"), Value: ptr.To("any")},
				},
				Gauge: &dto.Gauge{Value: ptr.To(expectedValue)},
			}},
		}
	}

	testCases := []struct {
		name       string
		timestamp  time.Time
		conditions []metav1.Condition
		tags       map[string]string
		expected   *dto.MetricFamily
	}{
		{
			name:      "When cluster does not have the required tags, metric is not reported",
			timestamp: now,
		},
		{
			name:      "When cluster has the required tags but etcd recovery is not active, metric is not reported",
			timestamp: now,
			tags: map[string]string{
				"red-hat-clustertype": "rosa",
			},
			conditions: []metav1.Condition{
				{
					Type:   string(hyperv1.EtcdRecoveryActive),
					Status: metav1.ConditionTrue,
				},
			},
		},
		{
			name:      "When cluster has the required tags and etcd recovery job failed, metric is reported",
			timestamp: now,
			tags: map[string]string{
				"red-hat-clustertype": "rosa",
			},
			conditions: []metav1.Condition{
				{
					Type:   string(hyperv1.EtcdRecoveryActive),
					Status: metav1.ConditionFalse,
					Reason: hyperv1.EtcdRecoveryJobFailedReason,
				},
			},
			expected: wrapExpectedValueAsMetric(1.0),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			hcluster := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hc",
					Namespace: "any",
				},
				Spec: hyperv1.HostedClusterSpec{
					ClusterID: "id",
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							ResourceTags: func() []hyperv1.AWSResourceTag {
								var tags []hyperv1.AWSResourceTag
								for k, v := range tc.tags {
									tags = append(tags, hyperv1.AWSResourceTag{Key: k, Value: v})
								}
								return tags
							}(),
						},
					},
				},
				Status: hyperv1.HostedClusterStatus{
					Conditions: tc.conditions,
				},
			}

			checkMetric(t,
				fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(hcluster).Build(),
				clocktesting.NewFakeClock(tc.timestamp),
				EtcdManualInterventionRequiredMetricName,
				tc.expected)
		})
	}
}

func TestProxyCAValidity(t *testing.T) {
	wrapExpectedValueAsMetric := func(expectedValue float64) *dto.MetricFamily {
		return createMetricValue(
			ProxyCAValidMetricName,
			proxyCAValidMetricHelp,
			expectedValue)
	}

	now := time.Now()
	_, invalidCAPEM, err := createCa(now.Add(-time.Hour), now.Add(-time.Minute))
	if err != nil {
		t.Fail()
	}
	_, validCAPEM, err := createCa(now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fail()
	}

	testCases := []struct {
		name          string
		timestamp     time.Time
		caConfigMap   string
		caCertificate string
		expected      *dto.MetricFamily
	}{
		{
			name:          "When cluster is not setting a CA bundle, the validity it not reported",
			timestamp:     now,
			caCertificate: "",
			caConfigMap:   "",
		},
		{
			name:          "When the configured certificates are expired, the CA is invalid",
			timestamp:     now,
			caCertificate: invalidCAPEM,
			caConfigMap:   "my-config-map",
			expected:      wrapExpectedValueAsMetric(0),
		},
		{
			name:          "When the configured certificates are valid, the CA is valid",
			timestamp:     now,
			caCertificate: validCAPEM,
			caConfigMap:   "my-config-map",
			expected:      wrapExpectedValueAsMetric(1),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			clientBuilder := fake.NewClientBuilder().WithScheme(api.Scheme)
			objects := make([]client.Object, 0)
			hcBase := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "hc",
					Namespace:  "any",
					Finalizers: []string{"necessary"}, // fake client needs finalizers when a deletionTimestamp is set
				},
				Spec: hyperv1.HostedClusterSpec{
					ClusterID: "id",
				},
			}
			objects = append(objects, hcBase)
			if tc.caConfigMap != "" {
				hcBase.Spec.Configuration = &hyperv1.ClusterConfiguration{
					Proxy: &configv1.ProxySpec{
						TrustedCA: configv1.ConfigMapNameReference{
							Name: tc.caConfigMap,
						},
					},
				}
				configMap := &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      tc.caConfigMap,
						Namespace: "any",
					},
					Data: map[string]string{ProxyCAConfigMapKey: tc.caCertificate},
				}
				objects = append(objects, configMap)
			}
			clientBuilder = clientBuilder.WithObjects(objects...)
			checkMetric(t,
				clientBuilder.Build(),
				clocktesting.NewFakeClock(tc.timestamp),
				ProxyCAValidMetricName,
				tc.expected)
		})
	}
}

func TestProxyCAExpiry(t *testing.T) {

	wrapExpectedValueAsMetric := func(expectedValue float64) *dto.MetricFamily {
		return createMetricValue(
			ProxyCAExpiryTimestampName,
			proxyCAExpiryTimestampMetricHelp,
			expectedValue)
	}

	now := time.Now()
	invalidCA, invalidCAPEM, err := createCa(now.Add(-time.Hour), now.Add(-time.Minute))
	if err != nil {
		t.Fail()
	}
	validCA, validCAPEM, err := createCa(now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fail()
	}

	testCases := []struct {
		name          string
		timestamp     time.Time
		caConfigMap   string
		caCertificate string
		expected      *dto.MetricFamily
	}{
		{
			name:          "When cluster is not setting a CA bundle, the validity it not reported",
			timestamp:     now,
			caCertificate: "",
			caConfigMap:   "",
		},
		{
			name:          "When the configured certificates are expired, the CA is invalid",
			timestamp:     now,
			caCertificate: invalidCAPEM,
			caConfigMap:   "my-config-map",
			expected:      wrapExpectedValueAsMetric(float64(invalidCA.NotAfter.UTC().Unix())),
		},
		{
			name:          "When the configured certificates are valid, the CA is valid",
			timestamp:     now,
			caCertificate: validCAPEM,
			caConfigMap:   "my-config-map",
			expected:      wrapExpectedValueAsMetric(float64(validCA.NotAfter.UTC().Unix())),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			clientBuilder := fake.NewClientBuilder().WithScheme(api.Scheme)
			objects := make([]client.Object, 0)
			hcBase := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "hc",
					Namespace:  "any",
					Finalizers: []string{"necessary"}, // fake client needs finalizers when a deletionTimestamp is set
				},
				Spec: hyperv1.HostedClusterSpec{
					ClusterID: "id",
				},
			}
			objects = append(objects, hcBase)
			if tc.caConfigMap != "" {
				hcBase.Spec.Configuration = &hyperv1.ClusterConfiguration{
					Proxy: &configv1.ProxySpec{
						TrustedCA: configv1.ConfigMapNameReference{
							Name: tc.caConfigMap,
						},
					},
				}
				configMap := &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      tc.caConfigMap,
						Namespace: "any",
					},
					Data: map[string]string{ProxyCAConfigMapKey: tc.caCertificate},
				}
				objects = append(objects, configMap)
			}
			clientBuilder = clientBuilder.WithObjects(objects...)
			checkMetric(t,
				clientBuilder.Build(),
				clocktesting.NewFakeClock(tc.timestamp),
				ProxyCAExpiryTimestampName,
				tc.expected)
		})
	}
}

func TestReportClusterSizeOverride(t *testing.T) {
	wrapExpectedValueAsMetric := func(size string, expectedValue float64) *dto.MetricFamily {
		return &dto.MetricFamily{
			Name: ptr.To(ClusterSizeOverrideMetricName),
			Help: ptr.To(clusterSizeOverrideMetricHelp),
			Type: func() *dto.MetricType { v := dto.MetricType(1); return &v }(),
			Metric: []*dto.Metric{{
				Label: []*dto.LabelPair{
					{Name: ptr.To("_id"), Value: ptr.To("id")},
					{Name: ptr.To("environment"), Value: ptr.To("")},
					{Name: ptr.To("internal_id"), Value: ptr.To("")},
					{Name: ptr.To("name"), Value: ptr.To("hc")},
					{Name: ptr.To("namespace"), Value: ptr.To("any")},
					{Name: ptr.To("size"), Value: ptr.To(size)},
				},
				Gauge: &dto.Gauge{Value: ptr.To(expectedValue)},
			}},
		}
	}

	testCases := []struct {
		name        string
		timestamp   time.Time
		tags        map[string]string
		annotations map[string]string
		expected    *dto.MetricFamily
	}{
		{
			name:      "When cluster does not have the cluster override annotation, metric is not reported",
			timestamp: now,
		},
		{
			name:      "When cluster has the cluster size annotation with a large value, metric is reported",
			timestamp: now,
			tags: map[string]string{
				"red-hat-clustertype": "rosa",
			},
			annotations: map[string]string{
				hyperv1.ClusterSizeOverrideAnnotation: "large",
			},
			expected: wrapExpectedValueAsMetric("large", 1.0),
		},
		{
			name:      "When cluster has the cluster size annotation with a small value, metric is reported",
			timestamp: now,
			tags: map[string]string{
				"red-hat-clustertype": "rosa",
			},
			annotations: map[string]string{
				hyperv1.ClusterSizeOverrideAnnotation: "small",
			},
			expected: wrapExpectedValueAsMetric("small", 1.0),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			hcluster := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "hc",
					Namespace:   "any",
					Annotations: tc.annotations,
				},
				Spec: hyperv1.HostedClusterSpec{
					ClusterID: "id",
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							ResourceTags: func() []hyperv1.AWSResourceTag {
								var tags []hyperv1.AWSResourceTag
								for k, v := range tc.tags {
									tags = append(tags, hyperv1.AWSResourceTag{Key: k, Value: v})
								}
								return tags
							}(),
						},
					},
				},
			}

			checkMetric(t,
				fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(hcluster).Build(),
				clocktesting.NewFakeClock(tc.timestamp),
				ClusterSizeOverrideMetricName,
				tc.expected)
		})
	}
}

func TestHostedClusterAzureInfo(t *testing.T) {
	const (
		one float64 = 1
	)
	testCases := []struct {
		name               string
		timestamp          time.Time
		platformType       hyperv1.PlatformType
		azureSpec          *hyperv1.AzurePlatformSpec
		managed            bool
		expectedMetricName string
		expected           *dto.MetricFamily
	}{
		{
			name:               "no Azure Platform, no metric",
			timestamp:          now,
			platformType:       hyperv1.IBMCloudPlatform,
			expectedMetricName: "no metric expected",
		},
		{
			name:               "Azure platform but no data, no metric",
			timestamp:          now,
			platformType:       hyperv1.AzurePlatform,
			azureSpec:          nil,
			expectedMetricName: "no metric expected",
		},
		{
			name:         "Azure, simple unmanaged",
			timestamp:    now,
			platformType: hyperv1.AzurePlatform,
			azureSpec: &hyperv1.AzurePlatformSpec{
				Cloud:             "AzureCloud",
				Location:          "eastus",
				ResourceGroupName: "myResourceGroup777",
				SubscriptionID:    "mySubscription888",
			},
			managed:            false,
			expectedMetricName: HostedClusterAzureInfoMetricName,
			expected: &dto.MetricFamily{
				Name: ptr.To(string(HostedClusterAzureInfoMetricName)),
				Help: ptr.To(string(HostedClusterAzureInfoMetricHelp)),
				Type: func() *dto.MetricType { v := dto.MetricType(1); return &v }(),
				Metric: []*dto.Metric{{
					Label: []*dto.LabelPair{
						{Name: ptr.To("_id"), Value: ptr.To("this-is-the-clusterID")},
						{Name: ptr.To("location"), Value: ptr.To("eastus")},
						{Name: ptr.To("microsoft_resource_group_name"), Value: ptr.To("myResourceGroup777")},
						{Name: ptr.To("microsoft_subscription_id"), Value: ptr.To("mySubscription888")},
						{Name: ptr.To("name"), Value: ptr.To("hc-name")},
						{Name: ptr.To("namespace"), Value: ptr.To("hc-ns")},
					},
					Gauge: &dto.Gauge{Value: ptr.To(one)},
				}},
			},
		},
		{
			name:         "Azure, simple managed",
			timestamp:    now,
			platformType: hyperv1.AzurePlatform,
			azureSpec: &hyperv1.AzurePlatformSpec{
				Cloud:             "AzureCloud",
				Location:          "eastus",
				ResourceGroupName: "myResourceGroup777",
				SubscriptionID:    "mySubscription888",
			},
			managed:            true,
			expectedMetricName: HostedClusterManagedAzureInfoMetricName,
			expected: &dto.MetricFamily{
				Name: ptr.To(string(HostedClusterManagedAzureInfoMetricName)),
				Help: ptr.To(string(HostedClusterManagedAzureInfoMetricHelp)),
				Type: func() *dto.MetricType { v := dto.MetricType(1); return &v }(),
				Metric: []*dto.Metric{{
					Label: []*dto.LabelPair{
						{Name: ptr.To("_id"), Value: ptr.To("this-is-the-clusterID")},
						{Name: ptr.To("location"), Value: ptr.To("eastus")},
						{Name: ptr.To("microsoft_resource_group_name"), Value: ptr.To("myResourceGroup777")},
						{Name: ptr.To("microsoft_resource_id"), Value: ptr.To("/subscriptions/mySubscription888/resourceGroups/myResourceGroup777/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/hc-name")},
						{Name: ptr.To("microsoft_resource_type"), Value: ptr.To("hcpOpenShiftClusters")},
						{Name: ptr.To("microsoft_subscription_id"), Value: ptr.To("mySubscription888")},
						{Name: ptr.To("name"), Value: ptr.To("hc-name")},
						{Name: ptr.To("namespace"), Value: ptr.To("hc-ns")},
					},
					Gauge: &dto.Gauge{Value: ptr.To(one)},
				}},
			},
		},
	}
	for _, tc := range testCases {
		hostedCluster := &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "hc-name",
				Namespace: "hc-ns",
			},
			Spec: hyperv1.HostedClusterSpec{
				ClusterID: "this-is-the-clusterID",
				Platform: hyperv1.PlatformSpec{
					Type:  tc.platformType,
					Azure: tc.azureSpec,
				},
			},
		}
		if tc.managed {
			t.Setenv("MANAGED_SERVICE", hyperv1.AroHCP)
		}
		checkMetric(t,
			fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(hostedCluster).Build(),
			clocktesting.NewFakeClock(tc.timestamp),
			tc.expectedMetricName,
			tc.expected)
	}
}

func createCa(notBefore, notAfter time.Time) (*x509.Certificate, string, error) {
	ca := &x509.Certificate{
		SerialNumber: big.NewInt(2019),
		Subject: pkix.Name{
			Organization:  []string{"Company, INC."},
			Country:       []string{"US"},
			Province:      []string{""},
			Locality:      []string{"City"},
			StreetAddress: []string{"Street"},
			PostalCode:    []string{"00000"},
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		IsCA:                  true,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	caPrivKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, "", err
	}

	// create the CA
	caBytes, err := x509.CreateCertificate(rand.Reader, ca, ca, &caPrivKey.PublicKey, caPrivKey)
	if err != nil {
		return nil, "", err
	}

	// pem encode
	caPEM := new(bytes.Buffer)
	_ = pem.Encode(caPEM, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: caBytes,
	})
	return ca, caPEM.String(), nil
}
