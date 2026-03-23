package globalconfig

import (
	"context"
	"fmt"
	"sort"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/capabilities"
	"github.com/openshift/hypershift/support/releaseinfo"
	hyperutil "github.com/openshift/hypershift/support/util"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/yaml"

	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func ImageContentSourcePolicy() *operatorv1alpha1.ImageContentSourcePolicy {
	return &operatorv1alpha1.ImageContentSourcePolicy{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ImageContentSourcePolicy",
			APIVersion: operatorv1alpha1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
	}
}

// ImageContentSourcePolicyList returns an initialized ImageContentSourcePolicyList pointer
func ImageContentSourcePolicyList() *operatorv1alpha1.ImageContentSourcePolicyList {
	return &operatorv1alpha1.ImageContentSourcePolicyList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ImageContentSourcePolicyList",
			APIVersion: configv1.GroupVersion.String(),
		},
	}
}

func ImageDigestMirrorSet() *configv1.ImageDigestMirrorSet {
	return &configv1.ImageDigestMirrorSet{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ImageDigestMirrorSet",
			APIVersion: configv1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
	}
}

func ImageDigestMirrorSetList() *configv1.ImageDigestMirrorSetList {
	return &configv1.ImageDigestMirrorSetList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ImageDigestMirrorSetList",
			APIVersion: configv1.GroupVersion.String(),
		},
	}
}

func ImageTagMirrorSet() *configv1.ImageTagMirrorSet {
	return &configv1.ImageTagMirrorSet{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ImageTagMirrorSet",
			APIVersion: configv1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
	}
}

func ImageTagMirrorSetList() *configv1.ImageTagMirrorSetList {
	return &configv1.ImageTagMirrorSetList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ImageTagMirrorSetList",
			APIVersion: configv1.GroupVersion.String(),
		},
	}
}

// ReconcileImageDigestMirrors reconciles the ImageContentSources from the HCP spec into an ImageDigestMirrorSet
// and optionally merges in additional IDMS configuration from a parsed ConfigMap
func ReconcileImageDigestMirrors(idms *configv1.ImageDigestMirrorSet, hcp *hyperv1.HostedControlPlane, additionalIDMS *configv1.ImageDigestMirrorSet) error {
	if idms.Labels == nil {
		idms.Labels = map[string]string{}
	}

	idms.Labels["machineconfiguration.openshift.io/role"] = "worker"
	idms.Spec.ImageDigestMirrors = []configv1.ImageDigestMirrors{}

	// First, add ImageContentSources from the HCP spec
	for _, source := range hcp.Spec.ImageContentSources {
		var mirrors []configv1.ImageMirror

		for _, mirror := range source.Mirrors {
			mirrors = append(mirrors, configv1.ImageMirror(mirror))
		}

		idms.Spec.ImageDigestMirrors = append(idms.Spec.ImageDigestMirrors, configv1.ImageDigestMirrors{
			Source:  source.Source,
			Mirrors: mirrors,
		})
	}

	// Then, merge in additional IDMS from the ConfigMap if provided
	if additionalIDMS != nil {
		idms.Spec.ImageDigestMirrors = append(idms.Spec.ImageDigestMirrors, additionalIDMS.Spec.ImageDigestMirrors...)
	}

	return nil
}

// ReconcileImageTagMirrors reconciles the ImageContentSources from the HCP spec into an ImageTagMirrorSet
// and optionally merges in additional ITMS configuration from a parsed ConfigMap
func ReconcileImageTagMirrors(itms *configv1.ImageTagMirrorSet, _ *hyperv1.HostedControlPlane, additionalITMS *configv1.ImageTagMirrorSet) error {
	if itms.Labels == nil {
		itms.Labels = map[string]string{}
	}
	itms.Labels["machineconfiguration.openshift.io/role"] = "worker"
	itms.Spec.ImageTagMirrors = []configv1.ImageTagMirrors{}

	// Populate ITMS from the additional ITMS config if provided
	if additionalITMS != nil {
		itms.Spec.ImageTagMirrors = append(itms.Spec.ImageTagMirrors, additionalITMS.Spec.ImageTagMirrors...)
	}

	return nil
}

