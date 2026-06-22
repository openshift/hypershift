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
	g.Expect(cmd.SilenceUsage).To(BeTrue())
}

func TestDrOidcIamOptions_Defaults(t *testing.T) {
	g := NewGomegaWithT(t)

	opts := &DrOidcIamOptions{
		InfraID: "test-cluster",
		Region:  "us-east-1",
	}

	if opts.OIDCStorageProviderS3Bucket == "" {
		opts.OIDCStorageProviderS3Bucket = fmt.Sprintf("%s-oidc", opts.InfraID)
	}

	g.Expect(opts.OIDCStorageProviderS3Bucket).To(Equal("test-cluster-oidc"))

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
		},
		"valid sts-creds and role-arn": {
			opts: &DrOidcIamOptions{
				InfraID:            "test-cluster",
				Region:             "us-east-1",
				STSCredentialsFile: "/path/to/sts-creds",
				RoleArn:            "arn:aws:iam::123456789:role/test",
			},
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
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}

func TestDrOidcIamOptions_Validate(t *testing.T) {
	tests := map[string]struct {
		opts        *DrOidcIamOptions
		expectError bool
		errorMsg    string
	}{
		"valid with infra-id and region": {
			opts: &DrOidcIamOptions{
				InfraID:            "test-cluster",
				Region:             "us-east-1",
				AWSCredentialsFile: "/path/to/creds",
			},
		},
		"valid with hc-name and hc-namespace": {
			opts: &DrOidcIamOptions{
				HostedClusterName:      "my-hc",
				HostedClusterNamespace: "clusters",
				AWSCredentialsFile:     "/path/to/creds",
			},
		},
		"missing both infra-id and region when no hc-name": {
			opts: &DrOidcIamOptions{
				AWSCredentialsFile: "/path/to/creds",
			},
			expectError: true,
			errorMsg:    "--infra-id and --region are required when --hc-name is not set",
		},
		"missing region when no hc-name": {
			opts: &DrOidcIamOptions{
				InfraID:            "test-cluster",
				AWSCredentialsFile: "/path/to/creds",
			},
			expectError: true,
			errorMsg:    "--infra-id and --region are required when --hc-name is not set",
		},
		"missing infra-id when no hc-name": {
			opts: &DrOidcIamOptions{
				Region:             "us-east-1",
				AWSCredentialsFile: "/path/to/creds",
			},
			expectError: true,
			errorMsg:    "--infra-id and --region are required when --hc-name is not set",
		},
		"hc-namespace without hc-name": {
			opts: &DrOidcIamOptions{
				HostedClusterNamespace: "clusters",
				AWSCredentialsFile:     "/path/to/creds",
			},
			expectError: true,
			errorMsg:    "--hc-namespace can only be used with --hc-name",
		},
		"hc-name without hc-namespace": {
			opts: &DrOidcIamOptions{
				HostedClusterName:  "my-hc",
				AWSCredentialsFile: "/path/to/creds",
			},
			expectError: true,
			errorMsg:    "--hc-namespace is required when using --hc-name",
		},
		"hc-name with infra-id conflict": {
			opts: &DrOidcIamOptions{
				HostedClusterName:      "my-hc",
				HostedClusterNamespace: "clusters",
				InfraID:                "manual-infra",
				AWSCredentialsFile:     "/path/to/creds",
			},
			expectError: true,
			errorMsg:    "when using --hc-name, --infra-id and --region should not be specified",
		},
		"hc-name with region conflict": {
			opts: &DrOidcIamOptions{
				HostedClusterName:      "my-hc",
				HostedClusterNamespace: "clusters",
				Region:                 "us-west-2",
				AWSCredentialsFile:     "/path/to/creds",
			},
			expectError: true,
			errorMsg:    "when using --hc-name, --infra-id and --region should not be specified",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			err := test.opts.validate()
			if test.expectError {
				g.Expect(err).To(HaveOccurred())
				if test.errorMsg != "" {
					g.Expect(err.Error()).To(ContainSubstring(test.errorMsg))
				}
			} else {
				g.Expect(err).ToNot(HaveOccurred())
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
			expectedThumbprint: digiCertS3RootCAThumbprint,
		},
		"S3 bucket URL with s3.amazonaws.com": {
			issuerURL:          "https://s3.amazonaws.com/my-bucket/cluster",
			expectedThumbprint: digiCertS3RootCAThumbprint,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			opts := &DrOidcIamOptions{}
			thumbprint, err := opts.getSSLThumbprint(t.Context(), test.issuerURL)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(thumbprint).To(Equal(test.expectedThumbprint))
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

func TestExtractBucketFromIssuerURL(t *testing.T) {
	tests := map[string]struct {
		issuerURL      string
		expectedBucket string
	}{
		"standard S3 URL": {
			issuerURL:      "https://my-bucket.s3.us-east-1.amazonaws.com/my-infra",
			expectedBucket: "my-bucket",
		},
		"bucket with dashes": {
			issuerURL:      "https://cluster-abc-oidc.s3.eu-west-1.amazonaws.com/cluster-abc",
			expectedBucket: "cluster-abc-oidc",
		},
		"non-S3 URL returns empty": {
			issuerURL:      "https://example.com/oidc",
			expectedBucket: "",
		},
		"empty string returns empty": {
			issuerURL:      "",
			expectedBucket: "",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			bucket := extractBucketFromIssuerURL(test.issuerURL)
			g.Expect(bucket).To(Equal(test.expectedBucket))
		})
	}
}

func TestExtractRegionFromIssuerURL(t *testing.T) {
	tests := map[string]struct {
		issuerURL      string
		expectedRegion string
	}{
		"standard S3 URL": {
			issuerURL:      "https://my-bucket.s3.us-east-1.amazonaws.com/my-infra",
			expectedRegion: "us-east-1",
		},
		"different region": {
			issuerURL:      "https://cluster-oidc.s3.eu-west-1.amazonaws.com/cluster-abc",
			expectedRegion: "eu-west-1",
		},
		"us-east-2 region": {
			issuerURL:      "https://jparrill-us-east-2-oidc.s3.us-east-2.amazonaws.com/jparrill-hosted-pkxb6",
			expectedRegion: "us-east-2",
		},
		"non-S3 URL returns empty": {
			issuerURL:      "https://example.com/oidc",
			expectedRegion: "",
		},
		"empty string returns empty": {
			issuerURL:      "",
			expectedRegion: "",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			region := extractRegionFromIssuerURL(test.issuerURL)
			g.Expect(region).To(Equal(test.expectedRegion))
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
		AWSCredentialsFile:          "/fake/path/to/creds",
		Timeout:                     1 * time.Minute,
		DryRun:                      true,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := opts.Run(ctx)
	// We expect this to fail because we don't have real AWS credentials,
	// but it should fail gracefully with a descriptive error.
	g.Expect(err).To(HaveOccurred())
	errorMsg := err.Error()
	g.Expect(errorMsg).To(Or(
		ContainSubstring("failed to create AWS config"),
		ContainSubstring("failed to read AWS credentials file"),
		ContainSubstring("failed to check OIDC provider"),
		ContainSubstring("context deadline exceeded"),
	))
}
