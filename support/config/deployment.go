package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"
)

const (
	// ManagedByLabel can be used to filter deployments.
	ManagedByLabel = "hypershift.openshift.io/managed-by"

	// There are used by NodeAffinity to prefer/tolerate Nodes.
	controlPlaneLabelTolerationKey = "hypershift.openshift.io/control-plane"

	// colocationLabelKey is used by PodAffinity to prefer colocating pods that belong to the same hosted cluster.
	colocationLabelKey = "hypershift.openshift.io/hosted-control-plane"

	// Specific cluster weight for soft affinity rule to node.
	clusterNodeSchedulingAffinityWeight = 100

	// Generic control plane workload weight for soft affinity rule to node.
	controlPlaneNodeSchedulingAffinityWeight = clusterNodeSchedulingAffinityWeight / 2
)

type DeploymentConfig struct {
	Replicas                  int
	Scheduling                Scheduling
	AdditionalLabels          AdditionalLabels
	AdditionalAnnotations     AdditionalAnnotations
	SecurityContexts          SecurityContextSpec
	SetDefaultSecurityContext bool
	LivenessProbes            LivenessProbes
	ReadinessProbes           ReadinessProbes
	StartupProbes             StartupProbes
	Resources                 ResourcesSpec
	DebugDeployments          sets.String
	ResourceRequestOverrides  ResourceOverrides
	IsolateAsRequestServing   bool
	RevisionHistoryLimit      int

	AdditionalRequestServingNodeSelector map[string]string
}

func (c *DeploymentConfig) SetContainerResourcesIfPresent(container *corev1.Container) {
	resources := container.Resources
	if len(resources.Requests) > 0 || len(resources.Limits) > 0 {
		if c.Resources != nil {
			c.Resources[container.Name] = resources
		}
	}
}

func (c *DeploymentConfig) SetRestartAnnotation(objectMetadata metav1.ObjectMeta) {
	if _, ok := objectMetadata.Annotations[hyperv1.RestartDateAnnotation]; ok {
		if c.AdditionalAnnotations == nil {
			c.AdditionalAnnotations = make(AdditionalAnnotations)
		}
		c.AdditionalAnnotations[hyperv1.RestartDateAnnotation] = objectMetadata.Annotations[hyperv1.RestartDateAnnotation]
	}
}

func (c *DeploymentConfig) SetReleaseImageAnnotation(releaseImage string) {
	if c.AdditionalAnnotations == nil {
		c.AdditionalAnnotations = make(AdditionalAnnotations)
	}
	c.AdditionalAnnotations[hyperv1.ReleaseImageAnnotation] = releaseImage
}

