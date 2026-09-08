package controlplanecomponent

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	karpenterassets "github.com/openshift/hypershift/karpenter-operator/controllers/karpenter/assets"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/k8sutil"
	"github.com/openshift/hypershift/support/podspec"
	"github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	DefaultSecurityContextUID = int64(1001)

	// This is used by NodeAffinity to prefer/tolerate Nodes.
	controlPlaneLabelTolerationKey = "hypershift.openshift.io/control-plane"
	// colocationLabelKey is used by PodAffinity to prefer colocating pods that belong to the same hosted cluster.
	colocationLabelKey = "hypershift.openshift.io/hosted-control-plane"
	// Specific cluster weight for soft affinity rule to node.
	clusterNodeSchedulingAffinityWeight = 100
	// Generic control plane workload weight for soft affinity rule to node.
	controlPlaneNodeSchedulingAffinityWeight = clusterNodeSchedulingAffinityWeight / 2
	// overflowNodeSchedulingAffinityWeight is the weight of the soft node-affinity that
	// steers "float" components onto overflow node pools in Preferred placement mode. It
	// is set higher than the cluster/control-plane colocation weights so that overflow
	// placement is preferred over packing with zone-critical pods on the zonal pools.
	overflowNodeSchedulingAffinityWeight = 100

	// ManagedByLabel can be used to filter deployments.
	ManagedByLabel = "hypershift.openshift.io/managed-by"
	// podSafeToEvictLocalVolumesAnnotation is an annotation denoting the local volumes of a pod that can be safely evicted.
	// This is needed for the CA operator to make sure it can properly drain the nodes with those volumes.
	podSafeToEvictLocalVolumesAnnotation = "cluster-autoscaler.kubernetes.io/safe-to-evict-local-volumes"

	// defaultNodeFailureTolerationSeconds is the TolerationSeconds applied to the
	// well-known node.kubernetes.io/not-ready and node.kubernetes.io/unreachable
	// NoExecute taints for API-critical components. When a management node fails,
	// this evicts and replaces those (stateless, highly-available) pods quickly
	// instead of waiting for the 300s default that the DefaultTolerationSeconds
	// admission plugin would otherwise inject, reducing HA recovery time. Users can
	// override this per-key via HostedControlPlane.spec.tolerations.
	defaultNodeFailureTolerationSeconds int64 = 10

	// etcdNodeFailureTolerationSeconds is the TolerationSeconds applied to the same
	// node-failure taints for etcd. etcd is a quorum-based StatefulSet, so evicting a
	// member too aggressively on a transient node partition forces unnecessary member
	// churn and data re-sync. This value is still far below the 300s default (so full
	// redundancy is restored faster after a genuine node loss) while long enough to
	// ride out brief blips.
	etcdNodeFailureTolerationSeconds int64 = 60
)

// shortNodeFailureToleration returns a NoExecute toleration for the given
// well-known node-failure taint key (node.kubernetes.io/not-ready or
// node.kubernetes.io/unreachable) with the provided short TolerationSeconds.
func shortNodeFailureToleration(key string, seconds int64) corev1.Toleration {
	return corev1.Toleration{
		Key:               key,
		Operator:          corev1.TolerationOpExists,
		Effect:            corev1.TaintEffectNoExecute,
		TolerationSeconds: ptr.To(seconds),
	}
}

// tolerationsTolerateTaint reports whether any of the given tolerations tolerates
// the provided taint using Kubernetes taint-matching semantics. This is used to
// decide whether a user-specified toleration already covers a node-failure taint,
// so we don't inject our short default. It correctly handles cases that a naive
// key/effect comparison would get wrong, e.g. an empty-Effect toleration (which
// matches all effects) or an Operator=Equal toleration with a non-empty Value
// (which does NOT tolerate the empty-valued node-failure taints).
func tolerationsTolerateTaint(tolerations []corev1.Toleration, taint *corev1.Taint) bool {
	for i := range tolerations {
		// enableComparisonOperators=false: the Lt/Gt operators are irrelevant for the
		// empty-valued node-failure taints and are treated as non-matching.
		if tolerations[i].ToleratesTaint(klog.Background(), taint, false) {
			return true
		}
	}
	return false
}

var (
	apiCriticalComponents = sets.New(
		"kube-apiserver",
		"openshift-apiserver",
		"openshift-oauth-apiserver",
		"oauth-openshift",
		"router",
		"packageserver",
	)

	// zoneCriticalComponents are non-request-serving, non-etcd components that are
	// nonetheless kept spread across availability zones under the Minimal
	// availability-zone scheduling policy because they sit on the guest API path.
	// Request-serving components and etcd are classified separately, see
	// zoneSpreadCritical.
	zoneCriticalComponents = sets.New(
		"openshift-apiserver",
		"openshift-oauth-apiserver",
	)

	configMapsToExcludeFromHash = []string{
		"client-ca",
	}
)

