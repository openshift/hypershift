package aws

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/openshift/hypershift/cmd/cluster/core"
)

func TestIsRequiredOption(t *testing.T) {
	tests := map[string]struct {
		value         string
		expectedError bool
	}{
		"when value is empty": {
			value:         "",
			expectedError: true,
		},
		"when value is not empty": {
			value:         "",
			expectedError: true,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			err := IsRequiredOption("", test.value)
			if test.expectedError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).To(BeNil())
			}
		})
	}
}

func TestValidateCreateCredentialInfo(t *testing.T) {
	tests := map[string]struct {
		inputOptions *core.CreateOptions
		expectError  bool
	}{
		"when CredentialSecretName is blank and aws-creds is also blank": {
			inputOptions: &core.CreateOptions{
				CredentialSecretName: "",
				AWSPlatform: core.AWSPlatformOptions{
					AWSCredentialsFile: "",
				},
			},
			expectError: true,
		},
		"when CredentialSecretName is blank, aws-creds is not blank, and pull-secret is blank": {
			inputOptions: &core.CreateOptions{
				CredentialSecretName: "",
				AWSPlatform: core.AWSPlatformOptions{
					AWSCredentialsFile: "asdf",
				},
				PullSecretFile: "",
			},
			expectError: true,
		},
		"when CredentialSecretName is blank, aws-creds is not blank, and pull-secret is not blank": {
			inputOptions: &core.CreateOptions{
				CredentialSecretName: "",
				AWSPlatform: core.AWSPlatformOptions{
					AWSCredentialsFile: "asdf",
				},
				PullSecretFile: "asdf",
			},
			expectError: false,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			err := ValidateCreateCredentialInfo(test.inputOptions)
			if test.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).To(BeNil())
			}
		})
	}
}

func TestValidateMultiArchRelease(t *testing.T) {
	tests := map[string]struct {
		inputOptions *core.CreateOptions
		expectError  bool
	}{
		"non-multi-arch release image used": {
			inputOptions: &core.CreateOptions{
				ReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.16.0-ec.3-aarch64",
				AWSPlatform: core.AWSPlatformOptions{
					MultiArch: true,
				},
			},
			expectError: true,
		},
		"non-multi-arch release stream used": {
			inputOptions: &core.CreateOptions{
				ReleaseStream: "stable",
				AWSPlatform: core.AWSPlatformOptions{
					MultiArch: true,
				},
			},
			expectError: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			err := validateMultiArchRelease(context.Background(), test.inputOptions)
			if test.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).To(BeNil())
			}
		})
	}
}
