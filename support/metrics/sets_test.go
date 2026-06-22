package metrics

import (
	_ "embed"
	"testing"
)

//go:embed testdata/sreconfig.yaml
var sampleSREConfigYAML string

func TestSchedulerResourceRelabelConfigs(t *testing.T) {
	tests := []struct {
		name string
		set  MetricsSet
		want int
	}{
		{
			name: "When using Telemetry metrics set it should return a drop-all relabel config",
			set:  MetricsSetTelemetry,
			want: 1,
		},
		{
			name: "When using SRE metrics set it should return SRE config",
			set:  MetricsSetSRE,
			want: 0,
		},
		{
			name: "When using All metrics set it should return nil",
			set:  MetricsSetAll,
			want: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SchedulerResourceRelabelConfigs(tt.set)
			if len(got) != tt.want {
				t.Errorf("SchedulerResourceRelabelConfigs(%s) returned %d configs, want %d", tt.set, len(got), tt.want)
			}
			if tt.set == MetricsSetTelemetry {
				if got[0].Action != "drop" {
					t.Errorf("expected drop action for Telemetry set, got %s", got[0].Action)
				}
			}
		})
	}
}

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
