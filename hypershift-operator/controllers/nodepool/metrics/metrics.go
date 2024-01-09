package metrics

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
	hyperv1 "github.com/openshift/hypershift/api/types/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/support/conditions"
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/clock"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	// Aggregating metrics - name & help

	CountByPlatformMetricName = "hypershift_nodepools" // What about renaming it to hypershift_nodepools_by_platform ?
	countByPlatformMetricHelp = "Number of NodePools for a given platform."

	CountByPlatformAndFailureConditionMetricName = "hypershift_nodepools_failure_conditions" // What about renaming it to hypershift_nodepools_by_platform_and_failure_condition ?
	countByPlatformAndFailureConditionMetricHelp = "Number of NodePools for a given platform and failure condition."

	CountByHClusterMetricName = "hypershift_hostedcluster_nodepools" // What about renaming it to hypershift_cluster_nodepools ?
	countByHClusterMetricHelp = "Number of NodePools for a given HostedCluster"

	CoresCountByHClusterMetricName = "hypershift_cluster_cores"
	CoresCountByHClusterMetricHelp = "Number of cores for a given HostedCluster. " +
		"-1 if this number cannot be computed." // Be careful when changing this metric as it is used for billing the customers

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

// semantically constant - not suposed to be changed at runtime
var (
	transitionDurationMetricConditions = map[string]void{
		hyperv1.NodePoolReachedIgnitionEndpoint:       void{},
		hyperv1.NodePoolAllMachinesReadyConditionType: void{},
		hyperv1.NodePoolAllNodesHealthyConditionType:  void{},
	}

	knownPlatforms = hyperv1.PlatformTypes()

	knownConditionToExpectedStatus = conditions.ExpectedNodePoolConditions()

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

	coresCountByHClusterMetricDesc = prometheus.NewDesc(
		CoresCountByHClusterMetricName,
		CoresCountByHClusterMetricHelp,
		hclusterLabels, nil)

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
	ec2Client ec2iface.EC2API
	clock     clock.Clock

	ec2InstanceTypeToCoresCount map[string]int32

	transitionDurationMetric *prometheus.HistogramVec

	lastCollectTime time.Time
}

func createNodePoolsMetricsCollector(client client.Client, ec2Client ec2iface.EC2API, clock clock.Clock) prometheus.Collector {
	return &nodePoolsMetricsCollector{
		Client:                      client,
		ec2Client:                   ec2Client,
		clock:                       clock,
		ec2InstanceTypeToCoresCount: make(map[string]int32),
		transitionDurationMetric: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    TransitionDurationMetricName,
			Help:    transitionDurationMetricHelp,
			Buckets: []float64{5, 10, 20, 30, 60, 90, 120, 180, 240, 300, 360, 480, 600},
		}, []string{"condition"}),
		lastCollectTime: time.UnixMilli(0),
	}
}

func CreateAndRegisterNodePoolsMetricsCollector(client client.Client, ec2Client ec2iface.EC2API) prometheus.Collector {
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
	coresCount     int32
}

