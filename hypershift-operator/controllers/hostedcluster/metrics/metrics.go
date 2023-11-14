package metrics

import (
	"context"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	platformaws "github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/internal/platform/aws"
	"github.com/openshift/hypershift/support/conditions"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/clock"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	HasBeenAvailableAnnotation = "hypershift.openshift.io/HasBeenAvailable"

	// Aggregating metrics - name & help

	CountByIdentityProviderMetricName = "hypershift_cluster_identity_providers" // What about renaming it to hypershift_clusters_by_identity_provider_type ?
	countByIdentityProviderMetricHelp = "Number of HostedClusters for a given identity provider."

	CountByPlatformMetricName = "hypershift_hostedclusters" // What about renaming it to hypershift_clusters_by_platform ?
	countByPlatformMetricHelp = "Number of HostedClusters for a given platform."

	CountByPlatformAndFailureConditionMetricName = "hypershift_hostedclusters_failure_conditions" // What about renaming it to hypershift_clusters_by_platform_and_failure_condition ?
	countByPlatformAndFailureConditionMetricHelp = "Number of HostedClusters for a given platform and failure condition."

	TransitionDurationMetricName = "hypershift_hosted_cluster_transition_seconds" // What about renaming it to hypershift_hosted_clusters_transition_duration_seconds ?
	transitionDurationMetricHelp = "Time in seconds it took for conditions to become true since the creation of the HostedCluster."

	// Per hosted cluster metrics - name & help

	WaitingInitialAvailabilityDurationMetricName = "hypershift_cluster_waiting_initial_avaibility_duration_seconds"
	waitingInitialAvailabilityDurationMetricHelp = "Time in seconds it is taking to get the HostedClusterAvailable condition becoming true since the creation of the HostedCluster. " +
		"Undefined if the condition has already become true once or if the cluster no longer exists."

	InitialRollingOutDurationMetricName = "hypershift_cluster_initial_rolling_out_duration_seconds"
	initialRollingOutDurationMetricHelp = "Time in seconds it is taking to roll out the initial version since the creation of the HostedCluster. " +
		"Version is rolled out when its state is set to 'Completed' in the history. " +
		"Undefined if this state has already been reached in the past or if the cluster no longer exists."

	UpgradingDurationMetricName = "hypershift_cluster_upgrading_duration_seconds"
	upgradingDurationMetricHelp = "Time in seconds it is taking to upgrade the HostedCluster / to roll out subsequent versions since the beginning of the update. " +
		"Version is rolled out when its state is set to 'Completed' in the history. " +
		"Undefined if the cluster is not upgrading or if the upgrade is finished or if the cluster no longer exists."

	LimitedSupportEnabledMetricName = "hypershift_cluster_limited_support_enabled"
	limitedSupportEnabledMetricHelp = "Indicates if the given HostedCluster is in limited support or not"

	SilenceAlertsMetricName = "hypershift_cluster_silence_alerts"
	silenceAlertsMetricHelp = "Indicates if the given HostedCluster is silenced or not"

	ProxyMetricName = "hypershift_cluster_proxy"
	proxyMetricHelp = "Indicates if the given HostedCluster is available through a proxy or not"

	InvalidAwsCredsMetricName = "hypershift_cluster_invalid_aws_creds"
	invalidAwsCredsMetricHelp = "Indicates if the given HostedCluster has valid AWS credentials or not"

	DeletingDurationMetricName = "hypershift_cluster_deleting_duration_seconds"
	deletingDurationMetricHelp = "Time in seconds it is taking to delete the HostedCluster since the beginning of the delete. " +
		"Undefined if the cluster is not deleting or no longer exists."

	GuestCloudResourcesDeletingDurationMetricName = "hypershift_cluster_guest_cloud_resources_deleting_duration_seconds"
	guestCloudResourcesDeletingDurationMetricHelp = "Time in seconds it is taking to get the CloudResourcesDestroyed condition become true since the beginning of the delete of the HostedCluster. " +
		"Undefined if the cluster is not deleting/no longer exists or if the condition has already become true."
)

