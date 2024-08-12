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
}

func (c *ControlPlaneWorkload) reconcileDeployment(cpContext ControlPlaneContext) error {
	hcp := cpContext.HCP
	ownerRef := config.OwnerRefFrom(hcp)

	deployment := deploymentManifest(c.Name(), hcp.Namespace)
	if _, err := cpContext.CreateOrUpdate(cpContext, cpContext.Client, deployment, func() error {
		ownerRef.ApplyTo(deployment)

		// preserve existing resource requirements, this needs to be done before calling c.reconcileDeployment() which might override the resources requirements.
		existingResources := make(map[string]corev1.ResourceRequirements)
		for _, container := range deployment.Spec.Template.Spec.Containers {
			existingResources[container.Name] = container.Resources
		}
		// preserve old label selector if it exist, this field is immutable and shouldn't be changed for the lifecycle of the component.
		existingLabelSelector := deployment.Spec.Selector.DeepCopy()

		if err := c.DeploymentReconciler.ReconcileDeployment(cpContext, deployment); err != nil {
			return err
		}

		c.applyOptionsToDeployment(cpContext, deployment, existingResources, existingLabelSelector)
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile component's deployment: %v", err)
	}

	return nil
}

func (c *ControlPlaneWorkload) applyOptionsToDeployment(cpContext ControlPlaneContext, deployment *appsv1.Deployment, existingResources map[string]corev1.ResourceRequirements, existingLabelSelector *metav1.LabelSelector) {
	deploymentConfig := c.defaultDeploymentConfig(cpContext, deployment.Spec.Replicas)
	deploymentConfig.Resources = existingResources
	deploymentConfig.ApplyTo(deployment)

	deployment.Spec.Template.Spec.AutomountServiceAccountToken = ptr.To(c.NeedsManagementKASAccess)
	if existingLabelSelector != nil {
		deployment.Spec.Selector = existingLabelSelector
	}

	if c.KonnectivityContainerOpts != nil {
		c.KonnectivityContainerOpts.injectKonnectivityContainer(cpContext, &deployment.Spec.Template.Spec)
	}
}
