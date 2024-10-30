package kubevirt

import (
	"fmt"

	"gopkg.in/yaml.v2"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
)

const (
	CloudConfigKey = "cloud-config"
)

// Cloud Config is a copy of the relevant subset of the upstream type
// at https://github.com/kubevirt/cloud-provider-kubevirt/blob/main/pkg/provider/cloud.go
type CloudConfig struct {
	Kubeconfig   string             `yaml:"kubeconfig"`
	LoadBalancer LoadBalancerConfig `yaml:"loadBalancer"`
	InstancesV2  InstancesV2Config  `yaml:"instancesV2"`
	Namespace    string             `yaml:"namespace"`
	InfraLabels  map[string]string  `yaml:"infraLabels"`
}

type LoadBalancerConfig struct {
	// Enabled activates the load balancer interface of the CCM
	Enabled bool `yaml:"enabled"`
	// CreationPollInterval determines how many seconds to wait for the load balancer creation
	CreationPollInterval int `yaml:"creationPollInterval"`
	// Selectorless delegate endpointslices creation on third party by
	// skipping service selector creation
	Selectorless *bool `yaml:"selectorless,omitempty"`
}

type InstancesV2Config struct {
	// Enabled activates the instances interface of the CCM
	Enabled bool `yaml:"enabled"`
	// ZoneAndRegionEnabled indicates if need to get Region and zone labels from the cloud provider
	ZoneAndRegionEnabled bool `yaml:"zoneAndRegionEnabled"`
}

func (c *CloudConfig) serialize() (string, error) {
	out, err := yaml.Marshal(c)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func adaptConfig(cpContext component.ControlPlaneContext, cm *corev1.ConfigMap) error {
	data := []byte(cm.Data[CloudConfigKey])
	cloudConfig := &CloudConfig{}
	if err := yaml.Unmarshal(data, cloudConfig); err != nil {
		return fmt.Errorf("failed to unmarshal CloudConfig: %v", err)
	}

	if kubevirt := cpContext.HCP.Spec.Platform.Kubevirt; kubevirt != nil && kubevirt.Credentials != nil {
		cloudConfig.Namespace = kubevirt.Credentials.InfraNamespace
		cloudConfig.Kubeconfig = "/etc/kubernetes/infra-kubeconfig/kubeconfig"
	}
	if cloudConfig.Namespace == "" {
		// mgmt cluster is used as infra cluster
		cloudConfig.Namespace = cpContext.HCP.Namespace
	}
	cloudConfig.InfraLabels[hyperv1.InfraIDLabel] = cpContext.HCP.Spec.InfraID

	serializedCfg, err := cloudConfig.serialize()
	if err != nil {
		return fmt.Errorf("failed to serialize cloudconfig: %w", err)
	}
	cm.Data[CloudConfigKey] = serializedCfg
	return nil
}