// semantically constant - not suposed to be changed at runtime
var (
	// List of known identidy providers
	// To be updated when a new identity provider is added; failure to do so is not a big deal it is just that
	// countByIdentityProviderMetric metric will be undefined rather than initialized to 0 for the new identity provider
	knownIdentityProviders = []configv1.IdentityProviderType{
		configv1.IdentityProviderTypeBasicAuth,
		configv1.IdentityProviderTypeGitHub,
		configv1.IdentityProviderTypeGitLab,
		configv1.IdentityProviderTypeGoogle,
		configv1.IdentityProviderTypeHTPasswd,
		configv1.IdentityProviderTypeKeystone,
		configv1.IdentityProviderTypeLDAP,
		configv1.IdentityProviderTypeOpenID,
		configv1.IdentityProviderTypeRequestHeader,
	}

	knownPlatforms = hyperv1.PlatformTypes()

	knownConditionToExpectedStatus = conditions.ExpectedHCConditions()

	// Metrics descriptions
	countByIdentityProviderMetricDesc = prometheus.NewDesc(
		CountByIdentityProviderMetricName,
		countByIdentityProviderMetricHelp,
		[]string{"identity_provider"}, nil)

	countByPlatformMetricDesc = prometheus.NewDesc(
		CountByPlatformMetricName,
		countByPlatformMetricHelp,
		[]string{"platform"}, nil)

	countByPlatformAndFailureConditionMetricDesc = prometheus.NewDesc(
		CountByPlatformAndFailureConditionMetricName,
		countByPlatformAndFailureConditionMetricHelp,
		[]string{"platform", "condition"}, nil)

	// One time series per hosted cluster for below metrics
	hclusterLabels = []string{"namespace", "name", "_id"}

	waitingInitialAvailabilityDurationMetricDesc = prometheus.NewDesc(
		WaitingInitialAvailabilityDurationMetricName,
		waitingInitialAvailabilityDurationMetricHelp,
		hclusterLabels, nil)

	initialRollingOutDurationMetricDesc = prometheus.NewDesc(
		InitialRollingOutDurationMetricName,
		initialRollingOutDurationMetricHelp,
		hclusterLabels, nil)

	upgradingDurationMetricDesc = prometheus.NewDesc(
		UpgradingDurationMetricName, upgradingDurationMetricHelp,
		append(hclusterLabels, "previous_version", "new_version"), nil)

	limitedSupportEnabledMetricDesc = prometheus.NewDesc(
		LimitedSupportEnabledMetricName, limitedSupportEnabledMetricHelp,
		hclusterLabels, nil)

	silenceAlertsMetricDesc = prometheus.NewDesc(
		SilenceAlertsMetricName, silenceAlertsMetricHelp,
		hclusterLabels, nil)

	proxyMetricDesc = prometheus.NewDesc(
		ProxyMetricName, proxyMetricHelp,
		append(hclusterLabels, "proxy_http", "proxy_https", "proxy_trusted_ca"), nil)

	invalidAwsCredsMetricDesc = prometheus.NewDesc(
		InvalidAwsCredsMetricName, invalidAwsCredsMetricHelp,
		hclusterLabels, nil)

	deletingDurationMetricDesc = prometheus.NewDesc(
		DeletingDurationMetricName, deletingDurationMetricHelp,
		hclusterLabels, nil)

	guestCloudResourcesDeletingDurationMetricDesc = prometheus.NewDesc(
		GuestCloudResourcesDeletingDurationMetricName, guestCloudResourcesDeletingDurationMetricHelp,
		hclusterLabels, nil)
)

type hostedClustersMetricsCollector struct {
	client.Client
	clock clock.Clock

	transitionDurationMetric *prometheus.HistogramVec

	lastCollectTime time.Time
}

func createHostedClustersMetricsCollector(client client.Client, clock clock.Clock) prometheus.Collector {
	return &hostedClustersMetricsCollector{
		Client: client,
		clock:  clock,
		transitionDurationMetric: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    TransitionDurationMetricName,
			Help:    transitionDurationMetricHelp,
			Buckets: []float64{5, 10, 20, 30, 60, 90, 120, 180, 240, 300, 360, 480, 600},
		}, []string{"condition"}),
		lastCollectTime: time.UnixMilli(0),
	}
}

func CreateAndRegisterHostedClustersMetricsCollector(client client.Client) prometheus.Collector {
	collector := createHostedClustersMetricsCollector(client, clock.RealClock{})

	metrics.Registry.MustRegister(collector)

	return collector
}

func (c *hostedClustersMetricsCollector) Describe(ch chan<- *prometheus.Desc) {
	prometheus.DescribeByCollect(c, ch)
}

func createFailureConditionToHClustersCountMap() *map[string]int {
	res := make(map[string]int)

	for conditionType, expectedStatus := range knownConditionToExpectedStatus {
		failureCondPrefix := ""

		if expectedStatus == metav1.ConditionTrue {
			failureCondPrefix = "not_"
		}

		res[failureCondPrefix+string(conditionType)] = 0
	}

	return &res
}

