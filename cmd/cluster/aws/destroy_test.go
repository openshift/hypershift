package aws

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/cmd/cluster/core"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
)

func Test_ValidateCredentialInfo(t *testing.T) {
	tests := map[string]struct {
		inputOptions *core.DestroyOptions
		expectError  bool
	}{
		"when CredentialSecretName is blank and aws-creds is also blank it should fall back to SDK default chain": {
			inputOptions: &core.DestroyOptions{
				CredentialSecretName: "",
				AWSPlatform: core.AWSPlatformDestroyOptions{
					Credentials: awsutil.AWSCredentialsOptions{
						AWSCredentialsFile: "",
					},
				},
			},
			expectError: false,
		},
		"when CredentialSecretName is blank and aws-creds is not blank": {
			inputOptions: &core.DestroyOptions{
				CredentialSecretName: "",
				AWSPlatform: core.AWSPlatformDestroyOptions{
					Credentials: awsutil.AWSCredentialsOptions{
						AWSCredentialsFile: "asdf",
					},
				},
			},
			expectError: false,
		},
		"when CredentialSecretName is set and AWSCredentialsFile is empty and RoleArn is empty it should fail": {
			inputOptions: &core.DestroyOptions{
				CredentialSecretName: "my-secret",
				AWSPlatform: core.AWSPlatformDestroyOptions{
					Credentials: awsutil.AWSCredentialsOptions{
						AWSCredentialsFile: "",
						RoleArn:            "",
					},
				},
			},
			expectError: true,
		},
		"when CredentialSecretName is set and AWSCredentialsFile is not empty it should try to validate the secret": {
			inputOptions: &core.DestroyOptions{
				CredentialSecretName: "my-secret",
				Kubeconfig:           "/nonexistent/kubeconfig",
				AWSPlatform: core.AWSPlatformDestroyOptions{
					Credentials: awsutil.AWSCredentialsOptions{
						AWSCredentialsFile: "/some/creds",
					},
				},
			},
			expectError: true,
		},
		"when CredentialSecretName is set and RoleArn is set it should try to validate the secret": {
			inputOptions: &core.DestroyOptions{
				CredentialSecretName: "my-secret",
				Kubeconfig:           "/nonexistent/kubeconfig",
				AWSPlatform: core.AWSPlatformDestroyOptions{
					Credentials: awsutil.AWSCredentialsOptions{
						AWSCredentialsFile: "",
						RoleArn:            "arn:aws:iam::123456789:role/my-role",
					},
				},
			},
			expectError: true,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			options := test.inputOptions
			err := ValidateCredentialInfo(options.AWSPlatform.Credentials, options.CredentialSecretName, options.Namespace, options.Kubeconfig)
			if test.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).To(BeNil())
			}
		})
	}
}
