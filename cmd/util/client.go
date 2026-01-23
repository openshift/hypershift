package util

import (
	"fmt"
	"os"
	"strings"

	hyperapi "github.com/openshift/hypershift/support/api"

	"k8s.io/client-go/rest"

	cr "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	AutoInfraLabelName = "hypershift.openshift.io/auto-created-for-infra"
	// DeleteWithClusterLabelName marks CLI created secrets, to be safely removed on hosted cluster deletion
	DeleteWithClusterLabelName = "hypershift.openshift.io/safe-to-delete-with-cluster"
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
	if os.Getenv("FAKE_CLIENT") == "true" {
		return fake.NewFakeClient(), nil
	}

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

// GetImpersonatedClient creates a controller-runtime client for Kubernetes
func GetImpersonatedClient(userName string) (crclient.Client, error) {
	config, err := GetConfig()
	if err != nil {
		return nil, fmt.Errorf("unable to get kubernetes config: %w", err)
	}
	config.Impersonate = rest.ImpersonationConfig{
		UserName: userName,
	}

	client, err := crclient.New(config, crclient.Options{Scheme: hyperapi.Scheme})
	if err != nil {
		return nil, fmt.Errorf("unable to get kubernetes client: %w", err)
	}
	return client, nil
}

// ParseAWSTags parses a slice of "key=value" strings into a map.
// Returns an error if any tag is malformed or if duplicate keys are found.
func ParseAWSTags(tags []string) (map[string]string, error) {
	tagMap := make(map[string]string, len(tags))
	for _, tagStr := range tags {
		parts := strings.SplitN(tagStr, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid tag specification: %q (expecting \"key=value\")", tagStr)
		}
		key := parts[0]
		if _, exists := tagMap[key]; exists {
			return nil, fmt.Errorf("duplicate tag key: %q", key)
		}
		tagMap[key] = parts[1]
	}
	return tagMap, nil
}

// AddUniqueTag appends a tag to the slice only if a tag with the same key doesn't already exist.
// The tag should be in "key=value" format.
func AddUniqueTag(tags []string, newTag string) []string {
	key := strings.SplitN(newTag, "=", 2)[0]
	for _, tag := range tags {
		if strings.HasPrefix(tag, key+"=") {
			return tags
		}
	}
	return append(tags, newTag)
}