// minimalZonalScheduling reports whether the hosted control plane has opted into the
// Minimal availability-zone scheduling policy.
func minimalZonalScheduling(hcp *hyperv1.HostedControlPlane) bool {
	return hcp.Spec.ControlPlaneAvailabilityZoneScheduling.Policy == hyperv1.ControlPlaneAvailabilityZoneSchedulingMinimal
}

// nonZonalPlacementRequired reports whether "float" components must (hard) rather than
// merely prefer (soft) to run on overflow capacity under the Minimal policy.
func nonZonalPlacementRequired(hcp *hyperv1.HostedControlPlane) bool {
	return hcp.Spec.ControlPlaneAvailabilityZoneScheduling.NonZonalPlacement == hyperv1.NonZonalPlacementRequired
}

// zoneSpreadCritical reports whether this component must be spread across availability
// zones under the Minimal policy: etcd (quorum), request-serving components, and the
// API-critical overrides. All other components are "float" and are steered onto overflow
// capacity.
func (c *controlPlaneWorkload[T]) zoneSpreadCritical() bool {
	return isEtcdComponent(c.Name()) || c.IsRequestServing() || zoneCriticalComponents.Has(c.Name())
}

func (c *controlPlaneWorkload[T]) setDefaultOptions(cpContext ControlPlaneContext, workloadObj T, existingResources map[string]corev1.ResourceRequirements) error {
	hcp := cpContext.HCP

	labels := workloadObj.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels[ManagedByLabel] = "control-plane-operator"
	workloadObj.SetLabels(labels)

	desiredReplicas := c.workloadProvider.Replicas(workloadObj)
	replicas := DefaultReplicas(cpContext.HCP, c.ComponentOptions, c.Name())
	if desiredReplicas != nil {
		replicas = *desiredReplicas
	}

	if debugComponentsSet(hcp).Has(c.Name()) {
		// scale to 0 if this component is in debug mode.
		c.workloadProvider.SetReplicasAndStrategy(workloadObj, 0, c.IsRequestServing())
	} else {
		c.workloadProvider.SetReplicasAndStrategy(workloadObj, replicas, c.IsRequestServing())
	}

	podTemplateSpec := c.workloadProvider.PodTemplateSpec(workloadObj)
	enforceVolumesDefaultMode(&podTemplateSpec.Spec)
	err := enforceImagePullPolicy(podTemplateSpec.Spec.Containers)
	if err != nil {
		return err
	}

	if err := replaceContainersImageFromPayload(cpContext.ReleaseImageProvider, hcp, podTemplateSpec.Spec.Containers); err != nil {
		return err
	}
	if err := replaceContainersImageFromPayload(cpContext.ReleaseImageProvider, hcp, podTemplateSpec.Spec.InitContainers); err != nil {
		return err
	}

	if c.serviceAccountKubeConfigOpts != nil {
		c.addServiceAccountKubeconfigVolumes(podTemplateSpec)
	}

	if c.konnectivityContainerOpts != nil {
		c.konnectivityContainerOpts.injectKonnectivityContainer(cpContext, &podTemplateSpec.Spec)
	}

	if c.tokenMinterContainerOpts != nil {
		c.tokenMinterContainerOpts.injectTokenMinterContainer(cpContext, &podTemplateSpec.Spec)
	}

	if err := c.applyWatchedResourcesAnnotation(cpContext, podTemplateSpec); err != nil {
		return err
	}

	if c.availabilityProberOpts != nil {
		availabilityProberImage := cpContext.ReleaseImageProvider.GetImage(podspec.AvailabilityProberImageName)
		podspec.AvailabilityProber(
			kas.InClusterKASReadyURL(hcp.Spec.Platform.Type), availabilityProberImage,
			&podTemplateSpec.Spec,
			podspec.WithOptions(c.availabilityProberOpts))
	}

	enforceTerminationMessagePolicy(podTemplateSpec.Spec.InitContainers)
	enforceTerminationMessagePolicy(podTemplateSpec.Spec.Containers)
	enforceReadOnlyRootFilesystem(&podTemplateSpec.Spec)

	if _, exist := podTemplateSpec.Annotations[config.NeedMetricsServerAccessLabel]; exist || c.NeedsManagementKASAccess() ||
		c.Name() == "packageserver" { // TODO: investigate why packageserver needs AutomountServiceAccountToken or set NeedsManagementKASAccess to true.
		podTemplateSpec.Spec.AutomountServiceAccountToken = ptr.To(true)
	} else {
		podTemplateSpec.Spec.AutomountServiceAccountToken = ptr.To(false)
	}

	// set default security context for the pod.
	if cpContext.SetDefaultSecurityContext {
		uid := cpContext.DefaultSecurityContextUID
		podTemplateSpec.Spec.SecurityContext = &corev1.PodSecurityContext{
			RunAsUser: ptr.To[int64](uid),
		}
		if isEtcdComponent(c.Name()) {
			podTemplateSpec.Spec.SecurityContext.FSGroup = ptr.To[int64](uid)
		}
	}

	// Apply GCP-specific container security context for GKE PodSecurity compliance.
	// This is applied here (after sidecars are injected) to ensure all containers,
	// including availability-prober and konnectivity-proxy sidecars, have the
	// required security context fields set.
	//
	// Containers that need specific capabilities (e.g., NET_BIND_SERVICE for haproxy)
	// should declare them in their deployment templates, and they will be preserved.
	if hcp.Spec.Platform.Type == hyperv1.GCPPlatform {
		if err := podspec.EnforceRestrictedSecurityContextToContainers(&podTemplateSpec.Spec); err != nil {
			return fmt.Errorf("failed to enforce restricted security context: %w", err)
		}
	}

	// preserve existing resource requirements.
	for idx, container := range podTemplateSpec.Spec.Containers {
		if res, exist := existingResources[container.Name]; exist {
			podTemplateSpec.Spec.Containers[idx].Resources = res
		}
	}

	// set PriorityClassName
	podTemplateSpec.Spec.PriorityClassName = priorityClass(c.Name(), hcp)
	// setNodeSelector sets a nodeSelector passed through the API.
	// This is useful to e.g ensure control plane pods land in management cluster Infra Nodes.
	if hcp.Spec.NodeSelector != nil {
		podTemplateSpec.Spec.NodeSelector = hcp.Spec.NodeSelector
	}

	c.setLabels(podTemplateSpec, hcp)
	c.setAnnotations(podTemplateSpec, hcp)
	c.setControlPlaneIsolation(podTemplateSpec, hcp)
	c.setColocation(podTemplateSpec, hcp)
	c.applyRequestsOverrides(podTemplateSpec, hcp)
	if minimalZonalScheduling(hcp) {
		// Under the Minimal policy, node placement (zonal vs overflow) is applied to
		// every component by setControlPlaneIsolation above. Zone/host spreading via
		// topologySpreadConstraints only applies to multi-replica workloads.
		if replicas > 1 {
			c.setMinimalZonalSpread(podTemplateSpec)
		}
	} else if replicas > 1 && c.MultiZoneSpread() {
		c.setMultizoneSpread(podTemplateSpec, hcp)
	}

	return nil
}

