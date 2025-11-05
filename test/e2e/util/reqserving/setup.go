package reqserving

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	schedulingv1alpha1 "github.com/openshift/hypershift/api/scheduling/v1alpha1"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedclustersizing"
	scheduleraws "github.com/openshift/hypershift/hypershift-operator/controllers/scheduler/aws"
	schedulerutil "github.com/openshift/hypershift/hypershift-operator/controllers/scheduler/util"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/util"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"

	configv1 "github.com/openshift/api/config/v1"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	autoscalingv1 "github.com/openshift/cluster-autoscaler-operator/pkg/apis/autoscaling/v1"
	autoscalingv1beta1 "github.com/openshift/cluster-autoscaler-operator/pkg/apis/autoscaling/v1beta1"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/route53"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

var (
	machineSizes  = []string{"small", "medium", "large"}
	instanceTypes = map[string]string{
		"small":  "m6i.large",
		"medium": "m6i.xlarge",
		"large":  "m6i.2xlarge",
	}
	goMemLimit = map[string]string{
		"small":  "4915MiB",
		"medium": "9830MiB",
		"large":  "19660MiB",
	}
)

const (
	requestServingPairCount = 20
)

type DryRunOptions struct {
	Dir string
}

func ConfigureManagementCluster(ctx context.Context, dryRunOpts *DryRunOptions) error {
	client, err := e2eutil.GetClient()
	if err != nil {
		return fmt.Errorf("failed to get cluster client: %w", err)
	}
	if err := checkStandaloneCluster(ctx, client); err != nil {
		return fmt.Errorf("failed standalone cluster check: %w", err)
	}
	if err := ConfigureClusterAutoscaler(ctx, client, dryRunOpts); err != nil {
		return err
	}
	if err := ConfigureMachineHealthCheck(ctx, client, dryRunOpts); err != nil {
		return err
	}
	if err := ConfigureControlPlaneMachineSets(ctx, client, dryRunOpts); err != nil {
		return err
	}
	return nil
}

// InferBaseDomain inspects the management cluster DNS resource and infers the base domain.
func InferBaseDomain(ctx context.Context, awsCredentialsFile string) (string, error) {
	client, err := e2eutil.GetClient()
	if err != nil {
		return "", fmt.Errorf("failed to get cluster client: %w", err)
	}

	dns := &configv1.DNS{}
	if err := client.Get(ctx, crclient.ObjectKey{Name: "cluster"}, dns); err != nil {
		return "", fmt.Errorf("failed to get DNS resource: %w", err)
	}
	// Use dns.Spec.PublicZone.ID to look up the base domain
	zoneID := dns.Spec.PublicZone.ID
	if zoneID == "" {
		return "", fmt.Errorf("no public zone ID found")
	}
	awsSession := awsutil.NewSession("e2e-route53", awsCredentialsFile, "", "", "us-east-1")
	route53Client := route53.New(awsSession)
	hostedZoneResult, err := route53Client.GetHostedZone(&route53.GetHostedZoneInput{
		Id: aws.String(zoneID),
	})
	if err != nil {
		return "", fmt.Errorf("failed to get hosted zone: %w", err)
	}
	domain := strings.TrimSuffix(aws.StringValue(hostedZoneResult.HostedZone.Name), ".")
	return domain, nil
}

// InferAWSAvailabilityZones inspects the management cluster worker MachineSets
// and infers the set of AWS availability zones present in the cluster.
// It returns a sorted list of unique zones.
func InferAWSAvailabilityZones(ctx context.Context) ([]string, error) {
	client, err := e2eutil.GetClient()
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster client: %w", err)
	}

	machineSetList := &machinev1beta1.MachineSetList{}
	if err := client.List(ctx, machineSetList, crclient.InNamespace("openshift-machine-api")); err != nil {
		return nil, fmt.Errorf("failed to list machinesets: %w", err)
	}

	zonesSet := map[string]struct{}{}
	for i := range machineSetList.Items {
		ms := &machineSetList.Items[i]
		if ms.Spec.Template.Spec.ProviderSpec.Value == nil {
			continue
		}
		awsProviderSpec := &machinev1beta1.AWSMachineProviderConfig{}
		if err := json.Unmarshal(ms.Spec.Template.Spec.ProviderSpec.Value.Raw, awsProviderSpec); err != nil {
			return nil, fmt.Errorf("failed to unmarshal machineset provider spec: %w", err)
		}
		if awsProviderSpec.Placement.AvailabilityZone == "" {
			continue
		}
		zonesSet[awsProviderSpec.Placement.AvailabilityZone] = struct{}{}
	}

	if len(zonesSet) < 3 {
		return nil, fmt.Errorf("expected at least 3 AWS availability zones, found %d", len(zonesSet))
	}

	zones := make([]string, 0, len(zonesSet))
	for z := range zonesSet {
		zones = append(zones, z)
	}
	sort.Strings(zones)
	return zones, nil
}

