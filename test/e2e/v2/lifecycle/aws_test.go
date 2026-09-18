//go:build e2ev2

package lifecycle

import "testing"

func TestAWSPlatformConfigClusterSpecs(t *testing.T) {
	t.Parallel()

	specs := (&AWSPlatformConfig{}).ClusterSpecs("release-image", "n1-image")
	specsByVariant := make(map[string]ClusterSpec, len(specs))
	for _, spec := range specs {
		specsByVariant[spec.Variant] = spec
	}

	for _, variant := range []string{"public", "upgrade", "karpenter", "karpenter-upgrade", "autoscaling", "external-oidc"} {
		if _, ok := specsByVariant[variant]; !ok {
			t.Errorf("AWS variant %q was not configured", variant)
		}
	}
	if got, want := len(specsByVariant), 6; got != want {
		t.Fatalf("got %d AWS variants, want %d", got, want)
	}

	for _, variant := range []string{"autoscaling", "external-oidc"} {
		spec := specsByVariant[variant]
		if spec.InitialNodePoolReplicas == nil || *spec.InitialNodePoolReplicas != 1 {
			t.Errorf("AWS variant %q should request one initial replica", variant)
		}
	}
}
