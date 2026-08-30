package util

import (
	"fmt"
	"time"

	"k8s.io/client-go/rest"

	cr "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// GetConfig creates a REST config from current context
func GetConfig() (*rest.Config, error) {
	cfg, err := cr.GetConfig()
	if err != nil {
		return nil, err
	}
	// Disable client-side rate limiting (QPS=-1) for e2e tests. The API server's
	// Priority and Fairness provides server-side flow control. Client-side rate
	// limiting produced misleading "client rate limiter Wait returned an error"
	// messages when test contexts expired.
	cfg.QPS = -1
	cfg.Burst = -1
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

func GetFakeClient(objects ...crclient.Object) crclient.Client {
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		Build()
}