func ConfigureClusterAutoscaler(ctx context.Context, client crclient.Client, dryRunOpts *DryRunOptions) error {
	autoscaler := &autoscalingv1.ClusterAutoscaler{}
	autoscaler.Name = "default"
	if dryRunOpts != nil {
		client = e2eutil.GetFakeClient()
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, client, autoscaler, func() error {
		autoscaler.Spec.BalanceSimilarNodeGroups = ptr.To(true)
		if autoscaler.Spec.ScaleDown == nil {
			autoscaler.Spec.ScaleDown = &autoscalingv1.ScaleDownConfig{}
		}
		autoscaler.Spec.ScaleDown.Enabled = true
		return nil
	}); err != nil {
		return fmt.Errorf("failed to update cluster autoscaler resource: %w", err)
	}
	if dryRunOpts != nil {
		outputFile := fmt.Sprintf("%s/cluster-autoscaler.yaml", dryRunOpts.Dir)
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(autoscaler), autoscaler); err != nil {
			return fmt.Errorf("failed to get cluster autoscaler resource: %w", err)
		}
		yaml, err := util.SerializeResource(autoscaler, api.Scheme)
		if err != nil {
			return fmt.Errorf("failed to serialize cluster autoscaler resource: %w", err)
		}
		if err := os.WriteFile(outputFile, []byte(yaml), 0644); err != nil {
			return fmt.Errorf("failed to write cluster autoscaler resource to file: %w", err)
		}
	}
	return nil
}

func ConfigureMachineHealthCheck(ctx context.Context, client crclient.Client, dryRunOpts *DryRunOptions) error {
	mhc := &machinev1beta1.MachineHealthCheck{}
	mhc.Name = "request-serving-mhc"
	mhc.Namespace = "openshift-machine-api"
	if dryRunOpts != nil {
		client = e2eutil.GetFakeClient()
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, client, mhc, func() error {
		mhc.Spec.Selector.MatchLabels = map[string]string{
			ControlPlaneNodeLabel: "true",
		}
		mhc.Spec.UnhealthyConditions = []machinev1beta1.UnhealthyCondition{
			{
				Type:    corev1.NodeReady,
				Status:  corev1.ConditionFalse,
				Timeout: metav1.Duration{Duration: 300 * time.Second},
			},
			{
				Type:    corev1.NodeReady,
				Status:  corev1.ConditionUnknown,
				Timeout: metav1.Duration{Duration: 300 * time.Second},
			},
		}
		mhc.Spec.MaxUnhealthy = ptr.To(intstr.FromInt(2))
		mhc.Spec.NodeStartupTimeout = &metav1.Duration{Duration: 20 * time.Minute}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to update the machine health check for request serving machines: %w", err)
	}
	if dryRunOpts != nil {
		outputFile := fmt.Sprintf("%s/machine-health-check.yaml", dryRunOpts.Dir)
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(mhc), mhc); err != nil {
			return fmt.Errorf("failed to get machine health check resource: %w", err)
		}
		yaml, err := util.SerializeResource(mhc, api.Scheme)
		if err != nil {
			return fmt.Errorf("failed to serialize machine health check resource: %w", err)
		}
		if err := os.WriteFile(outputFile, []byte(yaml), 0644); err != nil {
			return fmt.Errorf("failed to write machine health check resource to file: %w", err)
		}
	}
	return nil
}

