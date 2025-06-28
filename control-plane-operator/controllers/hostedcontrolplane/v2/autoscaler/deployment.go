package autoscaler

import (
	"errors"
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/go-logr/logr"
)

type AutoscalerArg string

const kubeconfigVolumeName string = "kubeconfig"

// Constants for cli args
const (
	BalancingIgnoreLabelArg          AutoscalerArg = "--balancing-ignore-label"
	ExpanderArg                      AutoscalerArg = "--expander"
	ExpendablePodsPriorityCutoffArg  AutoscalerArg = "--expendable-pods-priority-cutoff"
	MaxNodesTotalArg                 AutoscalerArg = "--max-nodes-total"
	MaxGracefulTerminationSecArg     AutoscalerArg = "--max-graceful-termination-sec"
	MaxNodeProvisionTimeArg          AutoscalerArg = "--max-node-provision-time"
	ScaleDownEnabledArg              AutoscalerArg = "--scale-down-enabled"
	ScaleDownDelayAfterAddArg        AutoscalerArg = "--scale-down-delay-after-add"
	ScaleDownDelayAfterDeleteArg     AutoscalerArg = "--scale-down-delay-after-delete"
	ScaleDownDelayAfterFailureArg    AutoscalerArg = "--scale-down-delay-after-failure"
	ScaleDownUnneededTimeArg         AutoscalerArg = "--scale-down-unneeded-time"
	ScaleDownUtilizationThresholdArg AutoscalerArg = "--scale-down-utilization-threshold"
	MaxFreeDifferenceRatioArg        AutoscalerArg = "--max-free-difference-ratio"
)

// Constants for expander flags
const (
	leastWasteFlag string = "least-waste"
	priorityFlag   string = "priority"
	randomFlag     string = "random"
)

// AWS cloud provider ignore labels for the autoscaler.
const (
	// AwsIgnoredLabelEksctlInstanceId  is a label used by eksctl to identify instances.
	AwsIgnoredLabelEksctlInstanceId = "alpha.eksctl.io/instance-id"

	// AwsIgnoredLabelEksctlNodegroupName is a label used by eksctl to identify "node group" names.
	AwsIgnoredLabelEksctlNodegroupName = "alpha.eksctl.io/nodegroup-name"

	// AwsIgnoredLabelEksNodegroup is a label used by eks to identify "node group".
	AwsIgnoredLabelEksNodegroup = "eks.amazonaws.com/nodegroup"

	// AwsIgnoredLabelK8sEniconfig is a label used by the AWS CNI for custom networking.
	AwsIgnoredLabelK8sEniconfig = "k8s.amazonaws.com/eniConfig"

	// AwsIgnoredLabelLifecycle is a label used by the AWS for spot.
	AwsIgnoredLabelLifecycle = "lifecycle"

	// AwsIgnoredLabelEbsCsiZone is a label used by the AWS EBS CSI driver as a target for Persistent Volume Node Affinity.
	AwsIgnoredLabelEbsCsiZone = "topology.ebs.csi.aws.com/zone"

	// AwsIgnoredLabelZoneID is a label used for the AWS-specific zone identifier, see https://github.com/kubernetes/cloud-provider-aws/issues/300 for a more detailed explanation of its use.
	AwsIgnoredLabelZoneID = "topology.k8s.aws/zone-id"
)

// Azure cloud provider ignore labels for the autoscaler.
const (
	// AzureDiskTopologyKey is the topology key of Azure Disk CSI driver.
	AzureDiskTopologyKey = "topology.disk.csi.azure.com/zone"

	// AzureNodepoolLegacyLabel is a label specifying which Azure node pool a particular node belongs to.
	AzureNodepoolLegacyLabel = "agentpool"

	// AzureNodepoolLabel is an AKS label specifying which nodepool a particular node belongs to.
	AzureNodepoolLabel = "kubernetes.azure.com/agentpool"
)

// IBM cloud provider ignore labels for the autoscaler.
const (
	IBMCloudWorkerIdLabel = "ibm-cloud.kubernetes.io/worker-id"
)

func (a AutoscalerArg) String() string {
	return string(a)
}

func (a AutoscalerArg) Value(v interface{}) string {
	return fmt.Sprintf("%s=%v", a.String(), v)
}

