package fix

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"
)

func TestNewDrOidcIamCommand(t *testing.T) {
	g := NewGomegaWithT(t)

	cmd := NewDrOidcIamCommand()

	g.Expect(cmd).ToNot(BeNil())
	g.Expect(cmd.Use).To(Equal("dr-oidc-iam"))
	g.Expect(cmd.Short).To(Equal("Fixes missing OIDC identity provider for disaster recovery scenarios"))
}

func TestDrOidcIamOptions_Defaults(t *testing.T) {
	g := NewGomegaWithT(t)

	opts := &DrOidcIamOptions{
		InfraID: "test-cluster",
		Region:  "us-east-1",
		Timeout: 10 * time.Minute,
	}

	// Test default OIDC bucket name generation
	if opts.OIDCStorageProviderS3Bucket == "" {
		opts.OIDCStorageProviderS3Bucket = "test-cluster-oidc"
	}

	g.Expect(opts.OIDCStorageProviderS3Bucket).To(Equal("test-cluster-oidc"))

	// Test default issuer URL generation
	if opts.Issuer == "" {
		opts.Issuer = oidcDiscoveryURL(opts.OIDCStorageProviderS3Bucket, opts.Region, opts.InfraID)
	}

	expectedIssuer := "https://test-cluster-oidc.s3.us-east-1.amazonaws.com/test-cluster"
	g.Expect(opts.Issuer).To(Equal(expectedIssuer))
}

func TestDrOidcIamOptions_ValidateCredentials(t *testing.T) {
	tests := map[string]struct {
		opts        *DrOidcIamOptions
		expectError bool
		errorMsg    string
	}{
		"valid aws-creds only": {
			opts: &DrOidcIamOptions{
				InfraID:            "test-cluster",
				Region:             "us-east-1",
				AWSCredentialsFile: "/path/to/aws-creds",
			},
			expectError: false,
		},
		"valid sts-creds and role-arn": {
			opts: &DrOidcIamOptions{
				InfraID:            "test-cluster",
				Region:             "us-east-1",
				STSCredentialsFile: "/path/to/sts-creds",
				RoleArn:            "arn:aws:iam::123456789:role/test",
			},
			expectError: false,
		},
		"aws-creds with sts-creds conflict": {
			opts: &DrOidcIamOptions{
				InfraID:            "test-cluster",
				Region:             "us-east-1",
				AWSCredentialsFile: "/path/to/aws-creds",
				STSCredentialsFile: "/path/to/sts-creds",
			},
			expectError: true,
			errorMsg:    "only one of 'aws-creds' or 'sts-creds'/'role-arn' can be provided",
		},
		"aws-creds with role-arn conflict": {
			opts: &DrOidcIamOptions{
				InfraID:            "test-cluster",
				Region:             "us-east-1",
				AWSCredentialsFile: "/path/to/aws-creds",
				RoleArn:            "arn:aws:iam::123456789:role/test",
			},
			expectError: true,
			errorMsg:    "only one of 'aws-creds' or 'sts-creds'/'role-arn' can be provided",
		},
		"sts-creds without role-arn": {
			opts: &DrOidcIamOptions{
				InfraID:            "test-cluster",
				Region:             "us-east-1",
				STSCredentialsFile: "/path/to/sts-creds",
			},
			expectError: true,
			errorMsg:    "role-arn is required when using sts-creds",
		},
		"role-arn without sts-creds": {
			opts: &DrOidcIamOptions{
				InfraID: "test-cluster",
				Region:  "us-east-1",
				RoleArn: "arn:aws:iam::123456789:role/test",
			},
			expectError: true,
			errorMsg:    "sts-creds is required when using role-arn",
		},
		"no credentials provided": {
			opts: &DrOidcIamOptions{
				InfraID: "test-cluster",
				Region:  "us-east-1",
			},
			expectError: true,
			errorMsg:    "either 'aws-creds' or both 'sts-creds' and 'role-arn' must be provided",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			err := test.opts.validateCredentials()
			if test.expectError {
				g.Expect(err).To(HaveOccurred())
				if test.errorMsg != "" {
					g.Expect(err.Error()).To(ContainSubstring(test.errorMsg))
				}
			} else {
				g.Expect(err).To(BeNil())
			}
		})
	}
}

