package controlplanecomponent

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/infra"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/metrics"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"

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

	// ApplyProvider knows how to create/update manifest based resources.
	upsert.ApplyProvider
	// Client knows how to perform CRUD operations on Kubernetes objects in the HCP namespace.
	Client client.Client
	// HCP is the HostedControlPlane object
	HCP *hyperv1.HostedControlPlane
	// ReleaseImageProvider contains the version and component images related to control-plane release image.
	ReleaseImageProvider imageprovider.ReleaseImageProvider
	// UserReleaseImageProvider contains the version and component images related to data-plane release image.
	UserReleaseImageProvider imageprovider.ReleaseImageProvider
	// ImageMetadataProvider returns metadata for a given release image using the given pull secret.
	ImageMetadataProvider util.ImageMetadataProvider

	// InfraStatus contains all the information about the Hosted cluster's infra services.
	InfraStatus infra.InfrastructureStatus
	// SetDefaultSecurityContext is used to configure Security Context for containers.
	SetDefaultSecurityContext bool
	// EnableCIDebugOutput enable extra debug logs.
	EnableCIDebugOutput bool
	// MetricsSet specifies which metrics to use in the service/pod-monitors.
	MetricsSet metrics.MetricsSet

	// This is needed for the generic unit test, so we can always generate a fixture for the components deployment/statefulset.
	SkipPredicate bool

	// SkipCertificateSigning is used for the generic unit test to skip the signing of certificates and maintain a stable output.
	SkipCertificateSigning bool
}

type WorkloadContext struct {
	context.Context

	// reader client, as workloads should not be creating resources.
	Client                   client.Reader
	HCP                      *hyperv1.HostedControlPlane
	ReleaseImageProvider     imageprovider.ReleaseImageProvider
	UserReleaseImageProvider imageprovider.ReleaseImageProvider
	ImageMetadataProvider    util.ImageMetadataProvider

	InfraStatus               infra.InfrastructureStatus
	SetDefaultSecurityContext bool
	EnableCIDebugOutput       bool
	MetricsSet                metrics.MetricsSet

	// skip generation of certificates for unit tests
	SkipCertificateSigning bool
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
		ImageMetadataProvider:     cp.ImageMetadataProvider,
		SkipCertificateSigning:    cp.SkipCertificateSigning,
	}
}

var _ ControlPlaneComponent = &controlPlaneWorkload[client.Object]{}

type ComponentOptions interface {
	IsRequestServing() bool
	MultiZoneSpread() bool
	NeedsManagementKASAccess() bool
}

// TODO: add unit test
type controlPlaneWorkload[T client.Object] struct {
	ComponentOptions

	name             string
	workloadProvider WorkloadProvider[T]

	// list of component names that this component depends on.
	// reconciliation will be blocked until all dependencies are available.
	dependencies []string

	adapt func(cpContext WorkloadContext, obj T) error

	// adapters for Secret, ConfigMap, Service, ServiceMonitor, etc.
	manifestsAdapters map[string]genericAdapter
	// predicate is called at the beginning, the component is disabled if it returns false.
	predicate func(cpContext WorkloadContext) (bool, error)

	// if provided, konnectivity proxy container and required volumes will be injected into the deployment/statefulset.
	konnectivityContainerOpts *KonnectivityContainerOptions
	// if provided, availabilityProber container and required volumes will be injected into the deployment/statefulset.
	availabilityProberOpts *util.AvailabilityProberOpts
	// if provided, token-minter container and required volumes will be injected into the deployment/statefulset.
	tokenMinterContainerOpts *TokenMinterContainerOptions
	// serviceAccountKubeConfigOpts will cause the generation of a secret with a kubeconfig using certificates for the given named service account
	// and the volume mounts for that secret within the given mountPath.
	serviceAccountKubeConfigOpts *ServiceAccountKubeConfigOpts
}

// Name implements ControlPlaneComponent.
func (c *controlPlaneWorkload[T]) Name() string {
	return c.name
}

