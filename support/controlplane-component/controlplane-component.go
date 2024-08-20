package controlplanecomponent

import (
	"context"
	"fmt"
	"slices"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/metrics"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

var _ ControlPlaneComponent = &controlPlaneWorkload{}

type controlPlaneWorkload struct {
	// one of DeploymentReconciler or StatefulSetReconciler is required
	deploymentReconciler  DeploymentReconciler
	statefulSetReconciler StatefulSetReconciler

	// optional
	rbacReconciler RBACReconciler
	// reconiclers for Secret, ConfigMap, Service, ServiceMonitor, etc.
	resourcesReconcilers []GenericReconciler
	// predicate is called at the begining, the component is disabled if it returns false.
	predicate func(cpContext ControlPlaneContext) (bool, error)
	// These resources will cause the Deployment/stateful to rollout when changed
	watchedResources []client.Object

	multiZoneSpreadLabels    map[string]string
	isRequestServing         bool
	needsManagementKASAccess bool

	// if provided, a konnectivity proxy container and required volumes will be injected into the deployment.
	konnectivityContainerOpts *KonnectivityContainerOptions
}

// Name implements ControlPlaneComponent.
func (c *controlPlaneWorkload) Name() string {
	if c.deploymentReconciler != nil {
		return c.deploymentReconciler.Name()
	} else {
		return c.statefulSetReconciler.Name()
	}

}

// reconcile implements ControlPlaneComponent.
func (c *controlPlaneWorkload) Reconcile(cpContext ControlPlaneContext) error {
	if c.predicate != nil {
		shouldReconcile, err := c.predicate(cpContext)
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
	for _, reconciler := range c.resourcesReconcilers {
		if reconciler.PredicateFn != nil && !reconciler.PredicateFn(cpContext) {
			continue
		}

		resource := reconciler.ManifestFn(hcp.Namespace)
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

	if c.deploymentReconciler != nil {
		return c.reconcileDeployment(cpContext)
	} else {
		return c.reconcileStatefulSet(cpContext)
	}
}

func (c *controlPlaneWorkload) reconcileRBAC(cpContext ControlPlaneContext) error {
	if c.rbacReconciler == nil {
		return nil
	}

	hcp := cpContext.HCP
	ownerRef := config.OwnerRefFrom(hcp)

	serviceAccount := serviceAccountManifest(c.Name(), hcp.Namespace)
	if _, err := cpContext.CreateOrUpdate(cpContext, cpContext.Client, serviceAccount, func() error {
		ownerRef.ApplyTo(serviceAccount)
		return c.rbacReconciler.reconcileServiceAccount(cpContext, serviceAccount)
	}); err != nil {
		return err
	}

	role := roleManifest(c.Name(), hcp.Namespace)
	if _, err := cpContext.CreateOrUpdate(cpContext, cpContext.Client, role, func() error {
		ownerRef.ApplyTo(role)
		return c.rbacReconciler.reconcileRole(cpContext, role)
	}); err != nil {
		return err
	}

	roleBinding := roleBindingManifest(c.Name(), hcp.Namespace)
	if _, err := cpContext.CreateOrUpdate(cpContext, cpContext.Client, roleBinding, func() error {
		ownerRef.ApplyTo(roleBinding)
		return c.rbacReconciler.reconcileRoleBinding(cpContext, roleBinding, role, serviceAccount)
	}); err != nil {
		return err
	}

	return nil
}

func (c *controlPlaneWorkload) defaultDeploymentConfig(cpContext ControlPlaneContext, desiredReplicas *int32) *config.DeploymentConfig {
	hcp := cpContext.HCP

	deploymentConfig := &config.DeploymentConfig{
		SetDefaultSecurityContext: cpContext.SetDefaultSecurityContext,
	}
	deploymentConfig.Scheduling.PriorityClass = config.DefaultPriorityClass
	if hcp.Annotations[hyperv1.ControlPlanePriorityClass] != "" {
		deploymentConfig.Scheduling.PriorityClass = hcp.Annotations[hyperv1.ControlPlanePriorityClass]
	}

	if c.needsManagementKASAccess {
		deploymentConfig.AdditionalLabels = map[string]string{
			config.NeedManagementKASAccessLabel: "true",
		}
	}

	var replicas *int
	if desiredReplicas != nil {
		replicas = ptr.To(int(*desiredReplicas))
	}
	if c.isRequestServing {
		deploymentConfig.SetRequestServingDefaults(hcp, c.multiZoneSpreadLabels, replicas)
	} else {
		deploymentConfig.SetDefaults(hcp, c.multiZoneSpreadLabels, replicas)
	}

	deploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	return deploymentConfig
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

type controlPlaneWorkloadBuilder struct {
	workload *controlPlaneWorkload
}

func NewDeploymentComponent(reconciler DeploymentReconciler) *controlPlaneWorkloadBuilder {
	return &controlPlaneWorkloadBuilder{
		workload: &controlPlaneWorkload{
			deploymentReconciler: reconciler,
		},
	}
}

func NewStatefulSetComponent(reconciler StatefulSetReconciler) *controlPlaneWorkloadBuilder {
	return &controlPlaneWorkloadBuilder{
		workload: &controlPlaneWorkload{
			statefulSetReconciler: reconciler,
		},
	}
}

func (b *controlPlaneWorkloadBuilder) WithRBAC(roleRules []rbacv1.PolicyRule) *controlPlaneWorkloadBuilder {
	return b.WithRBACReconciler(NewRBACReconciler(roleRules))
}

func (b *controlPlaneWorkloadBuilder) WithRBACReconciler(reconciler RBACReconciler) *controlPlaneWorkloadBuilder {
	b.workload.rbacReconciler = reconciler
	return b
}

func (b *controlPlaneWorkloadBuilder) WithPredicate(predicate func(cpContext ControlPlaneContext) (bool, error)) *controlPlaneWorkloadBuilder {
	b.workload.predicate = predicate
	return b
}

func (b *controlPlaneWorkloadBuilder) ResourcesReconcilers(reconcilers ...GenericReconciler) *controlPlaneWorkloadBuilder {
	b.workload.resourcesReconcilers = append(b.workload.resourcesReconcilers, reconcilers...)
	return b
}

func (b *controlPlaneWorkloadBuilder) WatchResources(resources ...client.Object) *controlPlaneWorkloadBuilder {
	b.workload.watchedResources = append(b.workload.watchedResources, resources...)
	return b
}

func (b *controlPlaneWorkloadBuilder) MultiZoneSpreadLabels(labels map[string]string) *controlPlaneWorkloadBuilder {
	b.workload.multiZoneSpreadLabels = labels
	return b
}

func (b *controlPlaneWorkloadBuilder) NeedsManagementKASAccess() *controlPlaneWorkloadBuilder {
	b.workload.needsManagementKASAccess = true
	return b
}

func (b *controlPlaneWorkloadBuilder) IsRequestServing() *controlPlaneWorkloadBuilder {
	b.workload.isRequestServing = true
	return b
}

func (b *controlPlaneWorkloadBuilder) InjectKonnectivityContainer(opts *KonnectivityContainerOptions) *controlPlaneWorkloadBuilder {
	b.workload.konnectivityContainerOpts = opts
	return b
}

func (b *controlPlaneWorkloadBuilder) Build() ControlPlaneComponent {
	return b.workload
}
