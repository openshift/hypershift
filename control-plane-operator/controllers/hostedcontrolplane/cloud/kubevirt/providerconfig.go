package kubevirt

import (
	"gopkg.in/yaml.v2"
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
}

type LoadBalancerConfig struct {
	// Enabled activates the load balancer interface of the CCM
	Enabled bool `yaml:"enabled"`
	// CreationPollInterval determines how many seconds to wait for the load balancer creation
	CreationPollInterval int `yaml:"creationPollInterval"`
}

type InstancesV2Config struct {
	// Enabled activates the instances interface of the CCM
	Enabled bool `yaml:"enabled"`
}

func (c *CloudConfig) String() (string, error) {
	out, err := yaml.Marshal(c)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func cloudConfig(kubeconfig []byte) CloudConfig {
	return CloudConfig{
		Kubeconfig: string(kubeconfig),
		LoadBalancer: LoadBalancerConfig{
			Enabled: true,
		},
		InstancesV2: InstancesV2Config{
			Enabled: true,
		},
	}
}
