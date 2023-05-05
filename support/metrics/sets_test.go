package metrics

import (
	_ "embed"
	"testing"
)

//go:embed testdata/sreconfig.yaml
var sampleSREConfigYAML string

func TestLoadMetricsConfig(t *testing.T) {
	config := MetricsSetConfig{}
	err := config.LoadFromString(sampleSREConfigYAML)
	if err != nil {
		t.Fatalf("Unexpected: %v", err)
	}
	if len(config.KubeAPIServer) == 0 {
		t.Errorf("Kube APIServer configuration not loaded")
	}
}
