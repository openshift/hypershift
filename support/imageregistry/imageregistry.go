package imageregistry

import (
	"context"
	"fmt"
	"sort"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	controlplaneoperatoroverrides "github.com/openshift/hypershift/hypershift-operator/controlplaneoperator-overrides"
	"github.com/openshift/hypershift/support/releaseinfo"

	"github.com/blang/semver"
)

func HCControlPlaneReleaseImage(hcluster *hyperv1.HostedCluster) string {
	if hcluster.Spec.ControlPlaneRelease != nil {
		return hcluster.Spec.ControlPlaneRelease.Image
	}
	return hcluster.Spec.Release.Image
}

func HCPControlPlaneReleaseImage(hcp *hyperv1.HostedControlPlane) string {
	if hcp.Spec.ControlPlaneReleaseImage != nil {
		return *hcp.Spec.ControlPlaneReleaseImage
	}
	return hcp.Spec.ReleaseImage
}

// CPOImageSource identifies which of the GetControlPlaneOperatorImage precedence
// rules produced an image. Callers need this to tell whether the image has already
// had the release provider's registry overrides applied to it: the release payload
// image is mirrored as a side effect of the provider rewriting the image stream tags
// it resolves, while the other sources are returned verbatim.
type CPOImageSource string

const (
	// CPOImageSourceAnnotation is the ControlPlaneOperatorImageAnnotation on the
	// HostedCluster. Returned verbatim.
	CPOImageSourceAnnotation CPOImageSource = "Annotation"
	// CPOImageSourceOverrideTable is the embedded CPO override table, consulted only
	// when CPO overrides are enabled. Returned verbatim.
	CPOImageSourceOverrideTable CPOImageSource = "OverrideTable"
	// CPOImageSourcePayload is the hypershift component image from the release payload.
	// Already mirrored by releaseinfo.RegistryMirrorProviderDecorator.
	CPOImageSourcePayload CPOImageSource = "Payload"
	// CPOImageSourceOperator is the hypershift operator's own image, used as a fallback
	// for release payloads that ship no hypershift component image.
	CPOImageSourceOperator CPOImageSource = "HypershiftOperator"
)

// GetControlPlaneOperatorImage resolves the appropriate control plane operator
// image. See GetControlPlaneOperatorImageWithSource for the order of precedence.
func GetControlPlaneOperatorImage(ctx context.Context, hc *hyperv1.HostedCluster, releaseProvider releaseinfo.Provider, hypershiftOperatorImage string, pullSecret []byte) (string, error) {
	image, _, err := GetControlPlaneOperatorImageWithSource(ctx, hc, releaseProvider, hypershiftOperatorImage, pullSecret)
	return image, err
}

// GetControlPlaneOperatorImageWithSource resolves the appropriate control plane
// operator image based on the following order of precedence (from most to least
// preferred), and reports which rule produced it:
//
//  1. The image specified by the ControlPlaneOperatorImageAnnotation on the
//     HostedCluster resource itself (CPOImageSourceAnnotation)
//  2. The image specified by the CPO override table, when CPO overrides are
//     enabled (CPOImageSourceOverrideTable)
//  3. The hypershift image specified in the release payload indicated by the
//     HostedCluster's release field (CPOImageSourcePayload)
//  4. The hypershift-operator's own image for release versions 4.9 and 4.10
//     (CPOImageSourceOperator)
//
// If no image can be found according to these rules, an error is returned.
func GetControlPlaneOperatorImageWithSource(ctx context.Context, hc *hyperv1.HostedCluster, releaseProvider releaseinfo.Provider, hypershiftOperatorImage string, pullSecret []byte) (string, CPOImageSource, error) {
	if val, ok := hc.Annotations[hyperv1.ControlPlaneOperatorImageAnnotation]; ok {
		return val, CPOImageSourceAnnotation, nil
	}
	releaseInfo, err := releaseProvider.Lookup(ctx, HCControlPlaneReleaseImage(hc), pullSecret)
	if err != nil {
		return "", "", err
	}
	version, err := semver.Parse(releaseInfo.Version())
	if err != nil {
		return "", "", err
	}
	if controlplaneoperatoroverrides.IsOverridesEnabled() {
		overrideImage := controlplaneoperatoroverrides.CPOImage(string(hc.Spec.Platform.Type), version.String())
		if overrideImage != "" {
			return overrideImage, CPOImageSourceOverrideTable, nil
		}
	}

	if hypershiftImage, exists := releaseInfo.ComponentImages()["hypershift"]; exists {
		return hypershiftImage, CPOImageSourcePayload, nil
	}

	if version.Minor < 9 {
		return "", "", fmt.Errorf("unsupported release image with version %s", version.String())
	}
	return hypershiftOperatorImage, CPOImageSourceOperator, nil
}

