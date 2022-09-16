package config

import (
	"fmt"
	"hash"
	"hash/fnv"
	"strings"

	"github.com/davecgh/go-spew/spew"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/support/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/pointer"
)

const (
	// ManagedByLabel can be used to filter deployments.
	ManagedByLabel = "hypershift.openshift.io/managed-by"

	// There are used by NodeAffinity to prefer/tolerate Nodes.
	controlPlaneLabelTolerationKey = "hypershift.openshift.io/control-plane"
	clusterLabelTolerationKey      = "hypershift.openshift.io/cluster"

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
	LabelSelector             AdditionalLabels
	AdditionalLabels          AdditionalLabels
	AdditionalAnnotations     AdditionalAnnotations
	SecurityContexts          SecurityContextSpec
	SetDefaultSecurityContext bool
	LivenessProbes            LivenessProbes
	ReadinessProbes           ReadinessProbes
	Resources                 ResourcesSpec
	DebugDeployments          sets.String
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
		deployment.Spec.Replicas = pointer.Int32(0)
	} else {
		deployment.Spec.Replicas = pointer.Int32Ptr(int32(c.Replicas))
	}
	// there are two standard cases currently with hypershift: HA mode where there are 3 replicas spread across
	// zones and then non ha with one replica. When only 3 zones are available you need to be able to set maxUnavailable
	// in order to progress the rollout. However, you do not want to set that in the single replica case because it will
	// result in downtime.
	if c.Replicas > 1 {
		maxSurge := intstr.FromInt(0)
		maxUnavailable := intstr.FromInt(1)
		if deployment.Spec.Strategy.RollingUpdate == nil {
			deployment.Spec.Strategy.RollingUpdate = &appsv1.RollingUpdateDeployment{}
		}
		deployment.Spec.Strategy.RollingUpdate.MaxSurge = &maxSurge
		deployment.Spec.Strategy.RollingUpdate.MaxUnavailable = &maxUnavailable
	}

	// set default security context for pod
	if c.SetDefaultSecurityContext {
		deployment.Spec.Template.Spec.SecurityContext = &corev1.PodSecurityContext{
			RunAsUser: pointer.Int64(DefaultSecurityContextUser),
		}
	}

	// set managed-by label
	if deployment.Labels == nil {
		deployment.Labels = map[string]string{}
	}
	deployment.Labels[ManagedByLabel] = "control-plane-operator"

	// Set default selector if there's none.
	// This can't be changed as the field is immutable, otherwise would break upgrades.
	if deployment.Spec.Selector == nil {
		deployment.Spec.Selector = &metav1.LabelSelector{
			MatchLabels: c.LabelSelector,
		}
	}

	c.Scheduling.ApplyTo(&deployment.Spec.Template.Spec)
	c.AdditionalLabels.ApplyTo(&deployment.Spec.Template.ObjectMeta)
	c.SecurityContexts.ApplyTo(&deployment.Spec.Template.Spec)
	c.Resources.ApplyTo(&deployment.Spec.Template.Spec)
	c.LivenessProbes.ApplyTo(&deployment.Spec.Template.Spec)
	c.ReadinessProbes.ApplyTo(&deployment.Spec.Template.Spec)
	c.Resources.ApplyTo(&deployment.Spec.Template.Spec)
	c.AdditionalAnnotations.ApplyTo(&deployment.Spec.Template.ObjectMeta)
	c.setMultizoneSpread(&deployment.Spec.Template)
}

func (c *DeploymentConfig) ApplyToDaemonSet(daemonset *appsv1.DaemonSet) {
	// replicas is not used for DaemonSets
	c.Scheduling.ApplyTo(&daemonset.Spec.Template.Spec)
	c.AdditionalLabels.ApplyTo(&daemonset.Spec.Template.ObjectMeta)
	c.SecurityContexts.ApplyTo(&daemonset.Spec.Template.Spec)
	c.Resources.ApplyTo(&daemonset.Spec.Template.Spec)
	c.LivenessProbes.ApplyTo(&daemonset.Spec.Template.Spec)
	c.ReadinessProbes.ApplyTo(&daemonset.Spec.Template.Spec)
	c.Resources.ApplyTo(&daemonset.Spec.Template.Spec)
	c.AdditionalAnnotations.ApplyTo(&daemonset.Spec.Template.ObjectMeta)
	c.setMultizoneSpread(&daemonset.Spec.Template)
}

func (c *DeploymentConfig) ApplyToStatefulSet(sts *appsv1.StatefulSet) {
	sts.Spec.Replicas = pointer.Int32Ptr(int32(c.Replicas))
	c.Scheduling.ApplyTo(&sts.Spec.Template.Spec)
	c.AdditionalLabels.ApplyTo(&sts.Spec.Template.ObjectMeta)
	c.SecurityContexts.ApplyTo(&sts.Spec.Template.Spec)
	c.Resources.ApplyTo(&sts.Spec.Template.Spec)
	c.LivenessProbes.ApplyTo(&sts.Spec.Template.Spec)
	c.ReadinessProbes.ApplyTo(&sts.Spec.Template.Spec)
	c.Resources.ApplyTo(&sts.Spec.Template.Spec)
	c.AdditionalAnnotations.ApplyTo(&sts.Spec.Template.ObjectMeta)
	c.setMultizoneSpread(&sts.Spec.Template)
}