func ConfigureControlPlaneMachineSets(ctx context.Context, client crclient.Client, dryRunOpts *DryRunOptions) error {
	infra, err := getClusterInfrastructure(ctx, client)
	if err != nil {
		return err
	}
	infraID := infra.Status.InfrastructureName

	updateClient := client
	if dryRunOpts != nil {
		updateClient = e2eutil.GetFakeClient()
	}

	machineSetList := &machinev1beta1.MachineSetList{}
	// FIXME: This should only fetch the default worker machinesets of a standalone cluster
	// It should not include control plane or control plane machinesets.
	if err := client.List(ctx, machineSetList, crclient.InNamespace("openshift-machine-api")); err != nil {
		return fmt.Errorf("failed to list machinesets: %w", err)
	}

	// Find the default worker machinesets
	workerMSByZone := map[string]*machinev1beta1.MachineSet{}
	for i, ms := range machineSetList.Items {
		if ms.Spec.Template.Spec.ProviderSpec.Value == nil {
			continue
		}
		awsProviderSpec := &machinev1beta1.AWSMachineProviderConfig{}
		if err := json.Unmarshal(ms.Spec.Template.Spec.ProviderSpec.Value.Raw, awsProviderSpec); err != nil {
			return fmt.Errorf("failed to unmarshal the machineset provider spec: %w", err)
		}
		// NOTE: The condition below should filter out the default worker machinesets, but it
		// could also be narrowed down by label in the initial query.
		if ms.Name == fmt.Sprintf("%s-worker-%s", infraID, awsProviderSpec.Placement.AvailabilityZone) {
			workerMSByZone[awsProviderSpec.Placement.AvailabilityZone] = &machineSetList.Items[i]
		}
	}
	if actual := len(workerMSByZone); actual < 3 {
		return fmt.Errorf("failed to find default worker machinesets, actual: %d, expected at least 3", actual)
	}

	// Pick the first 3 zones for non-request serving machinesets, sorted for consistency
	commonZones := make([]string, 0, len(workerMSByZone))
	for zone := range workerMSByZone {
		commonZones = append(commonZones, zone)
	}
	slices.Sort(commonZones)
	commonZones = commonZones[:3]

	// Common control plane machinesets
	for _, zone := range commonZones {
		workerMS := workerMSByZone[zone]
		ms := &machinev1beta1.MachineSet{}
		ms.Name = fmt.Sprintf("%s-common-%s", infraID, zone)
		ms.Namespace = workerMS.Namespace

		if _, err := controllerutil.CreateOrUpdate(ctx, updateClient, ms, func() error {
			clusterName := workerMS.Labels["machine.openshift.io/cluster-api-cluster"]
			ms.Labels = map[string]string{
				"machine.openshift.io/cluster-api-cluster": clusterName,
			}
			if ms.ObjectMeta.ResourceVersion == "" {
				// Only set the replicas to 0 if the machineset is new
				ms.Spec.Replicas = ptr.To(int32(0))
			}
			ms.Spec.Selector.MatchLabels = map[string]string{
				"machine.openshift.io/cluster-api-cluster":    clusterName,
				"machine.openshift.io/cluster-api-machineset": ms.Name,
			}
			// The labels of the spec.template.metadata will be set on individual
			// Machine resources created from the machineset.
			ms.Spec.Template.ObjectMeta.Labels = map[string]string{
				machinev1beta1.MachineClusterIDLabel:            clusterName,
				"machine.openshift.io/cluster-api-machineset":   ms.Name,
				"machine.openshift.io/cluster-api-machine-role": "worker",
				"machine.openshift.io/cluster-api-machine-type": fmt.Sprintf("non-serving-%s", zone),
				ControlPlaneNodeLabel:                           "true",
			}
			// The labels of the spec.template.spec.metadata will be set on nodes
			// created from this machineset. Please note that this is different than
			// the previous set of labels.
			ms.Spec.Template.Spec.ObjectMeta.Labels = map[string]string{
				ControlPlaneNodeLabel: "true",
			}
			ms.Spec.Template.Spec.Taints = []corev1.Taint{
				{
					Key:    scheduleraws.ControlPlaneTaint,
					Value:  "true",
					Effect: corev1.TaintEffectNoSchedule,
				},
			}
			// The provider spec can be exactly the same as the original worker machineset
			ms.Spec.Template.Spec.ProviderSpec.Value = workerMS.Spec.Template.Spec.ProviderSpec.Value.DeepCopy()
			return nil
		}); err != nil {
			return fmt.Errorf("failed to apply common machineset %s: %w", ms.Name, err)
		}

		ma := &autoscalingv1beta1.MachineAutoscaler{}
		ma.Name = ms.Name
		ma.Namespace = ms.Namespace
		if _, err := controllerutil.CreateOrUpdate(ctx, updateClient, ma, func() error {
			ma.Spec.MaxReplicas = 20
			ma.Spec.MinReplicas = 0
			ma.Spec.ScaleTargetRef.APIVersion = "machine.openshift.io/v1beta1"
			ma.Spec.ScaleTargetRef.Kind = "MachineSet"
			ma.Spec.ScaleTargetRef.Name = ms.Name
			return nil
		}); err != nil {
			return fmt.Errorf("failed to apply machine autoscaler %s: %w", ma.Name, err)
		}
	}

	// Pick the first 2 zones for request serving machinesets, sorted for consistency
	reqServingZones := make([]string, 0, len(workerMSByZone))
	for zone := range workerMSByZone {
		reqServingZones = append(reqServingZones, zone)
	}
	slices.Sort(reqServingZones)
	reqServingZones = reqServingZones[:2]

	// Create the request serving machine pairs
	for _, zone := range reqServingZones {
		workerMS := workerMSByZone[zone] // Select the template machineset for the zone
		for _, size := range machineSizes {
			for pairNumber := range requestServingPairCount {
				ms := &machinev1beta1.MachineSet{}
				ms.Name = fmt.Sprintf("%s-rs%02d-%s-%s", infraID, pairNumber, zone, size)
				ms.Namespace = workerMS.Namespace
				if _, err := controllerutil.CreateOrUpdate(ctx, updateClient, ms, func() error {
					clusterName := workerMS.Labels["machine.openshift.io/cluster-api-cluster"]
					ms.Labels = map[string]string{
						"machine.openshift.io/cluster-api-cluster": clusterName,
					}
					if ms.ObjectMeta.ResourceVersion == "" {
						// Only set the replicas to 0 if the machineset is new
						ms.Spec.Replicas = ptr.To(int32(0))
					}
					ms.Spec.Selector.MatchLabels = map[string]string{
						machinev1beta1.MachineClusterIDLabel:          clusterName,
						"machine.openshift.io/cluster-api-machineset": ms.Name,
					}
					ms.Spec.Template.ObjectMeta.Labels = map[string]string{
						machinev1beta1.MachineClusterIDLabel:            clusterName,
						"machine.openshift.io/cluster-api-machineset":   ms.Name,
						"machine.openshift.io/cluster-api-machine-role": "worker",
						"machine.openshift.io/cluster-api-machine-type": "worker",
						ControlPlaneNodeLabel:                           "true",
						hyperv1.RequestServingComponentLabel:            "true",
					}
					ms.Spec.Template.Spec.ObjectMeta.Labels = map[string]string{
						ControlPlaneNodeLabel:                        "true",
						hyperv1.RequestServingComponentLabel:         "true",
						hyperv1.NodeSizeLabel:                        size,
						schedulerutil.GoMemLimitLabel:                goMemLimit[size],
						scheduleraws.OSDFleetManagerPairedNodesLabel: fmt.Sprintf("pair-%d", pairNumber),
					}
					ms.Spec.Template.Spec.Taints = []corev1.Taint{
						{
							Key:    scheduleraws.ControlPlaneTaint,
							Value:  "true",
							Effect: corev1.TaintEffectNoSchedule,
						},
						{
							Key:    scheduleraws.ControlPlaneServingComponentTaint,
							Value:  "true",
							Effect: corev1.TaintEffectNoSchedule,
						},
					}
					providerSpec := workerMS.Spec.Template.Spec.ProviderSpec.Value.DeepCopy()
					awsProviderSpec := &machinev1beta1.AWSMachineProviderConfig{}
					if err := json.Unmarshal(providerSpec.Raw, awsProviderSpec); err != nil {
						return fmt.Errorf("failed to unmarshal the machineset provider spec: %w", err)
					}
					awsProviderSpec.InstanceType = instanceTypes[size]
					var err error
					providerSpec.Raw, err = json.Marshal(awsProviderSpec)
					if err != nil {
						return fmt.Errorf("failed to marshal the machineset provider spec: %w", err)
					}
					ms.Spec.Template.Spec.ProviderSpec.Value = providerSpec
					return nil
				}); err != nil {
					return fmt.Errorf("failed to apply request serving machineset: %s: %w", ms.Name, err)
				}

				ma := &autoscalingv1beta1.MachineAutoscaler{}
				ma.Name = ms.Name
				ma.Namespace = ms.Namespace
				if _, err := controllerutil.CreateOrUpdate(ctx, updateClient, ma, func() error {
					ma.Spec.MaxReplicas = 1
					ma.Spec.MinReplicas = 0
					ma.Spec.ScaleTargetRef.APIVersion = "machine.openshift.io/v1beta1"
					ma.Spec.ScaleTargetRef.Kind = "MachineSet"
					ma.Spec.ScaleTargetRef.Name = ms.Name
					return nil
				}); err != nil {
					return fmt.Errorf("failed to apply machine autoscaler %s: %w", ma.Name, err)
				}
			}
		}
	}
	if dryRunOpts != nil {
		{
			outputFile := fmt.Sprintf("%s/machine-sets.yaml", dryRunOpts.Dir)
			machineSetList := &machinev1beta1.MachineSetList{}
			if err := updateClient.List(ctx, machineSetList, crclient.InNamespace("openshift-machine-api")); err != nil {
				return fmt.Errorf("failed to list machinesets: %w", err)
			}
			yaml, err := util.SerializeResource(machineSetList, api.Scheme)
			if err != nil {
				return fmt.Errorf("failed to serialize cluster sizing configuration resource: %w", err)
			}
			if err := os.WriteFile(outputFile, []byte(yaml), 0644); err != nil {
				return fmt.Errorf("failed to write machine sets to file: %w", err)
			}
		}
		{
			outputFile := fmt.Sprintf("%s/machine-autoscalers.yaml", dryRunOpts.Dir)
			machineAutoscalerList := &autoscalingv1beta1.MachineAutoscalerList{}
			if err := updateClient.List(ctx, machineAutoscalerList, crclient.InNamespace("openshift-machine-api")); err != nil {
				return fmt.Errorf("failed to list machine autoscalers: %w", err)
			}
			yaml, err := util.SerializeResource(machineAutoscalerList, api.Scheme)
			if err != nil {
				return fmt.Errorf("failed to serialize machine autoscalers: %w", err)
			}
			if err := os.WriteFile(outputFile, []byte(yaml), 0644); err != nil {
				return fmt.Errorf("failed to write machine autoscalers to file: %w", err)
			}
		}
	}
	return nil
}

