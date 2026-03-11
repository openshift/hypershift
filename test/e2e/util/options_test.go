package util

import (
	"os"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

func TestComplete_BaseDomainFromEnvVar(t *testing.T) {
	tests := []struct {
		name               string
		envValue           string
		flagValue          string
		platform           hyperv1.PlatformType
		expectedBaseDomain string
	}{
		{
			name:               "When BASE_DOMAIN env var is set it should use the env var value",
			envValue:           "custom.example.com",
			flagValue:          "",
			platform:           hyperv1.AWSPlatform,
			expectedBaseDomain: "custom.example.com",
		},
		{
			name:               "When BASE_DOMAIN env var is not set it should fall back to DefaultCIBaseDomain",
			envValue:           "",
			flagValue:          "",
			platform:           hyperv1.AWSPlatform,
			expectedBaseDomain: DefaultCIBaseDomain,
		},
		{
			name:               "When BaseDomain is already set via flag it should not be overridden by env var",
			envValue:           "custom.example.com",
			flagValue:          "flag.example.com",
			platform:           hyperv1.AWSPlatform,
			expectedBaseDomain: "flag.example.com",
		},
		{
			name:               "When platform is KubeVirt it should not set BaseDomain from env var",
			envValue:           "custom.example.com",
			flagValue:          "",
			platform:           hyperv1.KubevirtPlatform,
			expectedBaseDomain: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Set up CI environment
			t.Setenv("OPENSHIFT_CI", "true")

			if tc.envValue != "" {
				t.Setenv("BASE_DOMAIN", tc.envValue)
			} else {
				os.Unsetenv("BASE_DOMAIN")
			}

			o := &Options{
				Platform: tc.platform,
			}
			o.ConfigurableClusterOptions.BaseDomain = tc.flagValue

			// Complete() also requires LatestReleaseImage to be set to avoid
			// calling into the cluster, so we pre-set it.
			o.LatestReleaseImage = "quay.io/openshift-release-dev/ocp-release:4.17.0-x86_64"

			err := o.Complete()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if o.ConfigurableClusterOptions.BaseDomain != tc.expectedBaseDomain {
				t.Errorf("expected BaseDomain %q, got %q", tc.expectedBaseDomain, o.ConfigurableClusterOptions.BaseDomain)
			}
		})
	}
}
