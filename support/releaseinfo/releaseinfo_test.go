package releaseinfo

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/releaseinfo/fixtures"

	imageapi "github.com/openshift/api/image/v1"
)

func TestParseComponentVersionsLabel(t *testing.T) {
	tests := []struct {
		name         string
		label        string
		displayNames string
		expectError  bool
		expectKey    string
		expectName   string
	}{
		{
			name:         "When display name has no periods it should parse successfully",
			label:        "rhel-coreos=98.20260101.0",
			displayNames: "rhel-coreos=Red Hat Enterprise Linux CoreOS 98",
			expectKey:    "rhel-coreos",
			expectName:   "Red Hat Enterprise Linux CoreOS 98",
		},
		{
			name:         "When display name contains periods it should parse successfully",
			label:        "rhel-coreos=98.20260101.0",
			displayNames: "rhel-coreos=Red Hat Enterprise Linux CoreOS 9.8",
			expectKey:    "rhel-coreos",
			expectName:   "Red Hat Enterprise Linux CoreOS 9.8",
		},
		{
			name:         "When display name contains parentheses and colons it should parse successfully",
			label:        "mycomponent=1.0.0",
			displayNames: "mycomponent=My Component (v1.0): Beta",
			expectKey:    "mycomponent",
			expectName:   "My Component (v1.0): Beta",
		},
		{
			name:         "When display name contains invalid characters it should return an error",
			label:        "mycomponent=1.0.0",
			displayNames: "mycomponent=Invalid <Name>",
			expectError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			versions, err := parseComponentVersionsLabel(tt.label, tt.displayNames)
			if tt.expectError {
				if err == nil {
					t.Fatal("expected error but got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			cv, ok := versions[tt.expectKey]
			if !ok {
				t.Fatalf("expected key %q in versions", tt.expectKey)
			}
			if cv.DisplayName != tt.expectName {
				t.Errorf("expected display name %q, got %q", tt.expectName, cv.DisplayName)
			}
		})
	}
}

func TestReadComponentVersions(t *testing.T) {
	tests := []struct {
		name        string
		tags        []imageapi.TagReference
		expectError bool
		expectKey   string
	}{
		{
			name: "When multiple machine-os versions exist it should not return an error",
			tags: []imageapi.TagReference{
				{
					Name: "machine-os-content",
					Annotations: map[string]string{
						annotationBuildVersions:             "machine-os=1.0.0",
						annotationBuildVersionsDisplayNames: "machine-os=Machine OS 1",
					},
				},
				{
					Name: "machine-os-content-2",
					Annotations: map[string]string{
						annotationBuildVersions:             "machine-os=2.0.0",
						annotationBuildVersionsDisplayNames: "machine-os=Machine OS 2",
					},
				},
			},
			expectKey: "machine-os",
		},
		{
			name: "When multiple non-machine-os versions exist it should return an error",
			tags: []imageapi.TagReference{
				{
					Name: "component-a",
					Annotations: map[string]string{
						annotationBuildVersions: "other-component=1.0.0",
					},
				},
				{
					Name: "component-b",
					Annotations: map[string]string{
						annotationBuildVersions: "other-component=2.0.0",
					},
				},
			},
			expectError: true,
			expectKey:   "other-component",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			is := &imageapi.ImageStream{
				Spec: imageapi.ImageStreamSpec{
					Tags: tt.tags,
				},
			}
			versions, errs := readComponentVersions(is)
			if tt.expectError {
				g.Expect(errs).NotTo(BeEmpty(), "expected errors but got none")
			} else {
				g.Expect(errs).To(BeEmpty(), "expected no errors but got: %v", errs)
			}
			g.Expect(versions).To(HaveKey(tt.expectKey))
		})
	}
}

// TestReleaseInfoPowerVS test validates the presence of the powervs images in the 4.10 release
func TestReleaseInfoPowerVS(t *testing.T) {
	metadata, err := DeserializeImageMetadata(fixtures.CoreOSBootImagesYAML_4_10)
	if err != nil {
		t.Fatal(err)
	}
	arch, ok := metadata.Architectures["ppc64le"]
	if !ok {
		t.Fatal("metadata does not contain the ppc64le architecture")
	}
	if len(arch.Images.PowerVS.Regions) == 0 {
		t.Fatal("metadata does not contain any powervs regions")
	}
	for _, region := range arch.Images.PowerVS.Regions {
		if region.Release == "" || region.Object == "" || region.Bucket == "" || region.URL == "" {
			t.Fatalf("none of the fields in the image can be empty: %+v", region)
		}
	}
}

// TestReleaseInfoKubeVirt tests validates the presence of the kubevirt images
func TestReleaseInfoKubeVirt(t *testing.T) {
	metadata, err := DeserializeImageMetadata(fixtures.CoreOSBootImagesYAML_4_10)
	if err != nil {
		t.Fatal(err)
	}
	arch, ok := metadata.Architectures["x86_64"]
	if !ok {
		t.Fatal("metadata does not contain the x86_64 architecture")
	}
	if arch.Images.Kubevirt.DigestRef == "" {
		t.Fatal("metadata does not contain a digest ref for kubevirt")
	}
}
