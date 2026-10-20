package nodepool

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1"
	karpenterutil "github.com/openshift/hypershift/support/karpenter"
	"github.com/openshift/hypershift/support/releaseinfo"

	corev1 "k8s.io/api/core/v1"

	"github.com/go-logr/logr"
)

// Azure Marketplace annotations identify no-purchase-plan Gen2 images on Karpenter
// userData Secrets. Each architecture has a complete publisher, offer, SKU, and
// version group.
const (
	azureMarketplacePublisherAnnotation = "hypershift.openshift.io/azure-marketplace-publisher"
	azureMarketplaceOfferAnnotation     = "hypershift.openshift.io/azure-marketplace-offer"
	azureMarketplaceSKUAnnotation       = "hypershift.openshift.io/azure-marketplace-sku"
	azureMarketplaceVersionAnnotation   = "hypershift.openshift.io/azure-marketplace-version"
)

func setKarpenterAMILabels(log logr.Logger, userDataSecret *corev1.Secret, region string, releaseImage *releaseinfo.ReleaseImage, rhelStream string) error {
	supportedArchitectures, err := karpenterutil.SupportedArchitectures(hyperv1.AWSPlatform)
	if err != nil {
		return fmt.Errorf("failed to get supported architectures: %w", err)
	}
	supported := 0
	for _, arch := range supportedArchitectures {
		ami, err := defaultNodePoolAMI(region, arch, rhelStream, releaseImage)
		if err != nil {
			// skip unavailable architectures gracefully
			log.Error(err, "failed to get default NodePool AMI for architecture", "architecture", arch)
			continue
		}
		labelKey := karpenterutil.ArchToImageMetadataKey(arch, hyperkarpenterv1.UserDataAMILabel)
		userDataSecret.Labels[labelKey] = ami
		supported++
	}
	if supported == 0 {
		return fmt.Errorf("no supported architectures found")
	}
	return nil
}

func setKarpenterAzureMarketplaceAnnotations(log logr.Logger, userDataSecret *corev1.Secret, releaseImage *releaseinfo.ReleaseImage, rhelStream string) error {
	supportedArchitectures, err := karpenterutil.SupportedArchitectures(hyperv1.AzurePlatform)
	if err != nil {
		return fmt.Errorf("failed to get supported architectures: %w", err)
	}
	// Resolve the stream first so stream errors are not treated as unavailable architectures.
	if _, err := releaseImage.StreamForName(rhelStream); err != nil {
		return fmt.Errorf("failed to resolve stream metadata for stream %q: %w", rhelStream, err)
	}

	resolvedAnnotations := make(map[string]string)
	for _, arch := range supportedArchitectures {
		marketplace, err := getAzureMarketplaceMetadata(releaseImage, hyperv1.ArchAliases[arch], rhelStream)
		if err != nil {
			log.Error(err, "failed to get Azure Marketplace metadata for architecture", "architecture", arch)
			continue
		}
		if marketplace == nil || marketplace.NoPurchasePlan == nil || marketplace.NoPurchasePlan.HyperVGen2 == nil {
			log.Info("Skipping architecture without an Azure Marketplace Gen2 image", "architecture", arch)
			continue
		}

		image := marketplace.NoPurchasePlan.HyperVGen2
		if image.Publisher == "" || image.Offer == "" || image.SKU == "" || image.Version == "" {
			log.Info("Skipping architecture with incomplete Azure Marketplace Gen2 image metadata", "architecture", arch)
			continue
		}
		resolvedAnnotations[karpenterutil.ArchToImageMetadataKey(arch, azureMarketplacePublisherAnnotation)] = image.Publisher
		resolvedAnnotations[karpenterutil.ArchToImageMetadataKey(arch, azureMarketplaceOfferAnnotation)] = image.Offer
		resolvedAnnotations[karpenterutil.ArchToImageMetadataKey(arch, azureMarketplaceSKUAnnotation)] = image.SKU
		resolvedAnnotations[karpenterutil.ArchToImageMetadataKey(arch, azureMarketplaceVersionAnnotation)] = image.Version
	}
	if len(resolvedAnnotations) == 0 {
		return fmt.Errorf("no Azure Marketplace Gen2 images found for supported architectures: %v", supportedArchitectures)
	}

	if userDataSecret.Annotations == nil {
		userDataSecret.Annotations = make(map[string]string)
	}
	// Replace only our image annotations after at least one complete image has been resolved.
	for _, arch := range supportedArchitectures {
		for _, baseKey := range []string{
			azureMarketplacePublisherAnnotation,
			azureMarketplaceOfferAnnotation,
			azureMarketplaceSKUAnnotation,
			azureMarketplaceVersionAnnotation,
		} {
			delete(userDataSecret.Annotations, karpenterutil.ArchToImageMetadataKey(arch, baseKey))
		}
	}
	for key, value := range resolvedAnnotations {
		userDataSecret.Annotations[key] = value
	}
	return nil
}
