package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	hyperapi "github.com/openshift/hypershift/api"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/cmd/install/assets"
	"github.com/openshift/hypershift/pkg/version"
	"github.com/openshift/hypershift/support/supportedversion"
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	tickerTime = 30
)

var (
	latestSupportedVersion = supportedversion.LatestSupportedVersion.String()
	hypershiftVersion      = version.GetRevision()
	goVersion              = runtime.Version()
	goArch                 = runtime.GOARCH

	// Metrics
	HypershiftOperatorInfoName = "hypershift_operator_info"
)

type hypershiftMetrics struct {
	clusterIdentityProviders           *prometheus.GaugeVec
	hostedClusters                     *prometheus.GaugeVec
	hostedClustersWithFailureCondition *prometheus.GaugeVec
	hostedClustersNodePools            *prometheus.GaugeVec
	nodePools                          *prometheus.GaugeVec
	nodePoolsWithFailureCondition      *prometheus.GaugeVec
	nodePoolSize                       *prometheus.GaugeVec
	hypershiftOperatorInfo             prometheus.GaugeFunc
	hostedClusterTransitionSeconds     *prometheus.HistogramVec

	client crclient.Client

	log logr.Logger
}

type ImageInfo struct {
	image   string
	imageId string
}

func newMetrics(client crclient.Client, log logr.Logger, hypershiftImage ImageInfo) *hypershiftMetrics {
	image := hypershiftImage.image
	imageId := hypershiftImage.imageId

	return &hypershiftMetrics{
		clusterIdentityProviders: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Help: "Indicates the number any identity provider in the fleet",
			Name: "hypershift_cluster_identity_providers",
		}, []string{"identity_provider"}),
		hostedClusters: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "hypershift_hostedclusters",
			Help: "Number of HostedClusters by platform",
		}, []string{"platform"}),
		hostedClustersWithFailureCondition: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "hypershift_hostedclusters_failure_conditions",
			Help: "Total number of HostedClusters by platform with conditions in undesired state",
		}, []string{"condition"}),
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
		// hypershiftOperatorInfo is a metric to capture the current operator details of the management cluster
		hypershiftOperatorInfo: prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: HypershiftOperatorInfoName,
			Help: "Metric to capture the current operator details of the management cluster",
			ConstLabels: prometheus.Labels{
				"version":                hypershiftVersion,
				"image":                  image,
				"imageId":                imageId,
				"latestSupportedVersion": latestSupportedVersion,
				"goVersion":              goVersion,
				"goArch":                 goArch,
			},
		}, func() float64 { return float64(1) }),
		// hostedClusterTransitionSeconds is a metric to capture the time between a HostedCluster being created and entering a particular condition.
		hostedClusterTransitionSeconds: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "hypershift_hosted_cluster_transition_seconds",
			Help:    "Number of seconds between HostedCluster creation and HostedCluster transition to a condition.",
			Buckets: []float64{5, 10, 20, 30, 60, 90, 120, 180, 240, 300, 360, 480, 600},
		}, []string{"condition"}),
		client: client,
		log:    log,
	}
}

func (m *hypershiftMetrics) Start(ctx context.Context) error {
	ticker := time.NewTicker(tickerTime * time.Second)

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
	if err := m.observeNodePools(ctx, &nodePools); err != nil {
		return err
	}

	return nil
}

