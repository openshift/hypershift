package controlplanecomponent

import (
	"fmt"

	"github.com/openshift/hypershift/support/config"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

type DeploymentReconciler interface {
	NamedComponent
	ReconcileDeployment(cpContext ControlPlaneContext, deployment *appsv1.Deployment) error
	Volumes(cpContext ControlPlaneContext) Volumes
}

func (c *controlPlaneWorkload) reconcileDeployment(cpContext ControlPlaneContext) error {
	hcp := cpContext.HCP
	ownerRef := config.OwnerRefFrom(hcp)

	deployment, err := LoadDeploymentManifest(c.Name())
	if err != nil {
		return fmt.Errorf("faild loading deployment manifest: %v", err)
	}
	deployment.SetNamespace(cpContext.HCP.Namespace)
	if _, err := cpContext.CreateOrUpdate(cpContext, cpContext.Client, deployment, func() error {
		ownerRef.ApplyTo(deployment)

		// preserve existing resource requirements, this needs to be done before calling c.reconcileDeployment() which might override the resources requirements.
		existingResources := make(map[string]corev1.ResourceRequirements)
		for _, container := range deployment.Spec.Template.Spec.Containers {
			existingResources[container.Name] = container.Resources
		}
		// preserve old label selector if it exist, this field is immutable and shouldn't be changed for the lifecycle of the component.
		existingLabelSelector := deployment.Spec.Selector.DeepCopy()

		if err := c.deploymentReconciler.ReconcileDeployment(cpContext, deployment); err != nil {
			return err
		}

		c.applyOptionsToDeployment(cpContext, deployment, existingResources, existingLabelSelector)
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile component's deployment: %v", err)
	}

	return nil
}

func (c *controlPlaneWorkload) applyOptionsToDeployment(cpContext ControlPlaneContext, deployment *appsv1.Deployment, existingResources map[string]corev1.ResourceRequirements, existingLabelSelector *metav1.LabelSelector) error {
	// apply volumes first, as deploymentConfig checks for local volumes to set PodSafeToEvictLocalVolumesKey annotation
	// c.deploymentReconciler.Volumes(cpContext).ApplyTo(&deployment.Spec.Template.Spec)

	deploymentConfig := c.defaultDeploymentConfig(cpContext, deployment.Spec.Replicas)
	deploymentConfig.Resources = existingResources
	deploymentConfig.ApplyTo(deployment)

	deployment.Spec.Template.Spec.AutomountServiceAccountToken = ptr.To(c.needsManagementKASAccess)
	if existingLabelSelector != nil {
		deployment.Spec.Selector = existingLabelSelector
	}

	if c.konnectivityContainerOpts != nil {
		c.konnectivityContainerOpts.injectKonnectivityContainer(cpContext, &deployment.Spec.Template.Spec)
	}

	return c.applyWatchedResourcesAnnotation(cpContext, &deployment.Spec.Template)
}
