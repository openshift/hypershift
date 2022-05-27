package scheduler

import (
	"encoding/json"
	"fmt"
	"path"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	componentbasev1 "k8s.io/component-base/config/v1alpha1"
	schedulerv1beta2 "k8s.io/kube-scheduler/config/v1beta2"
	"k8s.io/utils/pointer"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/support/config"
)

const (
	KubeSchedulerConfigKey = "config.json"
)

func ReconcileConfig(config *corev1.ConfigMap, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(config)
	if config.Data == nil {
		config.Data = map[string]string{}
	}
	serializedConfig, err := generateConfig()
	if err != nil {
		return fmt.Errorf("failed to create apiserver config: %w", err)
	}
	config.Data[KubeSchedulerConfigKey] = serializedConfig
	return nil
}

func generateConfig() (string, error) {
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
	config := schedulerv1beta2.KubeSchedulerConfiguration{
		TypeMeta: metav1.TypeMeta{
			Kind:       "KubeSchedulerConfiguration",
			APIVersion: schedulerv1beta2.SchemeGroupVersion.String(),
		},
		ClientConnection: componentbasev1.ClientConnectionConfiguration{
			Kubeconfig: kubeConfigPath,
		},
		LeaderElection: componentbasev1.LeaderElectionConfiguration{
			LeaderElect:   pointer.BoolPtr(true),
			LeaseDuration: metav1.Duration{Duration: leaseDuration},
			RenewDeadline: metav1.Duration{Duration: renewDeadline},
			RetryPeriod:   metav1.Duration{Duration: retryPeriod},
		},
	}
	b, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
