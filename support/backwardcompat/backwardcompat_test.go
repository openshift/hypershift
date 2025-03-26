package backwardcompat

import (
	"testing"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/util"

	v1 "github.com/openshift/api/config/v1"
)

func TestGetBackwardCompatibleConfigHash(t *testing.T) {
	tests := []struct {
		name                   string
		input                  v1beta1.ClusterConfiguration
		expectedHashedJSONE    string
		requiresBackwardCompat bool
	}{
		{
			name: "test config without an image",
			input: v1beta1.ClusterConfiguration{
				Proxy: &v1.ProxySpec{
					HTTPProxy: "http://proxy.example.com",
				},
			},
			expectedHashedJSONE: `{"proxy":{"httpProxy":"http://proxy.example.com"}}`,
		},
		{
			name: "test config with an image and no imageStreamImportMode",
			input: v1beta1.ClusterConfiguration{
				Proxy: &v1.ProxySpec{
					HTTPProxy: "http://proxy.example.com",
				},
				Image: &v1.ImageSpec{
					RegistrySources: v1.RegistrySources{
						InsecureRegistries: []string{"registry.example.com"},
					},
				},
			},
			expectedHashedJSONE:    `{"image":{"additionalTrustedCA":{"name":""},"registrySources":{"insecureRegistries":["registry.example.com"]}},"proxy":{"httpProxy":"http://proxy.example.com","trustedCA":{"name":""}}}`,
			requiresBackwardCompat: true,
		},
		{
			name: "test config with an image and imageStreamImportMode",
			input: v1beta1.ClusterConfiguration{
				Proxy: &v1.ProxySpec{
					HTTPProxy: "http://proxy.example.com",
				},
				Image: &v1.ImageSpec{
					RegistrySources: v1.RegistrySources{
						InsecureRegistries: []string{"registry.example.com"},
					},
					ImageStreamImportMode: "",
				},
			},
			expectedHashedJSONE:    `{"image":{"additionalTrustedCA":{"name":""},"registrySources":{"insecureRegistries":["registry.example.com"]}},"proxy":{"httpProxy":"http://proxy.example.com","trustedCA":{"name":""}}}`,
			requiresBackwardCompat: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withoutBackwardCompat, err := util.HashStructWithJSONMapper(tt.input, nil)
			if err != nil {
				t.Errorf("error hashing without backward compatibility: %v", err)
				return
			}
			withBackwardCompat, err := GetBackwardCompatibleConfigHash(&tt.input)
			if err != nil {
				t.Errorf("GetBackwardCompatibleConfigHash() error = %v", err)
				return
			}
			if !tt.requiresBackwardCompat && withoutBackwardCompat != withBackwardCompat {
				t.Errorf("GetBackwardCompatibleConfigHash() = %v, want %v", withBackwardCompat, withoutBackwardCompat)
			} else if tt.requiresBackwardCompat {
				if withoutBackwardCompat == withBackwardCompat {
					t.Errorf("GetBackwardCompatibleConfigHash() = %v, want %v", withBackwardCompat, withoutBackwardCompat)
				} else {
					want, err := util.HashBytes([]byte(tt.expectedHashedJSONE))
					if err != nil {
						t.Errorf("error hashing wanted config: %v", err)
						return
					}
					if withBackwardCompat != want {
						t.Errorf("GetBackwardCompatibleConfigHash() = %v, want %v", withBackwardCompat, want)
					}
				}
			}

		})
	}
}
