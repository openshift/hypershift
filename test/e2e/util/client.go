package util

import (
	"fmt"
	"os"

	"k8s.io/client-go/rest"
	cr "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	hyperapi "github.com/openshift/hypershift/api"
)

// GetConfigOrDie creates a REST config from current context
func GetConfigOrDie() *rest.Config {
	cfg := cr.GetConfigOrDie()
	cfg.QPS = 100
	cfg.Burst = 100
	return cfg
}

// GetClientOrDie creates a controller-runtime client for Kubernetes
func GetClientOrDie() crclient.Client {
	client, err := crclient.New(GetConfigOrDie(), crclient.Options{Scheme: hyperapi.Scheme})
	if err != nil {
		fmt.Fprintf(os.Stderr, "unable to get kubernetes client: %v", err)
		os.Exit(1)
	}
	return client
}
