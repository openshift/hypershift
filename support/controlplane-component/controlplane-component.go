package controlplanecomponent

import (
	"context"
	"fmt"
	"slices"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/infra"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/metrics"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type NamedComponent interface {
	Name() string
}
type ControlPlaneComponent interface {
	NamedComponent
	Reconcile(cpContext ControlPlaneContext) error
}

type ControlPlaneContext struct {
	context.Context

	upsert.CreateOrUpdateProviderV2
	Client                   client.Client
	HCP                      *hyperv1.HostedControlPlane
	ReleaseImageProvider     imageprovider.ReleaseImageProvider
	UserReleaseImageProvider imageprovider.ReleaseImageProvider

	InfraStatus               infra.InfrastructureStatus
	SetDefaultSecurityContext bool
	EnableCIDebugOutput       bool
	MetricsSet                metrics.MetricsSet

	// This is needed for the generic unit test, so we can always generate a fixture for the components deployment/statefulset.
	SkipPredicate bool
}

var _ ControlPlaneComponent = &controlPlaneWorkload{}

type workloadType string

const (
	deploymentWorkloadType  workloadType = "Deployment"
	statefulSetWorkloadType workloadType = "StatefulSet"
)

type ComponentOptions interface {
	IsRequestServing() bool
	MultiZoneSpread() bool
	NeedsManagementKASAccess() bool
}

// TODO: add unit test
type controlPlaneWorkload struct {
	ComponentOptions

	name         string
	workloadType workloadType

	// list of component names that this component depends on.
	// reconcilation will be blocked until all dependencies are available.
	dependencies []string

	adapt func(cpContext ControlPlaneContext, obj client.Object) error

	// adapters for Secret, ConfigMap, Service, ServiceMonitor, etc.
	manifestsAdapters map[string]genericAdapter
	// predicate is called at the begining, the component is disabled if it returns false.
	predicate func(cpContext ControlPlaneContext) (bool, error)
	// These resources will cause the Deployment/statefulset to rollout when changed.
	watchedResources []client.Object

	// if provided, konnectivity proxy container and required volumes will be injected into the deployment/statefulset.
	konnectivityContainerOpts *KonnectivityContainerOptions
	// if provided, availabilityProber container and required volumes will be injected into the deployment/statefulset.
	availabilityProberOpts *util.AvailabilityProberOpts
}

// Name implements ControlPlaneComponent.
func (c *controlPlaneWorkload) Name() string {
	return c.name
}

// reconcile implements ControlPlaneComponent.
func (c *controlPlaneWorkload) Reconcile(cpContext ControlPlaneContext) error {
	if !cpContext.SkipPredicate && c.predicate != nil {
		shouldReconcile, err := c.predicate(cpContext)
		if err != nil {
			return err
		}
		if !shouldReconcile {
			return nil
		}
	}

	unavailableDependencies, err := c.checkDependencies(cpContext)
	if err != nil {
		return fmt.Errorf("failed checking for dependencies availability: %v", err)
	}
	var reconcilationError error
	if len(unavailableDependencies) == 0 {
		// reconcile only when all dependencies are available, and don't return error immediatly so it can be included in the status condition first.
		reconcilationError = c.update(cpContext)
	}

	component := &hyperv1.ControlPlaneComponent{
		ObjectMeta: metav1.ObjectMeta{
			Name:      c.Name(),
			Namespace: cpContext.HCP.Namespace,
		},
	}

	if _, err := controllerutil.CreateOrPatch(cpContext, cpContext.Client, component, func() error {
		return c.reconcileComponentStatus(cpContext, component, unavailableDependencies, reconcilationError)
	}); err != nil {
		return err
	}
	return reconcilationError
}

// reconcile implements ControlPlaneComponent.
func (c *controlPlaneWorkload) update(cpContext ControlPlaneContext) error {
	hcp := cpContext.HCP
	ownerRef := config.OwnerRefFrom(hcp)
	// reconcile resources such as ConfigMaps and Secrets first, as the deployment might depend on them.
	if err := assets.ForEachManifest(c.name, func(manifestName string) error {
		adapter, exist := c.manifestsAdapters[manifestName]
		if exist {
			return adapter.reconcile(cpContext, c.Name(), manifestName)
		}

		obj, _, err := assets.LoadManifest(c.name, manifestName)
		if err != nil {
			return err
		}
		obj.SetNamespace(hcp.Namespace)
		ownerRef.ApplyTo(obj)

		switch typedObj := obj.(type) {
		case *rbacv1.RoleBinding:
			for i := range typedObj.Subjects {
				if typedObj.Subjects[i].Kind == "ServiceAccount" {
					typedObj.Subjects[i].Namespace = hcp.Namespace
				}
			}
		}

		if _, err := cpContext.CreateOrUpdateV2(cpContext, cpContext.Client, obj); err != nil {
			return err
		}

		return nil
	}); err != nil {
		return err
	}

	return c.reconcileWorkload(cpContext)
}

func (c *controlPlaneWorkload) reconcileWorkload(cpContext ControlPlaneContext) error {
	var workloadObj client.Object
	var oldWorkloadObj client.Object

	switch c.workloadType {
	case deploymentWorkloadType:
		dep, err := assets.LoadDeploymentManifest(c.Name())
		if err != nil {
			return fmt.Errorf("faild loading deployment manifest: %v", err)
		}
		workloadObj = dep
		oldWorkloadObj = &appsv1.Deployment{}
	case statefulSetWorkloadType:
		sts, err := assets.LoadStatefulSetManifest(c.Name())
		if err != nil {
			return fmt.Errorf("faild loading statefulset manifest: %v", err)
		}
		workloadObj = sts
		oldWorkloadObj = &appsv1.StatefulSet{}
	}
	// make sure that the Deployment/Statefulset name matches the component name.
	workloadObj.SetName(c.Name())
	workloadObj.SetNamespace(cpContext.HCP.Namespace)

	if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(workloadObj), oldWorkloadObj); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get old workload object: %v", err)
		}
	}

	ownerRef := config.OwnerRefFrom(cpContext.HCP)
	ownerRef.ApplyTo(workloadObj)
	if c.adapt != nil {
		if err := c.adapt(cpContext, workloadObj); err != nil {
			return err
		}
	}

	switch c.workloadType {
	case deploymentWorkloadType:
		if err := c.applyOptionsToDeployment(cpContext, workloadObj.(*appsv1.Deployment), oldWorkloadObj.(*appsv1.Deployment)); err != nil {
			return err
		}
	case statefulSetWorkloadType:
		if err := c.applyOptionsToStatefulSet(cpContext, workloadObj.(*appsv1.StatefulSet), oldWorkloadObj.(*appsv1.StatefulSet)); err != nil {
			return err
		}
	}

	if _, err := cpContext.CreateOrUpdateV2(cpContext, cpContext.Client, workloadObj); err != nil {
		return err
	}
	return nil
}