func (c *DeploymentConfig) ApplyTo(deployment *appsv1.Deployment) {
	if c.DebugDeployments != nil && c.DebugDeployments.Has(deployment.Name) {
		deployment.Spec.Replicas = ptr.To[int32](0)
	} else {
		deployment.Spec.Replicas = ptr.To(int32(c.Replicas))
	}
	// there are three standard cases currently with hypershift: HA mode where there are 3 replicas spread across
	// zones, HA mode with 2 replicas, and then non ha with one replica. When only 3 zones are available you need
	// to be able to set maxUnavailable in order to progress the rollout. However, you do not want to set that in
	// the single replica case because it will result in downtime.
	if c.Replicas > 1 {
		maxSurge := intstr.FromInt(1)
		maxUnavailable := intstr.FromInt(0)
		if val, ok := c.AdditionalLabels[hyperv1.RequestServingComponentLabel]; ok && val == "true" {
			maxUnavailable = intstr.FromInt(1)
		}
		if c.Replicas > 2 {
			maxSurge = intstr.FromInt(0)
			maxUnavailable = intstr.FromInt(1)
		}
		if deployment.Spec.Strategy.RollingUpdate == nil {
			deployment.Spec.Strategy.RollingUpdate = &appsv1.RollingUpdateDeployment{}
			deployment.Spec.Strategy.Type = appsv1.RollingUpdateDeploymentStrategyType
		}
		deployment.Spec.Strategy.RollingUpdate.MaxSurge = &maxSurge
		deployment.Spec.Strategy.RollingUpdate.MaxUnavailable = &maxUnavailable
	}

	// set revision history limit
	deployment.Spec.RevisionHistoryLimit = ptr.To(int32(c.RevisionHistoryLimit))

	c.setDefaultSecurityContextForPod(&deployment.Spec.Template.Spec, false)

	// set managed-by label
	if deployment.Labels == nil {
		deployment.Labels = map[string]string{}
	}
	deployment.Labels[ManagedByLabel] = "control-plane-operator"

	// adding annotation for EmptyDir volume safe-eviction
	if deployment.Spec.Template.ObjectMeta.Annotations == nil {
		deployment.Spec.Template.ObjectMeta.Annotations = make(map[string]string)
	}

	localStorageVolumes := make([]string, 0)

	for _, volume := range deployment.Spec.Template.Spec.Volumes {
		if volume.EmptyDir != nil || volume.HostPath != nil {
			localStorageVolumes = append(localStorageVolumes, volume.Name)
		}
	}

	if len(localStorageVolumes) > 0 {
		annotationsVolumes := strings.Join(localStorageVolumes, ",")
		deployment.Spec.Template.ObjectMeta.Annotations[PodSafeToEvictLocalVolumesKey] = annotationsVolumes
	}

	c.Scheduling.ApplyTo(&deployment.Spec.Template.Spec)
	c.AdditionalLabels.ApplyTo(&deployment.Spec.Template.ObjectMeta)
	c.SecurityContexts.ApplyTo(&deployment.Spec.Template.Spec)
	c.LivenessProbes.ApplyTo(&deployment.Spec.Template.Spec)
	c.ReadinessProbes.ApplyTo(&deployment.Spec.Template.Spec)
	c.StartupProbes.ApplyTo(&deployment.Spec.Template.Spec)
	c.Resources.ApplyTo(&deployment.Spec.Template.Spec)
	c.ResourceRequestOverrides.ApplyRequestsTo(deployment.Name, &deployment.Spec.Template.Spec)
	c.AdditionalAnnotations.ApplyTo(&deployment.Spec.Template.ObjectMeta)
}

func (c *DeploymentConfig) ApplyToDaemonSet(daemonset *appsv1.DaemonSet) {
	c.setDefaultSecurityContextForPod(&daemonset.Spec.Template.Spec, false)

	// replicas is not used for DaemonSets
	c.Scheduling.ApplyTo(&daemonset.Spec.Template.Spec)
	c.AdditionalLabels.ApplyTo(&daemonset.Spec.Template.ObjectMeta)
	c.SecurityContexts.ApplyTo(&daemonset.Spec.Template.Spec)
	c.LivenessProbes.ApplyTo(&daemonset.Spec.Template.Spec)
	c.ReadinessProbes.ApplyTo(&daemonset.Spec.Template.Spec)
	c.StartupProbes.ApplyTo(&daemonset.Spec.Template.Spec)
	c.Resources.ApplyTo(&daemonset.Spec.Template.Spec)
	c.ResourceRequestOverrides.ApplyRequestsTo(daemonset.Name, &daemonset.Spec.Template.Spec)
	c.AdditionalAnnotations.ApplyTo(&daemonset.Spec.Template.ObjectMeta)
}

func (c *DeploymentConfig) ApplyToStatefulSet(sts *appsv1.StatefulSet) {
	c.setDefaultSecurityContextForPod(&sts.Spec.Template.Spec, true)

	sts.Spec.Replicas = ptr.To(int32(c.Replicas))
	c.Scheduling.ApplyTo(&sts.Spec.Template.Spec)
	c.AdditionalLabels.ApplyTo(&sts.Spec.Template.ObjectMeta)
	c.SecurityContexts.ApplyTo(&sts.Spec.Template.Spec)
	c.LivenessProbes.ApplyTo(&sts.Spec.Template.Spec)
	c.ReadinessProbes.ApplyTo(&sts.Spec.Template.Spec)
	c.StartupProbes.ApplyTo(&sts.Spec.Template.Spec)
	c.Resources.ApplyTo(&sts.Spec.Template.Spec)
	c.ResourceRequestOverrides.ApplyRequestsTo(sts.Name, &sts.Spec.Template.Spec)
	c.AdditionalAnnotations.ApplyTo(&sts.Spec.Template.ObjectMeta)
}