func checkStandaloneCluster(ctx context.Context, client crclient.Client) error {
	infra, err := getClusterInfrastructure(ctx, client)
	if err != nil {
		return err
	}
	if infra.Status.ControlPlaneTopology != configv1.HighlyAvailableTopologyMode {
		return fmt.Errorf("management cluster must be a standalone highly available cluster, unexpected control plane topology: %s", infra.Status.ControlPlaneTopology)
	}
	return nil
}

func getClusterInfrastructure(ctx context.Context, client crclient.Client) (*configv1.Infrastructure, error) {
	infra := &configv1.Infrastructure{}
	infra.Name = "cluster"
	if err := client.Get(ctx, crclient.ObjectKeyFromObject(infra), infra); err != nil {
		return nil, fmt.Errorf("failed to get infrastructure resource: %w", err)
	}
	return infra, nil
}

func ConfigureClusterSizingConfiguration(ctx context.Context, dryRunOpts *DryRunOptions) error {
	client, err := e2eutil.GetClient()
	if err != nil {
		return fmt.Errorf("failed to get cluster client: %w", err)
	}
	csc := &schedulingv1alpha1.ClusterSizingConfiguration{}
	csc.Name = "cluster"
	updateClient := client
	if dryRunOpts != nil {
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(csc), csc); err != nil {
			// Defaulting to default cluster sizing configuration
			csc.Spec = hostedclustersizing.DefaultSizingConfig().Spec
		}
		updateClient = e2eutil.GetFakeClient(csc)
	}

	if _, err := controllerutil.CreateOrUpdate(ctx, updateClient, csc, func() error {
		reconcileClusterSizingConfiguration(csc)
		return nil
	}); err != nil {
		return fmt.Errorf("failed to update cluster sizing configuration: %w", err)
	}
	if dryRunOpts != nil {
		outputFile := fmt.Sprintf("%s/cluster-sizing-configuration.yaml", dryRunOpts.Dir)
		if err := updateClient.Get(ctx, crclient.ObjectKeyFromObject(csc), csc); err != nil {
			return fmt.Errorf("failed to get cluster sizing configuration: %w", err)
		}
		yaml, err := util.SerializeResource(csc, api.Scheme)
		if err != nil {
			return fmt.Errorf("failed to serialize cluster sizing configuration resource: %w", err)
		}
		if err := os.WriteFile(outputFile, []byte(yaml), 0644); err != nil {
			return fmt.Errorf("failed to write cluster sizing configuration to file: %w", err)
		}

	}
	return nil
}

