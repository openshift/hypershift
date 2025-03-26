package catalogs

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	component "github.com/openshift/hypershift/support/controlplane-component"

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

	catalogImages, err := getCatalogImages(cpContext, pullSecret.Data[corev1.DockerConfigJsonKey])
	if err != nil {
		return fmt.Errorf("failed to get catalog images: %w", err)
	}

	for name, image := range catalogImages {
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