func (c *DeploymentConfig) ApplyToCronJob(cronJob *batchv1.CronJob) {
	c.setDefaultSecurityContextForPod(&cronJob.Spec.JobTemplate.Spec.Template.Spec, false)

	c.Scheduling.ApplyTo(&cronJob.Spec.JobTemplate.Spec.Template.Spec)
	c.SecurityContexts.ApplyTo(&cronJob.Spec.JobTemplate.Spec.Template.Spec)
	c.LivenessProbes.ApplyTo(&cronJob.Spec.JobTemplate.Spec.Template.Spec)
	c.ReadinessProbes.ApplyTo(&cronJob.Spec.JobTemplate.Spec.Template.Spec)
	c.StartupProbes.ApplyTo(&cronJob.Spec.JobTemplate.Spec.Template.Spec)
	c.Resources.ApplyTo(&cronJob.Spec.JobTemplate.Spec.Template.Spec)
	c.ResourceRequestOverrides.ApplyRequestsTo(cronJob.Name, &cronJob.Spec.JobTemplate.Spec.Template.Spec)
	c.AdditionalLabels.ApplyTo(&cronJob.Spec.JobTemplate.Spec.Template.ObjectMeta)
	c.AdditionalAnnotations.ApplyTo(&cronJob.Spec.JobTemplate.Spec.Template.ObjectMeta)
}

func (c *DeploymentConfig) ApplyToJob(job *batchv1.Job) {
	c.setDefaultSecurityContextForPod(&job.Spec.Template.Spec, false)

	c.Scheduling.ApplyTo(&job.Spec.Template.Spec)
	c.SecurityContexts.ApplyTo(&job.Spec.Template.Spec)
	c.LivenessProbes.ApplyTo(&job.Spec.Template.Spec)
	c.ReadinessProbes.ApplyTo(&job.Spec.Template.Spec)
	c.StartupProbes.ApplyTo(&job.Spec.Template.Spec)
	c.Resources.ApplyTo(&job.Spec.Template.Spec)
	c.ResourceRequestOverrides.ApplyRequestsTo(job.Name, &job.Spec.Template.Spec)
	c.AdditionalLabels.ApplyTo(&job.Spec.Template.ObjectMeta)
	c.AdditionalAnnotations.ApplyTo(&job.Spec.Template.ObjectMeta)
}

var (
	DefaultSecurityContextUIDEnvVar = "DEFAULT_SECURITY_CONTEXT_UID"
	DefaultSecurityContextUID       = int64(1001)
)

func getSecurityContextUID() (int64, error) {
	uidInput := os.Getenv(DefaultSecurityContextUIDEnvVar)
	if uidInput == "" {
		return DefaultSecurityContextUID, nil
	}

	uid, err := strconv.ParseInt(uidInput, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse %q: %w", DefaultSecurityContextUIDEnvVar, err)
	}
	return uid, nil
}

func (c *DeploymentConfig) setDefaultSecurityContextForPod(spec *corev1.PodSpec, statefulSet bool) {
	if c.SetDefaultSecurityContext {
		uid, err := getSecurityContextUID()
		if err != nil {
			uid = DefaultSecurityContextUID
		}

		if !statefulSet {
			spec.SecurityContext = &corev1.PodSecurityContext{
				RunAsUser: ptr.To[int64](uid),
			}
			return
		}

		// Only impact ARO 4.19. Respect existing SecurityContext value for anything else, e.g. IBM.
		if statefulSet && os.Getenv("MANAGED_SERVICE") == hyperv1.AroHCP {
			spec.SecurityContext = &corev1.PodSecurityContext{
				RunAsUser: ptr.To[int64](uid),
				FSGroup:   ptr.To[int64](uid),
			}
		}
	}
}

func clusterKey(hcp *hyperv1.HostedControlPlane) string {
	return hcp.Namespace
}

func colocationLabelValue(hcp *hyperv1.HostedControlPlane) string {
	return clusterKey(hcp)
}

