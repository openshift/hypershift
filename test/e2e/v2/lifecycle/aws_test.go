//go:build e2ev2

package lifecycle

import (
	"testing"
)

func TestAWSPlatformConfigClusterSpecs(t *testing.T) {
	t.Run("When HYPERSHIFT_ETCD_KMS_KEY_ARN is not set, it should not include kms-reencryption variant", func(t *testing.T) {
		t.Setenv("HYPERSHIFT_ETCD_KMS_KEY_ARN", "")
		cfg := NewAWSPlatformConfig(AWSPlatformOptions{}, "")
		specs := cfg.ClusterSpecs("release-image", "n1-image")
		for _, spec := range specs {
			if spec.Variant == "kms-reencryption" {
				t.Error("kms-reencryption variant should not be present when HYPERSHIFT_ETCD_KMS_KEY_ARN is unset")
			}
		}
	})

	t.Run("When HYPERSHIFT_ETCD_KMS_KEY_ARN is set, it should include kms-reencryption variant with the key ARN", func(t *testing.T) {
		const keyARN = "arn:aws:kms:us-east-1:123456789012:key/test-key-id"
		t.Setenv("HYPERSHIFT_ETCD_KMS_KEY_ARN", keyARN)
		cfg := NewAWSPlatformConfig(AWSPlatformOptions{}, "")
		specs := cfg.ClusterSpecs("release-image", "n1-image")

		var found bool
		for _, spec := range specs {
			if spec.Variant == "kms-reencryption" {
				found = true
				wantArg := "--kms-key-arn=" + keyARN
				var hasArg bool
				for _, arg := range spec.ExtraArgs {
					if arg == wantArg {
						hasArg = true
						break
					}
				}
				if !hasArg {
					t.Errorf("kms-reencryption ExtraArgs %v missing %q", spec.ExtraArgs, wantArg)
				}
			}
		}
		if !found {
			t.Error("kms-reencryption variant should be present when HYPERSHIFT_ETCD_KMS_KEY_ARN is set")
		}
	})
}

func TestAWSPlatformConfigTestMatrix(t *testing.T) {
	t.Run("When HYPERSHIFT_ETCD_KMS_KEY_ARN is not set, it should not include kms-reencryption in the matrix", func(t *testing.T) {
		t.Setenv("HYPERSHIFT_ETCD_KMS_KEY_ARN", "")
		cfg := NewAWSPlatformConfig(AWSPlatformOptions{}, "")
		matrix := cfg.TestMatrix()
		for _, g := range matrix.Parallel {
			if g.Variant == "kms-reencryption" {
				t.Error("kms-reencryption should not be in TestMatrix when HYPERSHIFT_ETCD_KMS_KEY_ARN is unset")
			}
		}
	})

	t.Run("When HYPERSHIFT_ETCD_KMS_KEY_ARN is not set, it should produce a valid plan against default cluster specs", func(t *testing.T) {
		t.Setenv("HYPERSHIFT_ETCD_KMS_KEY_ARN", "")
		cfg := NewAWSPlatformConfig(AWSPlatformOptions{}, "")
		plan := cfg.DefaultTestPlan()
		specs := cfg.ClusterSpecs("release-image", "n1-image")
		if err := plan.Validate(specs); err != nil {
			t.Errorf("default plan should be valid when HYPERSHIFT_ETCD_KMS_KEY_ARN is unset, got: %v", err)
		}
	})

	t.Run("When HYPERSHIFT_ETCD_KMS_KEY_ARN is set, it should include kms-reencryption in the matrix", func(t *testing.T) {
		t.Setenv("HYPERSHIFT_ETCD_KMS_KEY_ARN", "arn:aws:kms:us-east-1:123456789012:key/test-key-id")
		cfg := NewAWSPlatformConfig(AWSPlatformOptions{}, "")
		matrix := cfg.TestMatrix()
		var found bool
		for _, g := range matrix.Parallel {
			if g.Variant == "kms-reencryption" {
				found = true
				break
			}
		}
		if !found {
			t.Error("kms-reencryption should be in TestMatrix when HYPERSHIFT_ETCD_KMS_KEY_ARN is set")
		}
	})

	t.Run("When HYPERSHIFT_ETCD_KMS_KEY_ARN is set, it should produce a valid plan against cluster specs", func(t *testing.T) {
		t.Setenv("HYPERSHIFT_ETCD_KMS_KEY_ARN", "arn:aws:kms:us-east-1:123456789012:key/test-key-id")
		cfg := NewAWSPlatformConfig(AWSPlatformOptions{}, "")
		plan := cfg.DefaultTestPlan()
		specs := cfg.ClusterSpecs("release-image", "n1-image")
		if err := plan.Validate(specs); err != nil {
			t.Errorf("default plan should be valid when HYPERSHIFT_ETCD_KMS_KEY_ARN is set, got: %v", err)
		}
	})
}