// GetControlPlaneOperatorImageLabels resolves the appropriate control plane
// operator image labels based on the following order of precedence (from most
// to least preferred):
//
//  1. The labels specified by the ControlPlaneOperatorImageLabelsAnnotation on the
//     HostedCluster resource itself
//  2. The image labels in the medata of the image as resolved by GetControlPlaneOperatorImage
func GetControlPlaneOperatorImageLabels(ctx context.Context, hc *hyperv1.HostedCluster, controlPlaneOperatorImage string, pullSecret []byte, imageMetadataProvider ImageMetadataProvider) (map[string]string, error) {
	if val, ok := hc.Annotations[hyperv1.ControlPlaneOperatorImageLabelsAnnotation]; ok {
		annotatedLabels := map[string]string{}
		rawLabels := strings.Split(val, ",")
		for i, rawLabel := range rawLabels {
			parts := strings.Split(rawLabel, "=")
			if len(parts) != 2 {
				return nil, fmt.Errorf("hosted cluster %s/%s annotation %d malformed: label %s not in key=value form", hc.Namespace, hc.Name, i, rawLabel)
			}
			annotatedLabels[parts[0]] = parts[1]
		}
		return annotatedLabels, nil
	}

	controlPlaneOperatorImageMetadata, err := imageMetadataProvider.ImageMetadata(ctx, controlPlaneOperatorImage, pullSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to look up image metadata for %s: %w", controlPlaneOperatorImage, err)
	}

	return ImageLabels(controlPlaneOperatorImageMetadata), nil
}

// ConvertRegistryOverridesToCommandLineFlag converts a map of registry sources and their mirrors into a string
func ConvertRegistryOverridesToCommandLineFlag(registryOverrides map[string]string) string {
	var commandLineFlagArray []string
	for registrySource, registryReplacement := range registryOverrides {
		commandLineFlagArray = append(commandLineFlagArray, fmt.Sprintf("%s=%s", registrySource, registryReplacement))
	}
	if len(commandLineFlagArray) > 0 {
		sort.Strings(commandLineFlagArray)
		return strings.Join(commandLineFlagArray, ",")
	}
	// this is the equivalent of null on a StringToString command line variable.
	return "="
}

// ConvertOpenShiftImageRegistryOverridesToCommandLineFlag converts a map of image registry sources and their mirrors into a string
func ConvertOpenShiftImageRegistryOverridesToCommandLineFlag(registryOverrides map[string][]string) string {
	var commandLineFlagArray []string
	var sortedRegistrySources []string

	for k := range registryOverrides {
		sortedRegistrySources = append(sortedRegistrySources, k)
	}
	sort.Strings(sortedRegistrySources)

	for _, registrySource := range sortedRegistrySources {
		registryReplacements := registryOverrides[registrySource]
		sort.Strings(registryReplacements)
		for _, registryReplacement := range registryReplacements {
			commandLineFlagArray = append(commandLineFlagArray, fmt.Sprintf("%s=%s", registrySource, registryReplacement))
		}
	}
	if len(commandLineFlagArray) > 0 {
		return strings.Join(commandLineFlagArray, ",")
	}
	// this is the equivalent of null on a StringToString command line variable.
	return "="
}

// ConvertImageRegistryOverrideStringToMap translates the environment variable containing registry source to mirror
// mappings back to a map[string]string structure that can be ingested by the registry image content policies release provider
func ConvertImageRegistryOverrideStringToMap(envVar string) map[string][]string {
	registryMirrorPair := strings.Split(envVar, ",")

	if (len(registryMirrorPair) == 1 && registryMirrorPair[0] == "") || envVar == "=" {
		return nil
	}

	imageRegistryOverrides := make(map[string][]string)

	for _, pair := range registryMirrorPair {
		registryMirror := strings.SplitN(pair, "=", 2)
		if len(registryMirror) != 2 {
			continue
		}
		registry := registryMirror[0]
		mirror := registryMirror[1]

		// Skip empty registry or mirror entries
		if registry == "" || mirror == "" {
			continue
		}

		imageRegistryOverrides[registry] = append(imageRegistryOverrides[registry], mirror)
	}

	return imageRegistryOverrides
}
