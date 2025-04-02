package util

import (
	"context"
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	schedulingv1alpha1 "github.com/openshift/hypershift/api/scheduling/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	GoMemLimitLabel = "hypershift.openshift.io/kas-go-mem-limit"
	LBSubnetsLabel  = "hypershift.openshift.io/request-serving-subnets"
)

// setHostedClusterSchedulingAnnotations sets the scheduling annotations on the hosted cluster based on the cluster sizing configuration.
// It will read the config for the specified size from the clustersizingconfiguration and set the appropriate annotationas on the hostedclust
// so that other controllers can leverage it to set the desired effects.
func setHostedClusterSchedulingAnnotations(hc *hyperv1.HostedCluster, size string, config *schedulingv1alpha1.ClusterSizingConfiguration, nodes []corev1.Node) (*hyperv1.HostedCluster, error) {
	if hc.Annotations != nil {
		hc.Annotations[hyperv1.HostedClusterScheduledAnnotation] = "true"
	} else {
		hc.Annotations = map[string]string{
			hyperv1.HostedClusterScheduledAnnotation: "true",
		}
	}
	sizeConfig := SizeConfiguration(config, size)
	if sizeConfig == nil {
		return nil, fmt.Errorf("could not find size configuration for size %s", size)
	}

	goMemLimit := ""
	if sizeConfig.Effects != nil && sizeConfig.Effects.KASGoMemLimit != nil {
		goMemLimit = ptr.Deref(sizeConfig.Effects.KASGoMemLimit, "")
	}

	// For AWS try and get the goMem limit from the nodes
	if len(nodes) > 0 {
		if limit := getGoMemLimitLabelFromNodes(nodes); limit != "" {
			goMemLimit = limit
		}
	}

	if goMemLimit != "" {
		hc.Annotations[hyperv1.KubeAPIServerGOMemoryLimitAnnotation] = goMemLimit
	}

	if sizeConfig.Effects != nil && sizeConfig.Effects.ControlPlanePriorityClassName != nil {
		hc.Annotations[hyperv1.ControlPlanePriorityClass] = *sizeConfig.Effects.ControlPlanePriorityClassName
	}
	if sizeConfig.Effects != nil && sizeConfig.Effects.EtcdPriorityClassName != nil {
		hc.Annotations[hyperv1.EtcdPriorityClass] = *sizeConfig.Effects.EtcdPriorityClassName
	}
	if sizeConfig.Effects != nil && sizeConfig.Effects.APICriticalPriorityClassName != nil {
		hc.Annotations[hyperv1.APICriticalPriorityClass] = *sizeConfig.Effects.APICriticalPriorityClassName
	}
	if sizeConfig.Effects != nil && sizeConfig.Effects.MachineHealthCheckTimeout != nil {
		hc.Annotations[hyperv1.MachineHealthCheckTimeoutAnnotation] = sizeConfig.Effects.MachineHealthCheckTimeout.Duration.String()
	} else {
		// If mhc timeout is configured for any size in the config, remove the annotation
		// to fallback to the default
		if configHasMHCTimeout(config) {
			delete(hc.Annotations, hyperv1.MachineHealthCheckTimeoutAnnotation)
		}
	}
	if sizeConfig.Effects != nil && sizeConfig.Effects.MaximumRequestsInflight != nil {
		hc.Annotations[hyperv1.KubeAPIServerMaximumRequestsInFlight] = fmt.Sprint(*sizeConfig.Effects.MaximumRequestsInflight)
	}
	if sizeConfig.Effects != nil && sizeConfig.Effects.MaximumMutatingRequestsInflight != nil {
		hc.Annotations[hyperv1.KubeAPIServerMaximumMutatingRequestsInFlight] = fmt.Sprint(*sizeConfig.Effects.MaximumMutatingRequestsInflight)
	}

	var resourceRequestAnnotations map[string]string
	if sizeConfig.Effects != nil {
		resourceRequestAnnotations = ResourceRequestsToOverrideAnnotations(sizeConfig.Effects.ResourceRequests)
	}
	for k, v := range resourceRequestAnnotations {
		hc.Annotations[k] = v
	}

	//For AWS, get the subnets from the nodes and set the annotation
	if len(nodes) > 0 {
		lbSubnets := getLBSubnetsFromNodes(nodes)
		if lbSubnets != "" {
			hc.Annotations[hyperv1.AWSLoadBalancerSubnetsAnnotation] = lbSubnets
		}

		hc.Annotations[hyperv1.RequestServingNodeAdditionalSelectorAnnotation] = fmt.Sprintf("%s=%s", hyperv1.NodeSizeLabel, size)

	}

	return hc, nil
}

// UpdateHostedCluster updates the hosted cluster with the scheduling annotations based on the cluster sizing configuration.
func UpdateHostedCluster(ctx context.Context, c client.Client, hc *hyperv1.HostedCluster, size string, config *schedulingv1alpha1.ClusterSizingConfiguration, nodes []corev1.Node) error {
	original := hc.DeepCopy()

	hc, err := setHostedClusterSchedulingAnnotations(hc, size, config, nodes)
	if err != nil {
		return fmt.Errorf("failed to update hostedcluster: %w", err)
	}

	if !equality.Semantic.DeepEqual(hc, original) {
		if err := c.Patch(ctx, hc, client.MergeFrom(original)); err != nil {
			return fmt.Errorf("failed to update hostedcluster: %w", err)
		}
	}
	return nil
}

func getLBSubnetsFromNodes(nodes []corev1.Node) string {
	lbSubnets := ""
	for _, node := range nodes {
		if node.Labels[LBSubnetsLabel] != "" {
			lbSubnets = node.Labels[LBSubnetsLabel]
			break
		}
	}
	if lbSubnets != "" {
		// If subnets are separated by periods, replace them with commas
		lbSubnets = strings.ReplaceAll(lbSubnets, ".", ",")
		return lbSubnets
	}
	return ""
}

func getGoMemLimitLabelFromNodes(nodes []corev1.Node) string {
	for _, node := range nodes {
		if node.Labels[GoMemLimitLabel] != "" {
			return node.Labels[GoMemLimitLabel]
		}
	}
	return ""
}

func SizeConfiguration(config *schedulingv1alpha1.ClusterSizingConfiguration, size string) *schedulingv1alpha1.SizeConfiguration {
	for i := range config.Spec.Sizes {
		if config.Spec.Sizes[i].Name == size {
			return &config.Spec.Sizes[i]
		}
	}
	return nil
}

func configHasMHCTimeout(config *schedulingv1alpha1.ClusterSizingConfiguration) bool {
	for _, size := range config.Spec.Sizes {
		if size.Effects != nil && size.Effects.MachineHealthCheckTimeout != nil {
			return true
		}
	}
	return false
}

func ResourceRequestsToOverrideAnnotations(requests []schedulingv1alpha1.ResourceRequest) map[string]string {
	annotations := map[string]string{}
	for _, request := range requests {
		key := fmt.Sprintf("%s/%s.%s", hyperv1.ResourceRequestOverrideAnnotationPrefix, request.DeploymentName, request.ContainerName)
		var value string
		if request.Memory != nil {
			value = fmt.Sprintf("memory=%s", request.Memory.String())
		}
		if request.CPU != nil {
			if value != "" {
				value += ","
			}
			value += fmt.Sprintf("cpu=%s", request.CPU.String())
		}
		annotations[key] = value
	}
	return annotations
}