func autoscalerArgs(options *hyperv1.ClusterAutoscaling, platformType hyperv1.PlatformType, log logr.Logger) []string {
	args := []string{}
	if options.ScaleDown != nil {
		args = append(args, ScaleDownArgs(options.ScaleDown)...)
	}

	if options.MaxNodesTotal != nil {
		args = append(args, MaxNodesTotalArg.Value(*options.MaxNodesTotal))
	}

	if options.MaxPodGracePeriod != nil {
		args = append(args, MaxGracefulTerminationSecArg.Value(*options.MaxPodGracePeriod))
	}

	if options.MaxNodeProvisionTime != "" {
		args = append(args, MaxNodeProvisionTimeArg.Value(options.MaxNodeProvisionTime))
	}

	if options.PodPriorityThreshold != nil {
		args = append(args, ExpendablePodsPriorityCutoffArg.Value(*options.PodPriorityThreshold))
	}

	for _, ignoredLabel := range options.BalancingIgnoredLabels {
		args = append(args, BalancingIgnoreLabelArg.Value(ignoredLabel))
	}

	if options.MaxFreeDifferenceRatioPercent != nil {
		// Convert percentage to decimal (e.g., 10% -> 0.1)
		ratio := float64(*options.MaxFreeDifferenceRatioPercent) / 100.0
		args = append(args, MaxFreeDifferenceRatioArg.Value(fmt.Sprintf("%.2f", ratio)))
	}

	if len(options.Expanders) > 0 {
		expanders := make([]string, 0)
		for _, v := range options.Expanders {
			switch v {
			case hyperv1.LeastWasteExpander:
				expanders = append(expanders, leastWasteFlag)
			case hyperv1.PriorityExpander:
				expanders = append(expanders, priorityFlag)
			case hyperv1.RandomExpander:
				expanders = append(expanders, randomFlag)
			default:
				// this shouldn't happen since we have validation on the API types, but just in case
				log.Error(errors.New("unknown priority expander"), "Unexpected Cluster Autoscaler priority expander", v)
				continue
			}
		}
		args = append(args, ExpanderArg.Value(strings.Join(expanders, ",")))
	}

	// Since we hardcode "balance-similar-node-groups" to true in the deployment yaml
	// Append basic ignore labels for a specific cloud provider.
	args = appendBasicIgnoreLabels(args, platformType)

	return args
}

func ScaleDownArgs(sd *hyperv1.ScaleDownConfig) []string {
	if sd.Enabled == "Disabled" {
		return []string{ScaleDownEnabledArg.Value(false)}
	}

	args := []string{
		ScaleDownEnabledArg.Value(true),
	}

	if sd.DelayAfterAddSeconds != nil {
		args = append(args, ScaleDownDelayAfterAddArg.Value(fmt.Sprintf("%ds", *sd.DelayAfterAddSeconds)))
	}

	if sd.DelayAfterDelete != nil {
		args = append(args, ScaleDownDelayAfterDeleteArg.Value(*sd.DelayAfterDelete))
	}

	if sd.DelayAfterFailure != nil {
		args = append(args, ScaleDownDelayAfterFailureArg.Value(*sd.DelayAfterFailure))
	}

	if sd.UnneededDurationSeconds != nil {
		args = append(args, ScaleDownUnneededTimeArg.Value(fmt.Sprintf("%ds", *sd.UnneededDurationSeconds)))
	}

	if sd.UtilizationThreshold != nil {
		args = append(args, ScaleDownUtilizationThresholdArg.Value(*sd.UtilizationThreshold))
	}

	return args
}

// AppendBasicIgnoreLabels appends ignore labels for specific cloud provider to the arguments
// so the autoscaler can use these labels without the user having to input them manually.
func appendBasicIgnoreLabels(args []string, platformType hyperv1.PlatformType) []string {
	switch platformType {
	case hyperv1.AWSPlatform:
		args = append(args, BalancingIgnoreLabelArg.Value(AwsIgnoredLabelEbsCsiZone),
			BalancingIgnoreLabelArg.Value(AwsIgnoredLabelLifecycle),
			BalancingIgnoreLabelArg.Value(AwsIgnoredLabelK8sEniconfig),
			BalancingIgnoreLabelArg.Value(AwsIgnoredLabelEksNodegroup),
			BalancingIgnoreLabelArg.Value(AwsIgnoredLabelEksctlNodegroupName),
			BalancingIgnoreLabelArg.Value(AwsIgnoredLabelEksctlInstanceId),
			BalancingIgnoreLabelArg.Value(AwsIgnoredLabelZoneID))
	case hyperv1.AzurePlatform:
		args = append(args, BalancingIgnoreLabelArg.Value(AzureDiskTopologyKey),
			BalancingIgnoreLabelArg.Value(AzureNodepoolLegacyLabel),
			BalancingIgnoreLabelArg.Value(AzureNodepoolLabel),
		)
	case hyperv1.IBMCloudPlatform:
		args = append(args, BalancingIgnoreLabelArg.Value(IBMCloudWorkerIdLabel))
	}

	return args
}

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	hcp := cpContext.HCP

	util.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		if image, ok := cpContext.HCP.Annotations[hyperv1.ClusterAutoscalerImage]; ok {
			c.Image = image
		}

		// TODO if the options for the cluster autoscaler continues to grow, we should take inspiration
		// from the cluster-autoscaler-operator and create some utility functions for these assignments.
		c.Args = append(c.Args, autoscalerArgs(&hcp.Spec.Autoscaling, hcp.Spec.Platform.Type, ctrl.LoggerFrom(cpContext))...)
	})

	util.UpdateVolume(kubeconfigVolumeName, deployment.Spec.Template.Spec.Volumes, func(v *corev1.Volume) {
		v.Secret.SecretName = manifests.KASServiceCAPIKubeconfigSecret(hcp.Namespace, hcp.Spec.InfraID).Name
	})

	deployment.Spec.Replicas = ptr.To[int32](1)
	if _, exists := hcp.Annotations[hyperv1.DisableClusterAutoscalerAnnotation]; exists {
		deployment.Spec.Replicas = ptr.To[int32](0)
	}

	return nil
}
