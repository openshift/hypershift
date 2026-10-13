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
		"when CredentialSecretName is blank and aws-creds is also blank": {
			inputOptions: &core.DestroyOptions{
				CredentialSecretName: "",
				AWSPlatform: core.AWSPlatformDestroyOptions{
					Credentials: awsutil.AWSCredentialsOptions{
						AWSCredentialsFile: "",
					},
				},
			},
			expectError: true,
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
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			options := test.inputOptions
			err := ValidateCredentialInfo(options.AWSPlatform.Credentials, options.CredentialSecretName, options.Namespace)
			if test.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).To(BeNil())
			}
		})
	}
}
