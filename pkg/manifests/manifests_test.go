package manifests

import "testing"

func TestHostedControlPlaneNamespace(t *testing.T) {
	tests := []struct {
		name              string
		clusterNamespace  string
		clusterName       string
		expectedNamespace string
	}{
		{
			name:              "When namespace and name are simple strings, it should concatenate with dash",
			clusterNamespace:  "clusters",
			clusterName:       "my-cluster",
			expectedNamespace: "clusters-my-cluster",
		},
		{
			name:              "When cluster name contains dots, it should replace them with dashes",
			clusterNamespace:  "clusters",
			clusterName:       "my.cluster.name",
			expectedNamespace: "clusters-my-cluster-name",
		},
		{
			name:              "When cluster name has no dots, it should return unchanged",
			clusterNamespace:  "ns",
			clusterName:       "simple",
			expectedNamespace: "ns-simple",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HostedControlPlaneNamespace(tt.clusterNamespace, tt.clusterName)
			if got != tt.expectedNamespace {
				t.Errorf("HostedControlPlaneNamespace(%q, %q) = %q, want %q",
					tt.clusterNamespace, tt.clusterName, got, tt.expectedNamespace)
			}
		})
	}
}
