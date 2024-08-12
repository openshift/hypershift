package controlplanecomponent

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/metrics"
	"github.com/openshift/hypershift/support/upsert"

	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
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

	Client                   client.Client
	HCP                      *hyperv1.HostedControlPlane
	CreateOrUpdate           upsert.CreateOrUpdateFN
	ReleaseImageProvider     *imageprovider.ReleaseImageProvider
	UserReleaseImageProvider *imageprovider.ReleaseImageProvider

	SetDefaultSecurityContext bool
	MetricsSet                metrics.MetricsSet
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