func (c *controlPlaneWorkload) applyOptionsToDeployment(cpContext ControlPlaneContext, deployment *appsv1.Deployment, oldDeployment *appsv1.Deployment) error {
	// preserve existing resource requirements.
	existingResources := make(map[string]corev1.ResourceRequirements)
	for _, container := range oldDeployment.Spec.Template.Spec.Containers {
		existingResources[container.Name] = container.Resources
	}
	// preserve old label selector if it exist, this field is immutable and shouldn't be changed for the lifecycle of the component.
	if oldDeployment.Spec.Selector != nil {
		deployment.Spec.Selector = oldDeployment.Spec.Selector.DeepCopy()
	}

	deploymentConfig, err := c.defaultOptions(cpContext, &deployment.Spec.Template, deployment.Spec.Replicas, existingResources)
	if err != nil {
		return err
	}
	deploymentConfig.ApplyTo(deployment)
	return nil
}

func (c *controlPlaneWorkload) applyOptionsToStatefulSet(cpContext ControlPlaneContext, statefulSet *appsv1.StatefulSet, oldStatefulSet *appsv1.StatefulSet) error {
	// preserve existing resource requirements.
	existingResources := make(map[string]corev1.ResourceRequirements)
	for _, container := range oldStatefulSet.Spec.Template.Spec.Containers {
		existingResources[container.Name] = container.Resources
	}
	// preserve old label selector if it exist, this field is immutable and shouldn't be changed for the lifecycle of the component.
	if oldStatefulSet.Spec.Selector != nil {
		statefulSet.Spec.Selector = oldStatefulSet.Spec.Selector.DeepCopy()
	}

	deploymentConfig, err := c.defaultOptions(cpContext, &statefulSet.Spec.Template, statefulSet.Spec.Replicas, existingResources)
	if err != nil {
		return err
	}
	deploymentConfig.ApplyToStatefulSet(statefulSet)
	return nil
}

