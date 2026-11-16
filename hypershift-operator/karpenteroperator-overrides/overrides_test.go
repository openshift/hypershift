package karpenteroperatoroverrides

import (
	"strings"
	"testing"
)

func TestImage(t *testing.T) {
	t.Run("When the environment override is empty, it should use the compiled pin or payload", func(t *testing.T) {
		t.Setenv(ImageOverrideEnvVar, "")
		got, err := Image("", func() string { return "payload-image" }, nil)
		if err != nil {
			t.Fatal(err)
		}
		want := karpenterOperatorImage
		if want == "" {
			want = "payload-image"
		}
		if got != want {
			t.Errorf("Image() = %q, want %q", got, want)
		}
	})
	t.Run("When the environment override is set, it should use that image", func(t *testing.T) {
		const image = "quay.io/openshift/karpenter-operator:ci-main"
		t.Setenv(ImageOverrideEnvVar, image)
		got, err := Image("", func() string { return "payload-image" }, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got != image {
			t.Errorf("Image() = %q, want %q", got, image)
		}
	})
}

func TestResolveImage(t *testing.T) {
	const (
		digest     = "a8756f77024e616ee670a993f5b2dee227e0ded6dc7f8a725c9cf62c8180eec5"
		pin        = "registry.redhat.io/openshift5/karpenter-operator-rhel9@sha256:" + digest
		override   = "quay.io/openshift/karpenter-operator:ci-main"
		annotation = "quay.io/custom/karpenter-operator:test"
		payload    = "mirror.internal:5000/openshift/karpenter-operator:payload"
	)
	tests := []struct {
		name                      string
		annotation, override, pin string
		registries                map[string]string
		wantImage, wantError      string
		wantLookups               int
	}{
		// Selection precedence and lazy payload lookup.
		{
			name:        "When no explicit image is configured, it should use the payload",
			wantImage:   payload,
			wantLookups: 1,
		},
		{
			name:      "When a pin is configured, it should use the pin without looking up the payload",
			pin:       pin,
			wantImage: pin,
		},
		{
			name:      "When an environment override and pin are configured, it should use the override",
			override:  override,
			pin:       pin,
			wantImage: override,
		},
		{
			name:       "When an annotation is configured, it should ignore invalid lower-priority images",
			annotation: annotation,
			override:   "not a pullspec",
			pin:        "not a pullspec",
			wantImage:  annotation,
		},
		// Validation errors must not trigger a lower-priority image lookup.
		{
			name:      "When the override is malformed, it should return a parsing error without using the pin",
			override:  "not a pullspec",
			pin:       pin,
			wantError: "parse " + ImageOverrideEnvVar,
		},
		// Remap environment overrides and pins once. Keep annotations and payload images unchanged.
		{
			name:       "When registry overrides match the pin, it should remap the pin",
			pin:        pin,
			registries: map[string]string{"registry.redhat.io": "mirror.internal:5000"},
			wantImage:  "mirror.internal:5000/openshift5/karpenter-operator-rhel9@sha256:" + digest,
		},
		{
			name:       "When registry overrides match the environment override, it should remap only once",
			override:   override,
			registries: map[string]string{"quay.io": "mirror.internal:5000", "mirror.internal:5000": "second.internal:5000"},
			wantImage:  "mirror.internal:5000/openshift/karpenter-operator:ci-main",
		},
		{
			name:       "When a registry override produces a bare payload key, it should reject the mapped image",
			override:   "quay.io/openshift/karpenter-operator",
			pin:        pin,
			registries: map[string]string{"quay.io/openshift/karpenter-operator": "karpenter-operator"},
			wantError:  "registry override:",
		},
		{
			name:       "When registry overrides match the annotation, it should keep the annotation unchanged",
			annotation: annotation,
			override:   override,
			registries: map[string]string{"quay.io": "mirror.internal:5000"},
			wantImage:  annotation,
		},
		{
			name:        "When the payload image is already mirrored, it should not remap it again",
			registries:  map[string]string{"mirror.internal:5000": "second.internal:5000"},
			wantImage:   payload,
			wantLookups: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lookups := 0
			got, err := resolveImage(tc.annotation, tc.override, tc.pin, func() string {
				lookups++
				return payload
			}, tc.registries)
			if tc.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("resolveImage() error = %v, want error containing %q", err, tc.wantError)
			}
			if got != tc.wantImage {
				t.Errorf("resolveImage() = %q, want %q", got, tc.wantImage)
			}
			if lookups != tc.wantLookups {
				t.Errorf("payload lookups = %d, want %d", lookups, tc.wantLookups)
			}
		})
	}
}

func TestValidateImage(t *testing.T) {
	const digest = "a8756f77024e616ee670a993f5b2dee227e0ded6dc7f8a725c9cf62c8180eec5"
	tests := []struct {
		name, image, wantError string
		requireDigest          bool
	}{
		{
			name:      "When the override is a bare payload key, it should reject it",
			image:     "karpenter-operator",
			wantError: "explicit registry",
		},
		{
			name:      "When the override has only a namespace, it should reject it",
			image:     "openshift/karpenter-operator:ci-main",
			wantError: "explicit registry",
		},
		{
			name:          "When the pin has no registry, it should reject it",
			image:         "karpenter-operator@sha256:" + digest,
			requireDigest: true,
			wantError:     "explicit registry",
		},
		{
			name:          "When the pin has only a tag, it should require a digest",
			image:         "registry.redhat.io/openshift5/karpenter-operator-rhel9:latest",
			requireDigest: true,
			wantError:     "pinned by digest",
		},
		{
			name:          "When the pin is malformed, it should return a parsing error",
			image:         "not a pullspec",
			requireDigest: true,
			wantError:     "parse pinned Karpenter Operator image",
		},
		{
			name:  "When the override uses an IPv6 registry, it should accept it",
			image: "[2001:db8::1]:5000/karpenter-operator:ci-main",
		},
		{
			name:          "When the pin uses an IPv6 registry, it should accept it",
			image:         "[2001:db8::1]:5000/karpenter-operator@sha256:" + digest,
			requireDigest: true,
		},
		{
			name:  "When the override uses the explicit Docker Hub registry alias, it should accept it",
			image: "index.docker.io/openshift/karpenter-operator:ci-main",
		},
		{
			name:      "When the override has uppercase digest hex, it should reject it",
			image:     "quay.io/openshift/karpenter-operator@sha256:" + strings.ToUpper(digest),
			wantError: "parse " + ImageOverrideEnvVar,
		},
		{
			name:          "When the pin has uppercase digest hex, it should reject it",
			image:         "registry.redhat.io/openshift5/karpenter-operator-rhel9@sha256:" + strings.ToUpper(digest),
			requireDigest: true,
			wantError:     "parse pinned Karpenter Operator image",
		},
		{
			name:          "When the pin uses SHA-512, it should accept it",
			image:         "registry.redhat.io/openshift5/karpenter-operator-rhel9@sha512:" + strings.Repeat("a", 128),
			requireDigest: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			source := ImageOverrideEnvVar
			if tc.requireDigest {
				source = "pinned Karpenter Operator image"
			}
			err := validateImage(tc.image, source, tc.requireDigest)
			if tc.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("validateImage() error = %v, want error containing %q", err, tc.wantError)
			}
		})
	}
}
