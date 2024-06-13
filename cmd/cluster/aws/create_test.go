package aws

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/openshift/hypershift/cmd/cluster/core"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/cmd/util"
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
			err := util.ValidateRequiredOption("", test.value)
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
		inputOptions    *core.RawCreateOptions
		inputAWSOptions *CreateOptions
		expectError     bool
	}{
		"when CredentialSecretName is blank and aws-creds is also blank": {
			inputOptions: &core.RawCreateOptions{},
			inputAWSOptions: &CreateOptions{
				CredentialSecretName: "",
				Credentials:          awsutil.AWSCredentialsOptions{},
			},
			expectError: true,
		},
		"when CredentialSecretName is blank, aws-creds is not blank, and pull-secret is blank": {
			inputOptions: &core.RawCreateOptions{
				PullSecretFile: "",
			},
			inputAWSOptions: &CreateOptions{
				CredentialSecretName: "",
				Credentials:          awsutil.AWSCredentialsOptions{AWSCredentialsFile: "asdf"},
			},
			expectError: true,
		},
		"when CredentialSecretName is blank, aws-creds is not blank, and pull-secret is not blank": {
			inputOptions: &core.RawCreateOptions{
				PullSecretFile: "asdf",
			},
			inputAWSOptions: &CreateOptions{
				CredentialSecretName: "",
				Credentials:          awsutil.AWSCredentialsOptions{AWSCredentialsFile: "asdf"},
			},
			expectError: false,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			err := ValidateCreateCredentialInfo(test.inputAWSOptions.Credentials, test.inputAWSOptions.CredentialSecretName, test.inputOptions.Namespace, test.inputOptions.PullSecretFile)
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
		inputOptions    *core.RawCreateOptions
		inputAWSOptions *CreateOptions
		expectError     bool
	}{
		"non-multi-arch release image used": {
			inputOptions: &core.RawCreateOptions{
				ReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.16.0-ec.3-aarch64",
			},
			inputAWSOptions: &CreateOptions{
				MultiArch: true,
			},
			expectError: true,
		},
		"non-multi-arch release stream used": {
			inputOptions: &core.RawCreateOptions{
				ReleaseStream: "stable",
			},
			inputAWSOptions: &CreateOptions{
				MultiArch: true,
			},
			expectError: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			err := validateMultiArchRelease(context.Background(), test.inputOptions, test.inputAWSOptions)
			if test.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).To(BeNil())
			}
		})
	}
}