func (c *controlPlaneWorkload[T]) setAnnotations(podTemplate *corev1.PodTemplateSpec, hcp *hyperv1.HostedControlPlane) {
	if podTemplate.Annotations == nil {
		podTemplate.Annotations = map[string]string{}
	}

	podTemplate.Annotations[hyperv1.ReleaseImageAnnotation] = util.HCPControlPlaneReleaseImage(hcp)
	if restartDate, ok := hcp.Annotations[hyperv1.RestartDateAnnotation]; ok {
		podTemplate.Annotations[hyperv1.RestartDateAnnotation] = restartDate
	}

	localStorageVolumes := make([]string, 0)
	for _, volume := range podTemplate.Spec.Volumes {
		if volume.EmptyDir != nil || volume.HostPath != nil {
			localStorageVolumes = append(localStorageVolumes, volume.Name)
		}
	}

	if len(localStorageVolumes) > 0 {
		annotationsVolumes := strings.Join(localStorageVolumes, ",")
		podTemplate.Annotations[podSafeToEvictLocalVolumesAnnotation] = annotationsVolumes
	}
}

func (c *controlPlaneWorkload[T]) setLabels(podTemplate *corev1.PodTemplateSpec, hcp *hyperv1.HostedControlPlane) {
	if podTemplate.Labels == nil {
		podTemplate.Labels = map[string]string{}
	}

	podTemplate.Labels[hyperv1.ControlPlaneComponentLabel] = c.Name()
	if c.NeedsManagementKASAccess() {
		podTemplate.Labels[config.NeedManagementKASAccessLabel] = "true"
	}
	if c.IsRequestServing() {
		podTemplate.Labels[hyperv1.RequestServingComponentLabel] = "true"
	}
	if minimalZonalScheduling(hcp) {
		podTemplate.Labels[hyperv1.ControlPlaneSchedulingTierLabel] = c.zoneSchedulingTier()
	}
	// set additional Labels
	maps.Copy(podTemplate.Labels, hcp.Spec.Labels)
}

// zoneSchedulingTier returns the scheduling tier label value for this component under
// the Minimal availability-zone scheduling policy.
func (c *controlPlaneWorkload[T]) zoneSchedulingTier() string {
	if c.zoneSpreadCritical() {
		return hyperv1.ControlPlaneNodeRoleZonal
	}
	return hyperv1.ControlPlaneNodeRoleOverflow
}

