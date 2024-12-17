package util

import (
	"fmt"
	"time"

	"k8s.io/client-go/rest"

	cr "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// GetConfig creates a REST config from current context
func GetConfig() (*rest.Config, error) {
	cfg, err := cr.GetConfig()
	if err != nil {
		return nil, err
	}
	cfg.QPS = 100
	cfg.Burst = 100
	cfg.Timeout = 5 * time.Minute
	return cfg, nil
}

// GetClient creates a controller-runtime client for Kubernetes
func GetClient() (crclient.Client, error) {
	config, err := GetConfig()
	if err != nil {
		return nil, fmt.Errorf("unable to get kubernetes config: %w", err)
	}
	client, err := crclient.New(config, crclient.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("unable to get kubernetes client: %w", err)
	}
	return client, nil
}