func clusterKey(hcp *hyperv1.HostedControlPlane) string {
	return hcp.Namespace
}

func colocationLabelValue(hcp *hyperv1.HostedControlPlane) string {
	return clusterKey(hcp)
}

const podHashLabel = "hypershift.openshift.io/pod-template-hash"

// setMultizoneSpread sets PodAntiAffinity with corev1.LabelTopologyZone as the topology key for a given set of labels.
// This is useful to e.g ensure pods are spread across availavility zones.
// There are certain situations where this results in additional roll-outs, most namely when the HCCO updates a deployment
// to add a checksum label in which case the CPO will instantly issue another update to update the hash label. We can
// not share this logic though, because the HCCO sees the defaulted deployment wheras the code on the CPO acts on
// undefaulted workloads. We can also not plug this into CreateOrUpdate, because it's defaulting is based on copying
// fields from the existing deployment - If we add it there, _all_ workloads will end up getting updated once after
// creation, which is strictly worse than having this issue just for the ones where the HCCO adds a label.
// To avoid triggering the "Scheduling failed" verification, the HCCO should clear the affinity field whenever
// a workload is updated.
func (c *DeploymentConfig) setMultizoneSpread(pod *corev1.PodTemplateSpec) {
	pod.Spec.Affinity = nil
	delete(pod.Labels, podHashLabel)
	if pod.Labels == nil {
		pod.Labels = map[string]string{}
	}
	pod.Labels[podHashLabel] = computeHash(pod)

	if c.Scheduling.Affinity == nil {
		c.Scheduling.Affinity = &corev1.Affinity{}
	}
	if c.Scheduling.Affinity.PodAntiAffinity == nil {
		c.Scheduling.Affinity.PodAntiAffinity = &corev1.PodAntiAffinity{}
	}
	c.Scheduling.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution = []corev1.PodAffinityTerm{
		{
			TopologyKey: corev1.LabelTopologyZone,
			LabelSelector: &metav1.LabelSelector{
				MatchLabels: pod.Labels,
			},
		},
	}

	pod.Spec.Affinity = c.Scheduling.Affinity
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
			Key:      clusterLabelTolerationKey,
			Operator: corev1.TolerationOpEqual,
			Value:    clusterKey(hcp),
			Effect:   corev1.TaintEffectNoSchedule,
		},
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
						Key:      clusterLabelTolerationKey,
						Operator: corev1.NodeSelectorOpIn,
						Values:   []string{clusterKey(hcp)},
					},
				},
			},
		},
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

func (c *DeploymentConfig) setLocation(hcp *hyperv1.HostedControlPlane) {
	c.setNodeSelector(hcp)
	c.setControlPlaneIsolation(hcp)
	c.setColocation(hcp)
}

func (c *DeploymentConfig) setReplicas(availability hyperv1.AvailabilityPolicy) {
	switch availability {
	case hyperv1.HighlyAvailable:
		c.Replicas = 3
	default:
		c.Replicas = 1
	}
}

// SetDefaults populates opinionated default DeploymentConfig for any Deployment.
func (c *DeploymentConfig) SetDefaults(hcp *hyperv1.HostedControlPlane, replicas *int, appName string) {
	// If no replicas is specified then infer it from the ControllerAvailabilityPolicy.
	if replicas == nil {
		c.setReplicas(hcp.Spec.ControllerAvailabilityPolicy)
	} else {
		c.Replicas = *replicas
	}
	c.DebugDeployments = debugDeployments(hcp)

	c.setLocation(hcp)
	// TODO (alberto): make this private, atm is needed for the konnectivity agent daemonset.
	c.SetReleaseImageAnnotation(hcp.Spec.ReleaseImage)

	// Set default labelSelector and labels.
	if c.LabelSelector == nil {
		c.LabelSelector = make(map[string]string)
		c.LabelSelector["app"] = appName
		c.LabelSelector[hyperv1.ControlPlaneComponent] = appName
	}

	if c.AdditionalLabels == nil {
		c.AdditionalLabels = make(map[string]string)
	}
	c.AdditionalLabels["app"] = appName
	c.AdditionalLabels[hyperv1.ControlPlaneComponent] = appName

	// TODO (alberto): add this to the setDefault signature.
	// c.priorityClass = priorityClass
	// c.SetDefaultSecurityContext = setDefaultSecurityContext
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

// Borrowed from upstream: https://github.com/kubernetes/kubernetes/blob/d5fdf3135e7c99e5f81e67986ae930f6a2ffb047/pkg/controller/controller_utils.go#L1152-L1167
// computeHash returns a hash value calculated from pod template.
// The hash will be safe encoded to avoid bad words.
func computeHash(template *corev1.PodTemplateSpec) string {
	podTemplateSpecHasher := fnv.New32a()
	deepHashObject(podTemplateSpecHasher, *template)

	return rand.SafeEncodeString(fmt.Sprint(podTemplateSpecHasher.Sum32()))
}

// deepHashObject writes specified object to hash using the spew library
// which follows pointers and prints actual values of the nested objects
// ensuring the hash does not change when a pointer changes.
func deepHashObject(hasher hash.Hash, objectToWrite interface{}) {
	hasher.Reset()
	printer := spew.ConfigState{
		Indent:         " ",
		SortKeys:       true,
		DisableMethods: true,
		SpewKeys:       true,
	}
	printer.Fprintf(hasher, "%#v", objectToWrite)
}
