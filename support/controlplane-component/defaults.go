package controlplanecomponent

import (
	"fmt"
	"slices"
	"strings"

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
	)
)

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