// setControlPlaneIsolation configures tolerations and NodeAffinity rules to prefer Nodes with controlPlaneNodeLabel and clusterNodeLabel.
func (c *controlPlaneWorkload[T]) setControlPlaneIsolation(podTemplate *corev1.PodTemplateSpec, hcp *hyperv1.HostedControlPlane) {
	isolateAsRequestServing := false
	if c.IsRequestServing() && hcp.Annotations[hyperv1.TopologyAnnotation] == hyperv1.DedicatedRequestServingComponentsTopology {
		isolateAsRequestServing = true
	}

	// set Tolerations
	podTemplate.Spec.Tolerations = []corev1.Toleration{
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
	if isolateAsRequestServing {
		podTemplate.Spec.Tolerations = append(podTemplate.Spec.Tolerations, corev1.Toleration{
			Key:      hyperv1.RequestServingComponentLabel,
			Operator: corev1.TolerationOpEqual,
			Value:    "true",
			Effect:   corev1.TaintEffectNoSchedule,
		})
	}
	// For API-critical and etcd components, default to short NoExecute tolerations
	// so pods are evicted and replaced quickly, reducing HA recovery time after a
	// management node failure. User-specified tolerations for the same keys take
	// precedence and are applied unfiltered below.
	if apiCriticalComponents.Has(c.Name()) || isEtcdComponent(c.Name()) {
		tolerationSeconds := defaultNodeFailureTolerationSeconds
		if isEtcdComponent(c.Name()) {
			// etcd uses a longer value to avoid quorum churn on transient partitions.
			tolerationSeconds = etcdNodeFailureTolerationSeconds
		}
		for _, key := range []string{corev1.TaintNodeNotReady, corev1.TaintNodeUnreachable} {
			// Only inject our short default if the user hasn't already provided a
			// toleration that actually tolerates this node-failure taint. The taints
			// are added by the node-lifecycle-controller with NoExecute and an empty
			// value, so we match against that exact taint.
			taint := &corev1.Taint{Key: key, Effect: corev1.TaintEffectNoExecute}
			if !tolerationsTolerateTaint(hcp.Spec.Tolerations, taint) {
				podTemplate.Spec.Tolerations = append(podTemplate.Spec.Tolerations,
					shortNodeFailureToleration(key, tolerationSeconds))
			}
		}
	}

	// set additional Tolerations
	if len(hcp.Spec.Tolerations) != 0 {
		podTemplate.Spec.Tolerations = append(podTemplate.Spec.Tolerations, hcp.Spec.Tolerations...)
	}

	// set Affinity
	if podTemplate.Spec.Affinity == nil {
		podTemplate.Spec.Affinity = &corev1.Affinity{}
	}
	if podTemplate.Spec.Affinity.NodeAffinity == nil {
		podTemplate.Spec.Affinity.NodeAffinity = &corev1.NodeAffinity{}
	}
	podTemplate.Spec.Affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution = []corev1.PreferredSchedulingTerm{
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

	if isolateAsRequestServing {
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

		var additionalRequestServingNodeSelector map[string]string
		if hcp.Annotations[hyperv1.RequestServingNodeAdditionalSelectorAnnotation] != "" {
			additionalRequestServingNodeSelector = k8sutil.ParseNodeSelector(hcp.Annotations[hyperv1.RequestServingNodeAdditionalSelectorAnnotation])
		}
		for key, value := range additionalRequestServingNodeSelector {
			nodeSelectorRequirements = append(nodeSelectorRequirements, corev1.NodeSelectorRequirement{
				Key:      key,
				Operator: corev1.NodeSelectorOpIn,
				Values:   []string{value},
			})
		}

		podTemplate.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution = &corev1.NodeSelector{
			NodeSelectorTerms: []corev1.NodeSelectorTerm{
				{
					MatchExpressions: nodeSelectorRequirements,
				},
			},
		}
	}

	if minimalZonalScheduling(hcp) {
		c.setMinimalZonalNodePlacement(podTemplate, hcp)
	}
}

// setMinimalZonalNodePlacement steers this component onto the correct management-cluster
// node pool under the Minimal availability-zone scheduling policy: zone-critical components
// onto the zonal (balanced) pools, and float components onto the overflow (non-zonal) pools.
// Zone-critical components additionally tolerate the zonal NoSchedule taint that is applied
// in hard (Required) placement mode, and float components are placed on overflow capacity
// either as a hard requirement (Required) or a strong preference (Preferred).
func (c *controlPlaneWorkload[T]) setMinimalZonalNodePlacement(podTemplate *corev1.PodTemplateSpec, hcp *hyperv1.HostedControlPlane) {
	if podTemplate.Spec.Affinity == nil {
		podTemplate.Spec.Affinity = &corev1.Affinity{}
	}
	if podTemplate.Spec.Affinity.NodeAffinity == nil {
		podTemplate.Spec.Affinity.NodeAffinity = &corev1.NodeAffinity{}
	}

	role := c.zoneSchedulingTier() // "zonal" or "overflow"
	roleRequirement := corev1.NodeSelectorRequirement{
		Key:      hyperv1.ControlPlaneNodeRoleLabel,
		Operator: corev1.NodeSelectorOpIn,
		Values:   []string{role},
	}

	if c.zoneSpreadCritical() {
		// Zone-critical components are required onto the zonal pools and tolerate the
		// zonal taint (present only in hard placement mode).
		podTemplate.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution = requireNodeSelector(
			podTemplate.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution, roleRequirement)
		podTemplate.Spec.Tolerations = append(podTemplate.Spec.Tolerations, corev1.Toleration{
			Key:      hyperv1.ControlPlaneNodeRoleLabel,
			Operator: corev1.TolerationOpEqual,
			Value:    hyperv1.ControlPlaneNodeRoleZonal,
			Effect:   corev1.TaintEffectNoSchedule,
		})
		return
	}

	// Float components target the overflow pools, either as a hard requirement or a
	// strong preference.
	if nonZonalPlacementRequired(hcp) {
		podTemplate.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution = requireNodeSelector(
			podTemplate.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution, roleRequirement)
		return
	}
	podTemplate.Spec.Affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution = append(
		podTemplate.Spec.Affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution,
		corev1.PreferredSchedulingTerm{
			Weight:     overflowNodeSchedulingAffinityWeight,
			Preference: corev1.NodeSelectorTerm{MatchExpressions: []corev1.NodeSelectorRequirement{roleRequirement}},
		})
}

// requireNodeSelector adds requirement as a required node-affinity match, ANDing it into
// every existing NodeSelectorTerm (or creating one if none exist). Required
// NodeSelectorTerms are ORed together, so appending the requirement to each term ANDs it
// with all pre-existing constraints.
func requireNodeSelector(existing *corev1.NodeSelector, requirement corev1.NodeSelectorRequirement) *corev1.NodeSelector {
	if existing == nil || len(existing.NodeSelectorTerms) == 0 {
		return &corev1.NodeSelector{
			NodeSelectorTerms: []corev1.NodeSelectorTerm{
				{MatchExpressions: []corev1.NodeSelectorRequirement{requirement}},
			},
		}
	}
	for i := range existing.NodeSelectorTerms {
		existing.NodeSelectorTerms[i].MatchExpressions = append(existing.NodeSelectorTerms[i].MatchExpressions, requirement)
	}
	return existing
}

// setColocation sets labels and PodAffinity rules for this deployment so that pods
// of the deployment will prefer to group with pods of the anchor deployment.
func (c *controlPlaneWorkload[T]) setColocation(podTemplate *corev1.PodTemplateSpec, hcp *hyperv1.HostedControlPlane) {
	if podTemplate.Labels == nil {
		podTemplate.Labels = map[string]string{}
	}
	podTemplate.Labels[colocationLabelKey] = clusterKey(hcp)

	if podTemplate.Spec.Affinity == nil {
		podTemplate.Spec.Affinity = &corev1.Affinity{}
	}
	if podTemplate.Spec.Affinity.PodAffinity == nil {
		podTemplate.Spec.Affinity.PodAffinity = &corev1.PodAffinity{}
	}
	colocationSelector := map[string]string{
		colocationLabelKey: clusterKey(hcp),
	}
	// Under the Minimal policy, scope colocation per scheduling tier so that float pods
	// are not pulled onto the zonal pools by the desire to pack with zone-critical pods
	// (which matters in soft/Preferred placement mode).
	if minimalZonalScheduling(hcp) {
		colocationSelector[hyperv1.ControlPlaneSchedulingTierLabel] = c.zoneSchedulingTier()
	}
	podTemplate.Spec.Affinity.PodAffinity.PreferredDuringSchedulingIgnoredDuringExecution = []corev1.WeightedPodAffinityTerm{
		{
			Weight: 100,
			PodAffinityTerm: corev1.PodAffinityTerm{
				LabelSelector: &metav1.LabelSelector{
					MatchLabels: colocationSelector,
				},
				TopologyKey: corev1.LabelHostname,
			},
		},
	}
}

// SetMultizoneSpread sets PodAntiAffinity with corev1.LabelTopologyZone as the topology key for a given set of labels.
// This is useful to e.g ensure pods are spread across availability zones.
// If required is true, the rule is set as RequiredDuringSchedulingIgnoredDuringExecution, otherwise it is set as
// PreferredDuringSchedulingIgnoredDuringExecution.
func (c *controlPlaneWorkload[T]) setMultizoneSpread(podTemplate *corev1.PodTemplateSpec, hcp *hyperv1.HostedControlPlane) {
	multiZoneSpreadLabels := podTemplate.ObjectMeta.Labels
	multiZoneRequired := true
	switch hcp.Spec.Platform.Type {
	// On OpenStack and Kubevirt we can't spread across zones in certain cases
	// so let's relax the requirement on those platforms.
	case hyperv1.OpenStackPlatform, hyperv1.KubevirtPlatform:
		multiZoneRequired = false
	}

	if podTemplate.Spec.Affinity == nil {
		podTemplate.Spec.Affinity = &corev1.Affinity{}
	}
	if podTemplate.Spec.Affinity.PodAntiAffinity == nil {
		podTemplate.Spec.Affinity.PodAntiAffinity = &corev1.PodAntiAffinity{}
	}

	if multiZoneRequired {
		podTemplate.Spec.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution = append(podTemplate.Spec.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution,
			corev1.PodAffinityTerm{
				TopologyKey: corev1.LabelTopologyZone,
				LabelSelector: &metav1.LabelSelector{
					MatchLabels: multiZoneSpreadLabels,
				},
			})
	} else {
		podTemplate.Spec.Affinity.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution = append(podTemplate.Spec.Affinity.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution,
			corev1.WeightedPodAffinityTerm{
				Weight: 100,
				PodAffinityTerm: corev1.PodAffinityTerm{
					TopologyKey: corev1.LabelTopologyZone,
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: multiZoneSpreadLabels,
					},
				},
			})
	}

	// set PodAntiAffinity with corev1.LabelHostname as the topology key for a given set of labels.
	// This is useful to e.g ensure pods are spread across nodes.
	podTemplate.Spec.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution = append(podTemplate.Spec.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution,
		corev1.PodAffinityTerm{
			TopologyKey: corev1.LabelHostname,
			LabelSelector: &metav1.LabelSelector{
				MatchLabels: multiZoneSpreadLabels,
			},
		},
	)
}

// setMinimalZonalSpread applies topologySpreadConstraints (in place of the podAntiAffinity
// used by setMultizoneSpread) for a multi-replica workload under the Minimal
// availability-zone scheduling policy.
//
// Zone-critical components are spread one-per-zone (hard) and one-per-host (hard). etcd,
// a StatefulSet, uses minDomains=3 so it fails closed if fewer than three zones are
// available; the Deployment-backed components use matchLabelKeys=[pod-template-hash] so a
// new rollout revision is spread independently (allowing a surge pod into the spare zone).
//
// Float components are spread one-per-host (hard) and best-effort across zones
// (ScheduleAnyway): they spread across overflow zones when the overflow pool spans more than
// one zone, but scheduling is never blocked when it does not.
func (c *controlPlaneWorkload[T]) setMinimalZonalSpread(podTemplate *corev1.PodTemplateSpec) {
	selector := &metav1.LabelSelector{MatchLabels: podTemplate.ObjectMeta.Labels}
	isEtcd := isEtcdComponent(c.Name())

	// matchLabelKeys scopes spreading to a single rollout revision. It only applies to
	// Deployment/ReplicaSet-backed workloads (pod-template-hash); etcd is a StatefulSet.
	var matchLabelKeys []string
	if !isEtcd {
		matchLabelKeys = []string{"pod-template-hash"}
	}

	// Hard per-host spread for every multi-replica workload.
	constraints := []corev1.TopologySpreadConstraint{
		{
			MaxSkew:           1,
			TopologyKey:       corev1.LabelHostname,
			WhenUnsatisfiable: corev1.DoNotSchedule,
			LabelSelector:     selector,
			MatchLabelKeys:    matchLabelKeys,
		},
	}

	if c.zoneSpreadCritical() {
		zoneConstraint := corev1.TopologySpreadConstraint{
			MaxSkew:           1,
			TopologyKey:       corev1.LabelTopologyZone,
			WhenUnsatisfiable: corev1.DoNotSchedule,
			LabelSelector:     selector,
			MatchLabelKeys:    matchLabelKeys,
		}
		if isEtcd {
			// Fail closed for quorum: never co-locate members; require three zones.
			zoneConstraint.MinDomains = ptr.To[int32](3)
		}
		constraints = append(constraints, zoneConstraint)
	} else {
		// Float: best-effort zone spread so overflow AZs are used when available, but
		// scheduling is never blocked when overflow spans a single zone.
		constraints = append(constraints, corev1.TopologySpreadConstraint{
			MaxSkew:           1,
			TopologyKey:       corev1.LabelTopologyZone,
			WhenUnsatisfiable: corev1.ScheduleAnyway,
			LabelSelector:     selector,
			MatchLabelKeys:    matchLabelKeys,
		})
	}

	podTemplate.Spec.TopologySpreadConstraints = append(podTemplate.Spec.TopologySpreadConstraints, constraints...)
}

func (c *controlPlaneWorkload[T]) applyRequestsOverrides(podTemplate *corev1.PodTemplateSpec, hcp *hyperv1.HostedControlPlane) {
	requestsOverrides := map[string]corev1.ResourceList{}
	for key, value := range hcp.Annotations {
		if strings.HasPrefix(key, hyperv1.ResourceRequestOverrideAnnotationPrefix+"/") {
			keyParts := strings.SplitN(key, "/", 2)
			deploymentContainerParts := strings.SplitN(keyParts[1], ".", 2)
			deploymentName, containerName := deploymentContainerParts[0], deploymentContainerParts[1]
			if deploymentName != c.Name() {
				continue
			}
			requestsOverrides[containerName] = parseResourceRequestOverrideAnnotation(value)
		}
	}

	for i, c := range podTemplate.Spec.InitContainers {
		if res, ok := requestsOverrides[c.Name]; ok {
			if podTemplate.Spec.InitContainers[i].Resources.Requests == nil {
				podTemplate.Spec.InitContainers[i].Resources.Requests = corev1.ResourceList{}
			}
			maps.Copy(podTemplate.Spec.InitContainers[i].Resources.Requests, res)
			applyNonOvercommitableResourceLimits(&podTemplate.Spec.InitContainers[i], res)
		}
	}
	for i, c := range podTemplate.Spec.Containers {
		if res, ok := requestsOverrides[c.Name]; ok {
			if podTemplate.Spec.Containers[i].Resources.Requests == nil {
				podTemplate.Spec.Containers[i].Resources.Requests = corev1.ResourceList{}
			}
			maps.Copy(podTemplate.Spec.Containers[i].Resources.Requests, res)
			applyNonOvercommitableResourceLimits(&podTemplate.Spec.Containers[i], res)
		}
	}
}

const aroSwiftNICResource corev1.ResourceName = "aro.openshift.io/swift-nic"

// applyNonOvercommitableResourceLimits sets limits equal to requests for extended
// resources that cannot be overcommitted, specifically "aro.openshift.io/swift-nic".
// The API server requires limits == requests for these resources.
// https://github.com/kubernetes/kubernetes/blob/621e250502ddeeab8274836e88b506c0c4f57232/pkg/apis/core/validation/validation.go#L7975-L7976
func applyNonOvercommitableResourceLimits(container *corev1.Container, overrides corev1.ResourceList) {
	if quantity, ok := overrides[aroSwiftNICResource]; ok {
		if container.Resources.Limits == nil {
			container.Resources.Limits = corev1.ResourceList{}
		}
		container.Resources.Limits[aroSwiftNICResource] = quantity
	}
}

func parseResourceRequestOverrideAnnotation(value string) corev1.ResourceList {
	result := corev1.ResourceList{}
	resourceRequests := strings.Split(value, ",")

	for _, request := range resourceRequests {
		requestParts := strings.SplitN(request, "=", 2)
		quantity, err := resource.ParseQuantity(requestParts[1])
		if err != nil {
			// Skip this request if invalid
			continue
		}
		result[corev1.ResourceName(requestParts[0])] = quantity
	}

	return result
}

func podConfigMapNames(spec *corev1.PodSpec, excludeNames []string) []string {
	names := sets.New[string]()
	for _, v := range spec.Volumes {
		switch {
		case v.ConfigMap != nil:
			names.Insert(v.ConfigMap.Name)
		case v.Projected != nil:
			for _, source := range v.Projected.Sources {
				if source.ConfigMap != nil {
					names.Insert(source.ConfigMap.Name)
				}
			}
		}
	}
	for _, name := range excludeNames {
		names.Delete(name)
	}

	return sets.List(names)
}

func podSecretNames(spec *corev1.PodSpec) []string {
	names := sets.New[string]()
	for _, v := range spec.Volumes {
		switch {
		case v.Secret != nil:
			names.Insert(v.Secret.SecretName)
		case v.Projected != nil:
			for _, source := range v.Projected.Sources {
				if source.Secret != nil {
					names.Insert(source.Secret.Name)
				}
			}
		}
	}
	return sets.List(names)
}

func fetchResource[T client.Object](ctx context.Context, obj T, namespace string, c client.Client) func(string) (T, error) {
	return func(name string) (T, error) {
		resource := obj.DeepCopyObject().(client.Object)
		resource.SetName(name)
		resource.SetNamespace(namespace)
		if err := c.Get(ctx, client.ObjectKeyFromObject(resource), resource); err != nil && !apierrors.IsNotFound(err) {
			return obj, err
		}
		return resource.(T), nil
	}
}

func (c *controlPlaneWorkload[T]) applyWatchedResourcesAnnotation(cpContext ControlPlaneContext, podTemplate *corev1.PodTemplateSpec) error {
	// remove duplicate entries if any.
	secretNames := podSecretNames(&podTemplate.Spec)
	configMapNames := podConfigMapNames(&podTemplate.Spec, configMapsToExcludeFromHash)

	hashString, err := computeResourceHash(secretNames, configMapNames,
		fetchResource(cpContext, &corev1.Secret{}, cpContext.HCP.Namespace, cpContext.Client),
		fetchResource(cpContext, &corev1.ConfigMap{}, cpContext.HCP.Namespace, cpContext.Client))
	if err != nil {
		return err
	}

	if podTemplate.Annotations == nil {
		podTemplate.Annotations = map[string]string{}
	}
	podTemplate.Annotations["component.hypershift.openshift.io/config-hash"] = hashString
	return nil
}

func computeResourceHash(secretNames, configMapNames []string,
	fetchSecret func(string) (*corev1.Secret, error),
	fetchConfigMap func(string) (*corev1.ConfigMap, error),
) (string, error) {
	var hashes []string
	for _, name := range secretNames {
		secret, err := fetchSecret(name)
		if err != nil {
			return "", err
		}
		for _, value := range secret.Data {
			hashes = append(hashes, util.HashSimple(value))
		}
	}

	for _, name := range configMapNames {
		configMap, err := fetchConfigMap(name)
		if err != nil {
			return "", err
		}
		for _, value := range configMap.Data {
			hashes = append(hashes, util.HashSimple(value))
		}
	}
	slices.Sort(hashes)
	return strings.Join(hashes, ""), nil
}

func enforceVolumesDefaultMode(podSpec *corev1.PodSpec) {
	for _, volume := range podSpec.Volumes {
		if volume.ConfigMap != nil {
			volume.ConfigMap.DefaultMode = ptr.To[int32](420)
		}

		if volume.Secret != nil {
			volume.Secret.DefaultMode = ptr.To[int32](416)
		}
	}
}

func enforceImagePullPolicy(containers []corev1.Container) error {
	for i := range containers {
		if containers[i].Image == "" {
			return fmt.Errorf("container %s has no image key specified", containers[i].Name)
		}
		// Use Always for :latest tag to ensure we get the most recent image
		if strings.HasSuffix(containers[i].Image, ":latest") {
			containers[i].ImagePullPolicy = corev1.PullAlways
		} else {
			containers[i].ImagePullPolicy = corev1.PullIfNotPresent
		}
	}
	return nil
}

func enforceReadOnlyRootFilesystem(podSpec *corev1.PodSpec) {
	podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{
		Name: podspec.PodTmpDirMountName,
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		},
	})
	enforceReadOnlyRootFilesystemContainers(podSpec.Containers)
}

