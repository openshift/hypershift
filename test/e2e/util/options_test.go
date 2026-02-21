package util

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

func TestComplete_BaseDomainFromEnvVar(t *testing.T) {
	testCases := []struct {
		name               string
		envBaseDomain      string
		flagBaseDomain     string
		isRunningInCI      bool
		platform           hyperv1.PlatformType
		expectedBaseDomain string
	}{
		{
			name:               "When BASE_DOMAIN env var is set in CI it should use the env var value",
			envBaseDomain:      "custom.example.com",
			flagBaseDomain:     "",
			isRunningInCI:      true,
			platform:           hyperv1.AWSPlatform,
			expectedBaseDomain: "custom.example.com",
		},
		{
			name:               "When BASE_DOMAIN env var is not set in CI it should fall back to the default",
			envBaseDomain:      "",
			flagBaseDomain:     "",
			isRunningInCI:      true,
			platform:           hyperv1.AWSPlatform,
			expectedBaseDomain: DefaultCIBaseDomain,
		},
		{
			name:               "When BaseDomain is already set via flag it should not be overridden by env var",
			envBaseDomain:      "custom.example.com",
			flagBaseDomain:     "flag.example.com",
			isRunningInCI:      true,
			platform:           hyperv1.AWSPlatform,
			expectedBaseDomain: "flag.example.com",
		},
		{
			name:               "When platform is KubeVirt it should not set BaseDomain even with env var",
			envBaseDomain:      "custom.example.com",
			flagBaseDomain:     "",
			isRunningInCI:      true,
			platform:           hyperv1.KubevirtPlatform,
			expectedBaseDomain: "",
		},
		{
			name:               "When not running in CI it should not set BaseDomain from env var",
			envBaseDomain:      "custom.example.com",
			flagBaseDomain:     "",
			isRunningInCI:      false,
			platform:           hyperv1.AWSPlatform,
			expectedBaseDomain: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			if tc.envBaseDomain != "" {
				t.Setenv("BASE_DOMAIN", tc.envBaseDomain)
			} else {
				t.Setenv("BASE_DOMAIN", "")
			}
			// Set OPENSHIFT_CI based on whether we want to simulate CI
			if tc.isRunningInCI {
				t.Setenv("OPENSHIFT_CI", "true")
			} else {
				t.Setenv("OPENSHIFT_CI", "false")
			}

			opts := &Options{
				LatestReleaseImage: "quay.io/openshift-release-dev/ocp-release:latest",
				Platform:           tc.platform,
			}
			opts.ConfigurableClusterOptions.BaseDomain = tc.flagBaseDomain

			err := opts.Complete()
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(opts.ConfigurableClusterOptions.BaseDomain).To(Equal(tc.expectedBaseDomain))
		})
	}
}
