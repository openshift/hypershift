package metrics

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/support/awsapi"
	"github.com/openshift/hypershift/support/conditions"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/clock"

	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	// Aggregating metrics - name & help

	CountByPlatformMetricName = "hypershift_nodepools" // What about renaming it to hypershift_nodepools_by_platform ?
	countByPlatformMetricHelp = "Number of NodePools for a given platform."

	CountByPlatformAndFailureConditionMetricName = "hypershift_nodepools_failure_conditions" // What about renaming it to hypershift_nodepools_by_platform_and_failure_condition ?
	countByPlatformAndFailureConditionMetricHelp = "Number of NodePools for a given platform and failure condition."

	CountByHClusterMetricName = "hypershift_hostedcluster_nodepools" // What about renaming it to hypershift_cluster_nodepools ?
	countByHClusterMetricHelp = "Number of NodePools for a given HostedCluster"

	VCpusCountByHClusterMetricName = "hypershift_cluster_vcpus"
	VCpusCountByHClusterMetricHelp = "Number of virtual CPUs as reported by the platform for a given HostedCluster. " +
		"-1 if this number cannot be computed." // Be careful when changing this metric as it is used for billing the customers

	VCpusComputationErrorByHClusterMetricName = "hypershift_cluster_vcpus_computation_error"
	VCpusComputationErrorByHClusterMetricHelp = "Defined if and only if " + VCpusCountByHClusterMetricName + " is cannot be computed and is set to -1. " +
		"Reason is given by the reason label which only takes a finite number of values."

	TransitionDurationMetricName = "hypershift_nodepools_transition_seconds" // What about renaming it to hypershift_nodepools_transition_duration_seconds ?
	transitionDurationMetricHelp = "Time in seconds it took for conditions to become true since the creation of the NodePool."

	// Per node pool metrics - name

	InitialRollingOutDurationMetricName = "hypershift_nodepools_initial_rolling_out_duration_seconds" // What about renaming it to hypershift_nodepool_initial_rolling_out_duration_seconds ?
	initialRollingOutDurationMetricHelp = "Time in seconds it is taking to roll out the initial version since the creation of the NodePool" +
		"Version is rolled out when the corresponding MachineDeployment has its number of available replicas matches the number of wished replicas. " +
		"Undefined if the number of available replicas is already reached or if the node pool no longer exists."

	SizeMetricName = "hypershift_nodepools_size" // What about renaming it to hypershift_nodepool_size ?
	sizeMetricHelp = "Number of desired replicas associated with a given NodePool"

	AvailableReplicasMetricName = "hypershift_nodepools_available_replicas" // What about renaming it to hypershift_nodepool_available_replicas ?
	availableReplicasMetricHelp = "Number of available replicas associated with a given NodePool"

	DeletingDurationMetricName = "hypershift_nodepools_deleting_duration_seconds" // What about renaming it to hypershift_nodepool_deleting_duration_seconds ?
	deletingDurationMetricHelp = "Time in seconds it is taking to delete the NodePool since the beginning of the delete. " +
		"Undefined if the node pool is not deleting or no longer exists."
)

type void struct{}