func enforceReadOnlyRootFilesystemContainers(containers []corev1.Container) {
	for i := range containers {
		if containers[i].SecurityContext == nil {
			containers[i].SecurityContext = &corev1.SecurityContext{}
		}
		if !slices.ContainsFunc(containers[i].VolumeMounts, func(vm corev1.VolumeMount) bool {
			return vm.MountPath == podspec.PodTmpDirMountPath
		}) {
			containers[i].VolumeMounts = append(containers[i].VolumeMounts, corev1.VolumeMount{
				Name:      podspec.PodTmpDirMountName,
				MountPath: podspec.PodTmpDirMountPath,
			})
		}
		containers[i].SecurityContext.ReadOnlyRootFilesystem = ptr.To(true)
	}
}

func enforceTerminationMessagePolicy(containers []corev1.Container) {
	for i := range containers {
		containers[i].TerminationMessagePolicy = corev1.TerminationMessageFallbackToLogsOnError
	}
}

func replaceContainersImageFromPayload(imageProvider imageprovider.ReleaseImageProvider, hcp *hyperv1.HostedControlPlane, containers []corev1.Container) error {
	for i, container := range containers {
		if container.Image == "" {
			return fmt.Errorf("container %s has no image key specified", container.Name)
		}
		key := container.Image
		if payloadImage, exist := imageProvider.ImageExist(key); exist {
			containers[i].Image = payloadImage
		} else if key == "cluster-version-operator" {
			// fallback to hcp releaseImage if "cluster-version-operator" image is not available.
			// This could happen for example in local dev environments if the "OPERATE_ON_RELEASE_IMAGE" env variable is not set.
			containers[i].Image = util.HCPControlPlaneReleaseImage(hcp)
		} else if key == "aws-karpenter-provider-aws" {
			// fallback to hardcoded aws image if karpenter image is not available in payload yet.
			containers[i].Image = karpenterassets.DefaultKarpenterProviderAWSImage
		}
	}

	return nil
}

