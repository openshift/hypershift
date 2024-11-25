package controlplanecomponent

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/infra"
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

type WorkloadContext struct {
	context.Context

	// reader client, as workloads should not be creating resources.
	Client                   client.Reader
	HCP                      *hyperv1.HostedControlPlane
	ReleaseImageProvider     imageprovider.ReleaseImageProvider
	UserReleaseImageProvider imageprovider.ReleaseImageProvider

	InfraStatus               infra.InfrastructureStatus
	SetDefaultSecurityContext bool
	EnableCIDebugOutput       bool
	MetricsSet                metrics.MetricsSet
}

func (cp *ControlPlaneContext) workloadContext() WorkloadContext {
	return WorkloadContext{
		Context:                   cp.Context,
		Client:                    cp.Client,
		HCP:                       cp.HCP,
		ReleaseImageProvider:      cp.ReleaseImageProvider,
		UserReleaseImageProvider:  cp.UserReleaseImageProvider,
		InfraStatus:               cp.InfraStatus,
		SetDefaultSecurityContext: cp.SetDefaultSecurityContext,
		EnableCIDebugOutput:       cp.EnableCIDebugOutput,
		MetricsSet:                cp.MetricsSet,
	}
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

	adapt func(cpContext WorkloadContext, obj client.Object) error

	// adapters for Secret, ConfigMap, Service, ServiceMonitor, etc.
	manifestsAdapters map[string]genericAdapter
	// predicate is called at the begining, the component is disabled if it returns false.
	predicate func(cpContext WorkloadContext) (bool, error)
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
	workloadContext := cpContext.workloadContext()

	if !cpContext.SkipPredicate && c.predicate != nil {
		shouldReconcile, err := c.predicate(workloadContext)
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
		if err := c.adapt(cpContext.workloadContext(), workloadObj); err != nil {
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
