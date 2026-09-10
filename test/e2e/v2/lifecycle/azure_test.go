//go:build e2ev2

package lifecycle

import (
	"strings"
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

func TestAzurePlatformConfigTestMatrix(t *testing.T) {
	t.Parallel()

	matrix := (&AzurePlatformConfig{}).TestMatrix()
	sequential := make(map[string]SequentialGroup, len(matrix.Sequential))
	for _, group := range matrix.Sequential {
		sequential[group.Name] = group
	}

	tests := []struct {
		name       string
		group      string
		stepNames  []string
		variants   []string
		labelMatch []string
	}{
		{
			name:       "When OAuth LoadBalancer tests run, autoscaling balancing follows configuration tests",
			group:      "oauth-lb",
			stepNames:  []string{"oauth-lb", "oauth-lb-nodepool-config", "oauth-lb-autoscaling"},
			variants:   []string{"oauth-lb", "oauth-lb", "oauth-lb"},
			labelMatch: []string{"", "", "nodepool-autoscaling-balancing"},
		},
		{
			name:       "When external OIDC tests run, autoscaling scale-up/down follows configuration tests",
			group:      "external-oidc",
			stepNames:  []string{"external-oidc", "external-oidc-autoscaling", "external-oidc-trust-bundle"},
			variants:   []string{"external-oidc", "external-oidc", "external-oidc"},
			labelMatch: []string{"", "nodepool-autoscaling-scale-up-down", ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			group, ok := sequential[tt.group]
			if !ok {
				t.Fatalf("sequential group %q was not configured", tt.group)
			}
			if len(group.Steps) != len(tt.stepNames) {
				t.Fatalf("group %q has %d steps, want %d", tt.group, len(group.Steps), len(tt.stepNames))
			}
			for i, step := range group.Steps {
				if step.Name != tt.stepNames[i] {
					t.Errorf("step %d in %q is %q, want %q", i, tt.group, step.Name, tt.stepNames[i])
				}
				if step.Variant != tt.variants[i] {
					t.Errorf("step %d in %q uses variant %q, want %q", i, tt.group, step.Variant, tt.variants[i])
				}
				if tt.labelMatch[i] != "" && !strings.Contains(step.LabelFilter, tt.labelMatch[i]) {
					t.Errorf("step %q in %q has label filter %q, want it to contain %q", step.Name, tt.group, step.LabelFilter, tt.labelMatch[i])
				}
			}
		})
	}

	oauthConfig := sequential["oauth-lb"].Steps[1].LabelFilter
	if !strings.Contains(oauthConfig, "nodepool-machineconfig-rollout") {
		t.Errorf("OAuth LoadBalancer configuration step must retain nodepool machineconfig coverage, got %q", oauthConfig)
	}

	for _, group := range matrix.Sequential {
		for _, step := range group.Steps {
			if step.Variant == "autoscaling" {
				t.Errorf("dedicated autoscaling variant must not be referenced by step %q", step.Name)
			}
		}
	}
}
