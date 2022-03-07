package util

import (
	"fmt"
	"strings"

	"k8s.io/client-go/rest"
	cr "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	hyperapi "github.com/openshift/hypershift/api"
)

const (
	AutoInfraLabelName = "hypershift.openshift.io/auto-created-for-infra"
)

// GetConfig creates a REST config from current context
func GetConfig() (*rest.Config, error) {
	cfg, err := cr.GetConfig()
	if err != nil {
		return nil, err
	}
	cfg.QPS = 100
	cfg.Burst = 100
	return cfg, nil
}

// GetClient creates a controller-runtime client for Kubernetes
func GetClient() (crclient.Client, error) {
	config, err := GetConfig()
	if err != nil {
		return nil, fmt.Errorf("unable to get kubernetes config: %w", err)
	}
	client, err := crclient.New(config, crclient.Options{Scheme: hyperapi.Scheme})
	if err != nil {
		return nil, fmt.Errorf("unable to get kubernetes client: %w", err)
	}
	return client, nil
}

// ParseAWSTags does exactly that
func ParseAWSTags(tags []string) (map[string]string, error) {
	tagMap := make(map[string]string, len(tags))
	for _, tagStr := range tags {
		parts := strings.SplitN(tagStr, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid tag specification: %q (expecting \"key=value\")", tagStr)
		}
		tagMap[parts[0]] = parts[1]
	}
	return tagMap, nil
}
