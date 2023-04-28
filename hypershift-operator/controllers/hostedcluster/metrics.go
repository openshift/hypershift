package hostedcluster

import (
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	DeletionDurationMetricName    = "hypershift_cluster_deletion_duration_seconds"
	hostedClusterDeletionDuration = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Help: "Time in seconds it took from HostedCluster having a deletion timestamp to all hypershift finalizers being removed",
		Name: DeletionDurationMetricName,
	}, []string{"namespace", "name"})

	GuestCloudResourcesDeletionDurationMetricName    = "hypershift_cluster_guest_cloud_resources_deletion_duration_seconds"
	hostedClusterGuestCloudResourcesDeletionDuration = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Help: "Time in seconds it took from HostedCluster having a deletion timestamp to the CloudResourcesDestroyed being true",
		Name: GuestCloudResourcesDeletionDurationMetricName,
	}, []string{"namespace", "name"})

	AvailableDurationName          = "hypershift_cluster_available_duration_seconds"
	hostedClusterAvailableDuration = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Help: "Time in seconds it took from initial cluster creation to HostedClusterAvailable condition becoming true",
		Name: AvailableDurationName,
	}, []string{"namespace", "name"})

	InitialRolloutDurationName          = "hypershift_cluster_initial_rollout_duration_seconds"
	hostedClusterInitialRolloutDuration = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Help: "Time in seconds it took from initial cluster creation and rollout of initial version",
		Name: InitialRolloutDurationName,
	}, []string{"namespace", "name"})

	LimitedSupportEnabledName = "hypershift_cluster_limited_support_enabled"
	limitedSupportEnabled     = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Help: "Indicates if limited support is enabled for each cluster",
		Name: LimitedSupportEnabledName,
	}, []string{"namespace", "name", "_id"})

	SilenceAlertsName = "hypershift_cluster_silence_alerts"
	silenceAlerts     = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Help: "Indicates if alerts are silenced for each cluster",
		Name: SilenceAlertsName,
	}, []string{"namespace", "name", "_id"})

	ProxyName   = "hypershift_cluster_proxy"
	proxyConfig = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Help: "Indicates cluster proxy state for each cluster",
		Name: ProxyName,
	}, []string{"namespace", "name", "proxy_http", "proxy_https", "proxy_trusted_ca"})
)

func init() {
	metrics.Registry.MustRegister(
		hostedClusterDeletionDuration,
		hostedClusterGuestCloudResourcesDeletionDuration,
		hostedClusterAvailableDuration,
		hostedClusterInitialRolloutDuration,
		limitedSupportEnabled,
		silenceAlerts,
		proxyConfig,
	)
}

// clusterAvailableTime returns the time in seconds from cluster creation to first available transition.
// If the condition is nil, false or the cluster has already been available it returns nil.
func clusterAvailableTime(hc *hyperv1.HostedCluster) *float64 {
	if HasBeenAvailable(hc) {
		return nil
	}
	condition := meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.HostedClusterAvailable))
	if condition == nil {
		return nil
	}
	if condition.Status == metav1.ConditionFalse {
		return nil
	}
	transitionTime := condition.LastTransitionTime
	return pointer.Float64(transitionTime.Sub(hc.CreationTimestamp.Time).Seconds())
}

func clusterVersionRolloutTime(hc *hyperv1.HostedCluster) *float64 {
	if hc.Status.Version == nil || len(hc.Status.Version.History) == 0 {
		return nil
	}
	completionTime := hc.Status.Version.History[len(hc.Status.Version.History)-1].CompletionTime
	if completionTime == nil {
		return nil
	}
	return pointer.Float64(completionTime.Sub(hc.CreationTimestamp.Time).Seconds())
}
