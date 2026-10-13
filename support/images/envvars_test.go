package images

import (
	"testing"
)

func TestGetSharedIngressHAProxyImage(t *testing.T) {
	testCases := []struct {
		name          string
		envVarValue   string
		expectedImage string
	}{
		{
			name:          "returns default when env var is not set",
			envVarValue:   "",
			expectedImage: DefaultSharedIngressHAProxyImage,
		},
		{
			name:          "returns custom image when env var is set",
			envVarValue:   "quay.io/custom/haproxy:latest",
			expectedImage: "quay.io/custom/haproxy:latest",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.envVarValue != "" {
				t.Setenv(SharedIngressHAProxyEnvVar, tc.envVarValue)
			}

			actualImage := GetSharedIngressHAProxyImage()

			if actualImage != tc.expectedImage {
				t.Errorf("expected image %q, got %q", tc.expectedImage, actualImage)
			}
		})
	}
}
