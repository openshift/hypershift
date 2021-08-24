package config

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
)

var featureGateBytes = `
apiVersion: config.openshift.io/v1
kind: FeatureGate
metadata:
  name: cluster
spec:
  featureSet: LatencySensitive
`

func TestParseGlobalConfig(t *testing.T) {
	config := &hyperv1.ClusterConfiguration{
		Items: []runtime.RawExtension{
			{
				Raw: []byte(featureGateBytes),
			},
		},
	}

	globalConfig, err := ParseGlobalConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if globalConfig.FeatureGate == nil {
		t.Fatalf("feature gate config not found")
	}
	if globalConfig.FeatureGate.Spec.FeatureSet != configv1.LatencySensitive {
		t.Errorf("unexpected featureset: %q", globalConfig.FeatureGate.Spec.FeatureSet)
	}
}