// semantically constant - not supposed to be changed at runtime
var (
	transitionDurationMetricConditions = map[string]void{
		hyperv1.NodePoolReachedIgnitionEndpoint:       void{},
		hyperv1.NodePoolAllMachinesReadyConditionType: void{},
		hyperv1.NodePoolAllNodesHealthyConditionType:  void{},
	}

	knownPlatforms = hyperv1.PlatformTypes()

	// Metrics descriptions
	countByPlatformMetricDesc = prometheus.NewDesc(
		CountByPlatformMetricName,
		countByPlatformMetricHelp,
		[]string{"platform"}, nil)

	countByPlatformAndFailureConditionMetricDesc = prometheus.NewDesc(
		CountByPlatformAndFailureConditionMetricName,
		countByPlatformAndFailureConditionMetricHelp,
		[]string{"platform", "condition"}, nil)

	hclusterLabels = []string{"namespace", "name", "_id", "platform"}

	countByHClusterMetricDesc = prometheus.NewDesc(
		CountByHClusterMetricName,
		countByHClusterMetricHelp,
		hclusterLabels, nil)

	vCpusCountByHClusterMetricDesc = prometheus.NewDesc(
		VCpusCountByHClusterMetricName,
		VCpusCountByHClusterMetricHelp,
		hclusterLabels, nil)

	vCpusComputationErrorByHClusterMetricDesc = prometheus.NewDesc(
		VCpusComputationErrorByHClusterMetricName,
		VCpusComputationErrorByHClusterMetricHelp,
		append(hclusterLabels, "reason"), nil)

	// One time series per node pool for below metrics
	nodePoolLabels = []string{"namespace", "name", "_id", "cluster_name", "platform"}

	initialRollingOutDurationMetricDesc = prometheus.NewDesc(
		InitialRollingOutDurationMetricName,
		initialRollingOutDurationMetricHelp,
		nodePoolLabels, nil)

	sizeMetricDesc = prometheus.NewDesc(
		SizeMetricName,
		sizeMetricHelp,
		nodePoolLabels, nil)

	availableReplicasMetricDesc = prometheus.NewDesc(
		AvailableReplicasMetricName,
		availableReplicasMetricHelp,
		nodePoolLabels, nil)

	deletingDurationMetricDesc = prometheus.NewDesc(
		DeletingDurationMetricName,
		deletingDurationMetricHelp,
		nodePoolLabels, nil)
)

type nodePoolsMetricsCollector struct {
	client.Client
	ec2Client ec2.DescribeInstanceTypesAPIClient
	clock     clock.Clock
	mu        sync.Mutex

	ec2InstanceTypeToVCpusCount map[string]int32
	// awsInstanceTypeUnknown caches instance types that are not recognized
	// by the EC2 API, so subsequent calls skip the API
	// and go directly to the ConfigMap fallback.
	awsInstanceTypeUnknown sets.Set[string]

	transitionDurationMetric *prometheus.HistogramVec

	lastCollectTime time.Time
}

func createNodePoolsMetricsCollector(client client.Client, ec2Client ec2.DescribeInstanceTypesAPIClient, clock clock.Clock) prometheus.Collector { //nolint:unparam // parameter kept for testability
	return &nodePoolsMetricsCollector{
		Client:                      client,
		ec2Client:                   ec2Client,
		clock:                       clock,
		ec2InstanceTypeToVCpusCount: make(map[string]int32),
		awsInstanceTypeUnknown:      sets.New[string](),
		transitionDurationMetric: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    TransitionDurationMetricName,
			Help:    transitionDurationMetricHelp,
			Buckets: []float64{5, 10, 20, 30, 60, 90, 120, 180, 240, 300, 360, 480, 600},
		}, []string{"condition"}),
		lastCollectTime: time.UnixMilli(0),
	}
}

func CreateAndRegisterNodePoolsMetricsCollector(client client.Client, ec2Client awsapi.EC2API) prometheus.Collector {
	collector := createNodePoolsMetricsCollector(client, ec2Client, clock.RealClock{})

	metrics.Registry.MustRegister(collector)

	return collector
}

func (c *nodePoolsMetricsCollector) Describe(ch chan<- *prometheus.Desc) {
	prometheus.DescribeByCollect(c, ch)
}

type hclusterData struct {
	id             string
	namespace      string
	name           string
	platform       hyperv1.PlatformType
	nodePoolsCount int
	vCpusCount     int32
	vCpusCountErr  error
}

func createFailureConditionToNodePoolsCountMap(knownConditionToExpectedStatus map[string]corev1.ConditionStatus) *map[string]int {
	res := make(map[string]int)

	for conditionType, expectedStatus := range knownConditionToExpectedStatus {
		failureCondPrefix := ""

		if expectedStatus == corev1.ConditionTrue {
			failureCondPrefix = "not_"
		}

		res[failureCondPrefix+conditionType] = 0
	}

	return &res
}

// Sentinel errors for vCPU lookup failures.
var (
	errNoAWSEC2Client                      = errors.New("no AWS EC2 client")
	errUnexpectedAWSOutput                 = errors.New("unexpected AWS output")
	errFailedToCallAWS                     = errors.New("failed to call AWS")
	errUnknownInstanceType                 = errors.New("unknown instance type")
	errUnableToParseConfigMapData          = errors.New("unable to parse ConfigMap data")
	errRosaCPUsInstanceTypesConfigNotFound = errors.New("ROSA CPUs instance types ConfigMap not found")
	errUnsupportedPlatform                 = errors.New("unsupported platform")
	errMissingAWSPlatformSpec              = errors.New("spec.platform.aws missing in node pool")
)