// SetMultizoneSpread sets PodAntiAffinity with corev1.LabelTopologyZone as the topology key for a given set of labels.
// This is useful to e.g ensure pods are spread across availability zones.
// If required is true, the rule is set as RequiredDuringSchedulingIgnoredDuringExecution, otherwise it is set as
// PreferredDuringSchedulingIgnoredDuringExecution.
func (c *DeploymentConfig) SetMultizoneSpread(labels map[string]string, required bool) {
	if labels == nil {
		return
	}
	if c.Scheduling.Affinity == nil {
		c.Scheduling.Affinity = &corev1.Affinity{}
	}
	if c.Scheduling.Affinity.PodAntiAffinity == nil {
		c.Scheduling.Affinity.PodAntiAffinity = &corev1.PodAntiAffinity{}
	}
	if required {
		c.Scheduling.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution = append(c.Scheduling.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution,
			corev1.PodAffinityTerm{
				TopologyKey: corev1.LabelTopologyZone,
				LabelSelector: &metav1.LabelSelector{
					MatchLabels: labels,
				},
			},
		)
	} else {
		c.Scheduling.Affinity.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution = append(c.Scheduling.Affinity.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution,
			corev1.WeightedPodAffinityTerm{
				Weight: 100,
				PodAffinityTerm: corev1.PodAffinityTerm{
					TopologyKey: corev1.LabelTopologyZone,
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: labels,
					},
				},
			},
		)
	}
}

// setNodeSpread sets PodAntiAffinity with corev1.LabelHostname as the topology key for a given set of labels.
// This is useful to e.g ensure pods are spread across nodes.
func (c *DeploymentConfig) setNodeSpread(labels map[string]string) {
	if labels == nil {
		return
	}
	if c.Scheduling.Affinity == nil {
		c.Scheduling.Affinity = &corev1.Affinity{}
	}
	if c.Scheduling.Affinity.PodAntiAffinity == nil {
		c.Scheduling.Affinity.PodAntiAffinity = &corev1.PodAntiAffinity{}
	}
	c.Scheduling.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution = append(c.Scheduling.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution,
		corev1.PodAffinityTerm{
			TopologyKey: corev1.LabelHostname,
			LabelSelector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
		},
	)
}

// setColocation sets labels and PodAffinity rules for this deployment so that pods
// of the deployment will prefer to group with pods of the anchor deployment.
func (c *DeploymentConfig) setColocation(hcp *hyperv1.HostedControlPlane) {
	if c.Scheduling.Affinity == nil {
		c.Scheduling.Affinity = &corev1.Affinity{}
	}
	if c.Scheduling.Affinity.PodAffinity == nil {
		c.Scheduling.Affinity.PodAffinity = &corev1.PodAffinity{}
	}
	if c.AdditionalLabels == nil {
		c.AdditionalLabels = map[string]string{}
	}
	c.AdditionalLabels[colocationLabelKey] = colocationLabelValue(hcp)
	c.Scheduling.Affinity.PodAffinity.PreferredDuringSchedulingIgnoredDuringExecution = []corev1.WeightedPodAffinityTerm{
		{
			Weight: 100,
			PodAffinityTerm: corev1.PodAffinityTerm{
				LabelSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						colocationLabelKey: colocationLabelValue(hcp),
					},
				},
				TopologyKey: corev1.LabelHostname,
			},
		},
	}
}

// setAdditionalTolerations adds any custom tolerations configured on the hcp to the deployment config
func (c *DeploymentConfig) setAdditionalTolerations(hcp *hyperv1.HostedControlPlane) {
	if len(hcp.Spec.Tolerations) == 0 {
		return
	}

	c.Scheduling.Tolerations = append(c.Scheduling.Tolerations, hcp.Spec.Tolerations...)
}

