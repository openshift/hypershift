package controlplanecomponent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sort"
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

	// ManagedByLabel can be used to filter deployments.
	ManagedByLabel = "hypershift.openshift.io/managed-by"
	// podSafeToEvictLocalVolumesAnnotation is an annotation denoting the local volumes of a pod that can be safely evicted.
	// This is needed for the CA operator to make sure it can properly drain the nodes with those volumes.
	podSafeToEvictLocalVolumesAnnotation = "cluster-autoscaler.kubernetes.io/safe-to-evict-local-volumes"

	// limitsRemovedAnnotation tracks containers whose resource limits have been
	// automatically removed because the manifest no longer specifies them.
	// The value is a comma-separated list of container names. When a user manually
	// re-adds limits to a container that is already listed here, the operator
	// detects this and preserves the user's limits on subsequent reconciliations.
	limitsRemovedAnnotation = "component.hypershift.openshift.io/limits-removed"

	// containerResourcesHashAnnotation records a hash of the final container resource
	// requirements after all modifications. This ensures DeepDerivative detects
	// resource changes (e.g. limit removal) even when the new value is a subset
	// of the old value.
	containerResourcesHashAnnotation = "component.hypershift.openshift.io/container-resources-hash"

	// capiProviderComponentName is the component name for the CAPI provider deployment.
	// Limit removal logic is scoped to this component only.
	capiProviderComponentName = "capi-provider"
)

var (
	apiCriticalComponents = sets.New(
		"kube-apiserver",
		"openshift-apiserver",
		"openshift-oauth-apiserver",
		"oauth-openshift",
		"router",
		"packageserver",
	)

	configMapsToExcludeFromHash = []string{
		"client-ca",
	}
)

func (c *controlPlaneWorkload[T]) setDefaultOptions(cpContext ControlPlaneContext, workloadObj T, existingResources map[string]corev1.ResourceRequirements, oldWorkloadAnnotations map[string]string) error {
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

	// Reconcile container resource requirements.
	// The default contract preserves existing resources so that user overrides
	// (e.g. via kubectl edit) are not reverted.
	//
	// For the capi-provider component only, when the manifest intentionally
	// removes limits (no limits in manifest but limits exist on the live
	// object), the operator removes them automatically and records the
	// container in the limitsRemovedAnnotation. If the user later re-adds
	// limits to a container already listed in that annotation, the operator
	// detects this as a deliberate user override and preserves the user's
	// resources from that point on.
	for idx, container := range podTemplateSpec.Spec.Containers {
		existing, hasExisting := existingResources[container.Name]
		if !hasExisting {
			// No existing deployment or container is new — use manifest resources.
			continue
		}

		if c.Name() == capiProviderComponentName {
			manifestHasLimits := len(container.Resources.Limits) > 0
			existingHasLimits := len(existing.Limits) > 0

			if !manifestHasLimits && existingHasLimits {
				// Manifest says no limits but the live object has them.
				if limitsAlreadyRemoved(oldWorkloadAnnotations, container.Name) {
					// We already removed limits before, but the user re-added them.
					// Respect the user's override — preserve existing resources.
					podTemplateSpec.Spec.Containers[idx].Resources = existing
				} else {
					// First time: manifest wants no limits. Use manifest resources
					// (which have no limits) and record that we removed them.
					markLimitsRemoved(workloadObj, container.Name)
				}
				continue
			}
		}

		// Default: preserve existing resources.
		podTemplateSpec.Spec.Containers[idx].Resources = existing
	}

	// Record a hash of the final container resources on the pod template so
	// that DeepDerivative detects changes even when the new value is a subset
	// of the old (e.g. limits removed).
	if podTemplateSpec.Annotations == nil {
		podTemplateSpec.Annotations = map[string]string{}
	}
	podTemplateSpec.Annotations[containerResourcesHashAnnotation] = computeContainerResourcesHash(podTemplateSpec.Spec.Containers)

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
	if replicas > 1 && c.MultiZoneSpread() {
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
	// set additional Labels
	maps.Copy(podTemplate.Labels, hcp.Spec.Labels)
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
	podTemplate.Spec.Affinity.PodAffinity.PreferredDuringSchedulingIgnoredDuringExecution = []corev1.WeightedPodAffinityTerm{
		{
			Weight: 100,
			PodAffinityTerm: corev1.PodAffinityTerm{
				LabelSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						colocationLabelKey: clusterKey(hcp),
					},
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
	if isEtcdComponent(name) || apiCriticalComponents.Has(name) {
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

// limitsAlreadyRemoved returns true if the given container name is listed in the
// limitsRemovedAnnotation on the workload. This indicates the operator previously
// removed limits for this container, so if limits are now present, a user must
// have re-added them.
func limitsAlreadyRemoved(annotations map[string]string, containerName string) bool {
	val, ok := annotations[limitsRemovedAnnotation]
	if !ok || val == "" {
		return false
	}
	return slices.Contains(strings.Split(val, ","), containerName)
}

// markLimitsRemoved adds a container name to the limitsRemovedAnnotation on the
// workload object. The annotation value is a comma-separated list of container names.
func markLimitsRemoved(obj client.Object, containerName string) {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	existing := annotations[limitsRemovedAnnotation]
	if existing == "" {
		annotations[limitsRemovedAnnotation] = containerName
	} else {
		// Avoid duplicates.
		if slices.Contains(strings.Split(existing, ","), containerName) {
			return
		}
		annotations[limitsRemovedAnnotation] = existing + "," + containerName
	}
	obj.SetAnnotations(annotations)
}

// computeContainerResourcesHash computes a stable hash of container resource
// requirements. This hash is stored as a pod template annotation so that
// DeepDerivative detects resource changes even when the new value is a subset
// of the old (e.g. limits removed).
func computeContainerResourcesHash(containers []corev1.Container) string {
	type containerResources struct {
		Name      string                      `json:"name"`
		Resources corev1.ResourceRequirements `json:"resources"`
	}
	resources := make([]containerResources, 0, len(containers))
	for _, c := range containers {
		resources = append(resources, containerResources{
			Name:      c.Name,
			Resources: c.Resources,
		})
	}
	sort.Slice(resources, func(i, j int) bool {
		return resources[i].Name < resources[j].Name
	})
	data, _ := json.Marshal(resources)
	hash := sha256.Sum256(data)
	return fmt.Sprintf("%x", hash[:8])
}
