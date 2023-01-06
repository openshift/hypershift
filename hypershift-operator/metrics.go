package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

type hypershiftMetrics struct {
	// clusterCreationTime is the time it takes between cluster creation until the first
	// version got successfully rolled out. Technically this is a const, but using a gauge
	// means we do not have to track what we already reported and can just call Set
	// repeatedly with the same value.
	clusterCreationTime *prometheus.GaugeVec

	// clusterCreationTime is the time it takes between cluster creation until the
	// HostedClusterAvailable condition becomes true. Technically this is a const, but using a gauge
	// means we do not have to track what we already reported and can just call Set
	// repeatedly with the same value.
	clusterAvailableTime *prometheus.GaugeVec

	// clusterDeletionTime is the time it takes between the HostedCluster gests a deletion timestamp until
	// it performs all its deletion tasks and so the finalizer is removed and it's removed from etcd.
	clusterDeletionTime                    *prometheus.GaugeVec
	clusterGuestCloudResourcesDeletionTime *prometheus.GaugeVec

	hostedClusters                     *prometheus.GaugeVec
	hostedClustersWithFailureCondition *prometheus.GaugeVec
	hostedClustersNodePools            *prometheus.GaugeVec
	nodePools                          *prometheus.GaugeVec
	nodePoolsWithFailureCondition      *prometheus.GaugeVec
	nodePoolSize                       *prometheus.GaugeVec

	client crclient.Client

	hostedClusterDeletingCache map[string]*metav1.Time

	log logr.Logger
}

func newMetrics(client crclient.Client, log logr.Logger) *hypershiftMetrics {
	return &hypershiftMetrics{
		clusterCreationTime: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Help: "Time in seconds it took from initial cluster creation and rollout of initial version",
			Name: "hypershift_cluster_initial_rollout_duration_seconds",
		}, []string{"name"}),
		clusterDeletionTime: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Help: "Time in seconds it took from initial cluster deletion to the resource being removed from etcd",
			Name: "hypershift_cluster_deletion_duration_seconds",
		}, []string{"name"}),
		clusterGuestCloudResourcesDeletionTime: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Help: "Time in seconds it took from initial cluster deletion to the resource being removed from etcd",
			Name: "hypershift_cluster_guest_cloud_resources_deletion_duration_seconds",
		}, []string{"name"}),
		clusterAvailableTime: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Help: "Time in seconds it took from initial cluster creation to HostedClusterAvailable condition becoming true",
			Name: "hypershift_cluster_available_duration_seconds",
		}, []string{"name"}),
		hostedClusters: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "hypershift_hostedclusters",
			Help: "Number of HostedClusters by platform",
		}, []string{"platform"}),
		hostedClustersWithFailureCondition: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "hypershift_hostedclusters_failure_conditions",
			Help: "Total number of HostedClusters by platform with conditions in undesired state",
		}, []string{"platform", "condition"}),
		hostedClustersNodePools: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "hypershift_hostedcluster_nodepools",
			Help: "Number of NodePools associated with a given HostedCluster",
		}, []string{"cluster_name", "platform"}),
		nodePools: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "hypershift_nodepools",
			Help: "Number of NodePools by platform",
		}, []string{"platform"}),
		nodePoolsWithFailureCondition: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "hypershift_nodepools_failure_conditions",
			Help: "Total number of NodePools by platform with conditions in undesired state",
		}, []string{"platform", "condition"}),
		nodePoolSize: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "hypershift_nodepools_size",
			Help: "Number of replicas associated with a given NodePool",
		}, []string{"name", "platform"}),
		client: client,
		log:    log,
	}
}

func (m *hypershiftMetrics) Start(ctx context.Context) error {
	ticker := time.NewTicker(30 * time.Second)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := m.collect(ctx); err != nil {
				m.log.Error(err, "failed to collect metrics")
			}
		}
	}
}

func (m *hypershiftMetrics) collect(ctx context.Context) error {
	var clusters hyperv1.HostedClusterList
	if err := m.client.List(ctx, &clusters); err != nil {
		return fmt.Errorf("failed to list hostedclusters: %w", err)
	}
	m.observeHostedClusters(&clusters)
	var nodePools hyperv1.NodePoolList
	if err := m.client.List(ctx, &nodePools); err != nil {
		return fmt.Errorf("failed to list nodepools: %w", err)
	}
	m.observeNodePools(ctx, &nodePools)
	return nil
}

