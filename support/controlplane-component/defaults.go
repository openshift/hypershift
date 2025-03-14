package controlplanecomponent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
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

func (c *controlPlaneWorkload[T]) defaultOptions(cpContext ControlPlaneContext, podTemplateSpec *corev1.PodTemplateSpec, desiredReplicas *int32) (*config.DeploymentConfig, error) {
	if _, exist := podTemplateSpec.Annotations[config.NeedMetricsServerAccessLabel]; exist || c.NeedsManagementKASAccess() ||
		c.Name() == "packageserver" { // TODO: investigate why packageserver needs AutomountServiceAccountToken or set NeedsManagementKASAccess to true.
		podTemplateSpec.Spec.AutomountServiceAccountToken = ptr.To(true)
	} else {
		podTemplateSpec.Spec.AutomountServiceAccountToken = ptr.To(false)
	}

	enforceVolumesDefaultMode(&podTemplateSpec.Spec)
	err := enforceImagePullPolicy(podTemplateSpec.Spec.Containers)
	if err != nil {
		return nil, err
	}

	if err := replaceContainersImageFromPayload(cpContext.ReleaseImageProvider, cpContext.HCP, podTemplateSpec.Spec.Containers); err != nil {
		return nil, err
	}
	if err := replaceContainersImageFromPayload(cpContext.ReleaseImageProvider, cpContext.HCP, podTemplateSpec.Spec.InitContainers); err != nil {
		return nil, err
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
		return nil, err
	}

	if c.availabilityProberOpts != nil {
		availabilityProberImage := cpContext.ReleaseImageProvider.GetImage(util.AvailabilityProberImageName)
		util.AvailabilityProber(
			kas.InClusterKASReadyURL(cpContext.HCP.Spec.Platform.Type), availabilityProberImage,
			&podTemplateSpec.Spec,
			util.WithOptions(c.availabilityProberOpts))
	}

	deploymentConfig := &config.DeploymentConfig{
		SetDefaultSecurityContext: cpContext.SetDefaultSecurityContext,
		AdditionalLabels: map[string]string{
			hyperv1.ControlPlaneComponentLabel: c.Name(),
		},
	}
	deploymentConfig.Scheduling.PriorityClass = getPriorityClass(c.Name(), cpContext.HCP)

	if c.NeedsManagementKASAccess() {
		deploymentConfig.AdditionalLabels[config.NeedManagementKASAccessLabel] = "true"
	}

	replicas := defaultReplicas(c.Name(), cpContext.HCP)
	if desiredReplicas != nil {
		replicas = int(*desiredReplicas)
	}

	var multiZoneSpreadLabels map[string]string
	if c.MultiZoneSpread() {
		multiZoneSpreadLabels = podTemplateSpec.ObjectMeta.Labels
	}

	if c.IsRequestServing() {
		deploymentConfig.SetRequestServingDefaults(cpContext.HCP, multiZoneSpreadLabels, ptr.To(replicas))
	} else {
		deploymentConfig.SetDefaults(cpContext.HCP, multiZoneSpreadLabels, ptr.To(replicas))
	}
	deploymentConfig.SetRestartAnnotation(cpContext.HCP.ObjectMeta)

	return deploymentConfig, nil
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

type ValueHash struct {
	Key       string `json:"k,omitempty"`
	ValueHash string `json:"h,omitempty"`
}

type ValueHashes []ValueHash

func (vh ValueHashes) Len() int {
	return len(vh)
}

func (vh ValueHashes) Swap(i, j int) {
	tmp := vh[i]
	vh[i] = vh[j]
	vh[j] = tmp
}

func (vh ValueHashes) Less(i, j int) bool {
	return vh[i].Key < vh[j].Key
}

type ResourceHashType int

var (
	SecretType    ResourceHashType = 0
	ConfigMapType ResourceHashType = 1
)

type ResourceHash struct {
	Type   ResourceHashType `json:"t,omitempty"`
	Name   string           `json:"n,omitempty"`
	Values ValueHashes      `json:"v,omitempty"`
}

func (rh *ResourceHash) SortValues() {
	sort.Sort(rh.Values)
}

type ResourceHashes []ResourceHash

func (rhs ResourceHashes) Len() int {
	return len(rhs)
}

func (rhs ResourceHashes) Swap(i, j int) {
	tmp := rhs[i]
	rhs[i] = rhs[j]
	rhs[j] = tmp
}

func (rhs ResourceHashes) Less(i, j int) bool {
	if rhs[i].Type == rhs[j].Type {
		return rhs[i].Name < rhs[j].Name
	}
	return rhs[i].Type < rhs[j].Type
}

func (rhs ResourceHashes) SortValues() {
	for i := range rhs {
		rhs[i].SortValues()
	}
}

func (rhs ResourceHashes) String() (string, error) {
	rhs.SortValues()
	sort.Sort(rhs)
	result, err := json.Marshal(rhs)
	if err != nil {
		return "", err
	}
	return string(result), nil
}

func computeResourceHash(secretNames, configMapNames []string,
	fetchSecret func(string) (*corev1.Secret, error),
	fetchConfigMap func(string) (*corev1.ConfigMap, error),
) (string, error) {
	hashes := ResourceHashes{}
	for _, name := range secretNames {
		rHash := ResourceHash{
			Type: SecretType,
			Name: name,
		}
		secret, err := fetchSecret(name)
		if err != nil {
			return "", err
		}
		for key, value := range secret.Data {
			rHash.Values = append(rHash.Values, ValueHash{
				Key:       key,
				ValueHash: util.HashSimple(value),
			})
		}
		hashes = append(hashes, rHash)
	}

	for _, name := range configMapNames {
		rHash := ResourceHash{
			Type: ConfigMapType,
			Name: name,
		}
		configMap, err := fetchConfigMap(name)
		if err != nil {
			return "", err
		}
		for key, value := range configMap.Data {
			rHash.Values = append(rHash.Values, ValueHash{
				Key:       key,
				ValueHash: util.HashSimple(value),
			})
		}
		hashes = append(hashes, rHash)
	}
	return hashes.String()
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
		containers[i].ImagePullPolicy = corev1.PullIfNotPresent
	}
	return nil
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
		}
	}

	return nil
}

func getPriorityClass(componentName string, hcp *hyperv1.HostedControlPlane) string {
	priorityClass := config.DefaultPriorityClass
	overrideAnnotation := hyperv1.ControlPlanePriorityClass

	if componentName == etcdComponentName {
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

func defaultReplicas(componentName string, hcp *hyperv1.HostedControlPlane) int {
	if hcp.Spec.ControllerAvailabilityPolicy == hyperv1.SingleReplica {
		return 1
	}

	// HighlyAvailable
	if componentName == etcdComponentName || apiCriticalComponents.Has(componentName) {
		return 3
	}
	return 2
}
