//go:build e2ev2

package lifecycle

import (
	"slices"
	"testing"
)

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
			name:    "When OAuth LoadBalancer private Azure variant is configured, it should request one initial replica",
			variant: "oauth-lb-private",
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

func TestAzureOAuthLBPrivateExtraArgs(t *testing.T) {
	t.Parallel()

	specs := (&AzurePlatformConfig{}).ClusterSpecs("release-image", "n1-image")
	var spec *ClusterSpec
	for i := range specs {
		if specs[i].Variant == "oauth-lb-private" {
			spec = &specs[i]
			break
		}
	}
	if spec == nil {
		t.Fatal("oauth-lb-private variant was not configured")
	}

	// The oauth-lb-private variant combines private endpoint access with the
	// LoadBalancer OAuth publishing strategy; each flag encodes a distinct
	// contract that a regression could silently drop.
	wantArgs := []string{
		"--endpoint-access=Private",
		"--endpoint-access-private-nat-subnet-id=", // empty subnet id with zero-value config
		"--oauth-publishing-strategy=LoadBalancer",
	}
	for _, arg := range wantArgs {
		if !slices.Contains(spec.ExtraArgs, arg) {
			t.Errorf("oauth-lb-private ExtraArgs %v missing %q", spec.ExtraArgs, arg)
		}
	}
}
