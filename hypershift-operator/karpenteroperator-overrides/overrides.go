package karpenteroperatoroverrides

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/reference"

	"gopkg.in/yaml.v2"
)

// ImageOverrideEnvVar overrides the standalone Karpenter Operator image on HO.
const ImageOverrideEnvVar = "KARPENTER_OPERATOR_IMAGE_OVERRIDE"

// Embed the directory so builds still work when overrides.yaml is absent.
//
//go:embed assets
var assets embed.FS

// Image returns the environment override or the pin. An empty result lets the
// caller use the payload when neither is configured.
func Image() (string, error) {
	return imageFromFS(assets, os.Getenv(ImageOverrideEnvVar))
}

func imageFromFS(assets fs.FS, overrideImage string) (string, error) {
	if overrideImage != "" {
		if _, err := reference.Parse(overrideImage); err != nil {
			return "", fmt.Errorf("parse %s: %w", ImageOverrideEnvVar, err)
		}
		return overrideImage, nil
	}

	content, err := fs.ReadFile(assets, "assets/overrides.yaml")
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read karpenter-operator override: %w", err)
	}
	var override struct {
		Image string `yaml:"image"`
	}
	if err := yaml.UnmarshalStrict(content, &override); err != nil {
		return "", fmt.Errorf("parse karpenter-operator override: %w", err)
	}
	if override.Image == "" {
		return "", nil
	}
	ref, err := reference.Parse(override.Image)
	if err != nil {
		return "", fmt.Errorf("parse karpenter-operator image: %w", err)
	}
	if ref.ID == "" {
		return "", fmt.Errorf("karpenter-operator override must be pinned by digest")
	}
	return override.Image, nil
}