func createFailureConditionToNodePoolsCountMap() *map[string]int {
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

func (c *nodePoolsMetricsCollector) resolveCoresCountPerNode(nodePool *hyperv1.NodePool, unresolvedEc2InstanceTypes *map[string]void) int32 {
	if nodePool.Spec.Platform.Type != hyperv1.AWSPlatform || c.ec2Client == nil {
		return -1
	}

	awsPlatform := nodePool.Spec.Platform.AWS

	if awsPlatform == nil {
		return -1
	}

	ec2InstanceType := awsPlatform.InstanceType

	if _, isUnresolved := (*unresolvedEc2InstanceTypes)[ec2InstanceType]; isUnresolved {
		return -1
	}

	if coreCountPerNode, isCached := c.ec2InstanceTypeToCoresCount[ec2InstanceType]; isCached {
		return coreCountPerNode
	}
	awsInput := ec2.DescribeInstanceTypesInput{InstanceTypes: []*string{&ec2InstanceType}}

	if awsOuput, err := c.ec2Client.DescribeInstanceTypes(&awsInput); awsOuput != nil && err == nil {
		if len(awsOuput.InstanceTypes) == 1 {
			ec2InstanceTypeInfo := awsOuput.InstanceTypes[0]

			if ec2InstanceTypeInfo != nil {
				instanceTypeInInfo := ec2InstanceTypeInfo.InstanceType
				cpuInfo := ec2InstanceTypeInfo.VCpuInfo

				if instanceTypeInInfo != nil && *instanceTypeInInfo == ec2InstanceType && cpuInfo != nil {
					coreCountPtr := cpuInfo.DefaultCores

					if coreCountPtr != nil {
						coreCount := int32(*coreCountPtr)

						c.ec2InstanceTypeToCoresCount[ec2InstanceType] = coreCount

						return coreCount
					}
				}
			}
		}

		ctrllog.Log.Error(fmt.Errorf("unexpected AWS output"), "unexpected output for EC2 verb 'describe-instance-types' while querying the following EC2 instance type: "+ec2InstanceType)
	} else {
		ctrllog.Log.Error(err, "failed to call AWS to resolve the number of cores per node for the following EC2 instance type: "+ec2InstanceType)
	}

	(*unresolvedEc2InstanceTypes)[ec2InstanceType] = void{}

	return -1
}

func (c *nodePoolsMetricsCollector) Collect(ch chan<- prometheus.Metric) {
	ctx := context.Background()
	currentCollectTime := c.clock.Now()
	log := ctrllog.Log
	unresolvedEc2InstanceTypes := make(map[string]void)

	// Data retrieved from objects other than node pools in below loops
	hclusterPathToData := make(map[string]*hclusterData)
	machineSetPathToReplicasCount := make(map[string]int32)
	machineDeploymentPathToReplicasCount := make(map[string]int32)

	// Hosted clusters loop
	{
		hclusters := &hyperv1.HostedClusterList{}

		if err := c.List(ctx, hclusters); err != nil {
			log.Error(err, "failed to list hosted clusters while collecting metrics")
		}

		for k := range hclusters.Items {
			hcluster := &hclusters.Items[k]

			hclusterPathToData[hcluster.Namespace+"/"+hcluster.Name] = &hclusterData{
				id:        hcluster.Spec.ClusterID,
				namespace: hcluster.Namespace,
				name:      hcluster.Name,
				platform:  hcluster.Spec.Platform.Type,
			}
		}
	}

	// Machine sets loop
	{
		machineSets := &capiv1.MachineSetList{}

		if err := c.List(ctx, machineSets); err != nil {
			log.Error(err, "failed to list machine sets while collecting metrics")
		}

		for k := range machineSets.Items {
			machineSet := &machineSets.Items[k]
			msPath := machineSet.Namespace + "/" + machineSet.Name

			machineSetPathToReplicasCount[msPath] = *machineSet.Spec.Replicas
		}
	}

	// Machine deployments loop
	{
		machineDeployments := &capiv1.MachineDeploymentList{}

		if err := c.List(ctx, machineDeployments); err != nil {
			log.Error(err, "failed to list machine deployments while collecting metrics")
		}

		for k := range machineDeployments.Items {
			machineDeployment := &machineDeployments.Items[k]
			mdPath := machineDeployment.Namespace + "/" + machineDeployment.Name

			machineDeploymentPathToReplicasCount[mdPath] = *machineDeployment.Spec.Replicas
		}
	}

	// countByPlatformMetric - init
	platformToNodePoolsCount := make(map[hyperv1.PlatformType]int)

	for k := range knownPlatforms {
		platformToNodePoolsCount[knownPlatforms[k]] = 0
	}

	// countByPlatformAndFailureConditionMetric - init
	platformToFailureConditionToNodePoolsCount := make(map[hyperv1.PlatformType]*map[string]int)

	for k := range knownPlatforms {
		platformToFailureConditionToNodePoolsCount[knownPlatforms[k]] = createFailureConditionToNodePoolsCountMap()
	}

	// MAIN LOOP - node pools loop
	{
		npList := &hyperv1.NodePoolList{}

		if err := c.List(ctx, npList); err != nil {
			log.Error(err, "failed to list node pools while collecting metrics")
		}

		for k := range npList.Items {
			nodePool := &npList.Items[k]
			hclusterId := ""

			// countByPlatformMetric - aggregation
			platform := nodePool.Spec.Platform.Type
			platformToNodePoolsCount[platform] += 1

			// countByPlatformAndFailureConditionMetric - aggregation
			{
				_, isKnownPlatform := platformToFailureConditionToNodePoolsCount[platform]

				if !isKnownPlatform {
					platformToFailureConditionToNodePoolsCount[platform] = createFailureConditionToNodePoolsCountMap()
				}

				failureConditionToNodePoolsCount := platformToFailureConditionToNodePoolsCount[platform]

				for _, condition := range nodePool.Status.Conditions {
					expectedStatus, isKnownCondition := knownConditionToExpectedStatus[condition.Type]

					if isKnownCondition && condition.Status != expectedStatus {
						failureCondPrefix := ""

						if expectedStatus == corev1.ConditionTrue {
							failureCondPrefix = "not_"
						}

						failureCondition := failureCondPrefix + condition.Type

						(*failureConditionToNodePoolsCount)[failureCondition] += 1
					}
				}
			}

			if hclusterData := hclusterPathToData[nodePool.Namespace+"/"+nodePool.Spec.ClusterName]; hclusterData != nil {
				hclusterId = hclusterData.id

				// countByHClusterMetric - aggregation
				hclusterData.nodePoolsCount += 1

				// coresCountByHClusterMetric - aggregation
				if hclusterData.coresCount >= 0 && nodePool.Status.Replicas > 0 {
					nodePoolCoresCount := c.resolveCoresCountPerNode(nodePool, &unresolvedEc2InstanceTypes)

					if nodePoolCoresCount >= 0 {
						hclusterData.coresCount += nodePoolCoresCount * nodePool.Status.Replicas
					} else {
						hclusterData.coresCount = -1
					}
				}
			}

			// transitionDurationMetric - aggregation
			for i := range nodePool.Status.Conditions {
				condition := &nodePool.Status.Conditions[i]
				if _, isRetained := transitionDurationMetricConditions[condition.Type]; isRetained {
					if condition.Status == corev1.ConditionTrue {
						t := condition.LastTransitionTime.Time

						if c.lastCollectTime.Before(t) && (t.Before(currentCollectTime) || t.Equal(currentCollectTime)) {
							c.transitionDurationMetric.With(map[string]string{"condition": condition.Type}).Observe(t.Sub(nodePool.CreationTimestamp.Time).Seconds())
						}
					}
				}
			}

			nodePoolLabelValues := []string{nodePool.Namespace, nodePool.Name, hclusterId, nodePool.Spec.ClusterName, string(nodePool.Spec.Platform.Type)}

			// initialRollingOutDurationMetric
			if nodePool.Status.Version == "" {
				initializingDuration := c.clock.Since(nodePool.CreationTimestamp.Time).Seconds()

				ch <- prometheus.MustNewConstMetric(
					initialRollingOutDurationMetricDesc,
					prometheus.GaugeValue,
					initializingDuration,
					nodePoolLabelValues...,
				)
			}

			// sizeMetric
			{
				var pathToReplicasCount *map[string]int32

				if nodePool.Spec.Management.UpgradeType == hyperv1.UpgradeTypeInPlace {
					// we use machineSet.Spec.Replicas because .Spec.Replicas will not be set if autoscaling is enabled
					pathToReplicasCount = &machineSetPathToReplicasCount
				} else if nodePool.Spec.Management.UpgradeType == hyperv1.UpgradeTypeReplace {
					// we use machineDeployment.Spec.Replicas because .Spec.Replicas will not be set if autoscaling is enabled
					pathToReplicasCount = &machineDeploymentPathToReplicasCount
				}

				if pathToReplicasCount != nil {
					hcpNs := manifests.HostedControlPlaneNamespace(nodePool.Namespace, nodePool.Spec.ClusterName)
					wishedReplicas := float64((*pathToReplicasCount)[hcpNs+"/"+nodePool.Name])

					ch <- prometheus.MustNewConstMetric(
						sizeMetricDesc,
						prometheus.GaugeValue,
						wishedReplicas,
						nodePoolLabelValues...,
					)
				}
			}

			// availableReplicasMetric
			{
				availableReplicas := float64(nodePool.Status.Replicas)

				ch <- prometheus.MustNewConstMetric(
					availableReplicasMetricDesc,
					prometheus.GaugeValue,
					availableReplicas,
					nodePoolLabelValues...,
				)
			}

			// deletingDurationMetric
			if !nodePool.DeletionTimestamp.IsZero() {
				deletingDuration := c.clock.Since(nodePool.DeletionTimestamp.Time).Seconds()

				ch <- prometheus.MustNewConstMetric(
					deletingDurationMetricDesc,
					prometheus.GaugeValue,
					deletingDuration,
					nodePoolLabelValues...,
				)
			}
		}
	}

	// AGGREGATED METRICS

	// countByPlatformMetric
	for platform, nodePoolsCount := range platformToNodePoolsCount {
		ch <- prometheus.MustNewConstMetric(
			countByPlatformMetricDesc,
			prometheus.GaugeValue,
			float64(nodePoolsCount),
			string(platform),
		)
	}

	// countByPlatformAndFailureConditionMetric
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

	for _, hclusterData := range hclusterPathToData {
		hclusterLabelValues := []string{hclusterData.namespace, hclusterData.name, hclusterData.id, string(hclusterData.platform)}

		// countByHClusterMetric
		ch <- prometheus.MustNewConstMetric(
			countByHClusterMetricDesc,
			prometheus.GaugeValue,
			float64(hclusterData.nodePoolsCount),
			hclusterLabelValues...,
		)

		// coresCountByHClusterMetric
		ch <- prometheus.MustNewConstMetric(
			coresCountByHClusterMetricDesc,
			prometheus.GaugeValue,
			float64(hclusterData.coresCount),
			hclusterLabelValues...,
		)
	}

	// transitionDurationMetric
	c.transitionDurationMetric.Collect(ch)

	c.lastCollectTime = currentCollectTime
}
