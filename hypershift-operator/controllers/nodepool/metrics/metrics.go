package metrics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/support/conditions"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/pricing"
	pricingtypes "github.com/aws/aws-sdk-go-v2/service/pricing/types"
	"github.com/aws/smithy-go"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/clock"

	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
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
	ec2Client     EC2API
	pricingClient PricingAPI
	clock         clock.Clock

	ec2InstanceTypeToVCpusCount map[string]int32

	transitionDurationMetric *prometheus.HistogramVec

	lastCollectTime time.Time
}

// EC2API defines the interface for EC2 operations needed by metrics collector
type EC2API interface {
	DescribeInstanceTypes(ctx context.Context, params *ec2.DescribeInstanceTypesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstanceTypesOutput, error)
}

// PricingAPI defines the interface for Pricing operations needed by metrics collector
type PricingAPI interface {
	GetProducts(ctx context.Context, params *pricing.GetProductsInput, optFns ...func(*pricing.Options)) (*pricing.GetProductsOutput, error)
}

func createNodePoolsMetricsCollector(client client.Client, ec2Client EC2API, pricingClient PricingAPI, clock clock.Clock) prometheus.Collector {
	return &nodePoolsMetricsCollector{
		Client:                      client,
		ec2Client:                   ec2Client,
		pricingClient:               pricingClient,
		clock:                       clock,
		ec2InstanceTypeToVCpusCount: make(map[string]int32),
		transitionDurationMetric: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    TransitionDurationMetricName,
			Help:    transitionDurationMetricHelp,
			Buckets: []float64{5, 10, 20, 30, 60, 90, 120, 180, 240, 300, 360, 480, 600},
		}, []string{"condition"}),
		lastCollectTime: time.UnixMilli(0),
	}
}

func CreateAndRegisterNodePoolsMetricsCollector(client client.Client, ec2Client EC2API, pricingClient PricingAPI) prometheus.Collector {
	collector := createNodePoolsMetricsCollector(client, ec2Client, pricingClient, clock.RealClock{})

	metrics.Registry.MustRegister(collector)

	return collector
}

func (c *nodePoolsMetricsCollector) Describe(ch chan<- *prometheus.Desc) {
	prometheus.DescribeByCollect(c, ch)
}

type hclusterData struct {
	id                    string
	namespace             string
	name                  string
	platform              hyperv1.PlatformType
	nodePoolsCount        int
	vCpusCount            int32
	vCpusCountErrorReason string
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

// Error reason constants for vCPU lookup failures
const (
	noAWSEC2ClientErrorReason                      = "no AWS EC2 client"
	noAWSPricingClientErrorReason                  = "no AWS Pricing client"
	unexpectedAWSOutputErrorReason                 = "unexpected AWS output"
	failedToCallAWSErrorReason                     = "failed to call AWS"
	unknownInstanceTypeErrorReason                 = "unknown instance type"
	unableToUnMarshalPricingDataErrorReason        = "unable to unmarshal pricing data"
	rosaCPUsInstanceTypesConfigNotFoundErrorReason = "ROSA CPUs instance types ConfigMap not found"

	rosaCPUsInstanceTypeConfigMapName     = "rosa-cpus-instance-types-config"
	rosaCPUInstanceTypeConfigMapNamespace = "hypershift"
)

func extractCPUFromInstanceTypeNameViaPricingAPI(instanceTypeName string, pricingClient PricingAPI) (int32, string) {
	if pricingClient == nil {
		ctrllog.Log.Error(errors.New(noAWSPricingClientErrorReason),
			"cannot retrieve the number of vCPUs for instance type "+instanceTypeName+" as the Pricing client used to query AWS API is not properly initialized")
		return -1, noAWSPricingClientErrorReason
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(time.Second*5))
	defer cancel()
	pricingInput := &pricing.GetProductsInput{
		ServiceCode: aws.String("AmazonEC2"),
		Filters: []pricingtypes.Filter{
			{
				Type:  pricingtypes.FilterTypeTermMatch,
				Field: aws.String("instanceType"),
				Value: aws.String(instanceTypeName),
			},
		},
	}
	pricingResult, err := pricingClient.GetProducts(ctx, pricingInput)
	if err != nil {
		ctrllog.Log.Error(err, "failed to call AWS Pricing API to resolve the number of vCPUs for instance type "+instanceTypeName)
		return -1, failedToCallAWSErrorReason
	}
	if pricingResult == nil || len(pricingResult.PriceList) == 0 {
		ctrllog.Log.Error(errors.New(unexpectedAWSOutputErrorReason),
			"unexpected output from AWS Pricing API for instance type "+instanceTypeName)
		return -1, unexpectedAWSOutputErrorReason
	}

	// In AWS SDK v2, PriceList is []string where each string is a JSON object
	for _, priceItemJSON := range pricingResult.PriceList {
		var priceItem PriceItemInstance
		if err := json.Unmarshal([]byte(priceItemJSON), &priceItem); err != nil {
			ctrllog.Log.Error(err, "unable to unmarshal pricing item for instance "+instanceTypeName)
			continue // Try next item
		}
		if priceItem.Product.Attributes.VCPU != "" {
			value, err := strconv.ParseInt(priceItem.Product.Attributes.VCPU, 10, 32)
			if err != nil {
				ctrllog.Log.Error(err, "couldn't parse VCPU data for instance "+instanceTypeName)
				continue // Try next item
			}
			return int32(value), ""
		}
	}
	ctrllog.Log.Error(errors.New(unknownInstanceTypeErrorReason),
		"unknown instance type "+instanceTypeName+" in AWS Pricing API response")
	return -1, unknownInstanceTypeErrorReason
}

func extractCPUFromInstanceTypeNameViaEC2API(instanceTypeName string, ec2Client EC2API) (int32, string) {
	if ec2Client == nil {
		ctrllog.Log.Error(errors.New(noAWSEC2ClientErrorReason),
			"cannot retrieve the number of vCPUs for instance type "+instanceTypeName+" as the EC2 client used to query AWS API is not properly initialized")
		return -1, noAWSEC2ClientErrorReason
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(time.Second*5))
	defer cancel()

	ec2InstanceTypes, err := ec2Client.DescribeInstanceTypes(ctx, &ec2.DescribeInstanceTypesInput{
		InstanceTypes: []ec2types.InstanceType{ec2types.InstanceType(instanceTypeName)},
	})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == "InvalidInstanceType" {
			ctrllog.Log.Error(err, "unknown instance type "+instanceTypeName+" in EC2 API")
			return -1, unknownInstanceTypeErrorReason
		}
		ctrllog.Log.Error(err, "failed to call AWS EC2 API to resolve the number of vCPUs for instance type "+instanceTypeName)
		return -1, failedToCallAWSErrorReason
	}
	if ec2InstanceTypes == nil ||
		len(ec2InstanceTypes.InstanceTypes) == 0 ||
		ec2InstanceTypes.InstanceTypes[0].VCpuInfo == nil ||
		ec2InstanceTypes.InstanceTypes[0].VCpuInfo.DefaultVCpus == nil {
		ctrllog.Log.Error(errors.New(unexpectedAWSOutputErrorReason),
			"unexpected output for EC2 verb 'describe-instance-types' for instance type "+instanceTypeName)
		return -1, unexpectedAWSOutputErrorReason
	}

	return *ec2InstanceTypes.InstanceTypes[0].VCpuInfo.DefaultVCpus, ""
}