func (c *controlPlaneWorkload) defaultOptions(cpContext ControlPlaneContext, podTemplateSpec *corev1.PodTemplateSpec, desiredReplicas *int32, existingResources map[string]corev1.ResourceRequirements) (*config.DeploymentConfig, error) {
	if _, exist := podTemplateSpec.Annotations[config.NeedMetricsServerAccessLabel]; exist || c.NeedsManagementKASAccess() {
		podTemplateSpec.Spec.AutomountServiceAccountToken = ptr.To(true)
	} else {
		podTemplateSpec.Spec.AutomountServiceAccountToken = ptr.To(false)
	}

	enforceVolumesDefaultMode(&podTemplateSpec.Spec)

	if err := replaceContainersImageFromPayload(cpContext.ReleaseImageProvider, cpContext.HCP, podTemplateSpec.Spec.Containers); err != nil {
		return nil, err
	}
	if err := replaceContainersImageFromPayload(cpContext.ReleaseImageProvider, cpContext.HCP, podTemplateSpec.Spec.InitContainers); err != nil {
		return nil, err
	}

	if err := c.applyWatchedResourcesAnnotation(cpContext, podTemplateSpec); err != nil {
		return nil, err
	}

	if c.konnectivityContainerOpts != nil {
		c.konnectivityContainerOpts.injectKonnectivityContainer(cpContext, &podTemplateSpec.Spec)
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
		Resources:                 existingResources,
		AdditionalLabels: map[string]string{
			hyperv1.ControlPlaneComponentLabel: c.Name(),
		},
	}
	deploymentConfig.Scheduling.PriorityClass = getPriorityClass(c.Name(), cpContext.HCP)

	if c.NeedsManagementKASAccess() {
		deploymentConfig.AdditionalLabels[config.NeedManagementKASAccessLabel] = "true"
	}

	var replicas *int
	if desiredReplicas != nil {
		replicas = ptr.To(int(*desiredReplicas))
	}
	var multiZoneSpreadLabels map[string]string
	if c.MultiZoneSpread() {
		multiZoneSpreadLabels = podTemplateSpec.ObjectMeta.Labels
	}
	if c.IsRequestServing() {
		deploymentConfig.SetRequestServingDefaults(cpContext.HCP, multiZoneSpreadLabels, replicas)
	} else {
		deploymentConfig.SetDefaults(cpContext.HCP, multiZoneSpreadLabels, replicas)
	}
	deploymentConfig.SetRestartAnnotation(cpContext.HCP.ObjectMeta)

	return deploymentConfig, nil
}

func (c *controlPlaneWorkload) applyWatchedResourcesAnnotation(cpContext ControlPlaneContext, podTemplate *corev1.PodTemplateSpec) error {
	if c.watchedResources == nil {
		return nil
	}

	var hashedData []string
	for _, resource := range c.watchedResources {
		if err := cpContext.Client.Get(cpContext, client.ObjectKey{Namespace: cpContext.HCP.Namespace, Name: resource.GetName()}, resource); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return err
		}

		switch obj := resource.(type) {
		case *corev1.ConfigMap:
			for _, value := range obj.Data {
				hashedData = append(hashedData, util.HashSimple(value))
			}
		case *corev1.Secret:
			for _, value := range obj.Data {
				hashedData = append(hashedData, util.HashSimple(value))
			}
		}
	}
	// if not sorted, we could get a different value on each reconcilation loop and cause unneeded rollout.
	slices.Sort(hashedData)

	if podTemplate.Annotations == nil {
		podTemplate.Annotations = map[string]string{}
	}
	podTemplate.Annotations["component.hypershift.openshift.io/config-hash"] = strings.Join(hashedData, "")
	return nil
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
			// This could happen for example in local dev enviroments if the "OPERATE_ON_RELEASE_IMAGE" env variable is not set.
			containers[i].Image = util.HCPControlPlaneReleaseImage(hcp)
		}
	}

	return nil
}

var (
	apiCriticalComponents = sets.New(
		"kube-apiserver",
		"openshift-apiserver",
		"openshift-oauth-apiserver",
	)
)

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
