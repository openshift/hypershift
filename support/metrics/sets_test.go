package metrics

import (
	_ "embed"
	"reflect"
	"sync"
	"testing"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
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
			name: "When using Telemetry metrics set, it should return a drop-all relabel config",
			set:  MetricsSetTelemetry,
			want: 1,
		},
		{
			name: "When using SRE metrics set, it should return SRE config",
			set:  MetricsSetSRE,
			want: 0,
		},
		{
			name: "When using All metrics set, it should return nil",
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

func TestServiceMonitorRelabelConfigs(t *testing.T) {
	sreConfig := MetricsSetConfig{}
	if err := sreConfig.LoadFromString(sampleSREConfigYAML); err != nil {
		t.Fatalf("Unexpected: %v", err)
	}
	if len(sreConfig.KubeAPIServer) == 0 {
		t.Fatal("sample SRE configuration has no kubeAPIServer rules")
	}

	knownNames := map[string]func(MetricsSet) []prometheusoperatorv1.RelabelConfig{
		"catalog-operator":                   CatalogOperatorRelabelConfigs,
		"cluster-version-operator":           CVORelabelConfigs,
		"etcd":                               EtcdRelabelConfigs,
		"kube-apiserver":                     KASRelabelConfigs,
		"kube-controller-manager":            KCMRelabelConfigs,
		"kube-scheduler":                     SchedulerRelabelConfigs,
		"node-tuning-operator":               NTORelabelConfigs,
		"olm-operator":                       OLMRelabelConfigs,
		"openshift-apiserver":                OpenShiftAPIServerRelabelConfigs,
		"openshift-controller-manager":       OpenShiftControllerManagerRelabelConfigs,
		"openshift-route-controller-manager": OpenShiftRouteControllerManagerRelabelConfigs,
	}

	t.Run("When the ServiceMonitor is a known component, it should return the rules of its relabel config function", func(t *testing.T) {
		for name, relabelConfigs := range knownNames {
			for _, set := range []MetricsSet{MetricsSetTelemetry, MetricsSetAll} {
				got, known := ServiceMonitorRelabelConfigs(name, set)
				if !known {
					t.Errorf("ServiceMonitorRelabelConfigs(%q, %s) reported an unknown component", name, set)
				}
				if !reflect.DeepEqual(got, relabelConfigs(set)) {
					t.Errorf("ServiceMonitorRelabelConfigs(%q, %s) = %v, want %v", name, set, got, relabelConfigs(set))
				}
			}
		}
	})

	t.Run("When the set is SRE, it should return the loaded sre-metric-set rules and nil for components without rules", func(t *testing.T) {
		cm := SREMetricsSetConfigurationConfigMap("clusters-test")
		cm.Data = map[string]string{SREConfigurationConfigMapKey: sampleSREConfigYAML}
		if err := LoadSREMetricsSetConfigurationFromConfigMap(cm); err != nil {
			t.Fatalf("Unexpected: %v", err)
		}
		t.Cleanup(func() {
			cm.Data[SREConfigurationConfigMapKey] = "{}"
			if err := LoadSREMetricsSetConfigurationFromConfigMap(cm); err != nil {
				t.Errorf("failed to reset SRE metrics set configuration: %v", err)
			}
		})

		got, known := ServiceMonitorRelabelConfigs("kube-apiserver", MetricsSetSRE)
		if !known || !reflect.DeepEqual(got, sreConfig.KubeAPIServer) {
			t.Errorf("ServiceMonitorRelabelConfigs(kube-apiserver, SRE) = %v, %v, want %v, true", got, known, sreConfig.KubeAPIServer)
		}
		if len(sreConfig.NodeTuningOperator) == 0 {
			got, known := ServiceMonitorRelabelConfigs("node-tuning-operator", MetricsSetSRE)
			if !known || got != nil {
				t.Errorf("ServiceMonitorRelabelConfigs(node-tuning-operator, SRE) = %v, %v, want nil, true", got, known)
			}
		}
	})

	t.Run("When the returned SRE rules are modified, it should not change the loaded rules", func(t *testing.T) {
		cm := SREMetricsSetConfigurationConfigMap("clusters-test")
		cm.Data = map[string]string{SREConfigurationConfigMapKey: sampleSREConfigYAML}
		if err := LoadSREMetricsSetConfigurationFromConfigMap(cm); err != nil {
			t.Fatalf("Unexpected: %v", err)
		}
		t.Cleanup(func() {
			cm.Data[SREConfigurationConfigMapKey] = "{}"
			if err := LoadSREMetricsSetConfigurationFromConfigMap(cm); err != nil {
				t.Errorf("failed to reset SRE metrics set configuration: %v", err)
			}
		})
		// sreConfig was parsed separately, so it is independent of the loaded rules.
		if len(sreConfig.KubeAPIServer) == 0 || len(sreConfig.KubeAPIServer[0].SourceLabels) == 0 {
			t.Fatal("sample SRE configuration needs a kubeAPIServer rule with source labels")
		}

		got, _ := ServiceMonitorRelabelConfigs("kube-apiserver", MetricsSetSRE)
		got[0].Regex = "modified"
		got[0].SourceLabels[0] = "modified"
		again, _ := ServiceMonitorRelabelConfigs("kube-apiserver", MetricsSetSRE)
		if !reflect.DeepEqual(again, sreConfig.KubeAPIServer) {
			t.Errorf("modifying the returned rules changed the loaded rules: %v", again)
		}
	})

	t.Run("When the SRE configuration is loaded concurrently, it should return complete copies without data races", func(t *testing.T) {
		cm := SREMetricsSetConfigurationConfigMap("clusters-test")
		cm.Data = map[string]string{SREConfigurationConfigMapKey: sampleSREConfigYAML}
		t.Cleanup(func() {
			cm.Data[SREConfigurationConfigMapKey] = "{}"
			if err := LoadSREMetricsSetConfigurationFromConfigMap(cm); err != nil {
				t.Errorf("failed to reset SRE metrics set configuration: %v", err)
			}
		})

		var wg sync.WaitGroup
		errs := make(chan string, 100)
		for range 50 {
			wg.Add(2)
			go func() {
				defer wg.Done()
				if err := LoadSREMetricsSetConfigurationFromConfigMap(cm.DeepCopy()); err != nil {
					errs <- err.Error()
				}
			}()
			go func() {
				defer wg.Done()
				got, known := ServiceMonitorRelabelConfigs("kube-apiserver", MetricsSetSRE)
				if !known || (got != nil && !reflect.DeepEqual(got, sreConfig.KubeAPIServer)) {
					errs <- "incomplete kube-apiserver rules"
				}
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Error(err)
		}
	})

	t.Run("When the ServiceMonitor is not a known component, it should report it as unknown", func(t *testing.T) {
		for _, name := range []string{"", "custom-exporter", "control-plane-operator", "cluster-image-registry-operator"} {
			if got, known := ServiceMonitorRelabelConfigs(name, MetricsSetTelemetry); known || got != nil {
				t.Errorf("ServiceMonitorRelabelConfigs(%q, Telemetry) = %v, %v, want nil, false", name, got, known)
			}
		}
	})
}

func TestResetSREMetricsSetConfiguration(t *testing.T) {
	cm := SREMetricsSetConfigurationConfigMap("clusters-test")
	cm.Data = map[string]string{SREConfigurationConfigMapKey: sampleSREConfigYAML}
	if err := LoadSREMetricsSetConfigurationFromConfigMap(cm); err != nil {
		t.Fatalf("Unexpected: %v", err)
	}
	t.Cleanup(ResetSREMetricsSetConfiguration)
	if rules, _ := ServiceMonitorRelabelConfigs("kube-apiserver", MetricsSetSRE); len(rules) == 0 {
		t.Fatal("expected loaded kubeAPIServer rules before the reset")
	}

	ResetSREMetricsSetConfiguration()

	t.Run("When the configuration is reset, it should leave no SRE rules", func(t *testing.T) {
		if rules, known := ServiceMonitorRelabelConfigs("kube-apiserver", MetricsSetSRE); !known || rules != nil {
			t.Errorf("ServiceMonitorRelabelConfigs(kube-apiserver, SRE) = %v, %v, want nil, true", rules, known)
		}
	})
	t.Run("When the configuration is reset, it should not change the built-in sets", func(t *testing.T) {
		if rules, _ := ServiceMonitorRelabelConfigs("kube-apiserver", MetricsSetTelemetry); !reflect.DeepEqual(rules, KASRelabelConfigs(MetricsSetTelemetry)) {
			t.Errorf("Telemetry rules changed after the reset: %v", rules)
		}
	})
}
