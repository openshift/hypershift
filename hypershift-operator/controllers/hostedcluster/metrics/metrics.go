package metrics

import (
	"context"
	"crypto/x509"
	"fmt"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	platformaws "github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/internal/platform/aws"
	"github.com/openshift/hypershift/support/conditions"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/library-go/pkg/crypto"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/clock"

	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	ProxyCAConfigMapKey = "ca-bundle.crt"

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

	ProxyCAValidMetricName = "hypershift_cluster_proxy_ca_valid"
	proxyCAValidMetricHelp = "Indicates if the given HostedCluster's proxy has a valid CA bundle configured"

	ProxyCAExpiryTimestampName       = "hypershift_cluster_proxy_ca_expiry_timestamp"
	proxyCAExpiryTimestampMetricHelp = "Shows the earliest timestamp when a certificate in the configured CA will expire."

	InvalidAwsCredsMetricName = "hypershift_cluster_invalid_aws_creds"
	invalidAwsCredsMetricHelp = "Indicates if the given HostedCluster has valid AWS credentials or not"

	DeletingDurationMetricName = "hypershift_cluster_deleting_duration_seconds"
	deletingDurationMetricHelp = "Time in seconds it is taking to delete the HostedCluster since the beginning of the delete. " +
		"Undefined if the cluster is not deleting or no longer exists."

	GuestCloudResourcesDeletingDurationMetricName = "hypershift_cluster_guest_cloud_resources_deleting_duration_seconds"
	guestCloudResourcesDeletingDurationMetricHelp = "Time in seconds it is taking to get the CloudResourcesDestroyed condition become true since the beginning of the delete of the HostedCluster. " +
		"Undefined if the cluster is not deleting/no longer exists or if the condition has already become true."

	EtcdManualInterventionRequiredMetricName = "hypershift_etcd_manual_intervention_required"
	etcdManualInterventionRequiredMetricHelp = "Indicates that manual intervention is required to recover the ETCD cluster"

	ClusterSizeOverrideMetricName = "hypershift_cluster_size_override_instances"
	clusterSizeOverrideMetricHelp = "Number of HostedClusters with a cluster size override annotation"
)

