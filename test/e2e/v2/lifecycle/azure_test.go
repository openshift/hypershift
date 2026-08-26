//go:build e2ev2

package lifecycle

import "testing"

func TestAzurePlatformConfigClusterSpecs(t *testing.T) {
	t.Parallel()

	specs := (&AzurePlatformConfig{}).ClusterSpecs("release-image", "n1-image")
	specsByVariant := make(map[string]ClusterSpec, len(specs))
	for _, spec := range specs {
		specsByVariant[spec.Variant] = spec
	}

	tests := []struct {
		name    string
		variant string
		want    int
	}{
		{
			name:    "When public Azure variant is configured, it should request two initial replicas",
			variant: "public",
			want:    2,
		},
		{
			name:    "When upgrade Azure variant is configured, it should request two initial replicas",
			variant: "upgrade",
			want:    2,
		},
		{
			name:    "When private Azure variant is configured, it should request one initial replica",
			variant: "private",
			want:    1,
		},
		{
			name:    "When OAuth LoadBalancer Azure variant is configured, it should request one initial replica",
			variant: "oauth-lb",
			want:    1,
		},
		{
			name:    "When autoscaling Azure variant is configured, it should request one initial replica",
			variant: "autoscaling",
			want:    1,
		},
		{
			name:    "When external OIDC Azure variant is configured, it should request one initial replica",
			variant: "external-oidc",
			want:    1,
		},
	}

	if len(specsByVariant) != len(tests) {
		t.Fatalf("got %d Azure variants, want %d", len(specsByVariant), len(tests))
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			spec, ok := specsByVariant[tt.variant]
			if !ok {
				t.Fatalf("Azure variant %q was not configured", tt.variant)
			}
			if spec.InitialNodePoolReplicas == nil {
				t.Fatalf("Azure variant %q has no initial replica override", tt.variant)
			}
			if got := *spec.InitialNodePoolReplicas; got != tt.want {
				t.Errorf("Azure variant %q requests %d initial replicas, want %d", tt.variant, got, tt.want)
			}
		})
	}
}
