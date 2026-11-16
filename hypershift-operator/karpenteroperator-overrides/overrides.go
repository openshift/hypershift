package karpenteroperatoroverrides

import (
	_ "crypto/sha256"
	_ "crypto/sha512"
	"fmt"
	"os"
	"strings"

	"github.com/openshift/hypershift/support/util/registryoverride"

	"github.com/distribution/reference"
)

// ImageOverrideEnvVar overrides the standalone Karpenter Operator image on HO.
const ImageOverrideEnvVar = "KARPENTER_OPERATOR_IMAGE_OVERRIDE"

// karpenterOperatorImage pins the standalone operator shipped with this HO build.
// Empty means use the hosted cluster's payload unless an override is configured.
const karpenterOperatorImage = ""

// Image selects the annotation override, environment override, pin, then payload.
// Registry overrides apply once to the environment override or pin, not the
// annotation. The payload lookup is lazy and already accounts for registry overrides.
func Image(annotationImage string, payloadImage func() string, registryOverrides map[string]string) (string, error) {
	return resolveImage(annotationImage, os.Getenv(ImageOverrideEnvVar), karpenterOperatorImage, payloadImage, registryOverrides)
}

func resolveImage(annotationImage, overrideImage, pinnedImage string, payloadImage func() string, registryOverrides map[string]string) (string, error) {
	if annotationImage != "" {
		return annotationImage, nil
	}

	image, source := overrideImage, ImageOverrideEnvVar
	isPin := false
	if image == "" {
		image, source = pinnedImage, "pinned Karpenter Operator image"
		isPin = true
	}
	if image == "" {
		return payloadImage(), nil
	}

	if err := validateImage(image, source, isPin); err != nil {
		return "", err
	}
	mappedImage := registryoverride.Replace(image, registryOverrides)
	if mappedImage != image {
		if err := validateImage(mappedImage, source, isPin); err != nil {
			return "", fmt.Errorf("registry override: %w", err)
		}
	}
	return mappedImage, nil
}

func validateImage(image, source string, requireDigest bool) error {
	named, err := reference.ParseNormalizedNamed(image)
	if err != nil {
		return fmt.Errorf("parse %s: %w", source, err)
	}
	registry, _, hasPath := strings.Cut(image, "/")
	if !hasPath || (registry != reference.Domain(named) && registry != "index.docker.io") {
		return fmt.Errorf("%s must include an explicit registry", source)
	}
	if _, hasDigest := named.(reference.Digested); requireDigest && !hasDigest {
		return fmt.Errorf("%s must be pinned by digest", source)
	}
	return nil
}
