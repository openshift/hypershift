package config

import (
	"testing"

	runtime "k8s.io/apimachinery/pkg/runtime"

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

func TestExtractConfig(t *testing.T) {
	hcp := &hyperv1.HostedControlPlane{}
	hcp.Name = "test"
	hcp.Namespace = "foo"
	hcp.Spec.Configs = []hyperv1.ClusterConfiguration{
		{
			Kind: "FeatureGate",
			Content: runtime.RawExtension{
				Raw: []byte(featureGateBytes),
			},
		},
	}

	featureGate := &configv1.FeatureGate{}

	err := ExtractConfig(hcp, featureGate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if featureGate.Spec.FeatureSet != configv1.LatencySensitive {
		t.Errorf("unexpected featureset: %q", featureGate.Spec.FeatureSet)
	}
}
