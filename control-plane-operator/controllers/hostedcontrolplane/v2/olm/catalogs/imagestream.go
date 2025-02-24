package catalogs

import (
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	imagev1 "github.com/openshift/api/image/v1"

	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (c *catalogOptions) imageStreamPredicate(cpContext component.WorkloadContext) bool {
	if !c.capabilityImageStream || cpContext.HCP.Annotations[hyperv1.RedHatOperatorsCatalogImageAnnotation] != "" {
		return false
	}
	return true
}

func adaptImageStream(cpContext component.WorkloadContext, imageStream *imagev1.ImageStream) error {
	if imageStream.Spec.Tags == nil {
		imageStream.Spec.Tags = []imagev1.TagReference{}
	}

	pullSecret := common.PullSecret(cpContext.HCP.Namespace)
	if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(pullSecret), pullSecret); err != nil {
		return fmt.Errorf("failed to get pull secret: %w", err)
	}

	var err error
	olmCatalogImagesOnce.Do(func() {
		catalogImages, err = getCatalogImages(cpContext, pullSecret.Data[corev1.DockerConfigJsonKey])
	})
	if err != nil {
		return fmt.Errorf("failed to get catalog images: %w", err)
	}

	isImageRegistryOverrides := util.ConvertImageRegistryOverrideStringToMap(cpContext.HCP.Annotations[hyperv1.OLMCatalogsISRegistryOverridesAnnotation])
	for name, image := range getCatalogToImageWithISImageRegistryOverrides(catalogImages, isImageRegistryOverrides) {
		imageStream.Spec.Tags = append(imageStream.Spec.Tags, imagev1.TagReference{
			Name: name,
			From: &corev1.ObjectReference{
				Kind: "DockerImage",
				Name: image,
			},
			ReferencePolicy: imagev1.TagReferencePolicy{
				Type: imagev1.LocalTagReferencePolicy,
			},
			ImportPolicy: imagev1.TagImportPolicy{
				Scheduled:  true,
				ImportMode: imagev1.ImportModePreserveOriginal,
			},
		})
	}

	return nil
}

// getCatalogToImageWithISImageRegistryOverrides returns a map of
// images to be used for the catalog registries where the image address got
// amended according to OpenShiftImageRegistryOverrides as set on the HostedControlPlaneReconciler
func getCatalogToImageWithISImageRegistryOverrides(catalogToImage map[string]string, isImageRegistryOverrides map[string][]string) map[string]string {
	catalogWithOverride := make(map[string]string)
	for name, image := range catalogToImage {
		for registrySource, registryDest := range isImageRegistryOverrides {
			if strings.Contains(image, registrySource) {
				for _, registryReplacement := range registryDest {
					image = strings.Replace(image, registrySource, registryReplacement, 1)
				}
			}
		}
		catalogWithOverride[name] = image
	}
	return catalogWithOverride
}