const (
	rosaCPUsInstanceTypeConfigMapName     = "rosa-cpus-instance-types-config"
	rosaCPUInstanceTypeConfigMapNamespace = "hypershift"
)

func extractCPUFromInstanceTypeNameViaEC2API(ctx context.Context, instanceTypeName string, ec2Client ec2.DescribeInstanceTypesAPIClient) (int32, error) {
	if ec2Client == nil {
		ctrllog.Log.Error(errNoAWSEC2Client,
			"cannot retrieve the number of vCPUs for instance type "+instanceTypeName+" as the EC2 client used to query AWS API is not properly initialized")
		return -1, errNoAWSEC2Client
	}

	ec2InstanceTypes, err := ec2Client.DescribeInstanceTypes(ctx, &ec2.DescribeInstanceTypesInput{
		InstanceTypes: []ec2types.InstanceType{ec2types.InstanceType(instanceTypeName)},
	})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == "InvalidInstanceType" {
			ctrllog.Log.Error(err, "unknown instance type "+instanceTypeName+" in EC2 API")
			return -1, errUnknownInstanceType
		}
		ctrllog.Log.Error(err, "failed to call AWS EC2 API to resolve the number of vCPUs for instance type "+instanceTypeName)
		return -1, errFailedToCallAWS
	}
	if ec2InstanceTypes == nil ||
		len(ec2InstanceTypes.InstanceTypes) == 0 ||
		ec2InstanceTypes.InstanceTypes[0].VCpuInfo == nil ||
		ec2InstanceTypes.InstanceTypes[0].VCpuInfo.DefaultVCpus == nil {
		ctrllog.Log.Error(errUnexpectedAWSOutput,
			"unexpected output for EC2 verb 'describe-instance-types' for instance type "+instanceTypeName)
		return -1, errUnexpectedAWSOutput
	}

	return *ec2InstanceTypes.InstanceTypes[0].VCpuInfo.DefaultVCpus, nil
}

// extractCPUFromInstanceTypeNameViaConfigMap extracts the vCPU count for the given instance type
// from a ConfigMap. The ConfigMap is fetched from the cluster using the provided client.Reader.
// The ConfigMap data is expected to map instance type names to vCPU counts, e.g.:
//
//	data:
//	  m5.xlarge: "4"
//	  m5.2xlarge: "8"
//	  c5.4xlarge: "16"
func extractCPUFromInstanceTypeNameViaConfigMap(ctx context.Context, instanceTypeName string, reader client.Reader) (int32, error) {
	configMap := &corev1.ConfigMap{}
	if err := reader.Get(ctx, types.NamespacedName{Name: rosaCPUsInstanceTypeConfigMapName, Namespace: rosaCPUInstanceTypeConfigMapNamespace},
		configMap); err != nil {
		ctrllog.Log.Error(err, "unable to retrieve ConfigMap "+rosaCPUsInstanceTypeConfigMapName+" in namespace "+rosaCPUInstanceTypeConfigMapNamespace)
		return -1, errRosaCPUsInstanceTypesConfigNotFound
	}
	if configMap.Data == nil {
		return -1, errRosaCPUsInstanceTypesConfigNotFound
	}
	value, ok := configMap.Data[instanceTypeName]
	if !ok {
		return -1, errUnknownInstanceType
	}
	vCpusCount, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		ctrllog.Log.Error(err, "couldn't parse VCPU data from ConfigMap for instance "+instanceTypeName)
		return -1, errUnableToParseConfigMapData
	}
	return int32(vCpusCount), nil
}

