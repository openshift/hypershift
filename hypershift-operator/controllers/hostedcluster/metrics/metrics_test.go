package metrics

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hypershift/api"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/clock"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var (
	now = time.Now().Truncate(time.Second)

	ignoreUnexportedDto = cmpopts.IgnoreUnexported(dto.MetricFamily{}, dto.Metric{}, dto.LabelPair{}, dto.Gauge{})
)

func createMetricValue(metricName, metricDesc string, value float64) *dto.MetricFamily {
	return &dto.MetricFamily{
		Name: pointer.String(metricName),
		Help: pointer.String(metricDesc),
		Type: func() *dto.MetricType { v := dto.MetricType(1); return &v }(),
		Metric: []*dto.Metric{{
			Label: []*dto.LabelPair{
				{
					Name: pointer.String("_id"), Value: pointer.String("id"),
				},
				{
					Name: pointer.String("name"), Value: pointer.String("hc"),
				},
				{
					Name: pointer.String("namespace"), Value: pointer.String("any"),
				},
			},
			Gauge: &dto.Gauge{Value: pointer.Float64(value)},
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
			Name: pointer.String(UpgradingDurationMetricName),
			Help: pointer.String(upgradingDurationMetricHelp),
			Type: func() *dto.MetricType { v := dto.MetricType(1); return &v }(),
			Metric: []*dto.Metric{{
				Label: []*dto.LabelPair{
					{
						Name: pointer.String("_id"), Value: pointer.String("id"),
					},
					{
						Name: pointer.String("name"), Value: pointer.String("hc"),
					},
					{
						Name: pointer.String("namespace"), Value: pointer.String("any"),
					},
					{
						Name: pointer.String("new_version"), Value: pointer.String(newVersion),
					},
					{
						Name: pointer.String("previous_version"), Value: pointer.String(previousVersion),
					},
				},
				Gauge: &dto.Gauge{Value: pointer.Float64(expectedValue)},
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
			Name: pointer.String(ProxyMetricName),
			Help: pointer.String(proxyMetricHelp),
			Type: func() *dto.MetricType { v := dto.MetricType(1); return &v }(),
			Metric: []*dto.Metric{{
				Label: []*dto.LabelPair{
					{
						Name: pointer.String("_id"), Value: pointer.String("id"),
					},
					{
						Name: pointer.String("name"), Value: pointer.String("hc"),
					},
					{
						Name: pointer.String("namespace"), Value: pointer.String("any"),
					},
					{
						Name: pointer.String("proxy_http"), Value: pointer.String(labelValue),
					},
					{
						Name: pointer.String("proxy_https"), Value: pointer.String(labelValue),
					},
					{
						Name: pointer.String("proxy_trusted_ca"), Value: pointer.String(labelValue),
					},
				},
				Gauge: &dto.Gauge{Value: pointer.Float64(expectedValue)},
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
