package hostedcluster

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/support/imageregistry"
	"github.com/openshift/hypershift/support/supportedversion"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilrand "k8s.io/apimachinery/pkg/util/rand"

	ipamv1alpha1 "sigs.k8s.io/cluster-api/api/ipam/v1alpha1"
	capiconversion "sigs.k8s.io/cluster-api/core/webhooks/conversion"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	"sigs.k8s.io/controller-runtime/pkg/webhook/conversion"

	jsonpatch "github.com/evanphx/json-patch/v5"
	"github.com/go-logr/logr"
)

type hostedClusterDefaulter struct{}

type nodePoolDefaulter struct {
	client client.Client
}

func (defaulter *hostedClusterDefaulter) Default(ctx context.Context, hcluster *hyperv1.HostedCluster) error {
	if hcluster.Spec.Release.Image == "" {
		pullSpec, err := supportedversion.LookupLatestSupportedRelease(ctx, hcluster)
		if err != nil {
			return fmt.Errorf("unable to find default release image: %w", err)
		}
		hcluster.Spec.Release.Image = pullSpec
	}

	// Default allocateNodeCIDRs to Disabled if not set
	if hcluster.Spec.Networking.AllocateNodeCIDRs == nil {
		disabled := hyperv1.AllocateNodeCIDRsDisabled
		hcluster.Spec.Networking.AllocateNodeCIDRs = &disabled
	}

	// Default platform specific values
	switch hcluster.Spec.Platform.Type {
	case hyperv1.KubevirtPlatform:
		if hcluster.Spec.Platform.Kubevirt == nil {
			hcluster.Spec.Platform.Kubevirt = &hyperv1.KubevirtPlatformSpec{}
		}
		if hcluster.Spec.Platform.Kubevirt.GenerateID == "" {
			hcluster.Spec.Platform.Kubevirt.GenerateID = utilrand.String(10)
		}
		if hcluster.Spec.DNS.BaseDomain == "" {
			isTrue := true
			hcluster.Spec.Platform.Kubevirt.BaseDomainPassthrough = &isTrue
		}
		if hcluster.Spec.Networking.NetworkType == "" {
			hcluster.Spec.Networking.NetworkType = hyperv1.OVNKubernetes
		}

		// Default services for any service types that were not configured
		existingServices := map[hyperv1.ServiceType]bool{}
		defaults := core.GetIngressServicePublishingStrategyMapping(hcluster.Spec.Networking.NetworkType, false, false)
		for _, entry := range hcluster.Spec.Services {
			existingServices[entry.Service] = true
		}

		for _, entry := range defaults {
			if existingServices[entry.Service] {
				continue
			}
			hcluster.Spec.Services = append(hcluster.Spec.Services, entry)
		}
	}

	return nil
}

func (defaulter *nodePoolDefaulter) Default(ctx context.Context, np *hyperv1.NodePool) error {
	if np.Spec.Release.Image == "" {
		if np.Spec.ClusterName == "" {
			return fmt.Errorf("nodePool.Spec.ClusterName is a required field")
		}

		hc := &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      np.Spec.ClusterName,
				Namespace: np.Namespace,
			},
		}

		err := defaulter.client.Get(ctx, client.ObjectKeyFromObject(hc), hc)
		if err != nil {
			return fmt.Errorf("error retrieving HostedCluster named [%s], %w", np.Spec.ClusterName, err)
		}
		np.Spec.Release.Image = hc.Spec.Release.Image
	}

	// Default platform specific values
	switch np.Spec.Platform.Type {
	case hyperv1.KubevirtPlatform:
		if np.Spec.Platform.Kubevirt == nil {
			// Setting the KubeVirtNodePoolPlatform to an empty struct allows for
			// the CRD defaulting for this struct to take place.
			np.Spec.Platform.Kubevirt = &hyperv1.KubevirtNodePoolPlatform{}
		}
		if np.Spec.Management.UpgradeType == "" {
			np.Spec.Management.UpgradeType = hyperv1.UpgradeTypeReplace
			np.Spec.Management.Replace = &hyperv1.ReplaceUpgrade{}
		}
	}

	return nil
}