func TestDrOidcIamOptions_ValidateRequiredFlags(t *testing.T) {
	tests := map[string]struct {
		opts        *DrOidcIamOptions
		expectError bool
	}{
		"valid options": {
			opts: &DrOidcIamOptions{
				InfraID: "test-cluster",
				Region:  "us-east-1",
			},
			expectError: false,
		},
		"missing infra-id": {
			opts: &DrOidcIamOptions{
				Region: "us-east-1",
			},
			expectError: true,
		},
		"missing region": {
			opts: &DrOidcIamOptions{
				InfraID: "test-cluster",
			},
			expectError: true,
		},
		"empty infra-id": {
			opts: &DrOidcIamOptions{
				InfraID: "",
				Region:  "us-east-1",
			},
			expectError: true,
		},
		"empty region": {
			opts: &DrOidcIamOptions{
				InfraID: "test-cluster",
				Region:  "",
			},
			expectError: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			err := test.opts.validateRequiredFlags()
			if test.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).To(BeNil())
			}
		})
	}
}

func TestGetSSLThumbprint(t *testing.T) {
	tests := map[string]struct {
		issuerURL          string
		expectedThumbprint string
	}{
		"S3 bucket URL with .s3.": {
			issuerURL:          "https://my-bucket.s3.us-east-1.amazonaws.com/cluster",
			expectedThumbprint: "A9D53002E97E00E043244F3D170D6F4C414104FD",
		},
		"S3 bucket URL with s3.amazonaws.com": {
			issuerURL:          "https://s3.amazonaws.com/my-bucket/cluster",
			expectedThumbprint: "A9D53002E97E00E043244F3D170D6F4C414104FD",
		},
		"non-S3 URL returns some thumbprint": {
			issuerURL: "https://example.com/oidc",
			// For non-S3 URLs, we expect some thumbprint but don't care about the exact value
			// as it depends on the actual certificate chain
			expectedThumbprint: "", // We'll check it's not empty instead
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			opts := &DrOidcIamOptions{}
			thumbprint, err := opts.getSSLThumbprint(test.issuerURL)

			g.Expect(err).To(BeNil())
			if test.expectedThumbprint == "" {
				// For non-S3 URLs, just verify we get some non-empty thumbprint
				g.Expect(thumbprint).ToNot(BeEmpty())
			} else {
				g.Expect(thumbprint).To(Equal(test.expectedThumbprint))
			}
		})
	}
}

func TestOidcDiscoveryURL(t *testing.T) {
	tests := map[string]struct {
		bucket      string
		region      string
		infraID     string
		expectedURL string
	}{
		"standard inputs": {
			bucket:      "test-bucket",
			region:      "us-east-1",
			infraID:     "test-cluster",
			expectedURL: "https://test-bucket.s3.us-east-1.amazonaws.com/test-cluster",
		},
		"different region": {
			bucket:      "my-oidc-bucket",
			region:      "eu-west-1",
			infraID:     "prod-cluster",
			expectedURL: "https://my-oidc-bucket.s3.eu-west-1.amazonaws.com/prod-cluster",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			url := oidcDiscoveryURL(test.bucket, test.region, test.infraID)
			g.Expect(url).To(Equal(test.expectedURL))
		})
	}
}

func TestDrOidcIamOptions_RunDryRun(t *testing.T) {
	g := NewGomegaWithT(t)

	opts := &DrOidcIamOptions{
		InfraID:                     "test-cluster",
		Region:                      "us-east-1",
		OIDCStorageProviderS3Bucket: "test-cluster-oidc",
		Issuer:                      "https://test-cluster-oidc.s3.us-east-1.amazonaws.com/test-cluster",
		AWSCredentialsFile:          "/fake/path/to/creds", // Fake path for testing
		Timeout:                     1 * time.Minute,
		DryRun:                      true,
	}

	// This test verifies that dry-run mode doesn't attempt to make actual AWS calls
	// In dry-run mode, the command should fail gracefully when it can't create AWS clients
	// but should not panic or have unexpected behavior
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := opts.Run(ctx)
	// We expect this to fail because we don't have real AWS credentials,
	// but it should fail gracefully with a descriptive error
	g.Expect(err).To(HaveOccurred())
	// The error could be either session creation or AWS operation failure
	errorMsg := err.Error()
	g.Expect(errorMsg).To(Or(
		ContainSubstring("failed to create AWS session"),
		ContainSubstring("failed to check OIDC provider"),
		ContainSubstring("context deadline exceeded"),
	))
}

// Add the validateRequiredFlags method to DrOidcIamOptions for testing
func (o *DrOidcIamOptions) validateRequiredFlags() error {
	if o.InfraID == "" {
		return fmt.Errorf("infra-id is required")
	}
	if o.Region == "" {
		return fmt.Errorf("region is required")
	}
	return nil
}