// Reconcile implements ControlPlaneComponent.
func (c *controlPlaneWorkload[T]) Reconcile(cpContext ControlPlaneContext) error {
	workloadContext := cpContext.workloadContext()

	if !cpContext.SkipPredicate && c.predicate != nil {
		isEnabled, err := c.predicate(workloadContext)
		if err != nil {
			return err
		}
		if !isEnabled {
			return c.delete(cpContext)
		}
	}

	unavailableDependencies, err := c.checkDependencies(cpContext)
	if err != nil {
		return fmt.Errorf("failed checking for dependencies availability: %v", err)
	}
	var reconcilationError error
	if len(unavailableDependencies) == 0 {
		// reconcile only when all dependencies are available, and don't return error immediately so it can be included in the status condition first.
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

func (c *controlPlaneWorkload[T]) delete(cpContext ControlPlaneContext) error {
	workloadObj := c.workloadProvider.NewObject()
	// make sure that the Deployment/Statefulset name matches the component name.
	workloadObj.SetName(c.Name())
	workloadObj.SetNamespace(cpContext.HCP.Namespace)

	_, err := util.DeleteIfNeeded(cpContext, cpContext.Client, workloadObj)
	if err != nil {
		return err
	}

	// delete all resources.
	if err := assets.ForEachManifest(c.name, func(manifestName string) error {
		obj, _, err := assets.LoadManifest(c.name, manifestName)
		if err != nil {
			return err
		}
		obj.SetNamespace(cpContext.HCP.Namespace)

		_, err = util.DeleteIfNeeded(cpContext, cpContext.Client, obj)
		return err
	}); err != nil {
		return err
	}

	component := &hyperv1.ControlPlaneComponent{
		ObjectMeta: metav1.ObjectMeta{
			Name:      c.Name(),
			Namespace: cpContext.HCP.Namespace,
		},
	}
	_, err = util.DeleteIfNeeded(cpContext, cpContext.Client, component)
	return err
}

// update reconciles component workload and related manifests
func (c *controlPlaneWorkload[T]) update(cpContext ControlPlaneContext) error {
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
		case *corev1.ServiceAccount:
			util.EnsurePullSecret(typedObj, common.PullSecret("").Name)
		}

		if _, err := cpContext.ApplyManifest(cpContext, cpContext.Client, obj); err != nil {
			return err
		}

		return nil
	}); err != nil {
		return err
	}

	if c.serviceAccountKubeConfigOpts != nil {
		_, disablePKIReconciliationAnnotation := cpContext.HCP.Annotations[hyperv1.DisablePKIReconciliationAnnotation]
		if !disablePKIReconciliationAnnotation {
			kubeconfigSecret := c.serviceAccountKubeconfigSecret(cpContext.HCP.Namespace)
			if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(kubeconfigSecret), kubeconfigSecret); err != nil {
				if !apierrors.IsNotFound(err) {
					return err
				}
			}
			if !cpContext.SkipCertificateSigning {
				if err := c.adaptServiceAccountKubeconfigSecret(cpContext.workloadContext(), kubeconfigSecret); err != nil {
					return err
				}
			}
			if _, err := cpContext.ApplyManifest(cpContext, cpContext.Client, kubeconfigSecret); err != nil {
				return err
			}
		}
	}

	return c.reconcileWorkload(cpContext)
}

func (c *controlPlaneWorkload[T]) reconcileWorkload(cpContext ControlPlaneContext) error {
	workloadObj, err := c.workloadProvider.LoadManifest(c.Name())
	if err != nil {
		return fmt.Errorf("failed loading workload manifest: %v", err)
	}
	// make sure that the Deployment/Statefulset name matches the component name.
	workloadObj.SetName(c.Name())
	workloadObj.SetNamespace(cpContext.HCP.Namespace)

	oldWorkloadObj := c.workloadProvider.NewObject()
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

	deploymentConfig, err := c.defaultOptions(cpContext, c.workloadProvider.PodTemplateSpec(workloadObj), c.workloadProvider.Replicas(workloadObj))
	if err != nil {
		return err
	}
	c.workloadProvider.ApplyOptionsTo(cpContext, workloadObj, oldWorkloadObj, deploymentConfig)

	if _, err := cpContext.ApplyManifest(cpContext, cpContext.Client, workloadObj); err != nil {
		return err
	}
	return nil
}