// SetupWebhookWithManager sets up HostedCluster webhooks.
func SetupWebhookWithManager(mgr ctrl.Manager, imageMetaDataProvider *imageregistry.RegistryClientImageMetadataProvider, logger logr.Logger) error {
	err := ctrl.NewWebhookManagedBy(mgr, &hyperv1.HostedCluster{}).
		WithDefaulter(&hostedClusterDefaulter{}).
		WithValidator(&hostedClusterValidator{}).
		Complete()
	if err != nil {
		return fmt.Errorf("unable to register hostedcluster webhook: %w", err)
	}
	err = ctrl.NewWebhookManagedBy(mgr, &hyperv1.NodePool{}).
		WithDefaulter(&nodePoolDefaulter{client: mgr.GetClient()}).
		WithValidator(newNodePoolValidator(logger)).
		Complete()
	if err != nil {
		return fmt.Errorf("unable to register nodepool webhook: %w", err)
	}
	err = ctrl.NewWebhookManagedBy(mgr, &hyperv1.HostedControlPlane{}).
		Complete()
	if err != nil {
		return fmt.Errorf("unable to register hostedcontrolplane webhook: %w", err)
	}

	// CAPI conversion between API versions requires resolving GroupKind to an API version string
	// to convert between corev1.ObjectReference and ContractVersionedObjectReference.
	// Without this, Cluster spec/status conversion fails and MachineDeployment controller
	// sees an empty Cluster status, never creates MachineSets or Machines.
	capiconversion.SetAPIVersionGetter(capiAPIVersionGetter(mgr.GetScheme()))

	// Register conversion webhook handler for CRD version conversions (HyperShift and CAPI types)
	registry, err := newCAPIConversionRegistry(mgr.GetScheme())
	if err != nil {
		return err
	}
	mgr.GetWebhookServer().Register("/convert", conversion.NewWebhookHandler(mgr.GetScheme(), registry))

	return nil
}

func capiAPIVersionGetter(scheme *runtime.Scheme) func(context.Context, schema.GroupKind) (string, error) {
	return func(_ context.Context, groupKind schema.GroupKind) (string, error) {
		// Preserve the manager scheme's registration priority, not a sorted API version.
		// Installed CRDs must serve that first version, including provider references;
		// TestNewCAPIConversionRegistry checks this against the embedded CRDs.
		versions := scheme.VersionsForGroupKind(groupKind)
		if len(versions) == 0 {
			return "", fmt.Errorf("no versions registered for GroupKind %s", groupKind)
		}
		return groupKind.WithVersion(versions[0].Version).GroupVersion().String(), nil
	}
}

func newCAPIConversionRegistry(scheme *runtime.Scheme) (conversion.Registry, error) {
	// Converter factories need only type registrations. Keep their required IPAM
	// alpha spoke private so constructing a registry cannot change the manager's
	// advertised versions or registration priority for served beta references.
	converterScheme := runtime.NewScheme()
	for groupVersionKind := range scheme.AllKnownTypes() {
		object, err := scheme.New(groupVersionKind)
		if err != nil {
			return nil, fmt.Errorf("unable to copy conversion type %s: %w", groupVersionKind, err)
		}
		converterScheme.AddKnownTypeWithName(groupVersionKind, object)
	}
	if err := ipamv1alpha1.AddToScheme(converterScheme); err != nil {
		return nil, fmt.Errorf("unable to register CAPI IPAM conversion types: %w", err)
	}
	registry := conversion.NewRegistry()
	for groupKind, newConverter := range map[schema.GroupKind]func(*runtime.Scheme) (conversion.Converter, error){
		{Group: "cluster.x-k8s.io", Kind: "Cluster"}:                          capiconversion.Cluster,
		{Group: "cluster.x-k8s.io", Kind: "ClusterClass"}:                     capiconversion.ClusterClass,
		{Group: "cluster.x-k8s.io", Kind: "Machine"}:                          capiconversion.Machine,
		{Group: "cluster.x-k8s.io", Kind: "MachineSet"}:                       capiconversion.MachineSet,
		{Group: "cluster.x-k8s.io", Kind: "MachineDeployment"}:                capiconversion.MachineDeployment,
		{Group: "cluster.x-k8s.io", Kind: "MachinePool"}:                      capiconversion.MachinePool,
		{Group: "cluster.x-k8s.io", Kind: "MachineHealthCheck"}:               capiconversion.MachineHealthCheck,
		{Group: "cluster.x-k8s.io", Kind: "MachineDrainRule"}:                 capiconversion.MachineDrainRule,
		{Group: "ipam.cluster.x-k8s.io", Kind: "IPAddress"}:                   capiconversion.IPAddress,
		{Group: "ipam.cluster.x-k8s.io", Kind: "IPAddressClaim"}:              capiconversion.IPAddressClaim,
		{Group: "addons.cluster.x-k8s.io", Kind: "ClusterResourceSet"}:        capiconversion.ClusterResourceSet,
		{Group: "addons.cluster.x-k8s.io", Kind: "ClusterResourceSetBinding"}: capiconversion.ClusterResourceSetBinding,
	} {
		converter, err := newConverter(converterScheme)
		if err != nil {
			return nil, fmt.Errorf("unable to create CAPI conversion for %s: %w", groupKind, err)
		}
		if err := registry.RegisterConverter(groupKind, converter); err != nil {
			return nil, fmt.Errorf("unable to register CAPI conversion for %s: %w", groupKind, err)
		}
	}
	return registry, nil
}

