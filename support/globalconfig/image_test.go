package globalconfig

import (
	"testing"

	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"

	"k8s.io/apimachinery/pkg/util/validation/field"
)

func TestValidateRegistrySources(t *testing.T) {
	fldPath := field.NewPath("spec", "configuration", "image", "registrySources")

	testCases := []struct {
		name        string
		input       *configv1.RegistrySources
		expectError bool
	}{
		{
			name:        "When registrySources is nil, it should pass",
			input:       nil,
			expectError: false,
		},
		{
			name: "When blockedRegistries contains a valid hostname, it should pass",
			input: &configv1.RegistrySources{
				BlockedRegistries: []string{"docker.io"},
			},
			expectError: false,
		},
		{
			name: "When blockedRegistries contains a valid hostname:port, it should pass",
			input: &configv1.RegistrySources{
				BlockedRegistries: []string{"registry.example.com:5000"},
			},
			expectError: false,
		},
		{
			name: "When blockedRegistries contains a valid hostname/path, it should pass",
			input: &configv1.RegistrySources{
				BlockedRegistries: []string{"quay.io/openshift"},
			},
			expectError: false,
		},
		{
			name: "When blockedRegistries contains a wildcard like *.example.com, it should pass",
			input: &configv1.RegistrySources{
				BlockedRegistries: []string{"*.example.com"},
			},
			expectError: false,
		},
		{
			name: "When blockedRegistries contains a tag like trusted.com/myrepo:latest, it should fail",
			input: &configv1.RegistrySources{
				BlockedRegistries: []string{"trusted.com/myrepo:latest"},
			},
			expectError: true,
		},
		{
			name: "When blockedRegistries contains a digest, it should fail",
			input: &configv1.RegistrySources{
				BlockedRegistries: []string{"quay.io/foo@sha256:abc123"},
			},
			expectError: true,
		},
		{
			name: "When allowedRegistries contains a tag, it should fail",
			input: &configv1.RegistrySources{
				AllowedRegistries: []string{"registry.io/repo:v1"},
			},
			expectError: true,
		},
		{
			name: "When insecureRegistries contains a tag, it should fail",
			input: &configv1.RegistrySources{
				InsecureRegistries: []string{"insecure.io/repo:latest"},
			},
			expectError: true,
		},
		{
			name: "When blockedRegistries has a mix of valid and invalid entries, it should fail",
			input: &configv1.RegistrySources{
				BlockedRegistries: []string{"docker.io", "trusted.com/myrepo:latest"},
			},
			expectError: true,
		},
		{
			name: "When all registries have valid entries, it should pass",
			input: &configv1.RegistrySources{
				BlockedRegistries:  []string{"block.io", "block-2.io/path"},
				InsecureRegistries: []string{"insecure.example.com:8080"},
			},
			expectError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			errs := ValidateRegistrySources(tc.input, fldPath)
			if tc.expectError {
				g.Expect(errs).NotTo(BeEmpty())
			} else {
				g.Expect(errs).To(BeEmpty())
			}
		})
	}
}