// setControlPlaneIsolation configures tolerations and NodeAffinity rules to prefer Nodes with controlPlaneNodeLabel and clusterNodeLabel.
func (c *DeploymentConfig) setControlPlaneIsolation(hcp *hyperv1.HostedControlPlane) {
	c.Scheduling.Tolerations = []corev1.Toleration{
		{
			Key:      controlPlaneLabelTolerationKey,
			Operator: corev1.TolerationOpEqual,
			Value:    "true",
			Effect:   corev1.TaintEffectNoSchedule,
		},
		{
			Key:      hyperv1.HostedClusterLabel,
			Operator: corev1.TolerationOpEqual,
			Value:    clusterKey(hcp),
			Effect:   corev1.TaintEffectNoSchedule,
		},
	}
	if c.IsolateAsRequestServing {
		c.Scheduling.Tolerations = append(c.Scheduling.Tolerations, corev1.Toleration{
			Key:      hyperv1.RequestServingComponentLabel,
			Operator: corev1.TolerationOpEqual,
			Value:    "true",
			Effect:   corev1.TaintEffectNoSchedule,
		})
	}

	if c.Scheduling.Affinity == nil {
		c.Scheduling.Affinity = &corev1.Affinity{}
	}
	if c.Scheduling.Affinity.NodeAffinity == nil {
		c.Scheduling.Affinity.NodeAffinity = &corev1.NodeAffinity{}
	}
	c.Scheduling.Affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution = []corev1.PreferredSchedulingTerm{
		{
			Weight: controlPlaneNodeSchedulingAffinityWeight,
			Preference: corev1.NodeSelectorTerm{
				MatchExpressions: []corev1.NodeSelectorRequirement{
					{
						Key:      controlPlaneLabelTolerationKey,
						Operator: corev1.NodeSelectorOpIn,
						Values:   []string{"true"},
					},
				},
			},
		},
		{
			Weight: clusterNodeSchedulingAffinityWeight,
			Preference: corev1.NodeSelectorTerm{
				MatchExpressions: []corev1.NodeSelectorRequirement{
					{
						Key:      hyperv1.HostedClusterLabel,
						Operator: corev1.NodeSelectorOpIn,
						Values:   []string{clusterKey(hcp)},
					},
				},
			},
		},
	}

	if c.IsolateAsRequestServing {
		nodeSelectorRequirements := []corev1.NodeSelectorRequirement{
			{
				Key:      hyperv1.RequestServingComponentLabel,
				Operator: corev1.NodeSelectorOpIn,
				Values:   []string{"true"},
			},
			{
				Key:      hyperv1.HostedClusterLabel,
				Operator: corev1.NodeSelectorOpIn,
				Values:   []string{clusterKey(hcp)},
			},
		}
		for key, value := range c.AdditionalRequestServingNodeSelector {
			nodeSelectorRequirements = append(nodeSelectorRequirements, corev1.NodeSelectorRequirement{
				Key:      key,
				Operator: corev1.NodeSelectorOpIn,
				Values:   []string{value},
			})
		}
		c.Scheduling.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution = &corev1.NodeSelector{
			NodeSelectorTerms: []corev1.NodeSelectorTerm{
				{
					MatchExpressions: nodeSelectorRequirements,
				},
			},
		}
	}

}

// setNodeSelector sets a nodeSelector passed through the API.
// This is useful to e.g ensure control plane pods land in management cluster Infra Nodes.
func (c *DeploymentConfig) setNodeSelector(hcp *hyperv1.HostedControlPlane) {
	if hcp.Spec.NodeSelector == nil {
		return
	}
	c.Scheduling.NodeSelector = hcp.Spec.NodeSelector
}

func (c *DeploymentConfig) setLocation(hcp *hyperv1.HostedControlPlane, multiZoneSpreadLabels map[string]string) {
	c.setNodeSelector(hcp)
	c.setControlPlaneIsolation(hcp)
	c.setAdditionalTolerations(hcp)
	c.setColocation(hcp)
	// TODO (alberto): pass labels with deployment hash and set this unconditionally so we don't skew setup.
	if c.Replicas > 1 {
		var multiZoneRequired bool
		switch hcp.Spec.Platform.Type {
		// On OpenStack and Kubevirt we can't spread across zones in certain cases
		// so let's relax the requirement on those platforms.
		case hyperv1.OpenStackPlatform, hyperv1.KubevirtPlatform:
			multiZoneRequired = false
		default:
			multiZoneRequired = true
		}
		c.SetMultizoneSpread(multiZoneSpreadLabels, multiZoneRequired)
		c.setNodeSpread(multiZoneSpreadLabels)
	}
}

func (c *DeploymentConfig) setReplicas(availability hyperv1.AvailabilityPolicy) {
	switch availability {
	case hyperv1.HighlyAvailable:
		if c.IsolateAsRequestServing {
			c.Replicas = 2
		} else {
			c.Replicas = 3
		}
	default:
		c.Replicas = 1
	}
}

func DefaultReplicas(hcp *hyperv1.HostedControlPlane, isRequestServingComponent bool) int {
	isolateAsRequestServing := false
	if hcp.Annotations[hyperv1.TopologyAnnotation] == hyperv1.DedicatedRequestServingComponentsTopology {
		isolateAsRequestServing = isRequestServingComponent
	}

	switch hcp.Spec.ControllerAvailabilityPolicy {
	case hyperv1.HighlyAvailable:
		if isolateAsRequestServing {
			return 2
		}
		return 3
	default:
		return 1
	}
}

