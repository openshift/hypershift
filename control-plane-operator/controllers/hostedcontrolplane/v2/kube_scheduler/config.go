package scheduler

import (
	"encoding/json"
	"fmt"

	component "github.com/openshift/hypershift/support/controlplane-component"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	schedulerv1 "k8s.io/kube-scheduler/config/v1"
	"k8s.io/utils/ptr"
)

const (
	kubeSchedulerConfigKey = "config.json"
)

func adaptConfigMap(cpContext component.ControlPlaneContext, cm *corev1.ConfigMap) error {
	configuration := cpContext.HCP.Spec.Configuration
	if configuration == nil || configuration.Scheduler == nil || configuration.Scheduler.Profile == "" {
		return nil
	}

	schedulerConfig := &schedulerv1.KubeSchedulerConfiguration{}
	err := json.Unmarshal([]byte(cm.Data[kubeSchedulerConfigKey]), schedulerConfig)
	if err != nil {
		return fmt.Errorf("unable to decode existing KubeScheduler configuration: %w", err)
	}

	// Source for Scheduler profiles:
	// https://github.com/openshift/cluster-kube-scheduler-operator/tree/master/bindata/assets/config
	switch configuration.Scheduler.Profile {
	case configv1.HighNodeUtilization:
		schedulerConfig.Profiles = []schedulerv1.KubeSchedulerProfile{highNodeUtilizationProfile()}
	case configv1.NoScoring:
		schedulerConfig.Profiles = []schedulerv1.KubeSchedulerProfile{noScoringProfile()}
	}

	serializedConfig, err := json.MarshalIndent(schedulerConfig, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize KubeScheduler configuration: %w", err)
	}
	cm.Data[kubeSchedulerConfigKey] = string(serializedConfig)
	return nil
}

func highNodeUtilizationProfile() schedulerv1.KubeSchedulerProfile {
	return schedulerv1.KubeSchedulerProfile{
		SchedulerName: ptr.To("default-scheduler"),
		PluginConfig: []schedulerv1.PluginConfig{
			{
				Name: "NodeResourcesFit",
				Args: runtime.RawExtension{
					Raw: []byte(`{"scoringStrategy":{"type": "MostAllocated"}}`),
				},
			},
		},
		Plugins: &schedulerv1.Plugins{
			Score: schedulerv1.PluginSet{
				Disabled: []schedulerv1.Plugin{
					{
						Name: "NodeResourcesBalancedAllocation",
					},
				},
				Enabled: []schedulerv1.Plugin{
					{
						Name:   "NodeResourcesFit",
						Weight: ptr.To[int32](5),
					},
				},
			},
		},
	}
}

func noScoringProfile() schedulerv1.KubeSchedulerProfile {
	return schedulerv1.KubeSchedulerProfile{
		SchedulerName: ptr.To("default-scheduler"),
		Plugins: &schedulerv1.Plugins{
			Score: schedulerv1.PluginSet{
				Disabled: []schedulerv1.Plugin{
					{
						Name: "*",
					},
				},
			},
			PreScore: schedulerv1.PluginSet{
				Disabled: []schedulerv1.Plugin{
					{
						Name: "*",
					},
				},
			},
		},
	}
}