func priorityClass(componentName string, hcp *hyperv1.HostedControlPlane) string {
	priorityClass := config.DefaultPriorityClass
	overrideAnnotation := hyperv1.ControlPlanePriorityClass

	if isEtcdComponent(componentName) {
		priorityClass = config.EtcdPriorityClass
		overrideAnnotation = hyperv1.EtcdPriorityClass
	} else if apiCriticalComponents.Has(componentName) {
		priorityClass = config.APICriticalPriorityClass
		overrideAnnotation = hyperv1.APICriticalPriorityClass
	}

	if overrideValue := hcp.Annotations[overrideAnnotation]; overrideValue != "" {
		priorityClass = overrideValue
	}

	return priorityClass
}

func DefaultReplicas(hcp *hyperv1.HostedControlPlane, options ComponentOptions, name string) int32 {
	if hcp.Spec.ControllerAvailabilityPolicy == hyperv1.SingleReplica {
		return 1
	}

	// HighlyAvailable
	if options.IsRequestServing() && hcp.Annotations[hyperv1.TopologyAnnotation] == hyperv1.DedicatedRequestServingComponentsTopology {
		return 2
	}
	// etcd always runs with three replicas for quorum. Under the Minimal
	// availability-zone scheduling policy, non-quorum API-critical components run as
	// two-replica pairs instead of three-replica triplets (etcd remains at three).
	if isEtcdComponent(name) {
		return 3
	}
	if apiCriticalComponents.Has(name) {
		if minimalZonalScheduling(hcp) {
			return 2
		}
		return 3
	}
	return 2
}

// debugComponentsSet returns a set of Components to debug based on the
// debugDeploymentsAnnotation value, indicating the Component should be considered to
// be in development mode.
func debugComponentsSet(hcp *hyperv1.HostedControlPlane) sets.Set[string] {
	val, exists := hcp.Annotations[k8sutil.DebugDeploymentsAnnotation]
	if !exists {
		return nil
	}
	names := strings.Split(val, ",")
	return sets.New(names...)
}

func clusterKey(hcp *hyperv1.HostedControlPlane) string {
	return hcp.Namespace
}