// SetRequestServingDefaults wraps the call to SetDefaults. It is meant to be invoked by request serving components so that their scheduling
// attributes can be modified accordingly.
func (c *DeploymentConfig) SetRequestServingDefaults(hcp *hyperv1.HostedControlPlane, multiZoneSpreadLabels map[string]string, replicas *int) {
	if hcp.Annotations[hyperv1.TopologyAnnotation] == hyperv1.DedicatedRequestServingComponentsTopology {
		c.IsolateAsRequestServing = true
	}
	if hcp.Annotations[hyperv1.RequestServingNodeAdditionalSelectorAnnotation] != "" {
		c.AdditionalRequestServingNodeSelector = util.ParseNodeSelector(hcp.Annotations[hyperv1.RequestServingNodeAdditionalSelectorAnnotation])
	}
	c.SetDefaults(hcp, multiZoneSpreadLabels, replicas)
	if c.AdditionalLabels == nil {
		c.AdditionalLabels = map[string]string{}
	}
	c.AdditionalLabels[hyperv1.RequestServingComponentLabel] = "true"

}

// SetDefaults populates opinionated default DeploymentConfig for any Deployment.
func (c *DeploymentConfig) SetDefaults(hcp *hyperv1.HostedControlPlane, multiZoneSpreadLabels map[string]string, replicas *int) {
	// If no replicas is specified then infer it from the ControllerAvailabilityPolicy.
	if replicas == nil {
		c.setReplicas(hcp.Spec.ControllerAvailabilityPolicy)
	} else {
		c.Replicas = *replicas
	}
	c.DebugDeployments = debugDeployments(hcp)
	c.ResourceRequestOverrides = resourceRequestOverrides(hcp)
	c.RevisionHistoryLimit = 2

	c.setLocation(hcp, multiZoneSpreadLabels)
	// TODO (alberto): make this private, atm is needed for the konnectivity agent daemonset.
	c.SetReleaseImageAnnotation(util.HCPControlPlaneReleaseImage(hcp))

	if c.AdditionalLabels == nil {
		c.AdditionalLabels = map[string]string{}
	}
	for key, value := range hcp.Spec.Labels {
		c.AdditionalLabels[key] = value
	}
}

func resourceRequestOverrides(hcp *hyperv1.HostedControlPlane) ResourceOverrides {
	result := ResourceOverrides{}
	for key, value := range hcp.Annotations {
		if strings.HasPrefix(key, hyperv1.ResourceRequestOverrideAnnotationPrefix+"/") {
			result = parseResourceRequestOverrideAnnotation(key, value, result)
		}
	}
	return result
}

func parseResourceRequestOverrideAnnotation(key, value string, overrides ResourceOverrides) ResourceOverrides {
	keyParts := strings.SplitN(key, "/", 2)
	deploymentContainerParts := strings.SplitN(keyParts[1], ".", 2)
	deployment, container := deploymentContainerParts[0], deploymentContainerParts[1]
	resourceRequests := strings.Split(value, ",")
	spec, exists := overrides[deployment]
	if !exists {
		spec = ResourcesSpec{}
	}
	requirements, exists := spec[container]
	if !exists {
		requirements = corev1.ResourceRequirements{}
	}
	if requirements.Requests == nil {
		requirements.Requests = corev1.ResourceList{}
	}
	for _, request := range resourceRequests {
		requestParts := strings.SplitN(request, "=", 2)
		quantity, err := resource.ParseQuantity(requestParts[1])
		if err != nil {
			// Skip this request if invalid
			continue
		}
		requirements.Requests[corev1.ResourceName(requestParts[0])] = quantity
	}
	spec[container] = requirements
	overrides[deployment] = spec
	return overrides
}

// debugDeployments returns a set of deployments to debug based on the
// debugDeploymentsAnnotation value, indicating the deployment should be considered to
// be in development mode.
func debugDeployments(hc *hyperv1.HostedControlPlane) sets.String {
	val, exists := hc.Annotations[util.DebugDeploymentsAnnotation]
	if !exists {
		return nil
	}
	names := strings.Split(val, ",")
	return sets.NewString(names...)
}
