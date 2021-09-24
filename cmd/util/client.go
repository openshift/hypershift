package util

import (
	"fmt"
	"os"
	"strings"

	"k8s.io/client-go/rest"
	cr "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	hyperapi "github.com/openshift/hypershift/api"
)

const (
	AutoInfraLabelName = "hypershift.openshift.io/auto-created-for-infra"
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