type hostedClusterValidator struct{}

func (v hostedClusterValidator) ValidateCreate(ctx context.Context, hc *hyperv1.HostedCluster) (admission.Warnings, error) {
	switch hc.Spec.Platform.Type {
	case hyperv1.KubevirtPlatform:
		return v.validateCreateKubevirtHostedCluster(ctx, hc)
	default:
		return nil, nil // no validation needed
	}
}

func (v hostedClusterValidator) ValidateUpdate(ctx context.Context, hcOld, hc *hyperv1.HostedCluster) (admission.Warnings, error) {
	switch hc.Spec.Platform.Type {
	case hyperv1.KubevirtPlatform:
		err := v.validateUpdateKubevirtHostedCluster(ctx, hcOld, hc)
		if err != nil {
			return nil, err
		}
	}
	return nil, nil
}

func (v hostedClusterValidator) ValidateDelete(_ context.Context, _ *hyperv1.HostedCluster) (admission.Warnings, error) {
	return nil, nil
}

func (v hostedClusterValidator) validateCreateKubevirtHostedCluster(_ context.Context, hc *hyperv1.HostedCluster) (admission.Warnings, error) {
	err := validateJsonAnnotation(hc.Annotations)
	if err != nil {
		return nil, err
	}

	return nil, nil
}

func (v hostedClusterValidator) validateUpdateKubevirtHostedCluster(_ context.Context, _, newHC *hyperv1.HostedCluster) error {
	err := validateJsonAnnotation(newHC.Annotations)
	if err != nil {
		return err
	}

	return nil
}

type nodePoolValidator struct {
	logger logr.Logger
}

func newNodePoolValidator(logger logr.Logger) *nodePoolValidator {
	return &nodePoolValidator{
		logger: logr.New(logger.GetSink()).WithName("nodePoolValidator"),
	}
}

func (v nodePoolValidator) ValidateCreate(ctx context.Context, np *hyperv1.NodePool) (admission.Warnings, error) {
	switch np.Spec.Platform.Type {
	case hyperv1.KubevirtPlatform:
		return v.validateCreateKubevirtNodePool(ctx, np)
	default:
		return nil, nil // no validation needed
	}
}

func (v nodePoolValidator) ValidateUpdate(ctx context.Context, npOld, npNew *hyperv1.NodePool) (admission.Warnings, error) {
	switch npNew.Spec.Platform.Type {
	case hyperv1.KubevirtPlatform:
		err := v.validateUpdateKubevirtNodePool(ctx, npOld, npNew)
		if err != nil {
			return nil, err
		}
	}

	return nil, nil
}

func (v nodePoolValidator) ValidateDelete(_ context.Context, _ *hyperv1.NodePool) (admission.Warnings, error) {
	return nil, nil
}

func (v nodePoolValidator) validateCreateKubevirtNodePool(_ context.Context, np *hyperv1.NodePool) (admission.Warnings, error) {
	err := validateJsonAnnotation(np.Annotations)
	if err != nil {
		return nil, err
	}

	return nil, nil
}

func (v nodePoolValidator) validateUpdateKubevirtNodePool(_ context.Context, _, newNP *hyperv1.NodePool) error {
	err := validateJsonAnnotation(newNP.Annotations)
	if err != nil {
		return err
	}
	return nil
}

func validateJsonAnnotation(annotations map[string]string) error {
	if ann, exists := annotations[hyperv1.JSONPatchAnnotation]; exists {
		patch, err := jsonpatch.DecodePatch([]byte(ann))
		if err != nil {
			return fmt.Errorf("wrong json patch structure in the %q annotation: %w", hyperv1.JSONPatchAnnotation, err)
		}

		for _, p := range patch {
			kind := p.Kind()
			if kind == "unknown" {
				return fmt.Errorf("wrong json patch structure in the %q annotation: missing op field", hyperv1.JSONPatchAnnotation)
			} else if kind != "remove" {
				v, err := p.ValueInterface()
				if err != nil {
					return fmt.Errorf("wrong json patch structure in the %q annotation: %w, %v", hyperv1.JSONPatchAnnotation, err, v)
				}
			}
			_, err = p.Path()
			if err != nil {
				return fmt.Errorf("wrong json patch structure in the %q annotation: %w", hyperv1.JSONPatchAnnotation, err)
			}
		}
	}

	return nil
}
