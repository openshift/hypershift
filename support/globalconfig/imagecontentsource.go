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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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

// ReconcileImageDigestMirrors reconciles the ImageContentSources from the HCP spec into an ImageDigestMirrorSet
func ReconcileImageDigestMirrors(idms *configv1.ImageDigestMirrorSet, hcp *hyperv1.HostedControlPlane) error {
	if idms.Labels == nil {
		idms.Labels = map[string]string{}
	}

	idms.Labels["machineconfiguration.openshift.io/role"] = "worker"
	idms.Spec.ImageDigestMirrors = []configv1.ImageDigestMirrors{}
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
	return nil
}

// GetAllImageRegistryMirrors returns any image registry mirrors from any ImageDigestMirrorSet or ImageContentSourcePolicy
// in an OpenShift management cluster (other management cluster types will not have these policies).
// ImageContentSourcePolicy will be deprecated and removed in 4.17 according to the following Jira ticket and PR
// description in favor of ImageDigestMirrorSet. We will need to look for both policy types in the cluster.
//
//		https://issues.redhat.com/browse/OCPNODE-1258
//	    https://github.com/openshift/hypershift/pull/1776
func GetAllImageRegistryMirrors(ctx context.Context, client crclient.Client, mgmtClusterHasIDMSCapability, mgmtClusterHasICSPCapability bool) (map[string][]string, error) {
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

	if capChecker.Has(capabilities.CapabilityICSP) || capChecker.Has(capabilities.CapabilityIDMS) {
		imageRegistryMirrors, err = GetAllImageRegistryMirrors(ctx, client, capChecker.Has(capabilities.CapabilityIDMS), capChecker.Has(capabilities.CapabilityICSP))
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

	if rp.capChecker.Has(capabilities.CapabilityICSP) || rp.capChecker.Has(capabilities.CapabilityIDMS) {
		imageRegistryMirrors, err = GetAllImageRegistryMirrors(ctx, client, rp.capChecker.Has(capabilities.CapabilityIDMS), rp.capChecker.Has(capabilities.CapabilityICSP))
		if err != nil {
			return fmt.Errorf("failed to reconcile over image registry mirrors: %w", err)
		}
	}

	rp.ReleaseProvider.OpenShiftImageRegistryOverrides = imageRegistryMirrors
	rp.MetadataProvider.OpenShiftImageRegistryOverrides = imageRegistryMirrors

	return nil
}
