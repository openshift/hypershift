package monitoring

import (
	"fmt"

	cmoconfig "github.com/openshift/hypershift/thirdparty/clustermonitoringoperator/pkg/manifests"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"
)

const configKey = "config.yaml"

var (
	workerNodeSelector = map[string]string{"kubernetes.io/os": "linux"}
)

// ReconcileMonitoringConfig ensures that the prometheus operator nodeselector does not select
// master nodes. Other changes made by the guest cluster user should be preserved.
func ReconcileMonitoringConfig(cm *corev1.ConfigMap) error {
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	var config *cmoconfig.Config
	configYAML, hasConfig := cm.Data[configKey]
	if hasConfig {
		var err error
		if config, err = cmoconfig.NewConfigFromString(configYAML); err != nil {
			return fmt.Errorf("failed to parse monitoring config: %w", err)
		}
	} else {
		config = &cmoconfig.Config{}
	}
	config.ClusterMonitoringConfiguration = &cmoconfig.ClusterMonitoringConfiguration{
		PrometheusOperatorConfig: &cmoconfig.PrometheusOperatorConfig{
			NodeSelector: workerNodeSelector,
		},
	}
	configBytes, err := yaml.Marshal(config.ClusterMonitoringConfiguration)
	if err != nil {
		return fmt.Errorf("failed to serialize monitoring config: %w", err)
	}
	cm.Data[configKey] = string(configBytes)
	return nil
}