func setupMetrics(mgr manager.Manager) error {
	metrics := newMetrics(mgr.GetClient(), mgr.GetLogger().WithName("metrics"))
	if err := crmetrics.Registry.Register(metrics.clusterCreationTime); err != nil {
		return fmt.Errorf("failed to to register clusterCreationTime metric: %w", err)
	}
	if err := crmetrics.Registry.Register(metrics.clusterDeletionTime); err != nil {
		return fmt.Errorf("failed to to register clusterDeletionTime metric: %w", err)
	}
	if err := crmetrics.Registry.Register(metrics.clusterGuestCloudResourcesDeletionTime); err != nil {
		return fmt.Errorf("failed to to register clusterGuestCloudResourcesDeletionTime metric: %w", err)
	}
	if err := crmetrics.Registry.Register(metrics.clusterAvailableTime); err != nil {
		return fmt.Errorf("failed to to register clusterAvailableTime metric: %w", err)
	}
	if err := crmetrics.Registry.Register(metrics.hostedClusters); err != nil {
		return fmt.Errorf("failed to to register hostedClusters metric: %w", err)
	}
	if err := crmetrics.Registry.Register(metrics.hostedClustersWithFailureCondition); err != nil {
		return fmt.Errorf("failed to to register hostedClustersWithCondition metric: %w", err)
	}
	if err := crmetrics.Registry.Register(metrics.hostedClustersNodePools); err != nil {
		return fmt.Errorf("failed to to register hostedClustersNodePools metric: %w", err)
	}
	if err := crmetrics.Registry.Register(metrics.nodePools); err != nil {
		return fmt.Errorf("failed to to register nodePools metric: %w", err)
	}
	if err := crmetrics.Registry.Register(metrics.nodePoolSize); err != nil {
		return fmt.Errorf("failed to to register nodePoolSize metric: %w", err)
	}
	if err := mgr.Add(metrics); err != nil {
		return fmt.Errorf("failed to add metrics runnable to manager: %w", err)
	}

	return nil
}

var expectedHCConditionStates = map[hyperv1.ConditionType]bool{
	hyperv1.HostedClusterAvailable:          true,
	hyperv1.HostedClusterProgressing:        false,
	hyperv1.IgnitionEndpointAvailable:       true,
	hyperv1.UnmanagedEtcdAvailable:          true,
	hyperv1.ValidHostedClusterConfiguration: true,
	hyperv1.SupportedHostedCluster:          true,
	hyperv1.ClusterVersionSucceeding:        true,
	hyperv1.ClusterVersionUpgradeable:       true,
	hyperv1.ReconciliationActive:            true,
	hyperv1.ValidOIDCConfiguration:          true,
}

func (m *hypershiftMetrics) observeHostedClusters(hostedClusters *hyperv1.HostedClusterList) {
	hcCount := newLabelCounter()
	hcByConditions := newLabelCounter()

	for _, hc := range hostedClusters.Items {
		creationTime := clusterCreationTime(&hc)
		if creationTime != nil {
			m.clusterCreationTime.WithLabelValues(hc.Namespace + "/" + hc.Name).Set(*creationTime)
		}

		availableTime := clusterAvailableTime(&hc)
		if availableTime != nil {
			m.clusterAvailableTime.WithLabelValues(hc.Namespace + "/" + hc.Name).Set(*availableTime)
		}
		platform := string(hc.Spec.Platform.Type)
		hcCount.Add(platform)
		for _, cond := range hc.Status.Conditions {
			expectedState, known := expectedHCConditionStates[hyperv1.ConditionType(cond.Type)]
			if !known {
				continue
			}
			if expectedState {
				if cond.Status == metav1.ConditionFalse {
					hcByConditions.Add(platform, "not_"+cond.Type)
				}
			} else {
				if cond.Status == metav1.ConditionTrue {
					hcByConditions.Add(platform, cond.Type)
				}
			}
		}

		guestCloudResourcesDeletionTime := clusterGuestCloudResourcesDeletionTime(&hc)
		if guestCloudResourcesDeletionTime != nil {
			m.clusterGuestCloudResourcesDeletionTime.WithLabelValues(crclient.ObjectKeyFromObject(&hc).String()).Set(*guestCloudResourcesDeletionTime)
		}
	}

	// Capture cluster deletion time.
	existingHostedClusters := make(map[string]bool)
	if m.hostedClusterDeletingCache == nil {
		m.hostedClusterDeletingCache = make(map[string]*metav1.Time)
	}
	for _, hc := range hostedClusters.Items {
		// store hostedClusters with a deletion timestamp.
		if deletionTime := hc.DeletionTimestamp; deletionTime != nil {
			m.hostedClusterDeletingCache[crclient.ObjectKeyFromObject(&hc).String()] = deletionTime
		}

		// store all existing hostedClusters
		existingHostedClusters[crclient.ObjectKeyFromObject(&hc).String()] = true
	}
	for key, deletionTime := range m.hostedClusterDeletingCache {
		if ok := existingHostedClusters[key]; ok {
			continue
		}

		// If the hostedCluster had a deletion timestamp and does not exist anymore, capture metric.
		deletionTime := time.Now().Sub(deletionTime.Time).Seconds()
		m.clusterDeletionTime.WithLabelValues(key).Set(deletionTime)
		delete(m.hostedClusterDeletingCache, key)
	}

	for key, count := range hcCount.Counts() {
		labels := counterKeyToLabels(key)
		m.hostedClusters.WithLabelValues(labels...).Set(float64(count))
	}

	for key, count := range hcByConditions.Counts() {
		labels := counterKeyToLabels(key)
		m.hostedClustersWithFailureCondition.WithLabelValues(labels...).Set(float64(count))
	}
}