// ParseImageMirrorConfigMap parses a ConfigMap containing IDMS and/or ITMS configurations.
// The ConfigMap is expected to have keys "idms.yaml" and/or "itms.yaml" containing the respective configurations.
// Returns the parsed IDMS and ITMS objects, or nil if the respective key is not found.
func ParseImageMirrorConfigMap(ctx context.Context, client crclient.Client, configMapRef *corev1.LocalObjectReference, namespace string) (*configv1.ImageDigestMirrorSet, *configv1.ImageTagMirrorSet, error) {
	if configMapRef == nil {
		return nil, nil, nil
	}

	configMap := &corev1.ConfigMap{}
	if err := client.Get(ctx, crclient.ObjectKey{Name: configMapRef.Name, Namespace: namespace}, configMap); err != nil {
		return nil, nil, fmt.Errorf("failed to get ImageMirrorConfig ConfigMap: %w", err)
	}

	var idms *configv1.ImageDigestMirrorSet
	var itms *configv1.ImageTagMirrorSet

	// Parse IDMS if present
	if idmsYAML, ok := configMap.Data["idms.yaml"]; ok && idmsYAML != "" {
		idms = &configv1.ImageDigestMirrorSet{}
		if err := yaml.Unmarshal([]byte(idmsYAML), idms); err != nil {
			return nil, nil, fmt.Errorf("failed to parse idms.yaml from ConfigMap: %w", err)
		}
	}

	// Parse ITMS if present
	if itmsYAML, ok := configMap.Data["itms.yaml"]; ok && itmsYAML != "" {
		itms = &configv1.ImageTagMirrorSet{}
		if err := yaml.Unmarshal([]byte(itmsYAML), itms); err != nil {
			return nil, nil, fmt.Errorf("failed to parse itms.yaml from ConfigMap: %w", err)
		}
	}

	return idms, itms, nil
}

// GetAllImageRegistryMirrors returns any image registry mirrors from any ImageDigestMirrorSet, ImageTagMirrorSet or ImageContentSourcePolicy
// in an OpenShift management cluster (other management cluster types will not have these policies).
// ImageContentSourcePolicy will be deprecated and removed in 4.17 according to the following Jira ticket and PR
// description in favor of ImageDigestMirrorSet and ImageTagMirrorSet. We will need to look for all policy types in the cluster.
//
//		https://issues.redhat.com/browse/OCPNODE-1258
//	    https://github.com/openshift/hypershift/pull/1776
func GetAllImageRegistryMirrors(ctx context.Context, client crclient.Client, mgmtClusterHasIDMSCapability, mgmtClusterHasITMSCapability, mgmtClusterHasICSPCapability bool) (map[string][]string, error) {
	var mgmtClusterRegistryOverrides = make(map[string][]string)

	if mgmtClusterHasIDMSCapability {
		idms, err := getImageDigestMirrorSets(ctx, client)
		if err != nil {
			return nil, err
		}

		for key, values := range idms {
			mgmtClusterRegistryOverrides[key] = append(mgmtClusterRegistryOverrides[key], values...)
		}
	}

	if mgmtClusterHasITMSCapability {
		itms, err := getImageTagMirrorSets(ctx, client)
		if err != nil {
			return nil, err
		}

		for key, values := range itms {
			mgmtClusterRegistryOverrides[key] = append(mgmtClusterRegistryOverrides[key], values...)
		}
	}

	if mgmtClusterHasICSPCapability {
		icsp, err := getImageContentSourcePolicies(ctx, client)
		if err != nil {
			return nil, err
		}

		for key, values := range icsp {
			mgmtClusterRegistryOverrides[key] = append(mgmtClusterRegistryOverrides[key], values...)
		}
	}

	return mgmtClusterRegistryOverrides, nil
}

