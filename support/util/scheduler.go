package util

import (
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	schedulingv1alpha1 "github.com/openshift/hypershift/api/scheduling/v1alpha1"

	corev1 "k8s.io/api/core/v1"
)

const (
	GoMemLimitLabel = "hypershift.openshift.io/kas-go-mem-limit"
	LBSubnetsLabel  = "hypershift.openshift.io/request-serving-subnets"
)

func UpdateHostedCluster(hc *hyperv1.HostedCluster, size string, config *schedulingv1alpha1.ClusterSizingConfiguration, nodes []corev1.Node) (*hyperv1.HostedCluster, error) {
	hc.Annotations[hyperv1.HostedClusterScheduledAnnotation] = "true"
	sizeConfig := SizeConfiguration(config, size)
	if sizeConfig == nil {
		return nil, fmt.Errorf("could not find size configuration for size %s", size)
	}

	goMemLimit := ""
	if sizeConfig.Effects != nil && sizeConfig.Effects.KASGoMemLimit != nil {
		goMemLimit = sizeConfig.Effects.KASGoMemLimit.String()
	}

	// For ROSA try and get the goMem limit from the nodes
	if nodes != nil {
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

	//For ROSA, get the subnets from the nodes and set the annotation
	if nodes != nil {
		lbSubnets := getLBSubnetsFromNodes(nodes)
		if lbSubnets != "" {
			hc.Annotations[hyperv1.AWSLoadBalancerSubnetsAnnotation] = lbSubnets
		}
	}

	hc.Annotations[hyperv1.RequestServingNodeAdditionalSelectorAnnotation] = fmt.Sprintf("%s=%s", hyperv1.NodeSizeLabel, size)

	return hc, nil
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
