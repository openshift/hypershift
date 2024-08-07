package controlplanecomponent

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/metrics"
	"github.com/openshift/hypershift/support/upsert"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

type NamedComponent interface {
	Name() string
}
type ControlPlaneComponent interface {
	NamedComponent
	Reconcile(cpContext ControlPlaneContext) error
	// TODO:
	// ReconcileStatus(cpContext ControlPlaneContext) error
}

type ControlPlaneContext struct {
	context.Context

	Client                   client.Client
	HCP                      *hyperv1.HostedControlPlane
	CreateOrUpdate           upsert.CreateOrUpdateFN
	ReleaseImageProvider     *imageprovider.ReleaseImageProvider
	UserReleaseImageProvider *imageprovider.ReleaseImageProvider

	SetDefaultSecurityContext bool
	MetricsSet                metrics.MetricsSet
}

type DeploymentReconciler interface {
	NamedComponent
	ReconcileDeployment(cpContext ControlPlaneContext, deployment *appsv1.Deployment) error
}

type StatefulSetReconciler interface {
	NamedComponent
	ReconcileStatefulSet(cpContext ControlPlaneContext, statefulSet *appsv1.StatefulSet) error
}

var _ ControlPlaneComponent = &ControlPlaneWorkload{}

type ControlPlaneWorkload struct {
	// one of DeploymentReconciler or StatefulSetReconciler is required
	DeploymentReconciler
	StatefulSetReconciler

	// optional
	RBACReconciler
	// reconiclers for Secret, ConfigMap, Service, ServiceMonitor, etc.
	ResourcesReconcilers []GenericReconciler
	// Predicate is called at the begining, the component is disabled if it returns false.
	Predicate func(cpContext ControlPlaneContext) (bool, error)

	MultiZoneSpreadLabels    map[string]string
	IsRequestServing         bool
	NeedsManagementKASAccess bool

	// if provided, a konnectivity proxy container and required volumes will be injected into the deployment.
	KonnectivityContainerOpts *KonnectivityContainerOptions
}

// Name implements ControlPlaneComponent.
func (c *ControlPlaneWorkload) Name() string {
	if c.DeploymentReconciler != nil {
		return c.DeploymentReconciler.Name()
	} else {
		return c.StatefulSetReconciler.Name()
	}

}

// reconcile implements ControlPlaneComponent.
func (c *ControlPlaneWorkload) Reconcile(cpContext ControlPlaneContext) error {
	if c.Predicate != nil {
		shouldReconcile, err := c.Predicate(cpContext)
		if err != nil {
			return err
		}
		if !shouldReconcile {
			return nil
		}
	}

	hcp := cpContext.HCP
	ownerRef := config.OwnerRefFrom(hcp)
	// reconcile resources such as ConfigMaps and Secrets first, as the deployment might depend on them.
	for _, reconciler := range c.ResourcesReconcilers {
		if reconciler.Predicatefn != nil && !reconciler.Predicatefn(cpContext) {
			continue
		}

		resource := reconciler.Manifestfn(hcp.Namespace)
		if _, err := cpContext.CreateOrUpdate(cpContext, cpContext.Client, resource, func() error {
			// ensure owner reference is set on all resources.
			ownerRef.ApplyTo(resource)
			if reconciler.ReconcileFn != nil {
				return reconciler.ReconcileFn(cpContext, resource)
			}
			return nil
		}); err != nil {
			return err
		}
	}

	// reconcile RBAC if RBACReconciler is provided.
	if err := c.reconcileRBAC(cpContext); err != nil {
		return fmt.Errorf("failed to reconcile RBAC for component '%s': %v", c.Name(), err)
	}

	if c.DeploymentReconciler != nil {
		return c.reconcileDeployment(cpContext)
	} else {
		return c.reconcileStatefulSet(cpContext)
	}
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

func (c *ControlPlaneWorkload) reconcileStatefulSet(cpContext ControlPlaneContext) error {
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

		if err := c.StatefulSetReconciler.ReconcileStatefulSet(cpContext, statefulSet); err != nil {
			return err
		}

		c.applyOptionsToStatefulSet(cpContext, statefulSet, existingResources, existingLabelSelector)
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile component's statefulSet: %v", err)
	}

	return nil
}