// getImageDigestMirrorSets retrieves any IDMS CRs from an OpenShift management cluster
func getImageDigestMirrorSets(ctx context.Context, client crclient.Client) (map[string][]string, error) {
	var idmsRegistryOverrides = make(map[string][]string)
	var imageDigestMirrorSets = ImageDigestMirrorSetList()

	err := client.List(ctx, imageDigestMirrorSets)
	if err != nil {
		return nil, err
	}

	// For each image digest mirror set in the management cluster, map the source with each of its mirrors
	for _, item := range imageDigestMirrorSets.Items {
		for _, imageDigestMirror := range item.Spec.ImageDigestMirrors {
			source := imageDigestMirror.Source

			// Skip empty sources
			if source == "" {
				continue
			}

			for n := range imageDigestMirror.Mirrors {
				mirror := string(imageDigestMirror.Mirrors[n])
				// Skip empty mirrors
				if mirror == "" {
					continue
				}
				idmsRegistryOverrides[source] = append(idmsRegistryOverrides[source], mirror)
			}
		}
	}

	return idmsRegistryOverrides, nil
}

// getImageTagMirrorSets retrieves any ITMS CRs from an OpenShift management cluster
func getImageTagMirrorSets(ctx context.Context, client crclient.Client) (map[string][]string, error) {
	var itmsRegistryOverrides = make(map[string][]string)
	var imageTagMirrorSets = ImageTagMirrorSetList()

	err := client.List(ctx, imageTagMirrorSets)
	if err != nil {
		return nil, err
	}

	// For each image tag mirror set in the management cluster, map the source with each of its mirrors
	for _, item := range imageTagMirrorSets.Items {
		for _, imageTagMirror := range item.Spec.ImageTagMirrors {
			source := imageTagMirror.Source

			// Skip empty sources
			if source == "" {
				continue
			}

			for n := range imageTagMirror.Mirrors {
				mirror := string(imageTagMirror.Mirrors[n])
				// Skip empty mirrors
				if mirror == "" {
					continue
				}
				itmsRegistryOverrides[source] = append(itmsRegistryOverrides[source], mirror)
			}
		}
	}

	return itmsRegistryOverrides, nil
}

// getImageContentSourcePolicies retrieves any ICSP CRs from an OpenShift management cluster
func getImageContentSourcePolicies(ctx context.Context, client crclient.Client) (map[string][]string, error) {
	log := ctrl.LoggerFrom(ctx)
	var icspRegistryOverrides = make(map[string][]string)
	var imageContentSourcePolicies = ImageContentSourcePolicyList()

	err := client.List(ctx, imageContentSourcePolicies)
	if err != nil {
		return nil, err
	}

	// Warn the user this CR will be deprecated in the future
	if len(imageContentSourcePolicies.Items) > 0 {
		log.Info("Detected ImageContentSourcePolicy Custom Resources. ImageContentSourcePolicy will be deprecated in favor of ImageDigestMirrorSet. See https://issues.redhat.com/browse/OCPNODE-1258 for more details.")
	}

	// Sort the items by name to ensure consistent ordering
	sort.Slice(imageContentSourcePolicies.Items, func(i, j int) bool {
		// This sorts ascending by name, so we can unit test the output.
		// The fake client, unlike the actual kubernetes client, returns
		// items in descending order by name.  By inverting the returned
		// sorting order, we can unit test that the output is deterministic.
		return strings.Compare(imageContentSourcePolicies.Items[i].Name, imageContentSourcePolicies.Items[j].Name) > 0
	})

	// For each image content source policy in the management cluster, map the source with each of its mirrors
	for _, item := range imageContentSourcePolicies.Items {
		for _, mirror := range item.Spec.RepositoryDigestMirrors {
			source := mirror.Source

			// Skip empty sources
			if source == "" {
				continue
			}

			// Filter out empty mirrors
			var validMirrors []string
			for _, m := range mirror.Mirrors {
				if m != "" {
					validMirrors = append(validMirrors, m)
				}
			}

			if len(validMirrors) > 0 {
				icspRegistryOverrides[source] = append(icspRegistryOverrides[source], validMirrors...)
			}
		}
	}

	return icspRegistryOverrides, nil
}

