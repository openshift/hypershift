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

	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ControlPlaneComponent interface {
	Reconcile(cpContext ControlPlaneContext) error
	Delete(cpContext ControlPlaneContext) error

	Name() string
}

type ControlPlaneContext struct {
	context.Context

	Client                   client.Client
	Hcp                      *hyperv1.HostedControlPlane
	CreateOrUpdate           upsert.CreateOrUpdateFN
	ReleaseImageProvider     *imageprovider.ReleaseImageProvider
	UserReleaseImageProvider *imageprovider.ReleaseImageProvider

	SetDefaultSecurityContext bool
	MetricsSet                metrics.MetricsSet
}

type DeploymentReconciler interface {
	ReconcileDeployment(cpContext ControlPlaneContext, deployment *appsv1.Deployment) error
	// Predicate is called at the begining, the component is disabled if it returns false.
	Predicate(cpContext ControlPlaneContext) (bool, error)

	Name() string
}

var _ ControlPlaneComponent = &ControlPlaneDeployment{}

type ControlPlaneDeployment struct {
	// required
	DeploymentReconciler

	// optional
	RBACReconciler
	// reconiclers for Secret, ConfigMap, Service, ServiceMonitor, etc.
	ResourcesReconcilers []GenericReconciler

	MultiZoneSpreadLabels    map[string]string
	IsRequestServing         bool
	NeedsManagementKASAccess bool
}

// Name implements ControlPlaneComponent.
func (c *ControlPlaneDeployment) Name() string {
	return c.DeploymentReconciler.Name()
}

// delete implements ControlPlaneComponent.
func (c *ControlPlaneDeployment) Delete(cpContext ControlPlaneContext) error {
	return nil
}

// reconcile implements ControlPlaneComponent.
func (c *ControlPlaneDeployment) Reconcile(cpContext ControlPlaneContext) error {
	shouldReconcile, err := c.Predicate(cpContext)
	if err != nil {
		return err
	}
	if !shouldReconcile {
		return nil
	}

	hcp := cpContext.Hcp
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

	deployment := deploymentManifest(c.Name(), hcp.Namespace)
	if _, err := cpContext.CreateOrUpdate(cpContext, cpContext.Client, deployment, func() error {
		ownerRef.ApplyTo(deployment)

		deploymentConfig := &config.DeploymentConfig{
			Resources: make(map[string]corev1.ResourceRequirements),
		}
		// preserve existing resource requirements, this needs to be done before calling c.reconcileDeployment() which might override the resources requirements.
		for _, container := range deployment.Spec.Template.Spec.Containers {
			deploymentConfig.Resources[container.Name] = container.Resources
		}

		// reconcile deployment
		if err := c.ReconcileDeployment(cpContext, deployment); err != nil {
			return err
		}

		c.setDefaults(cpContext, deploymentConfig, deployment)
		return nil
	}); err != nil {
		return err
	}

	return nil
}

func (c *ControlPlaneDeployment) reconcileRBAC(cpContext ControlPlaneContext) error {
	if c.RBACReconciler == nil {
		return nil
	}

	hcp := cpContext.Hcp
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

func (c *ControlPlaneDeployment) setDefaults(cpContext ControlPlaneContext, deploymentConfig *config.DeploymentConfig, deployment *appsv1.Deployment) {
	hcp := cpContext.Hcp

	deploymentConfig.SetDefaultSecurityContext = cpContext.SetDefaultSecurityContext
	deploymentConfig.Scheduling.PriorityClass = config.DefaultPriorityClass
	if hcp.Annotations[hyperv1.ControlPlanePriorityClass] != "" {
		deploymentConfig.Scheduling.PriorityClass = hcp.Annotations[hyperv1.ControlPlanePriorityClass]
	}

	if c.NeedsManagementKASAccess {
		deploymentConfig.AdditionalLabels = map[string]string{
			config.NeedManagementKASAccessLabel: "true",
		}
	}
	deployment.Spec.Template.Spec.AutomountServiceAccountToken = ptr.To(c.NeedsManagementKASAccess)

	var replicas *int
	if deployment.Spec.Replicas != nil {
		replicas = ptr.To(int(*deployment.Spec.Replicas))
	}
	if c.IsRequestServing {
		deploymentConfig.SetRequestServingDefaults(hcp, c.MultiZoneSpreadLabels, replicas)
	} else {
		deploymentConfig.SetDefaults(hcp, c.MultiZoneSpreadLabels, replicas)
	}

	deploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	deploymentConfig.ApplyTo(deployment)
}