func mustQuantity(s string) *resource.Quantity {
	q, err := resource.ParseQuantity(s)
	if err != nil {
		panic(err)
	}
	return &q
}

func reconcileClusterSizingConfiguration(csc *schedulingv1alpha1.ClusterSizingConfiguration) {
	csc.Spec = schedulingv1alpha1.ClusterSizingConfigurationSpec{
		Concurrency: schedulingv1alpha1.ConcurrencyConfiguration{
			Limit: 2,
			SlidingWindow: metav1.Duration{
				Duration: 5 * time.Minute,
			},
		},
		NonRequestServingNodesBufferPerZone: mustQuantity("0.33"),
		Sizes: []schedulingv1alpha1.SizeConfiguration{
			{
				Name: "small",
				Criteria: schedulingv1alpha1.NodeCountCriteria{
					From: 0,
					To:   ptr.To(uint32(2)),
				},
				Effects: &schedulingv1alpha1.Effects{
					MaximumMutatingRequestsInflight: ptr.To(50),
					MaximumRequestsInflight:         ptr.To(100),
					ResourceRequests: []schedulingv1alpha1.ResourceRequest{
						{
							ContainerName:  "cluster-autoscaler",
							DeploymentName: "cluster-autoscaler",
							Memory:         mustQuantity("2.75Gi"),
						},
						{
							ContainerName:  "kube-controller-manager",
							DeploymentName: "kube-controller-manager",
							Memory:         mustQuantity("2.5Gi"),
						},
						{
							ContainerName:  "cluster-policy-controller",
							DeploymentName: "cluster-policy-controller",
							Memory:         mustQuantity("2Gi"),
						},
						{
							ContainerName:  "openshift-apiserver",
							DeploymentName: "openshift-apiserver",
							Memory:         mustQuantity("1.5Gi"),
						},
						{
							ContainerName:  "openshift-controller-manager",
							DeploymentName: "openshift-controller-manager",
							Memory:         mustQuantity("1.5Gi"),
						},
						{
							ContainerName:  "kube-scheduler",
							DeploymentName: "kube-scheduler",
							Memory:         mustQuantity("1Gi"),
						},
						/*
							TODO: Uncomment this once we're able to set resource requests for storage pods
							{
								ContainerName:  "csi-resizer",
								DeploymentName: "aws-ebs-csi-driver-controller",
								Memory:         mustQuantity(".75Gi"),
							},
						*/
						{
							ContainerName:  "etcd",
							DeploymentName: "etcd",
							Memory:         mustQuantity(".7Gi"),
						},
						/*
								TODO: Uncomment this once we're able to set resource requests for networking pods
							{
								ContainerName:  "multus-admission-controller",
								DeploymentName: "multus-admission-controller",
								Memory:         mustQuantity(".5Gi"),
							},
						*/
					},
				},
				Management: &schedulingv1alpha1.Management{
					NonRequestServingNodesPerZone: mustQuantity("0.5"),
					Placeholders:                  2,
				},
			},
			{
				Name: "medium",
				Criteria: schedulingv1alpha1.NodeCountCriteria{
					From: 3,
					To:   ptr.To(uint32(3)),
				},
				Effects: &schedulingv1alpha1.Effects{
					MaximumMutatingRequestsInflight: ptr.To(100),
					MaximumRequestsInflight:         ptr.To(200),
					ResourceRequests: []schedulingv1alpha1.ResourceRequest{
						{
							ContainerName:  "cluster-autoscaler",
							DeploymentName: "cluster-autoscaler",
							Memory:         mustQuantity("4Gi"),
						},
						{
							ContainerName:  "kube-controller-manager",
							DeploymentName: "kube-controller-manager",
							Memory:         mustQuantity("3.7Gi"),
						},
						{
							ContainerName:  "cluster-policy-controller",
							DeploymentName: "cluster-policy-controller",
							Memory:         mustQuantity("2.5Gi"),
						},
						{
							ContainerName:  "openshift-apiserver",
							DeploymentName: "openshift-apiserver",
							Memory:         mustQuantity("2Gi"),
						},
						{
							ContainerName:  "openshift-controller-manager",
							DeploymentName: "openshift-controller-manager",
							Memory:         mustQuantity("2Gi"),
						},
						{
							ContainerName:  "kube-scheduler",
							DeploymentName: "kube-scheduler",
							Memory:         mustQuantity("1.3Gi"),
						},
						/*
								TODO: Uncomment this once we're able to set resource requests for storage pods
							{
								ContainerName:  "csi-resizer",
								DeploymentName: "aws-ebs-csi-driver-controller",
								Memory:         mustQuantity("1.1Gi"),
							},
						*/
						{
							ContainerName:  "etcd",
							DeploymentName: "etcd",
							Memory:         mustQuantity("1Gi"),
						},
						/*
								TODO: Uncomment this once we're able to set resource requests for networking pods
							{
								ContainerName:  "multus-admission-controller",
								DeploymentName: "multus-admission-controller",
								Memory:         mustQuantity(".75Gi"),
							},
							{
								ContainerName:  "ovnkube-control-plane",
								DeploymentName: "ovnkube-control-plane",
								Memory:         mustQuantity(".75Gi"),
							},
						*/
					},
				},
				Management: &schedulingv1alpha1.Management{
					NonRequestServingNodesPerZone: mustQuantity("1"),
				},
			},
			{
				Name: "large",
				Criteria: schedulingv1alpha1.NodeCountCriteria{
					From: 4,
				},
				Effects: &schedulingv1alpha1.Effects{
					MaximumMutatingRequestsInflight: ptr.To(200),
					MaximumRequestsInflight:         ptr.To(400),
					ResourceRequests: []schedulingv1alpha1.ResourceRequest{
						{
							ContainerName:  "cluster-autoscaler",
							DeploymentName: "cluster-autoscaler",
							Memory:         mustQuantity("5.3Gi"),
						},
						{
							ContainerName:  "kube-controller-manager",
							DeploymentName: "kube-controller-manager",
							Memory:         mustQuantity("5.3Gi"),
						},
						{
							ContainerName:  "cluster-policy-controller",
							DeploymentName: "cluster-policy-controller",
							Memory:         mustQuantity("4Gi"),
						},
						{
							ContainerName:  "openshift-apiserver",
							DeploymentName: "openshift-apiserver",
							Memory:         mustQuantity("3.3Gi"),
						},
						{
							ContainerName:  "openshift-controller-manager",
							DeploymentName: "openshift-controller-manager",
							Memory:         mustQuantity("2.75Gi"),
						},
						{
							ContainerName:  "kube-scheduler",
							DeploymentName: "kube-scheduler",
							Memory:         mustQuantity("1.75Gi"),
						},
						/*
								TODO: Uncomment this once we're able to set resource requests for storage pods
							{
								ContainerName:  "csi-resizer",
								DeploymentName: "aws-ebs-csi-driver-controller",
								Memory:         mustQuantity("1.5Gi"),
							},
						*/
						{
							ContainerName:  "etcd",
							DeploymentName: "etcd",
							Memory:         mustQuantity("1.25Gi"),
						},
						/*
								TODO: Uncomment this once we're able to set resource requests for networking pods
							{
								ContainerName:  "multus-admission-controller",
								DeploymentName: "multus-admission-controller",
								Memory:         mustQuantity("1Gi"),
							},
							{
								ContainerName:  "ovnkube-control-plane",
								DeploymentName: "ovnkube-control-plane",
								Memory:         mustQuantity("1Gi"),
							},
						*/
						{
							ContainerName:  "route-controller-manager",
							DeploymentName: "openshift-route-controller-manager",
							Memory:         mustQuantity(".6Gi"),
						},
					},
				},
				Management: &schedulingv1alpha1.Management{
					NonRequestServingNodesPerZone: mustQuantity("1"),
				},
			},
		},
		TransitionDelay: schedulingv1alpha1.TransitionDelayConfiguration{
			Decrease: metav1.Duration{Duration: 5 * time.Minute},
			Increase: metav1.Duration{Duration: 0 * time.Second},
		},
	}
}