func setupMetrics(mgr manager.Manager) error {
	var hypershiftImage ImageInfo

	// We need to create a new client because the manager one still does not have the cache started
	tmpClient, err := crclient.New(mgr.GetConfig(), crclient.Options{Scheme: hyperapi.Scheme})
	if err != nil {
		return fmt.Errorf("error creating a temporary client: %w", err)
	}
	// Grabbing the Image and ImageID from Operator
	if hypershiftImage.image, hypershiftImage.imageId, err = getOperatorImage(tmpClient); err != nil {
		if apierrors.IsNotFound(err) {
			log := mgr.GetLogger()
			log.Error(err, "pod not found, reporting empty image")
		} else {
			return err
		}
	}

	metrics := newMetrics(mgr.GetClient(), mgr.GetLogger().WithName("metrics"), hypershiftImage)
	if err := crmetrics.Registry.Register(metrics.clusterIdentityProviders); err != nil {
		return fmt.Errorf("failed to register clusterIdentityProviders metric: %w", err)
	}
	if err := crmetrics.Registry.Register(metrics.hostedClusters); err != nil {
		return fmt.Errorf("failed to register hostedClusters metric: %w", err)
	}
	if err := crmetrics.Registry.Register(metrics.hostedClustersWithFailureCondition); err != nil {
		return fmt.Errorf("failed to register hostedClustersWithCondition metric: %w", err)
	}
	if err := crmetrics.Registry.Register(metrics.hostedClustersNodePools); err != nil {
		return fmt.Errorf("failed to register hostedClustersNodePools metric: %w", err)
	}
	if err := crmetrics.Registry.Register(metrics.nodePools); err != nil {
		return fmt.Errorf("failed to register nodePools metric: %w", err)
	}
	if err := crmetrics.Registry.Register(metrics.nodePoolSize); err != nil {
		return fmt.Errorf("failed to register nodePoolSize metric: %w", err)
	}
	if err := crmetrics.Registry.Register(metrics.hostedClusterTransitionSeconds); err != nil {
		return fmt.Errorf("failed to register hostedClusterTransitionSeconds metric: %w", err)
	}
	if err := crmetrics.Registry.Register(metrics.hypershiftOperatorInfo); err != nil {
		return fmt.Errorf("failed to register hypershiftOperatorInfo metric: %w", err)
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
	hyperv1.ValidAWSIdentityProvider:        true,
	hyperv1.ValidAWSKMSConfig:               true,
	hyperv1.AWSDefaultSecurityGroupCreated:  true,
}

func (m *hypershiftMetrics) observeHostedClusters(hostedClusters *hyperv1.HostedClusterList) {
	hcCount := newLabelCounter()
	// We init to 0, this is needed so the metrics reports 0 in cases there's no HostedClusters.
	// Otherwise the time series would show the last reported value by the counter.
	hcCount.Init(string(hyperv1.AWSPlatform))
	hcCount.Init(string(hyperv1.NonePlatform))
	hcCount.Init(string(hyperv1.IBMCloudPlatform))
	hcCount.Init(string(hyperv1.AgentPlatform))
	hcCount.Init(string(hyperv1.KubevirtPlatform))
	hcCount.Init(string(hyperv1.AzurePlatform))
	hcCount.Init(string(hyperv1.PowerVSPlatform))

	// Init hcByConditions counter.
	hcByConditions := make(map[string]float64)
	for condition, expectedState := range expectedHCConditionStates {
		if expectedState == true {
			hcByConditions[string("not_"+condition)] = 0
		} else {
			hcByConditions[string(condition)] = 0
		}
	}

	// Init identityProvidersCounter counter.
	identityProvidersCounter := map[configv1.IdentityProviderType]float64{
		configv1.IdentityProviderTypeBasicAuth:     0,
		configv1.IdentityProviderTypeGitHub:        0,
		configv1.IdentityProviderTypeGitLab:        0,
		configv1.IdentityProviderTypeGoogle:        0,
		configv1.IdentityProviderTypeHTPasswd:      0,
		configv1.IdentityProviderTypeKeystone:      0,
		configv1.IdentityProviderTypeLDAP:          0,
		configv1.IdentityProviderTypeOpenID:        0,
		configv1.IdentityProviderTypeRequestHeader: 0,
	}

	now := time.Now()
	for _, hc := range hostedClusters.Items {
		// Collect transition metrics.
		// Every time a condition has a transition from false -> true, that would increase observation in the "time from creation to last transition" bucket.
		if transitionTime := transitionTime(&hc, hyperv1.EtcdAvailable); transitionTime != nil {
			if now.Sub(transitionTime.Time).Seconds() <= tickerTime {
				conditionTimeToTrueSinceCreation := pointer.Float64(transitionTime.Sub(hc.CreationTimestamp.Time).Seconds())
				m.hostedClusterTransitionSeconds.With(map[string]string{"condition": string(hyperv1.EtcdAvailable)}).Observe(*conditionTimeToTrueSinceCreation)
			}
		}
		if transitionTime := transitionTime(&hc, hyperv1.InfrastructureReady); transitionTime != nil {
			if now.Sub(transitionTime.Time).Seconds() <= tickerTime {
				conditionTimeToTrueSinceCreation := pointer.Float64(transitionTime.Sub(hc.CreationTimestamp.Time).Seconds())
				m.hostedClusterTransitionSeconds.With(map[string]string{"condition": string(hyperv1.InfrastructureReady)}).Observe(*conditionTimeToTrueSinceCreation)
			}
		}
		if transitionTime := transitionTime(&hc, hyperv1.ExternalDNSReachable); transitionTime != nil {
			if now.Sub(transitionTime.Time).Seconds() <= tickerTime {
				conditionTimeToTrueSinceCreation := pointer.Float64(transitionTime.Sub(hc.CreationTimestamp.Time).Seconds())
				m.hostedClusterTransitionSeconds.With(map[string]string{"condition": string(hyperv1.ExternalDNSReachable)}).Observe(*conditionTimeToTrueSinceCreation)
			}
		}

		// Group identityProviders by type.
		if hc.Spec.Configuration != nil && hc.Spec.Configuration.OAuth != nil {
			for _, identityProvider := range hc.Spec.Configuration.OAuth.IdentityProviders {
				identityProvidersCounter[identityProvider.Type] = identityProvidersCounter[identityProvider.Type] + 1
			}
		}

		platform := string(hc.Spec.Platform.Type)
		hcCount.Add(platform)

		for _, cond := range hc.Status.Conditions {
			expectedState, known := expectedHCConditionStates[hyperv1.ConditionType(cond.Type)]
			if !known {
				continue
			}
			if expectedState == true {
				if cond.Status == metav1.ConditionFalse {
					hcByConditions["not_"+cond.Type] = hcByConditions["not_"+cond.Type] + 1
				}
			} else {
				hcByConditions[cond.Type] = hcByConditions[cond.Type] + 1
			}
		}
	}

	// Collect identityProvider metric.
	for identityProvider, count := range identityProvidersCounter {
		m.clusterIdentityProviders.WithLabelValues(string(identityProvider)).Set(count)
	}

	for key, count := range hcCount.Counts() {
		labels := counterKeyToLabels(key)
		m.hostedClusters.WithLabelValues(labels...).Set(float64(count))
	}

	// Collect hcByConditions metric.
	for condition, count := range hcByConditions {
		m.hostedClustersWithFailureCondition.WithLabelValues(condition).Set(count)
	}
}

func transitionTime(hc *hyperv1.HostedCluster, conditionType hyperv1.ConditionType) *metav1.Time {
	condition := meta.FindStatusCondition(hc.Status.Conditions, string(conditionType))
	if condition == nil {
		return nil
	}
	if condition.Status == metav1.ConditionFalse {
		return nil
	}
	return &condition.LastTransitionTime
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

func getOperatorImage(client crclient.Client) (string, string, error) {
	ctx := context.TODO()
	var image, imageId string
	hypershiftNamespace := os.Getenv("MY_NAMESPACE")
	hypershiftPodName := os.Getenv("MY_NAME")

	hypershiftPod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: hypershiftPodName, Namespace: hypershiftNamespace}}
	if err := client.Get(ctx, crclient.ObjectKeyFromObject(hypershiftPod), hypershiftPod); err != nil {
		image = "not found"
		imageId = "not found"
		return image, imageId, err
	} else {
		for _, c := range hypershiftPod.Status.ContainerStatuses {
			if c.Name == assets.HypershiftOperatorName {
				image = c.Image
				imageId = c.ImageID
			}
		}
	}
	return image, imageId, nil
}

type labelCounter struct {
	counts map[string]int
}

func newLabelCounter() *labelCounter {
	return &labelCounter{
		counts: map[string]int{},
	}
}

func (c *labelCounter) Init(values ...string) {
	key := strings.Join(values, "|")
	c.counts[key] = 0
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
