package controlplanecomponent

import (
	"fmt"

	"github.com/openshift/hypershift/support/config"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

type StatefulSetReconciler interface {
	NamedComponent
	ReconcileStatefulSet(cpContext ControlPlaneContext, statefulSet *appsv1.StatefulSet) error
	Volumes(cpContext ControlPlaneContext) Volumes
}

func (c *controlPlaneWorkload) reconcileStatefulSet(cpContext ControlPlaneContext) error {
	hcp := cpContext.HCP
	ownerRef := config.OwnerRefFrom(hcp)

	statefulSet := statefulSetManifest(c.Name(), hcp.Namespace)
	if _, err := cpContext.CreateOrUpdate(cpContext, cpContext.Client, statefulSet, func() error {
		ownerRef.ApplyTo(statefulSet)

		// preserve existing resource requirements, this needs to be done before calling c.ReconcileStatefulSet() which might override the resources requirements.
		existingResources := make(map[string]corev1.ResourceRequirements)
		for _, container := range statefulSet.Spec.Template.Spec.Containers {
			existingResources[container.Name] = container.Resources
		}
		// preserve old label selector if it exist, this field is immutable and shouldn't be changed for the lifecycle of the component.
		existingLabelSelector := statefulSet.Spec.Selector.DeepCopy()

		if err := c.statefulSetReconciler.ReconcileStatefulSet(cpContext, statefulSet); err != nil {
			return err
		}

		return c.applyOptionsToStatefulSet(cpContext, statefulSet, existingResources, existingLabelSelector)
	}); err != nil {
		return fmt.Errorf("failed to reconcile component's statefulSet: %v", err)
	}

	return nil
}

func (c *controlPlaneWorkload) applyOptionsToStatefulSet(cpContext ControlPlaneContext, statefulSet *appsv1.StatefulSet, existingResources map[string]corev1.ResourceRequirements, existingLabelSelector *metav1.LabelSelector) error {
	deploymentConfig := c.defaultDeploymentConfig(cpContext, statefulSet.Spec.Replicas)
	deploymentConfig.Resources = existingResources
	deploymentConfig.ApplyToStatefulSet(statefulSet)

	statefulSet.Spec.Template.Spec.AutomountServiceAccountToken = ptr.To(c.needsManagementKASAccess)
	if existingLabelSelector != nil {
		statefulSet.Spec.Selector = existingLabelSelector
	}

	c.statefulSetReconciler.Volumes(cpContext).ApplyTo(&statefulSet.Spec.Template.Spec)

	if c.konnectivityContainerOpts != nil {
		c.konnectivityContainerOpts.injectKonnectivityContainer(cpContext, &statefulSet.Spec.Template.Spec)
	}

	return c.applyWatchedResourcesAnnotation(cpContext, &statefulSet.Spec.Template)
}
