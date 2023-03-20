package nodepool

import (
	"testing"

	"github.com/openshift/hypershift/support/testutil"
	"sigs.k8s.io/yaml"
)

func TestAPIServerHAProxyConfig(t *testing.T) {
	image := "ha-proxy-image:latest"
	externalAddress := "cluster.example.com"
	internalAddress := "cluster.internal.example.com"
	config, err := apiServerProxyConfig(image, "", externalAddress, internalAddress, 443, 8443, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	yamlConfig, err := yaml.JSONToYAML(config)
	if err != nil {
		t.Fatalf("cannot convert to yaml: %v", err)
	}
	testutil.CompareWithFixture(t, yamlConfig)
}
