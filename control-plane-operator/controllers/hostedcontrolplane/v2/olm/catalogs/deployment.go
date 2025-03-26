package catalogs

import (
	"errors"
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/support/catalogs"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/reference"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (c *catalogOptions) adaptCatalogDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	catalogSource := deployment.Spec.Template.Labels["olm.catalogSource"]
	imageOverrides, err := getCatalogImagesOverrides(cpContext, c.capabilityImageStream)
	if err != nil {
		return err
	}

	var image string
	if imageOverride := imageOverrides[catalogSource]; imageOverride != "" {
		image = imageOverride
		delete(deployment.Annotations, "image.openshift.io/triggers")
	} else {
		existingDeployment := &appsv1.Deployment{}
		if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(deployment), existingDeployment); err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to get existing deployment: %v", err)
			}
		} else {
			// If deployment already exists, imagestream tag will already populate the container image
			image = existingDeployment.Spec.Template.Spec.Containers[0].Image
		}
	}

	if image != "" {
		util.UpdateContainer("registry", deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
			c.Image = image
		})
		util.UpdateContainer("extract-content", deployment.Spec.Template.Spec.InitContainers, func(c *corev1.Container) {
			c.Image = image
		})
	}

	return nil
}

func getCatalogImagesOverrides(cpContext component.WorkloadContext, capabilityImageStream bool) (map[string]string, error) {
	hcp := cpContext.HCP
	catalogOverrides := map[string]string{
		"redhat-operators":    hcp.Annotations[hyperv1.RedHatOperatorsCatalogImageAnnotation],
		"redhat-marketplace":  hcp.Annotations[hyperv1.RedHatMarketplaceCatalogImageAnnotation],
		"community-operators": hcp.Annotations[hyperv1.CommunityOperatorsCatalogImageAnnotation],
		"certified-operators": hcp.Annotations[hyperv1.CertifiedOperatorsCatalogImageAnnotation],
	}

	overrideImages, err := checkCatalogImageOverides(catalogOverrides)
	if err != nil {
		return nil, err
	}
	if overrideImages {
		// annotations overrides , return.
		return catalogOverrides, nil
	}

	if capabilityImageStream {
		// imageStream triggers take presedence if they exist.
		return nil, nil
	}

	pullSecret := common.PullSecret(cpContext.HCP.Namespace)
	if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(pullSecret), pullSecret); err != nil {
		return nil, fmt.Errorf("failed to get pull secret: %w", err)
	}

	catalogImages, err := getCatalogImages(cpContext, pullSecret.Data[corev1.DockerConfigJsonKey])
	if err != nil {
		return nil, fmt.Errorf("failed to get catalog images: %w", err)
	}

	for name, catalog := range catalogImages {
		imageRef, err := reference.Parse(catalog)
		if err != nil {
			return nil, fmt.Errorf("failed to parse catalog image %s: %w", catalog, err)
		}

		digest, _, err := cpContext.ImageMetadataProvider.GetDigest(cpContext, imageRef.Exact(), pullSecret.Data[corev1.DockerConfigJsonKey])
		if err != nil {
			return nil, fmt.Errorf("failed to get manifest for image %s: %v", imageRef.Exact(), err)
		}
		imageRef.ID = digest.String()

		catalogOverrides[name] = imageRef.Exact()
	}

	return catalogOverrides, nil
}

func getCatalogImages(cpContext component.WorkloadContext, pullSecret []byte) (map[string]string, error) {
	registryOverrides := util.ConvertImageRegistryOverrideStringToMap(cpContext.HCP.Annotations[hyperv1.OLMCatalogsISRegistryOverridesAnnotation])
	return catalogs.GetCatalogImages(cpContext, *cpContext.HCP, pullSecret, cpContext.ImageMetadataProvider, registryOverrides)
}

func checkCatalogImageOverides(images map[string]string) (bool, error) {
	override := false
	for _, image := range images {
		if image != "" {
			override = true
			if !strings.Contains(image, "@sha256:") {
				return false, errors.New("images for OLM catalogs should be referenced only by digest")
			}
		}
	}
	if override {
		for _, image := range images {
			if image == "" {
				return false, errors.New("if OLM catalog images are overridden, all the values for the 4 default catalogs should be provided")
			}
		}
	}
	return override, nil
}
