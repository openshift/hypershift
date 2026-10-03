package aws

import (
	"context"
	"errors"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/cmd/cluster/core"
	awsinfra "github.com/openshift/hypershift/cmd/infra/aws"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"

	"github.com/go-logr/logr"
)

func TestDestroyPlatformSpecificsRunsPostDeleteActionAfterInfra(t *testing.T) {
	g := NewGomegaWithT(t)
	originalRunDestroyInfra := runDestroyInfra
	t.Cleanup(func() { runDestroyInfra = originalRunDestroyInfra })

	infraFinished := false
	postDeleteCalled := false
	postDeleteObservedInfraFinished := false
	runDestroyInfra = func(_ context.Context, _ *awsinfra.DestroyInfraOptions) error {
		infraFinished = true
		return errors.New("infra failure")
	}

	err := destroyPlatformSpecifics(t.Context(), &core.DestroyOptions{
		InfraID: "infra-id",
		Name:    "cluster",
		Log:     logr.Discard(),
		AWSPlatform: core.AWSPlatformDestroyOptions{
			Region:      "us-east-1",
			PreserveIAM: true,
			PostDeleteAction: func() {
				postDeleteCalled = true
				postDeleteObservedInfraFinished = infraFinished
			},
		},
	})

	g.Expect(err).To(MatchError(ContainSubstring("failed to destroy infrastructure")))
	g.Expect(postDeleteCalled).To(BeTrue())
	g.Expect(postDeleteObservedInfraFinished).To(BeTrue())
}

func TestValidateCredentialInfo(t *testing.T) {
	tests := map[string]struct {
		inputOptions *core.DestroyOptions
		expectError  bool
	}{
		"When CredentialSecretName is blank and aws-creds is also blank, it should fall back to SDK default chain": {
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
		"When CredentialSecretName is blank and aws-creds is not blank, it should succeed": {
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
		"When CredentialSecretName is set and AWSCredentialsFile is empty and RoleArn is empty, it should fail": {
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
		"When CredentialSecretName is set and AWSCredentialsFile is not empty, it should try to validate the secret": {
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
		"When CredentialSecretName is set and RoleArn is set, it should try to validate the secret": {
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