// RegistryProvider is an interface for release and metadata providers to enable the reconcilliation
// of those providers
type RegistryProvider interface {
	GetMetadataProvider() hyperutil.ImageMetadataProvider
	GetReleaseProvider() releaseinfo.ProviderWithOpenShiftImageRegistryOverrides
	Reconcile(context.Context, crclient.Client) error
}

// CommonRegistryProvider is the default RegistyProvider implementation
type CommonRegistryProvider struct {
	capChecker       capabilities.CapabiltyChecker
	MetadataProvider *hyperutil.RegistryClientImageMetadataProvider
	ReleaseProvider  *releaseinfo.ProviderWithOpenShiftImageRegistryOverridesDecorator
}

// NewCommonRegistryProvider creates a CommonRegistryProvider
func NewCommonRegistryProvider(ctx context.Context, capChecker capabilities.CapabiltyChecker, client crclient.Client, registryOverrides map[string]string) (CommonRegistryProvider, error) {

	var (
		imageRegistryMirrors map[string][]string
		err                  error
	)

	if capChecker.Has(capabilities.CapabilityICSP) || capChecker.Has(capabilities.CapabilityIDMS) || capChecker.Has(capabilities.CapabilityITMS) {
		imageRegistryMirrors, err = GetAllImageRegistryMirrors(ctx, client, capChecker.Has(capabilities.CapabilityIDMS), capChecker.Has(capabilities.CapabilityITMS), capChecker.Has(capabilities.CapabilityICSP))
		if err != nil {
			return CommonRegistryProvider{}, fmt.Errorf("failed to reconcile over image registry mirrors: %w", err)
		}
	}

	releaseProvider := &releaseinfo.ProviderWithOpenShiftImageRegistryOverridesDecorator{
		Delegate: &releaseinfo.RegistryMirrorProviderDecorator{
			Delegate: &releaseinfo.CachedProvider{
				Inner: &releaseinfo.RegistryClientProvider{},
				Cache: map[string]*releaseinfo.ReleaseImage{},
			},
			RegistryOverrides: registryOverrides,
		},
		OpenShiftImageRegistryOverrides: imageRegistryMirrors,
	}

	metadataProvider := &hyperutil.RegistryClientImageMetadataProvider{
		OpenShiftImageRegistryOverrides: imageRegistryMirrors,
	}

	provider := CommonRegistryProvider{
		capChecker:       capChecker,
		MetadataProvider: metadataProvider,
		ReleaseProvider:  releaseProvider,
	}

	return provider, nil
}

// GetMetadataProvider returns the image metadata provider for the registry provider
func (rp CommonRegistryProvider) GetMetadataProvider() hyperutil.ImageMetadataProvider {
	return rp.MetadataProvider
}

// GetReleaseProvider returns the release provider for the registry provider
func (rp CommonRegistryProvider) GetReleaseProvider() releaseinfo.ProviderWithOpenShiftImageRegistryOverrides {
	return rp.ReleaseProvider
}

// Reconcile updates the image registry mirrors for the providers according to the capabilities of the cluster
func (rp CommonRegistryProvider) Reconcile(ctx context.Context, client crclient.Client) error {

	var (
		imageRegistryMirrors map[string][]string
		err                  error
	)

	if rp.capChecker.Has(capabilities.CapabilityICSP) || rp.capChecker.Has(capabilities.CapabilityIDMS) || rp.capChecker.Has(capabilities.CapabilityITMS) {
		imageRegistryMirrors, err = GetAllImageRegistryMirrors(ctx, client, rp.capChecker.Has(capabilities.CapabilityIDMS), rp.capChecker.Has(capabilities.CapabilityITMS), rp.capChecker.Has(capabilities.CapabilityICSP))
		if err != nil {
			return fmt.Errorf("failed to reconcile over image registry mirrors: %w", err)
		}
	}

	rp.ReleaseProvider.OpenShiftImageRegistryOverrides = imageRegistryMirrors
	rp.MetadataProvider.OpenShiftImageRegistryOverrides = imageRegistryMirrors

	return nil
}