// semantically constant - not supposed to be changed at runtime
var (
	// List of known identity providers
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

	proxyCAMetricDesc = prometheus.NewDesc(
		ProxyCAValidMetricName, proxyCAValidMetricHelp,
		hclusterLabels, nil)

	proxyCAExpiryMetricDesc = prometheus.NewDesc(
		ProxyCAExpiryTimestampName, proxyCAExpiryTimestampMetricHelp,
		hclusterLabels, nil)

	invalidAwsCredsMetricDesc = prometheus.NewDesc(
		InvalidAwsCredsMetricName, invalidAwsCredsMetricHelp,
		hclusterLabels, nil)

	deletingDurationMetricDesc = prometheus.NewDesc(
		DeletingDurationMetricName, deletingDurationMetricHelp,
		hclusterLabels, nil)

	guestCloudResourcesDeletingDurationMetricDesc = prometheus.NewDesc(
		GuestCloudResourcesDeletingDurationMetricName, guestCloudResourcesDeletingDurationMetricHelp,
		hclusterLabels, nil)

	etcdManualInterventionRequiredMetricDesc = prometheus.NewDesc(
		EtcdManualInterventionRequiredMetricName, etcdManualInterventionRequiredMetricHelp,
		append(hclusterLabels, "environment", "internal_id"), nil)

	clusterSizeOverrideMetricDesc = prometheus.NewDesc(
		ClusterSizeOverrideMetricName, clusterSizeOverrideMetricHelp,
		append(hclusterLabels, "environment", "internal_id", "size"), nil)
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

func createFailureConditionToHClustersCountMap(knownConditionToExpectedStatus map[hyperv1.ConditionType]metav1.ConditionStatus) *map[string]int {
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
		platformToFailureConditionToHClustersCount[knownPlatforms[k]] = createFailureConditionToHClustersCountMap(conditions.ExpectedHCConditions(&hyperv1.HostedCluster{
			Spec: hyperv1.HostedClusterSpec{
				Platform: hyperv1.PlatformSpec{
					Type: knownPlatforms[k],
				},
			},
		}))
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
				expectedConditions := conditions.ExpectedHCConditions(hcluster)
				_, isKnownPlatform := platformToFailureConditionToHClustersCount[platform]

				if !isKnownPlatform {
					platformToFailureConditionToHClustersCount[platform] = createFailureConditionToHClustersCountMap(expectedConditions)
				}

				failureConditionToHClustersCount := platformToFailureConditionToHClustersCount[platform]

				for _, condition := range hcluster.Status.Conditions {
					expectedStatus, isKnownCondition := expectedConditions[hyperv1.ConditionType(condition.Type)]

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
				proxyCAValid := 0.0
				proxyExpiryTime := 0.0
				if hcluster.Spec.Configuration != nil && hcluster.Spec.Configuration.Proxy != nil {
					if hcluster.Spec.Configuration.Proxy.HTTPProxy != "" {
						proxyHTTP = "1"
					}
					if hcluster.Spec.Configuration.Proxy.HTTPSProxy != "" {
						proxyHTTPS = "1"
					}
					if hcluster.Spec.Configuration.Proxy.TrustedCA.Name != "" {
						proxyTrustedCA = "1"

						err := c.validateProxyCAValidity(hcluster)
						if err != nil {
							// In case anything goes wrong (loading ConfigMap or certificates) put the CA bundle to invalid
							log.Info("proxy ca bundle is invalid, due to erroring while validating", "error", err)
							proxyCAValid = 0.0
						} else {
							proxyCAValid = 1.0
						}

						// Only report CA validity if a CA is actually configured
						ch <- prometheus.MustNewConstMetric(
							proxyCAMetricDesc,
							prometheus.GaugeValue,
							proxyCAValid,
							hclusterLabelValues...,
						)

						expiryTime, err := c.expiryTimeProxyCA(hcluster)
						if err != nil {
							log.Info("proxy ca bundle is invalid, due to erroring while fetching expiry", "error", err)
						} else {
							proxyExpiryTime = float64(expiryTime.Unix())
						}
						ch <- prometheus.MustNewConstMetric(
							proxyCAExpiryMetricDesc,
							prometheus.GaugeValue,
							proxyExpiryTime,
							hclusterLabelValues...,
						)
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

			// etcdManualInterventionRequiredMetric
			// clusterSizeOverrideMetric
			{
				metricLabels := make(map[string]string, 0)
				if hcluster.Spec.Platform.Type == hyperv1.AWSPlatform && hcluster.Spec.Platform.AWS.ResourceTags != nil {
					for _, resourceTag := range hcluster.Spec.Platform.AWS.ResourceTags {
						switch resourceTag.Key {
						case "api.openshift.com/environment":
							metricLabels["environment"] = resourceTag.Value
						case "api.openshift.com/id":
							metricLabels["internal_id"] = resourceTag.Value
						case "red-hat-clustertype":
							metricLabels["cluster_type"] = resourceTag.Value
						}
					}
				}

				if metricLabels["cluster_type"] == "rosa" {
					etcdRecoveryActiveCondition := meta.FindStatusCondition(hcluster.Status.Conditions, string(hyperv1.EtcdRecoveryActive))
					if etcdRecoveryActiveCondition != nil && etcdRecoveryActiveCondition.Status == metav1.ConditionFalse && etcdRecoveryActiveCondition.Reason == hyperv1.EtcdRecoveryJobFailedReason {
						etcdManualInterventionRequiredValue := 1.0
						ch <- prometheus.MustNewConstMetric(
							etcdManualInterventionRequiredMetricDesc,
							prometheus.GaugeValue,
							etcdManualInterventionRequiredValue,
							append(hclusterLabelValues, metricLabels["environment"], metricLabels["internal_id"])...,
						)

					}

					if sizeOverride := hcluster.Annotations[hyperv1.ClusterSizeOverrideAnnotation]; sizeOverride != "" {
						ch <- prometheus.MustNewConstMetric(
							clusterSizeOverrideMetricDesc,
							prometheus.GaugeValue,
							1.0,
							append(hclusterLabelValues, metricLabels["environment"], metricLabels["internal_id"], sizeOverride)...,
						)
					}
				}
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

// Load the CA bundle for the hosted cluster and find the earliest expiring certificate time.
//
// Returns the time.Time in UTC format.
func (c *hostedClustersMetricsCollector) expiryTimeProxyCA(hcluster *hyperv1.HostedCluster) (*time.Time, error) {
	cmName := hcluster.Spec.Configuration.Proxy.TrustedCA.Name
	caConfigMap := corev1.ConfigMap{}
	err := c.Get(context.TODO(), types.NamespacedName{
		Namespace: hcluster.Namespace,
		Name:      cmName,
	}, &caConfigMap)
	if err != nil {
		return nil, err
	}
	certBundle, err := loadCABundle(caConfigMap)
	if err != nil {
		return nil, err
	}
	var earliest time.Time
	for i, cert := range certBundle {
		// First cert to initiate our variable instead of constructing an artificially big time.Time
		if i == 0 {
			earliest = cert.NotAfter.UTC()
		}
		if cert.NotAfter.UTC().Before(earliest) {
			earliest = cert.NotAfter.UTC()
		}
	}
	return &earliest, nil
}

// Load the CA bundle for the hosted cluster and verify the contained certificates are still valid.
//
// Return value of nil is considered valid, any error is considered invalid.
func (c *hostedClustersMetricsCollector) validateProxyCAValidity(hcluster *hyperv1.HostedCluster) error {
	cmName := hcluster.Spec.Configuration.Proxy.TrustedCA.Name
	caConfigMap := corev1.ConfigMap{}
	err := c.Get(context.TODO(), types.NamespacedName{
		Namespace: hcluster.Namespace,
		Name:      cmName,
	}, &caConfigMap)
	if err != nil {
		return err
	}
	certBundle, err := loadCABundle(caConfigMap)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, cert := range certBundle {
		if cert.NotAfter.UTC().Before(now) {
			return fmt.Errorf("a configured certificate in the ca bundle was no longer valid: %s", cert.Subject.CommonName)
		}
	}
	return nil
}

func loadCABundle(configMap corev1.ConfigMap) ([]*x509.Certificate, error) {
	if _, ok := configMap.Data[ProxyCAConfigMapKey]; !ok {
		return nil, fmt.Errorf("ConfigMap %q is missing %q", configMap.Name, ProxyCAConfigMapKey)
	}
	trustBundleData := []byte(configMap.Data[ProxyCAConfigMapKey])
	if len(trustBundleData) == 0 {
		return nil, fmt.Errorf("data key %q is empty from ConfigMap %q", ProxyCAConfigMapKey, configMap.Name)
	}
	certBundle, err := crypto.CertsFromPEM(trustBundleData)
	if err != nil {
		return nil, fmt.Errorf("failed parsing certificate data from ConfigMap %q: %v", configMap.Name, err)
	}
	return certBundle, nil
}
