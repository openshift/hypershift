package kas

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAdaptDNSProxyConfigMap(t *testing.T) {
	tests := []struct {
		name                   string
		annotations            map[string]string
		expectedMgmtClusterDNS string
	}{
		{
			name:                   "When no custom DNS annotation is provided, it should use default management cluster DNS",
			annotations:            map[string]string{},
			expectedMgmtClusterDNS: "10.130.0.10",
		},
		{
			name: "When custom DNS annotation is provided, it should use custom management cluster DNS",
			annotations: map[string]string{
				"hypershift.openshift.io/management-cluster-dns": "10.96.0.10",
			},
			expectedMgmtClusterDNS: "10.96.0.10",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-cluster",
					Namespace:   "test-namespace",
					Annotations: tt.annotations,
				},
			}

			cpContext := component.WorkloadContext{
				HCP: hcp,
			}

			cm := &corev1.ConfigMap{}
			err := adaptDNSProxyConfigMap(cpContext, cm)
			if err != nil {
				t.Fatalf("adaptDNSProxyConfigMap() error = %v", err)
			}

			corefile, ok := cm.Data["Corefile"]
			if !ok {
				t.Fatal("Corefile key not found in ConfigMap data")
			}

			// Verify the management cluster DNS is in the Corefile
			expectedPattern := "forward . " + tt.expectedMgmtClusterDNS
			if !contains(corefile, expectedPattern) {
				t.Errorf("Expected Corefile to contain %q, but it doesn't.\nCorefile:\n%s", expectedPattern, corefile)
			}

			// Verify Azure DNS is in the Corefile
			if !contains(corefile, "168.63.129.16") {
				t.Error("Expected Corefile to contain Azure DNS 168.63.129.16")
			}

			// Verify vault domains are configured with separate forward directives
			if !contains(corefile, "forward vault.azure.net 168.63.129.16") {
				t.Error("Expected Corefile to contain 'forward vault.azure.net 168.63.129.16'")
			}
			if !contains(corefile, "forward vaultcore.azure.net 168.63.129.16") {
				t.Error("Expected Corefile to contain 'forward vaultcore.azure.net 168.63.129.16'")
			}
		})
	}
}

func TestEnableDNSProxySidecar(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		expected    bool
	}{
		{
			name:        "When Swift annotation is not present, it should return false",
			annotations: map[string]string{},
			expected:    false,
		},
		{
			name: "When Swift annotation is present, it should return true",
			annotations: map[string]string{
				hyperv1.SwiftPodNetworkInstanceAnnotationCpo: "swift-instance-1",
			},
			expected: true,
		},
		{
			name: "When Swift annotation is present but empty, it should return false",
			annotations: map[string]string{
				hyperv1.SwiftPodNetworkInstanceAnnotationCpo: "",
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-cluster",
					Namespace:   "test-namespace",
					Annotations: tt.annotations,
				},
			}

			cpContext := component.WorkloadContext{
				HCP: hcp,
			}

			result := enableDNSProxySidecar(cpContext)
			if result != tt.expected {
				t.Errorf("enableDNSProxySidecar() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && containsAtIndex(s, substr))
}

func containsAtIndex(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
