//go:build e2ev2

package lifecycle

import (
	"testing"
)

func TestAWSPlatformConfigClusterSpecs(t *testing.T) {
	t.Run("When HYPERSHIFT_ETCD_KMS_KEY_ARN is not set, kms-reencryption variant is present without --kms-key-arn", func(t *testing.T) {
		t.Setenv("HYPERSHIFT_ETCD_KMS_KEY_ARN", "")
		cfg := NewAWSPlatformConfig(AWSPlatformOptions{}, "")
		specs := cfg.ClusterSpecs("release-image", "n1-image")
		var found bool
		for _, spec := range specs {
			if spec.Variant == "kms-reencryption" {
				found = true
				for _, arg := range spec.ExtraArgs {
					if len(arg) >= 14 && arg[:14] == "--kms-key-arn=" {
						t.Error("kms-reencryption ExtraArgs should not contain --kms-key-arn when HYPERSHIFT_ETCD_KMS_KEY_ARN is unset")
					}
				}
			}
		}
		if !found {
			t.Error("kms-reencryption variant should always be present")
		}
	})

	t.Run("When HYPERSHIFT_ETCD_KMS_KEY_ARN is set, kms-reencryption variant includes --kms-key-arn", func(t *testing.T) {
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
	t.Run("kms-reencryption is always included in the matrix regardless of HYPERSHIFT_ETCD_KMS_KEY_ARN", func(t *testing.T) {
		for _, kmsKeyARN := range []string{"", "arn:aws:kms:us-east-1:123456789012:key/test-key-id"} {
			t.Setenv("HYPERSHIFT_ETCD_KMS_KEY_ARN", kmsKeyARN)
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
				t.Errorf("kms-reencryption should always be in TestMatrix (HYPERSHIFT_ETCD_KMS_KEY_ARN=%q)", kmsKeyARN)
			}
		}
	})

	t.Run("plan is valid against cluster specs regardless of HYPERSHIFT_ETCD_KMS_KEY_ARN", func(t *testing.T) {
		for _, kmsKeyARN := range []string{"", "arn:aws:kms:us-east-1:123456789012:key/test-key-id"} {
			t.Setenv("HYPERSHIFT_ETCD_KMS_KEY_ARN", kmsKeyARN)
			cfg := NewAWSPlatformConfig(AWSPlatformOptions{}, "")
			plan := cfg.DefaultTestPlan()
			specs := cfg.ClusterSpecs("release-image", "n1-image")
			if err := plan.Validate(specs); err != nil {
				t.Errorf("plan should be valid (HYPERSHIFT_ETCD_KMS_KEY_ARN=%q), got: %v", kmsKeyARN, err)
			}
		}
	})
}