func (c *ControlPlaneWorkload) reconcileRBAC(cpContext ControlPlaneContext) error {
	if c.RBACReconciler == nil {
		return nil
	}

	hcp := cpContext.HCP
	ownerRef := config.OwnerRefFrom(hcp)

	serviceAccount := serviceAccountManifest(c.Name(), hcp.Namespace)
	if _, err := cpContext.CreateOrUpdate(cpContext, cpContext.Client, serviceAccount, func() error {
		ownerRef.ApplyTo(serviceAccount)
		return c.reconcileServiceAccount(cpContext, serviceAccount)
	}); err != nil {
		return err
	}

	role := roleManifest(c.Name(), hcp.Namespace)
	if _, err := cpContext.CreateOrUpdate(cpContext, cpContext.Client, role, func() error {
		ownerRef.ApplyTo(role)
		return c.reconcileRole(cpContext, role)
	}); err != nil {
		return err
	}

	roleBinding := roleBindingManifest(c.Name(), hcp.Namespace)
	if _, err := cpContext.CreateOrUpdate(cpContext, cpContext.Client, roleBinding, func() error {
		ownerRef.ApplyTo(roleBinding)
		return c.reconcileRoleBinding(cpContext, roleBinding, role, serviceAccount)
	}); err != nil {
		return err
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

func (c *ControlPlaneWorkload) applyOptionsToStatefulSet(cpContext ControlPlaneContext, statefulSet *appsv1.StatefulSet, existingResources map[string]corev1.ResourceRequirements, existingLabelSelector *metav1.LabelSelector) {
	deploymentConfig := c.defaultDeploymentConfig(cpContext, statefulSet.Spec.Replicas)
	deploymentConfig.Resources = existingResources
	deploymentConfig.ApplyToStatefulSet(statefulSet)

	statefulSet.Spec.Template.Spec.AutomountServiceAccountToken = ptr.To(c.NeedsManagementKASAccess)
	if existingLabelSelector != nil {
		statefulSet.Spec.Selector = existingLabelSelector
	}

	if c.KonnectivityContainerOpts != nil {
		c.KonnectivityContainerOpts.injectKonnectivityContainer(cpContext, &statefulSet.Spec.Template.Spec)
	}
}

func (c *ControlPlaneWorkload) defaultDeploymentConfig(cpContext ControlPlaneContext, desiredReplicas *int32) *config.DeploymentConfig {
	hcp := cpContext.HCP

	deploymentConfig := &config.DeploymentConfig{
		SetDefaultSecurityContext: cpContext.SetDefaultSecurityContext,
	}
	deploymentConfig.Scheduling.PriorityClass = config.DefaultPriorityClass
	if hcp.Annotations[hyperv1.ControlPlanePriorityClass] != "" {
		deploymentConfig.Scheduling.PriorityClass = hcp.Annotations[hyperv1.ControlPlanePriorityClass]
	}

	if c.NeedsManagementKASAccess {
		deploymentConfig.AdditionalLabels = map[string]string{
			config.NeedManagementKASAccessLabel: "true",
		}
	}

	var replicas *int
	if desiredReplicas != nil {
		replicas = ptr.To(int(*desiredReplicas))
	}
	if c.IsRequestServing {
		deploymentConfig.SetRequestServingDefaults(hcp, c.MultiZoneSpreadLabels, replicas)
	} else {
		deploymentConfig.SetDefaults(hcp, c.MultiZoneSpreadLabels, replicas)
	}

	deploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	return deploymentConfig
}
