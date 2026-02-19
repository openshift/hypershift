package mcs

import (
	"testing"

	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/globalconfig"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestReconcileMachineConfigServerConfig(t *testing.T) {
	baseMCSParams := func() *MCSParams {
		return &MCSParams{
			RootCA: &corev1.Secret{
				Data: map[string][]byte{
					certs.CASignerCertMapKey: []byte("root-ca-cert"),
				},
			},
			KubeletClientCA: &corev1.ConfigMap{
				Data: map[string]string{
					certs.CASignerCertMapKey: "kubelet-client-ca-cert",
				},
			},
			DNS:            globalconfig.DNSConfig(),
			Infrastructure: globalconfig.InfrastructureConfig(),
			Network:        globalconfig.NetworkConfig(),
			Proxy:          globalconfig.ProxyConfig(),
			Image:          globalconfig.ImageConfig(),
			InstallConfig:  &globalconfig.InstallConfig{},
		}
	}

	tests := []struct {
		name                    string
		imageRegistryCA         *corev1.ConfigMap
		expectImageRegistryCA   bool
		expectedImageRegistryCA string
	}{
		{
			name: "When image registry CA is provided it should include image-registry-ca.crt in ConfigMap data",
			imageRegistryCA: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "service-serving-ca",
					Namespace: "test-ns",
				},
				Data: map[string]string{
					ImageRegistryCAKey: "test-service-ca-cert-data",
				},
			},
			expectImageRegistryCA:   true,
			expectedImageRegistryCA: "test-service-ca-cert-data",
		},
		{
			name:                  "When image registry CA is nil it should not include image-registry-ca.crt",
			imageRegistryCA:       nil,
			expectImageRegistryCA: false,
		},
		{
			name: "When image registry CA ConfigMap has no matching key it should not include image-registry-ca.crt",
			imageRegistryCA: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "service-serving-ca",
					Namespace: "test-ns",
				},
				Data: map[string]string{},
			},
			expectImageRegistryCA: false,
		},
		{
			name: "When image registry CA ConfigMap has empty cert data it should not include image-registry-ca.crt",
			imageRegistryCA: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "service-serving-ca",
					Namespace: "test-ns",
				},
				Data: map[string]string{
					ImageRegistryCAKey: "",
				},
			},
			expectImageRegistryCA: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := baseMCSParams()
			p.ImageRegistryCA = tt.imageRegistryCA

			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "machine-config-server",
					Namespace: "test-ns",
				},
			}

			if err := ReconcileMachineConfigServerConfig(cm, p); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			caData, hasCA := cm.Data[ImageRegistryCADataKey]
			if tt.expectImageRegistryCA {
				if !hasCA {
					t.Errorf("expected %s key in ConfigMap data, but it was not found", ImageRegistryCADataKey)
				}
				if caData != tt.expectedImageRegistryCA {
					t.Errorf("expected %s = %q, got %q", ImageRegistryCADataKey, tt.expectedImageRegistryCA, caData)
				}
			} else {
				if hasCA {
					t.Errorf("expected %s key to be absent from ConfigMap data, but found value %q", ImageRegistryCADataKey, caData)
				}
			}
		})
	}
}