func (c *hostedClustersMetricsCollector) Collect(ch chan<- prometheus.Metric) {
	currentCollectTime := c.clock.Now()
	log := ctrllog.Log

	// countByIdentityProviderMetric - init
	identityProviderToHClustersCount := make(map[configv1.IdentityProviderType]int)

	for k := range knownIdentityProviders {
		identityProviderToHClustersCount[knownIdentityProviders[k]] = 0
	}

	// countByPlatformMetric - init
	platformToHClustersCount := make(map[hyperv1.PlatformType]int)

	for k := range knownPlatforms {
		platformToHClustersCount[knownPlatforms[k]] = 0
	}

	// countByPlatformAndFailureConditionMetric - init
	platformToFailureConditionToHClustersCount := make(map[hyperv1.PlatformType]*map[string]int)

	for k := range knownPlatforms {
		platformToFailureConditionToHClustersCount[knownPlatforms[k]] = createFailureConditionToHClustersCountMap()
	}

	// MAIN LOOP - Hosted clusters loop
	{
		hclusters := &hyperv1.HostedClusterList{}

		if err := c.List(context.Background(), hclusters); err != nil {
			log.Error(err, "failed to list hosted clusters while collecting metrics")
		}

		for k := range hclusters.Items {
			hcluster := &hclusters.Items[k]

			// countByIdentityProviderMetric - aggregation
			if hcluster.Spec.Configuration != nil && hcluster.Spec.Configuration.OAuth != nil {
				for _, identityProvider := range hcluster.Spec.Configuration.OAuth.IdentityProviders {
					identityProviderToHClustersCount[identityProvider.Type] = identityProviderToHClustersCount[identityProvider.Type] + 1
				}
			}

			// countByPlatformMetric - aggregation
			platform := hcluster.Spec.Platform.Type
			platformToHClustersCount[platform] = platformToHClustersCount[platform] + 1

			// countByPlatformAndFailureConditionMetric - aggregation
			{
				_, isKnownPlatform := platformToFailureConditionToHClustersCount[platform]

				if !isKnownPlatform {
					platformToFailureConditionToHClustersCount[platform] = createFailureConditionToHClustersCountMap()
				}

				failureConditionToHClustersCount := platformToFailureConditionToHClustersCount[platform]

				for _, condition := range hcluster.Status.Conditions {
					expectedStatus, isKnownCondition := knownConditionToExpectedStatus[hyperv1.ConditionType(condition.Type)]

					if isKnownCondition && condition.Status != expectedStatus {
						failureCondPrefix := ""

						if expectedStatus == metav1.ConditionTrue {
							failureCondPrefix = "not_"
						}

						failureCondition := failureCondPrefix + condition.Type

						(*failureConditionToHClustersCount)[failureCondition] = (*failureConditionToHClustersCount)[failureCondition] + 1
					}
				}
			}

			// transitionDurationMetric - aggregation
			for _, conditionType := range []hyperv1.ConditionType{hyperv1.EtcdAvailable, hyperv1.InfrastructureReady, hyperv1.ExternalDNSReachable} {
				condition := meta.FindStatusCondition(hcluster.Status.Conditions, string(conditionType))

				if condition != nil && condition.Status == metav1.ConditionTrue {
					t := condition.LastTransitionTime.Time

					if c.lastCollectTime.Before(t) && (t.Before(currentCollectTime) || t.Equal(currentCollectTime)) {
						c.transitionDurationMetric.With(map[string]string{"condition": string(conditionType)}).Observe(t.Sub(hcluster.CreationTimestamp.Time).Seconds())
					}
				}
			}

			hclusterLabelValues := []string{hcluster.Namespace, hcluster.Name, hcluster.Spec.ClusterID}

			// waitingInitialAvailabilityDurationMetric
			{
				_, hasBeenAvailable := hcluster.Annotations[HasBeenAvailableAnnotation]

				if !hasBeenAvailable {
					initializingDuration := c.clock.Since(hcluster.CreationTimestamp.Time).Seconds()

					ch <- prometheus.MustNewConstMetric(
						waitingInitialAvailabilityDurationMetricDesc,
						prometheus.GaugeValue,
						initializingDuration,
						hclusterLabelValues...,
					)
				}
			}

			// initialRollingOutDurationMetric
			if hcluster.Status.Version == nil || len(hcluster.Status.Version.History) == 0 || hcluster.Status.Version.History[0].CompletionTime == nil {
				initializingDuration := c.clock.Since(hcluster.CreationTimestamp.Time).Seconds()

				ch <- prometheus.MustNewConstMetric(
					initialRollingOutDurationMetricDesc,
					prometheus.GaugeValue,
					initializingDuration,
					hclusterLabelValues...,
				)
			}

			// upgradingDurationMetric
			// The upgrade is adding a new entry in the history on top of the initial rollout.
			if hcluster.Status.Version != nil && len(hcluster.Status.Version.History) > 1 {
				newVersionEntry := hcluster.Status.Version.History[len(hcluster.Status.Version.History)-1]

				if newVersionEntry.CompletionTime == nil {
					previousVersionEntry := hcluster.Status.Version.History[len(hcluster.Status.Version.History)-2]
					upgradingDuration := c.clock.Since(newVersionEntry.StartedTime.Time).Seconds()

					ch <- prometheus.MustNewConstMetric(
						upgradingDurationMetricDesc,
						prometheus.GaugeValue,
						upgradingDuration,
						append(hclusterLabelValues, previousVersionEntry.Version, newVersionEntry.Version)...,
					)
				}
			}

			// limitedSupportEnabledMetric
			{
				limitedSupportValue := 0.0
				if _, ok := hcluster.Labels[hyperv1.LimitedSupportLabel]; ok {
					limitedSupportValue = 1.0
				}

				ch <- prometheus.MustNewConstMetric(
					limitedSupportEnabledMetricDesc,
					prometheus.GaugeValue,
					limitedSupportValue,
					hclusterLabelValues...,
				)
			}

			// silenceAlertsMetric
			{
				silenceAlertsValue := 0.0
				if _, ok := hcluster.Labels[hyperv1.SilenceClusterAlertsLabel]; ok {
					silenceAlertsValue = 1.0
				}

				ch <- prometheus.MustNewConstMetric(
					silenceAlertsMetricDesc,
					prometheus.GaugeValue,
					silenceAlertsValue,
					hclusterLabelValues...,
				)
			}

			// proxyMetric
			{
				var proxyHTTP, proxyHTTPS, proxyTrustedCA string
				proxyValue := 0.0
				if hcluster.Spec.Configuration != nil && hcluster.Spec.Configuration.Proxy != nil {
					if hcluster.Spec.Configuration.Proxy.HTTPProxy != "" {
						proxyHTTP = "1"
					}
					if hcluster.Spec.Configuration.Proxy.HTTPSProxy != "" {
						proxyHTTPS = "1"
					}
					if hcluster.Spec.Configuration.Proxy.TrustedCA.Name != "" {
						proxyTrustedCA = "1"
					}
					proxyValue = 1.0
				}

				ch <- prometheus.MustNewConstMetric(
					proxyMetricDesc,
					prometheus.GaugeValue,
					proxyValue,
					append(hclusterLabelValues, proxyHTTP, proxyHTTPS, proxyTrustedCA)...,
				)
			}

			// invalidAwsCredsMetric
			{
				invalidAwsCredsValue := 0.0
				if !platformaws.ValidCredentials(hcluster) {
					invalidAwsCredsValue = 1.0
				}

				ch <- prometheus.MustNewConstMetric(
					invalidAwsCredsMetricDesc,
					prometheus.GaugeValue,
					invalidAwsCredsValue,
					hclusterLabelValues...,
				)
			}

			if !hcluster.DeletionTimestamp.IsZero() {
				// deletingDurationMetric
				deletingDuration := c.clock.Since(hcluster.DeletionTimestamp.Time).Seconds()

				ch <- prometheus.MustNewConstMetric(
					deletingDurationMetricDesc,
					prometheus.GaugeValue,
					deletingDuration,
					hclusterLabelValues...,
				)

				// guestCloudResourcesDeletingDurationMetric
				condition := meta.FindStatusCondition(hcluster.Status.Conditions, string(hyperv1.CloudResourcesDestroyed))

				if condition == nil || condition.Status != metav1.ConditionTrue {
					ch <- prometheus.MustNewConstMetric(
						guestCloudResourcesDeletingDurationMetricDesc,
						prometheus.GaugeValue,
						deletingDuration,
						hclusterLabelValues...,
					)
				}
			}
		}
	}

	// AGGREGATED METRICS

	// countByIdentityProviderMetric
	for identityProvider, hclustersCount := range identityProviderToHClustersCount {
		ch <- prometheus.MustNewConstMetric(
			countByIdentityProviderMetricDesc,
			prometheus.GaugeValue,
			float64(hclustersCount),
			string(identityProvider),
		)
	}

	// countByPlatformMetric
	for platform, hclustersCount := range platformToHClustersCount {
		ch <- prometheus.MustNewConstMetric(
			countByPlatformMetricDesc,
			prometheus.GaugeValue,
			float64(hclustersCount),
			string(platform),
		)
	}

	// countByPlatformAndFailureConditionMetric
	for platform, failureConditionToHClustersCount := range platformToFailureConditionToHClustersCount {
		for failureCondition, hclustersCount := range *failureConditionToHClustersCount {
			ch <- prometheus.MustNewConstMetric(
				countByPlatformAndFailureConditionMetricDesc,
				prometheus.GaugeValue,
				float64(hclustersCount),
				string(platform),
				failureCondition,
			)
		}
	}

	// transitionDurationMetric
	c.transitionDurationMetric.Collect(ch)

	c.lastCollectTime = currentCollectTime
}