func extractCPUFromInstanceTypeNameViaConfigMap(instanceTypeName string, client client.Client) (int32, string) {
	context, cancel := context.WithTimeout(context.Background(), time.Duration(time.Second*5))
	defer cancel()
	vCPUsIntstanceTypeConfig := &corev1.ConfigMap{}
	if err := client.Get(context, types.NamespacedName{Name: rosaCPUsInstanceTypeConfigMapName, Namespace: rosaCPUInstanceTypeConfigMapNamespace},
		vCPUsIntstanceTypeConfig); err != nil {
		ctrllog.Log.Error(err, "unable to retrieve ConfigMap "+rosaCPUsInstanceTypeConfigMapName+" in namespace "+rosaCPUInstanceTypeConfigMapNamespace+" for instance "+instanceTypeName)
		return -1, rosaCPUsInstanceTypesConfigNotFoundErrorReason
	}
	if value, ok := vCPUsIntstanceTypeConfig.Data[instanceTypeName]; ok {
		vCpusCount, err := strconv.ParseInt(value, 10, 32)
		if err != nil {
			ctrllog.Log.Error(err, "couldn't parse VCPU data from ConfigMap for instance "+instanceTypeName)
			return -1, unableToUnMarshalPricingDataErrorReason
		}
		return int32(vCpusCount), ""
	}
	ctrllog.Log.Error(errors.New(unknownInstanceTypeErrorReason), "unknown instance type "+instanceTypeName+" in ConfigMap")
	return -1, unknownInstanceTypeErrorReason
}