func clusterCreationTime(hc *hyperv1.HostedCluster) *float64 {
	if hc.Status.Version == nil || len(hc.Status.Version.History) == 0 {
		return nil
	}
	completionTime := hc.Status.Version.History[len(hc.Status.Version.History)-1].CompletionTime
	if completionTime == nil {
		return nil
	}
	creationTime := completionTime.Sub(hc.CreationTimestamp.Time).Seconds()
	return &creationTime
}

func clusterAvailableTime(hc *hyperv1.HostedCluster) *float64 {
	condition := meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.HostedClusterAvailable))
	if condition == nil {
		return nil
	}
	if condition.Status == metav1.ConditionFalse {
		return nil
	}
	transitionTime := condition.LastTransitionTime
	availableTime := transitionTime.Sub(hc.CreationTimestamp.Time).Seconds()
	return &availableTime
}

func clusterGuestCloudResourcesDeletionTime(hc *hyperv1.HostedCluster) *float64 {
	condition := meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.CloudResourcesDestroyed))
	if condition == nil {
		return nil
	}
	if condition.Status == metav1.ConditionFalse {
		return nil
	}
	transitionTime := condition.LastTransitionTime
	availableTime := transitionTime.Sub(hc.DeletionTimestamp.Time).Seconds()
	return &availableTime
}

var expectedNPConditionStates = map[string]bool{
	hyperv1.NodePoolValidReleaseImageConditionType:  true,
	hyperv1.NodePoolValidPlatformImageType:          true,
	hyperv1.NodePoolValidMachineConfigConditionType: true,
	hyperv1.NodePoolReadyConditionType:              true,
	hyperv1.NodePoolUpdatingVersionConditionType:    false,
	hyperv1.NodePoolUpdatingConfigConditionType:     false,
}

func (m *hypershiftMetrics) observeNodePools(ctx context.Context, nodePools *hyperv1.NodePoolList) error {
	npByCluster := newLabelCounter()
	npCount := newLabelCounter()
	npByCondition := newLabelCounter()
	for _, np := range nodePools.Items {
		hc := &hyperv1.HostedCluster{}
		hc.Namespace = np.Namespace
		hc.Name = np.Spec.ClusterName
		hcPlatform := ""
		if err := m.client.Get(ctx, crclient.ObjectKeyFromObject(hc), hc); err == nil {
			hcPlatform = string(hc.Spec.Platform.Type)
			npByCluster.Add(crclient.ObjectKeyFromObject(hc).String(), hcPlatform)
		} else {
			m.log.Error(err, "cannot get hosted cluster for nodepool", "nodepool", crclient.ObjectKeyFromObject(&np).String())
		}
		platform := string(np.Spec.Platform.Type)
		npCount.Add(platform)

		for _, cond := range np.Status.Conditions {
			expectedState, known := expectedNPConditionStates[cond.Type]
			if !known {
				continue
			}
			if expectedState {
				if cond.Status == corev1.ConditionFalse {
					npByCondition.Add(platform, "not_"+cond.Type)
				}
			} else {
				if cond.Status == corev1.ConditionTrue {
					npByCondition.Add(platform, cond.Type)
				}
			}
		}
		m.nodePoolSize.WithLabelValues(crclient.ObjectKeyFromObject(&np).String(), platform).Set(float64(np.Status.Replicas))
	}
	for key, count := range npByCluster.Counts() {
		labels := counterKeyToLabels(key)
		m.hostedClustersNodePools.WithLabelValues(labels...).Set(float64(count))
	}
	for key, count := range npCount.Counts() {
		labels := counterKeyToLabels(key)
		m.nodePools.WithLabelValues(labels...).Set(float64(count))
	}
	for key, count := range npByCondition.Counts() {
		labels := counterKeyToLabels(key)
		m.nodePoolsWithFailureCondition.WithLabelValues(labels...).Set(float64(count))
	}
	return nil
}

type labelCounter struct {
	counts map[string]int
}

func newLabelCounter() *labelCounter {
	return &labelCounter{
		counts: map[string]int{},
	}
}

func (c *labelCounter) Add(values ...string) {
	key := strings.Join(values, "|")
	c.counts[key]++
}

func (c *labelCounter) Counts() map[string]int {
	return c.counts
}

func counterKeyToLabels(key string) []string {
	return strings.Split(key, "|")
}
