package hostedcluster

import (
	"context"
	"fmt"
	"net"
	"reflect"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/support/supportedversion"
	hyperutil "github.com/openshift/hypershift/support/util"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/validation/field"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/blang/semver"
	jsonpatch "github.com/evanphx/json-patch/v5"
	"github.com/go-logr/logr"
)

type hostedClusterDefaulter struct {
}

type nodePoolDefaulter struct {
	client client.Client
}

func (defaulter *hostedClusterDefaulter) Default(ctx context.Context, obj runtime.Object) error {
	hcluster, ok := obj.(*hyperv1.HostedCluster)
	if !ok {
		return apierrors.NewBadRequest(fmt.Sprintf("expected a HostedCluster but got a %T", obj))
	}

	if hcluster.Spec.Release.Image == "" {
		pullSpec, err := supportedversion.LookupLatestSupportedRelease(ctx, hcluster)
		if err != nil {
			return fmt.Errorf("unable to find default release image: %w", err)
		}
		hcluster.Spec.Release.Image = pullSpec
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
		defaults := core.GetIngressServicePublishingStrategyMapping(hcluster.Spec.Networking.NetworkType, false)
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

func (defaulter *nodePoolDefaulter) Default(ctx context.Context, obj runtime.Object) error {
	np, ok := obj.(*hyperv1.NodePool)
	if !ok {
		return apierrors.NewBadRequest(fmt.Sprintf("expected a NodePool but got a %T", obj))
	}

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
			return fmt.Errorf("error retrieving HostedCluster named [%s], %v", np.Spec.ClusterName, err)
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
func SetupWebhookWithManager(mgr ctrl.Manager, imageMetaDataProvider *hyperutil.RegistryClientImageMetadataProvider, logger logr.Logger) error {
	err := ctrl.NewWebhookManagedBy(mgr).
		For(&hyperv1.HostedCluster{}).
		WithDefaulter(&hostedClusterDefaulter{}).
		WithValidator(&hostedClusterValidator{}).
		Complete()
	if err != nil {
		return fmt.Errorf("unable to register hostedcluster webhook: %w", err)
	}
	err = ctrl.NewWebhookManagedBy(mgr).
		For(&hyperv1.NodePool{}).
		WithDefaulter(&nodePoolDefaulter{client: mgr.GetClient()}).
		WithValidator(newNodePoolValidator(logger)).
		Complete()
	if err != nil {
		return fmt.Errorf("unable to register nodepool webhook: %w", err)
	}
	err = ctrl.NewWebhookManagedBy(mgr).
		For(&hyperv1.HostedControlPlane{}).
		Complete()
	if err != nil {
		return fmt.Errorf("unable to register hostedcontrolplane webhook: %w", err)
	}
	return nil
}

var _ admission.CustomValidator = (*hostedClusterValidator)(nil)

type hostedClusterValidator struct{}

func (v hostedClusterValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	hc, ok := obj.(*hyperv1.HostedCluster)
	if !ok {
		return nil, fmt.Errorf("wrong type %T for validation, instead of HostedCluster", obj)
	}
	if err := validateNetworking(hc); err != nil {
		return nil, err
	}

	switch hc.Spec.Platform.Type {
	case hyperv1.KubevirtPlatform:
		return v.validateCreateKubevirtHostedCluster(ctx, hc)
	default:
		return nil, nil // no validation needed
	}
}

func (v hostedClusterValidator) ValidateUpdate(ctx context.Context, oldHC, newHC runtime.Object) (admission.Warnings, error) {
	hc, ok := newHC.(*hyperv1.HostedCluster)
	if !ok {
		return nil, fmt.Errorf("wrong type %T for validation, instead of HostedCluster", newHC)
	}

	hcOld, ok := oldHC.(*hyperv1.HostedCluster)
	if !ok {
		return nil, fmt.Errorf("wrong type %T for validation, instead of HostedCluster", oldHC)
	}

	if !reflect.DeepEqual(hcOld.Spec.Networking, hc.Spec.Networking) {
		ackAnnotation := "hypershift.openshift.io/acknowledge-networking-disruption"
		if val, ok := hc.Annotations[ackAnnotation]; !ok || val != "true" {
			return nil, apierrors.NewForbidden(hyperv1.Resource("hostedcluster"), hc.Name,
				fmt.Errorf("modifying networking on a running cluster is a highly disruptive action. To proceed, you must set the 'hypershift.openshift.io/acknowledge-networking-disruption: \"true\"' annotation"))
		}
		delete(hc.Annotations, ackAnnotation)
		if meta.IsStatusConditionTrue(hc.Status.Conditions, string(hyperv1.ClusterVersionProgressing)) {
			return nil, apierrors.NewInvalid(hyperv1.GroupVersion.WithKind("HostedCluster").GroupKind(), hc.Name,
				field.ErrorList{field.Forbidden(field.NewPath("spec", "networking"), "networking configuration cannot be changed while a cluster upgrade is in progress.")})
		}
	}
	if err := validateNetworking(hc); err != nil {
		return nil, err
	}

	switch hc.Spec.Platform.Type {
	case hyperv1.KubevirtPlatform:
		err := v.validateUpdateKubevirtHostedCluster(ctx, hcOld, hc)
		if err != nil {
			return nil, err
		}
	}
	return nil, nil
}

func (v hostedClusterValidator) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

func (v hostedClusterValidator) validateCreateKubevirtHostedCluster(ctx context.Context, hc *hyperv1.HostedCluster) (admission.Warnings, error) {
	err := validateJsonAnnotation(hc.Annotations)
	if err != nil {
		return nil, err
	}

	return nil, nil
}

func (v hostedClusterValidator) validateUpdateKubevirtHostedCluster(ctx context.Context, oldHC, newHC *hyperv1.HostedCluster) error {
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

func (v nodePoolValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	np, ok := obj.(*hyperv1.NodePool)
	if !ok {
		return nil, fmt.Errorf("wrong type %T for validation, instead of NodePool", obj)
	}

	switch np.Spec.Platform.Type {
	case hyperv1.KubevirtPlatform:
		return v.validateCreateKubevirtNodePool(ctx, np)
	default:
		return nil, nil // no validation needed
	}
}

func (v nodePoolValidator) ValidateUpdate(ctx context.Context, oldNP, newNP runtime.Object) (admission.Warnings, error) {
	npNew, ok := newNP.(*hyperv1.NodePool)
	if !ok {
		return nil, fmt.Errorf("wrong type %T for validation, instead of NodePool", newNP)
	}

	npOld, ok := oldNP.(*hyperv1.NodePool)
	if !ok {
		return nil, fmt.Errorf("wrong type %T for validation, instead of NodePool", npOld)
	}

	switch npNew.Spec.Platform.Type {
	case hyperv1.KubevirtPlatform:
		err := v.validateUpdateKubevirtNodePool(ctx, npOld, npNew)
		if err != nil {
			return nil, err
		}
	}

	return nil, nil
}

func (v nodePoolValidator) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

func (v nodePoolValidator) validateCreateKubevirtNodePool(ctx context.Context, np *hyperv1.NodePool) (admission.Warnings, error) {
	err := validateJsonAnnotation(np.Annotations)
	if err != nil {
		return nil, err
	}

	return nil, nil
}

func (v nodePoolValidator) validateUpdateKubevirtNodePool(ctx context.Context, oldNP, newNP *hyperv1.NodePool) error {
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

func validateNetworking(hc *hyperv1.HostedCluster) error {
	networkingPath := field.NewPath("spec", "networking")
	// Version Gate Check
	hasNewNetConfig := hc.Spec.Networking.OVNKubernetesConfig != nil || hc.Spec.Networking.IPSecConfig != nil
	if hasNewNetConfig {
		v, err := supportedversion.VersionFromPullSpec(hc.Spec.Release.Image)
		if err != nil {
			return fmt.Errorf("cannot parse release image version: %w", err)
		}
		minVersionForFeature := semver.MustParse("4.16.0")
		if v.LT(minVersionForFeature) {
			return fmt.Errorf("custom OVN and IPSec configuration is only supported on OCP 4.16+ versions")
		}
	}
	// Collect all defined CIDRs for overlap validation.
	var allCIDRs []hyperutil.CidrEntry
	for i, netEntry := range hc.Spec.Networking.ClusterNetwork {
		allCIDRs = append(allCIDRs, hyperutil.CidrEntry{Cidr: (*net.IPNet)(&netEntry.CIDR), Path: networkingPath.Child("clusterNetwork").Index(i)})
	}
	for i, netEntry := range hc.Spec.Networking.ServiceNetwork {
		allCIDRs = append(allCIDRs, hyperutil.CidrEntry{Cidr: (*net.IPNet)(&netEntry.CIDR), Path: networkingPath.Child("serviceNetwork").Index(i)})
	}
	for i, netEntry := range hc.Spec.Networking.MachineNetwork {
		allCIDRs = append(allCIDRs, hyperutil.CidrEntry{Cidr: (*net.IPNet)(&netEntry.CIDR), Path: networkingPath.Child("machineNetwork").Index(i)})
	}
	if hc.Spec.Networking.OVNKubernetesConfig != nil {
		ovnPath := networkingPath.Child("ovnKubernetesConfig")
		if hc.Spec.Networking.OVNKubernetesConfig.InternalJoinSubnet != nil {
			_, parsed, err := net.ParseCIDR(*hc.Spec.Networking.OVNKubernetesConfig.InternalJoinSubnet)
			if err != nil {
				return fmt.Errorf("invalid CIDR format for internalJoinSubnet: %w", err)
			}
			allCIDRs = append(allCIDRs, hyperutil.CidrEntry{Cidr: parsed, Path: ovnPath.Child("internalJoinSubnet")})
		}
		if hc.Spec.Networking.OVNKubernetesConfig.InternalTransitSwitchSubnet != nil {
			_, parsed, err := net.ParseCIDR(*hc.Spec.Networking.OVNKubernetesConfig.InternalTransitSwitchSubnet)
			if err != nil {
				return fmt.Errorf("invalid CIDR format for internalTransitSwitchSubnet: %w", err)
			}
			allCIDRs = append(allCIDRs, hyperutil.CidrEntry{Cidr: parsed, Path: ovnPath.Child("internalTransitSwitchSubnet")})
		}
	}
	if err := hyperutil.CheckCIDROverlaps(allCIDRs); err != nil {
		return err
	}
	return nil
}
