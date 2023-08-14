package aws

import (
	"github.com/openshift/hypershift/cmd/cluster/core"
	"testing"

	. "github.com/onsi/gomega"
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
					AWSCredentialsFile: "",
				},
			},
			expectError: true,
		},
		"when CredentialSecretName is blank and aws-creds is not blank": {
			inputOptions: &core.DestroyOptions{
				CredentialSecretName: "",
				AWSPlatform: core.AWSPlatformDestroyOptions{
					AWSCredentialsFile: "asdf",
				},
			},
			expectError: false,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			err := ValidateCredentialInfo(test.inputOptions)
			if test.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).To(BeNil())
			}
		})
	}
}