func (c *nodePoolsMetricsCollector) retrieveVCpusDetailsPerNode(ctx context.Context, nodePool *hyperv1.NodePool) (int32, error) {
	if nodePool.Spec.Platform.Type != hyperv1.AWSPlatform {
		// vCPU counting only supported for AWS platform
		return -1, errUnsupportedPlatform
	}

	awsPlatform := nodePool.Spec.Platform.AWS
	if awsPlatform == nil {
		ctrllog.Log.Error(errMissingAWSPlatformSpec, "cannot retrieve the number of vCPUs for "+nodePool.Name+" node pool as its specification is inconsistent")
		return -1, errMissingAWSPlatformSpec
	}

	ec2InstanceType := awsPlatform.InstanceType

	// Check if we have a cached vCPU count for this instance type
	if vCpusCountPerNode, isCached := c.ec2InstanceTypeToVCpusCount[ec2InstanceType]; isCached {
		return vCpusCountPerNode, nil
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// If this instance type was previously determined to be unknown by
	// the EC2 API, skip it and go directly to the ConfigMap.
	if c.awsInstanceTypeUnknown.Has(ec2InstanceType) {
		return extractCPUFromInstanceTypeNameViaConfigMap(timeoutCtx, ec2InstanceType, c.Client)
	}

	// Try EC2 API
	vCpusCount, ec2Err := extractCPUFromInstanceTypeNameViaEC2API(timeoutCtx, ec2InstanceType, c.ec2Client)
	if ec2Err == nil {
		c.ec2InstanceTypeToVCpusCount[ec2InstanceType] = vCpusCount
		return vCpusCount, nil
	}

	// Cache the fact that the EC2 API does not recognize this instance type
	// so that subsequent calls skip straight to the ConfigMap fallback.
	if errors.Is(ec2Err, errUnknownInstanceType) {
		c.awsInstanceTypeUnknown.Insert(ec2InstanceType)
	}

	// Try ConfigMap as fallback. ConfigMap values may change without
	// restarting the operator. The controller-runtime client has an
	// informer-based cache, so repeated Get calls hit the local cache,
	// not the API server. We intentionally do not cache ConfigMap results
	// so that updates to the ConfigMap are picked up on the next collection.
	return extractCPUFromInstanceTypeNameViaConfigMap(timeoutCtx, ec2InstanceType, c.Client)
}

func (c *nodePoolsMetricsCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ctx := context.Background()
	currentCollectTime := c.clock.Now()

	hclusterPathToData := c.collectHostedClusterData(ctx)
	machineSetPathToReplicasCount := c.collectMachineSetReplicas(ctx)
	machineDeploymentPathToReplicasCount := c.collectMachineDeploymentReplicas(ctx)

	platformToNodePoolsCount := make(map[hyperv1.PlatformType]int)
	for k := range knownPlatforms {
		platformToNodePoolsCount[knownPlatforms[k]] = 0
	}

	platformToFailureConditionToNodePoolsCount := make(map[hyperv1.PlatformType]*map[string]int)
	for k := range knownPlatforms {
		platformToFailureConditionToNodePoolsCount[knownPlatforms[k]] = createFailureConditionToNodePoolsCountMap(conditions.ExpectedNodePoolConditions(&hyperv1.NodePool{
			Spec: hyperv1.NodePoolSpec{
				Platform: hyperv1.NodePoolPlatform{
					Type: knownPlatforms[k],
				},
			},
		}))
	}

	npList := &hyperv1.NodePoolList{}
	if err := c.List(ctx, npList); err != nil {
		ctrllog.Log.Error(err, "failed to list node pools while collecting metrics")
	}

	for k := range npList.Items {
		nodePool := &npList.Items[k]
		hclusterId := ""

		platform := nodePool.Spec.Platform.Type
		platformToNodePoolsCount[platform] += 1

		c.aggregateFailureConditions(nodePool, platform, platformToFailureConditionToNodePoolsCount)

		if hcData := hclusterPathToData[nodePool.Namespace+"/"+nodePool.Spec.ClusterName]; hcData != nil {
			hclusterId = hcData.id
			hcData.nodePoolsCount += 1
			c.aggregateVCpus(ctx, nodePool, hcData)
		}

		c.observeTransitionDurations(nodePool, currentCollectTime)

		nodePoolLabelValues := []string{nodePool.Namespace, nodePool.Name, hclusterId, nodePool.Spec.ClusterName, string(nodePool.Spec.Platform.Type)}
		c.collectPerNodePoolMetrics(ch, nodePool, nodePoolLabelValues, machineSetPathToReplicasCount, machineDeploymentPathToReplicasCount)
	}

	c.emitAggregatedMetrics(ch, platformToNodePoolsCount, platformToFailureConditionToNodePoolsCount, hclusterPathToData)

	c.transitionDurationMetric.Collect(ch)
	c.lastCollectTime = currentCollectTime
}

// Hosted clusters loop
func (c *nodePoolsMetricsCollector) collectHostedClusterData(ctx context.Context) map[string]*hclusterData {
	hclusterPathToData := make(map[string]*hclusterData)
	hclusters := &hyperv1.HostedClusterList{}
	if err := c.List(ctx, hclusters); err != nil {
		ctrllog.Log.Error(err, "failed to list hosted clusters while collecting metrics")
	}
	for k := range hclusters.Items {
		hcluster := &hclusters.Items[k]
		data := &hclusterData{
			id:        hcluster.Spec.ClusterID,
			namespace: hcluster.Namespace,
			name:      hcluster.Name,
			platform:  hcluster.Spec.Platform.Type,
		}
		// Seed with Karpenter-managed vCPUs from AutoNode status.
		// Native NodePool vCPUs accumulate on top in the NodePool loop below.
		if hcluster.Status.AutoNode.VCPUs != nil {
			data.vCpusCount = *hcluster.Status.AutoNode.VCPUs
		}
		hclusterPathToData[hcluster.Namespace+"/"+hcluster.Name] = data
	}
	return hclusterPathToData
}

// Machine sets loop
func (c *nodePoolsMetricsCollector) collectMachineSetReplicas(ctx context.Context) map[string]int32 {
	result := make(map[string]int32)
	machineSets := &capiv1.MachineSetList{}
	if err := c.List(ctx, machineSets); err != nil {
		ctrllog.Log.Error(err, "failed to list machine sets while collecting metrics")
	}
	for k := range machineSets.Items {
		machineSet := &machineSets.Items[k]
		// we use machineSet.Spec.Replicas because nodePool.Spec.Replicas will not be set if autoscaling is enabled
		result[machineSet.Namespace+"/"+machineSet.Name] = *machineSet.Spec.Replicas
	}
	return result
}

// Machine deployments loop
func (c *nodePoolsMetricsCollector) collectMachineDeploymentReplicas(ctx context.Context) map[string]int32 {
	result := make(map[string]int32)
	machineDeployments := &capiv1.MachineDeploymentList{}
	if err := c.List(ctx, machineDeployments); err != nil {
		ctrllog.Log.Error(err, "failed to list machine deployments while collecting metrics")
	}
	for k := range machineDeployments.Items {
		md := &machineDeployments.Items[k]
		// we use machineDeployment.Spec.Replicas because nodePool.Spec.Replicas will not be set if autoscaling is enabled
		result[md.Namespace+"/"+md.Name] = *md.Spec.Replicas
	}
	return result
}

func (c *nodePoolsMetricsCollector) aggregateFailureConditions(nodePool *hyperv1.NodePool, platform hyperv1.PlatformType, platformMap map[hyperv1.PlatformType]*map[string]int) {
	knownConditionToExpectedStatus := conditions.ExpectedNodePoolConditions(nodePool)
	if _, isKnownPlatform := platformMap[platform]; !isKnownPlatform {
		platformMap[platform] = createFailureConditionToNodePoolsCountMap(knownConditionToExpectedStatus)
	}
	failureConditionToNodePoolsCount := platformMap[platform]
	for _, condition := range nodePool.Status.Conditions {
		expectedStatus, isKnownCondition := knownConditionToExpectedStatus[condition.Type]
		if isKnownCondition && condition.Status != expectedStatus {
			failureCondPrefix := ""
			if expectedStatus == corev1.ConditionTrue {
				failureCondPrefix = "not_"
			}
			(*failureConditionToNodePoolsCount)[failureCondPrefix+condition.Type] += 1
		}
	}
}

func (c *nodePoolsMetricsCollector) aggregateVCpus(ctx context.Context, nodePool *hyperv1.NodePool, hcData *hclusterData) {
	if hcData.vCpusCount >= 0 && nodePool.Status.Replicas > 0 {
		nodeVCpus, err := c.retrieveVCpusDetailsPerNode(ctx, nodePool)
		if err != nil {
			hcData.vCpusCount = -1
			hcData.vCpusCountErr = err
		} else {
			hcData.vCpusCount += nodeVCpus * nodePool.Status.Replicas
		}
	}
}

func (c *nodePoolsMetricsCollector) observeTransitionDurations(nodePool *hyperv1.NodePool, currentCollectTime time.Time) {
	for i := range nodePool.Status.Conditions {
		condition := &nodePool.Status.Conditions[i]
		if _, isRetained := transitionDurationMetricConditions[condition.Type]; !isRetained {
			continue
		}
		if condition.Status != corev1.ConditionTrue {
			continue
		}
		t := condition.LastTransitionTime.Time
		if c.lastCollectTime.Before(t) && (t.Before(currentCollectTime) || t.Equal(currentCollectTime)) {
			c.transitionDurationMetric.With(map[string]string{"condition": condition.Type}).Observe(t.Sub(nodePool.CreationTimestamp.Time).Seconds())
		}
	}
}

func (c *nodePoolsMetricsCollector) collectPerNodePoolMetrics(ch chan<- prometheus.Metric, nodePool *hyperv1.NodePool, labelValues []string, msReplicas, mdReplicas map[string]int32) {
	if nodePool.Status.Version == "" {
		ch <- prometheus.MustNewConstMetric(
			initialRollingOutDurationMetricDesc,
			prometheus.GaugeValue,
			c.clock.Since(nodePool.CreationTimestamp.Time).Seconds(),
			labelValues...,
		)
	}

	var pathToReplicasCount *map[string]int32
	switch nodePool.Spec.Management.UpgradeType {
	case hyperv1.UpgradeTypeInPlace:
		pathToReplicasCount = &msReplicas
	case hyperv1.UpgradeTypeReplace:
		pathToReplicasCount = &mdReplicas
	}
	if pathToReplicasCount != nil {
		hcpNs := manifests.HostedControlPlaneNamespace(nodePool.Namespace, nodePool.Spec.ClusterName)
		ch <- prometheus.MustNewConstMetric(
			sizeMetricDesc,
			prometheus.GaugeValue,
			float64((*pathToReplicasCount)[hcpNs+"/"+nodePool.Name]),
			labelValues...,
		)
	}

	ch <- prometheus.MustNewConstMetric(
		availableReplicasMetricDesc,
		prometheus.GaugeValue,
		float64(nodePool.Status.Replicas),
		labelValues...,
	)

	if !nodePool.DeletionTimestamp.IsZero() {
		ch <- prometheus.MustNewConstMetric(
			deletingDurationMetricDesc,
			prometheus.GaugeValue,
			c.clock.Since(nodePool.DeletionTimestamp.Time).Seconds(),
			labelValues...,
		)
	}
}

func (c *nodePoolsMetricsCollector) emitAggregatedMetrics(ch chan<- prometheus.Metric, platformToNodePoolsCount map[hyperv1.PlatformType]int, platformToFailureConditionToNodePoolsCount map[hyperv1.PlatformType]*map[string]int, hclusterPathToData map[string]*hclusterData) {
	for platform, nodePoolsCount := range platformToNodePoolsCount {
		ch <- prometheus.MustNewConstMetric(
			countByPlatformMetricDesc,
			prometheus.GaugeValue,
			float64(nodePoolsCount),
			string(platform),
		)
	}

	for platform, failureConditionToNodePoolsCount := range platformToFailureConditionToNodePoolsCount {
		for failureCondition, nodePoolsCount := range *failureConditionToNodePoolsCount {
			ch <- prometheus.MustNewConstMetric(
				countByPlatformAndFailureConditionMetricDesc,
				prometheus.GaugeValue,
				float64(nodePoolsCount),
				string(platform),
				failureCondition,
			)
		}
	}

	for _, hcData := range hclusterPathToData {
		hclusterLabelValues := []string{hcData.namespace, hcData.name, hcData.id, string(hcData.platform)}
		ch <- prometheus.MustNewConstMetric(
			countByHClusterMetricDesc,
			prometheus.GaugeValue,
			float64(hcData.nodePoolsCount),
			hclusterLabelValues...,
		)
		ch <- prometheus.MustNewConstMetric(
			vCpusCountByHClusterMetricDesc,
			prometheus.GaugeValue,
			float64(hcData.vCpusCount),
			hclusterLabelValues...,
		)
		if hcData.vCpusCountErr != nil {
			ch <- prometheus.MustNewConstMetric(
				vCpusComputationErrorByHClusterMetricDesc,
				prometheus.GaugeValue,
				1.0,
				append(hclusterLabelValues, hcData.vCpusCountErr.Error())...,
			)
		}
	}
}