func (c *nodePoolsMetricsCollector) retrieveVCpusDetailsPerNode(nodePool *hyperv1.NodePool, ec2InstanceTypeToResolutionErrorReason *map[string]string) (int32, error) {
	if nodePool.Spec.Platform.Type != hyperv1.AWSPlatform {
		ctrllog.Log.Info("cannot retrieve the number of vCPUs for " + nodePool.Name + " node pool as its platform is not supported (supported platforms: AWS)")
		return -1, fmt.Errorf("unsupported platform")
	}

	if c.ec2Client == nil {
		errorReason := "no AWS EC2 client"
		ctrllog.Log.Error(errors.New(errorReason), "cannot retrieve the number of vCPUs for "+nodePool.Name+" node pool as the client used to query AWS API is not properly initialized")
		return -1, errors.New(errorReason)
	}

	awsPlatform := nodePool.Spec.Platform.AWS
	if awsPlatform == nil {
		err := fmt.Errorf("spec.platform.aws missing in node pool")
		ctrllog.Log.Error(err, "cannot retrieve the number of vCPUs for "+nodePool.Name+" node pool as its specification is inconsistent")
		return -1, err
	}

	ec2InstanceType := awsPlatform.InstanceType

	// Check if this instance type has a cached error reason
	if unresolvedErrorReason, isUnresolved := (*ec2InstanceTypeToResolutionErrorReason)[ec2InstanceType]; isUnresolved {
		return -1, errors.New(unresolvedErrorReason)
	}

	// Check if we have a cached vCPU count for this instance type
	if vCpusCountPerNode, isCached := c.ec2InstanceTypeToVCpusCount[ec2InstanceType]; isCached {
		return vCpusCountPerNode, nil
	}

	// Try ConfigMap first (outside cache to avoid caching ConfigMap values)
	vCpusCount, errReason := extractCPUFromInstanceTypeNameViaConfigMap(ec2InstanceType, c.Client)
	if errReason == "" {
		// Don't cache ConfigMap values
		return vCpusCount, nil
	}

	// Try EC2 API
	vCpusCount, errReason = extractCPUFromInstanceTypeNameViaEC2API(ec2InstanceType, c.ec2Client)
	if errReason == "" {
		// Cache successful EC2 API result
		c.ec2InstanceTypeToVCpusCount[ec2InstanceType] = vCpusCount
		return vCpusCount, nil
	}

	// Try Pricing API as last resort
	vCpusCount, errReason = extractCPUFromInstanceTypeNameViaPricingAPI(ec2InstanceType, c.pricingClient)
	if errReason == "" {
		// Cache successful Pricing API result
		c.ec2InstanceTypeToVCpusCount[ec2InstanceType] = vCpusCount
		return vCpusCount, nil
	}

	// All methods failed - cache "unknown instance type" as the error reason to avoid spamming AWS APIs
	(*ec2InstanceTypeToResolutionErrorReason)[ec2InstanceType] = unknownInstanceTypeErrorReason
	return -1, errors.New(unknownInstanceTypeErrorReason)
}

func (c *nodePoolsMetricsCollector) Collect(ch chan<- prometheus.Metric) {
	ctx := context.Background()
	currentCollectTime := c.clock.Now()
	log := ctrllog.Log
	ec2InstanceTypeToResolutionErrorReason := make(map[string]string)

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
		platformToFailureConditionToNodePoolsCount[knownPlatforms[k]] = createFailureConditionToNodePoolsCountMap(conditions.ExpectedNodePoolConditions(&hyperv1.NodePool{
			Spec: hyperv1.NodePoolSpec{
				Platform: hyperv1.NodePoolPlatform{
					Type: knownPlatforms[k],
				},
			},
		}))
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
				knownConditionToExpectedStatus := conditions.ExpectedNodePoolConditions(nodePool)
				_, isKnownPlatform := platformToFailureConditionToNodePoolsCount[platform]

				if !isKnownPlatform {
					platformToFailureConditionToNodePoolsCount[platform] = createFailureConditionToNodePoolsCountMap(knownConditionToExpectedStatus)
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

				// vCpusCountByHClusterMetric - aggregation
				if hclusterData.vCpusCount >= 0 && nodePool.Status.Replicas > 0 {
					nodeVCpus, err := c.retrieveVCpusDetailsPerNode(nodePool, &ec2InstanceTypeToResolutionErrorReason)
					if err != nil {
						hclusterData.vCpusCount = -1
						hclusterData.vCpusCountErrorReason = err.Error()
					} else {
						hclusterData.vCpusCount += nodeVCpus * nodePool.Status.Replicas
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

				switch nodePool.Spec.Management.UpgradeType {
				case hyperv1.UpgradeTypeInPlace:
					// we use machineSet.Spec.Replicas because .Spec.Replicas will not be set if autoscaling is enabled
					pathToReplicasCount = &machineSetPathToReplicasCount
				case hyperv1.UpgradeTypeReplace:
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

		// vCpusCountByHClusterMetric
		ch <- prometheus.MustNewConstMetric(
			vCpusCountByHClusterMetricDesc,
			prometheus.GaugeValue,
			float64(hclusterData.vCpusCount),
			hclusterLabelValues...,
		)

		// vCpusCountByHClusterMetric
		if hclusterData.vCpusCountErrorReason != "" {
			ch <- prometheus.MustNewConstMetric(
				vCpusComputationErrorByHClusterMetricDesc,
				prometheus.GaugeValue,
				1.0,
				append(hclusterLabelValues, hclusterData.vCpusCountErrorReason)...,
			)
		}
	}

	// transitionDurationMetric
	c.transitionDurationMetric.Collect(ch)

	c.lastCollectTime = currentCollectTime
}
