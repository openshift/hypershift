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
	Namespace    string             `yaml:"namespace"`
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

func cloudConfig(namespace string) CloudConfig {
	return CloudConfig{
		LoadBalancer: LoadBalancerConfig{
			Enabled: true,
		},
		InstancesV2: InstancesV2Config{
			Enabled:              true,
			ZoneAndRegionEnabled: false,
		},
		Namespace: namespace,
	}
}
