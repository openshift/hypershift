package karpenteroperatoroverrides

import (
	_ "embed"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

//go:embed assets/overrides_sample.yaml
var overridesSampleYAML []byte

func TestImage(t *testing.T) {
	t.Run("When the environment override is empty, it should accept the embedded pin configuration", func(t *testing.T) {
		t.Setenv(ImageOverrideEnvVar, "")
		if _, err := Image(); err != nil {
			t.Fatalf("invalid embedded karpenter-operator override: %v", err)
		}
	})
	t.Run("When the environment override is set, it should use that image", func(t *testing.T) {
		const image = "quay.io/openshift/karpenter-operator:ci-main"
		t.Setenv(ImageOverrideEnvVar, image)
		got, err := Image()
		if err != nil {
			t.Fatal(err)
		}
		if got != image {
			t.Errorf("Image() = %q, want %q", got, image)
		}
	})
	t.Run("When the environment override is invalid, it should return an image parsing error", func(t *testing.T) {
		t.Setenv(ImageOverrideEnvVar, "not a pullspec")
		if _, err := Image(); err == nil || !strings.Contains(err.Error(), "parse "+ImageOverrideEnvVar+":") {
			t.Fatalf("Image() error = %v, want an image parsing error", err)
		}
	})
}

func TestImageFromFS(t *testing.T) {
	const pinnedImage = "registry.redhat.io/openshift4/karpenter-operator-rhel9@sha256:88c55ea554f7e62a64e34ff8d3be45ef85ef6b80fe4e9b0240b9a1aa226f9d98"
	tests := []struct {
		overrideImage string
		name          string
		files         fstest.MapFS
		wantImage     string
		wantError     string
	}{
		// Without an environment override: optional inputs, then a configured pin.
		{
			name: "When the pin file is absent, it should return no pin",
		},
		{
			name:  "When the pin file is empty, it should return no pin",
			files: fstest.MapFS{"assets/overrides.yaml": {Data: nil}},
		},
		{
			name:  "When the image field is empty, it should return no pin",
			files: fstest.MapFS{"assets/overrides.yaml": {Data: []byte("image: \"\"\n")}},
		},
		{
			name:      "When the sample file contains a digest-pinned image, it should return that pullspec",
			files:     fstest.MapFS{"assets/overrides.yaml": {Data: overridesSampleYAML}},
			wantImage: pinnedImage,
		},
		// Environment override precedence and validation.
		{
			name:          "When an environment override supplies a tag and a pin exists, it should use the override",
			overrideImage: "quay.io/openshift/karpenter-operator:ci-main",
			files:         fstest.MapFS{"assets/overrides.yaml": {Data: overridesSampleYAML}},
			wantImage:     "quay.io/openshift/karpenter-operator:ci-main",
		},
		{
			name:          "When an environment override supplies a digest and pin YAML is malformed, it should use the override without reading the pin",
			overrideImage: "registry.stage.redhat.io/openshift5/karpenter-operator-rhel9@sha256:a8756f77024e616ee670a993f5b2dee227e0ded6dc7f8a725c9cf62c8180eec5",
			files:         fstest.MapFS{"assets/overrides.yaml": {Data: []byte("image: [")}},
			wantImage:     "registry.stage.redhat.io/openshift5/karpenter-operator-rhel9@sha256:a8756f77024e616ee670a993f5b2dee227e0ded6dc7f8a725c9cf62c8180eec5",
		},
		{
			name:          "When the environment override is invalid and a pin exists, it should return an error without falling back",
			overrideImage: "not a pullspec",
			files:         fstest.MapFS{"assets/overrides.yaml": {Data: overridesSampleYAML}},
			wantError:     "parse KARPENTER_OPERATOR_IMAGE_OVERRIDE:",
		},
		// Failures in processing order: file read, YAML decoding, image parsing, digest requirement.
		{
			name:      "When the pin file path is a directory, it should return a file read error",
			files:     fstest.MapFS{"assets/overrides.yaml": {Mode: fs.ModeDir}},
			wantError: "read karpenter-operator override:",
		},
		{
			name:      "When the YAML is malformed, it should return a YAML parsing error",
			files:     fstest.MapFS{"assets/overrides.yaml": {Data: []byte("image: [")}},
			wantError: "parse karpenter-operator override:",
		},
		{
			name:      "When the YAML contains an unknown field, it should return a strict decoding error",
			files:     fstest.MapFS{"assets/overrides.yaml": {Data: []byte("images: " + pinnedImage)}},
			wantError: "field images not found",
		},
		{
			name:      "When the image digest is malformed, it should return an image parsing error",
			files:     fstest.MapFS{"assets/overrides.yaml": {Data: []byte("image: registry.redhat.io/openshift4/karpenter-operator-rhel9@sha256:invalid")}},
			wantError: "parse karpenter-operator image:",
		},
		{
			name:      "When the image has only a tag, it should return a missing-digest error",
			files:     fstest.MapFS{"assets/overrides.yaml": {Data: []byte("image: registry.redhat.io/openshift4/karpenter-operator-rhel9:latest")}},
			wantError: "karpenter-operator override must be pinned by digest",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := imageFromFS(tc.files, tc.overrideImage)
			if tc.wantError == "" {
				if err != nil {
					t.Fatalf("imageFromFS() returned an unexpected error: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("imageFromFS() error = %v, want error containing %q", err, tc.wantError)
			}
			if got != tc.wantImage {
				t.Errorf("imageFromFS() = %q, want %q", got, tc.wantImage)
			}
		})
	}
}
