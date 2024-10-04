package scheduler

import (
	"encoding/json"
	"fmt"
	"path"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	componentbasev1 "k8s.io/component-base/config/v1alpha1"
	schedulerv1 "k8s.io/kube-scheduler/config/v1"
	"k8s.io/utils/ptr"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/support/config"
)

const (
	KubeSchedulerConfigKey = "config.json"
)

func ReconcileConfig(config *corev1.ConfigMap, ownerRef config.OwnerRef, profile configv1.SchedulerProfile) error {
	ownerRef.ApplyTo(config)
	if config.Data == nil {
		config.Data = map[string]string{}
	}
	serializedConfig, err := generateConfig(profile)
	if err != nil {
		return fmt.Errorf("failed to create apiserver config: %w", err)
	}
	config.Data[KubeSchedulerConfigKey] = serializedConfig
	return nil
}

func generateConfig(profile configv1.SchedulerProfile) (string, error) {
	leaseDuration, err := time.ParseDuration(config.RecommendedLeaseDuration)
	if err != nil {
		return "", err
	}
	renewDeadline, err := time.ParseDuration(config.RecommendedRenewDeadline)
	if err != nil {
		return "", err
	}
	retryPeriod, err := time.ParseDuration(config.RecommendedRetryPeriod)
	if err != nil {
		return "", err
	}
	kubeConfigPath := path.Join(volumeMounts.Path(schedulerContainerMain().Name, schedulerVolumeKubeconfig().Name), kas.KubeconfigKey)
	config := schedulerv1.KubeSchedulerConfiguration{
		TypeMeta: metav1.TypeMeta{
			Kind:       "KubeSchedulerConfiguration",
			APIVersion: schedulerv1.SchemeGroupVersion.String(),
		},
		ClientConnection: componentbasev1.ClientConnectionConfiguration{
			Kubeconfig: kubeConfigPath,
		},
		LeaderElection: componentbasev1.LeaderElectionConfiguration{
			LeaderElect:   ptr.To(true),
			LeaseDuration: metav1.Duration{Duration: leaseDuration},
			RenewDeadline: metav1.Duration{Duration: renewDeadline},
			RetryPeriod:   metav1.Duration{Duration: retryPeriod},
		},
	}
	// Source for Scheduler profiles:
	// https://github.com/openshift/cluster-kube-scheduler-operator/tree/master/bindata/assets/config
	switch profile {
	case configv1.HighNodeUtilization:
		config.Profiles = []schedulerv1.KubeSchedulerProfile{highNodeUtilizationProfile()}
	case configv1.NoScoring:
		config.Profiles = []schedulerv1.KubeSchedulerProfile{noScoringProfile()}
	}
	b, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	return string(b), nil
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
