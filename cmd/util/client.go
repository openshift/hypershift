package util

import (
	"context"
	"fmt"
	"os"
	"strings"

	"k8s.io/client-go/rest"
	cr "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	hyperapi "github.com/openshift/hypershift/api"
	"github.com/openshift/hypershift/cmd/log"
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

func ApplyObjects(ctx context.Context, crcObjects []crclient.Object, render bool, infraID string) error {
	switch {
	case render:
		for _, object := range crcObjects {
			key := crclient.ObjectKeyFromObject(object)
			err := hyperapi.YamlSerializer.Encode(object, os.Stdout)
			if err != nil {
				return fmt.Errorf("failed to encode objects: %w", err)
			}
			fmt.Println("---")
			log.Info("Rendered Kube resource", "kind", object.GetObjectKind().GroupVersionKind().Kind, "namespace", key.Namespace, "name", key.Name)
		}
	default:
		client := GetClientOrDie()
		for _, object := range crcObjects {
			key := crclient.ObjectKeyFromObject(object)
			object.SetLabels(map[string]string{AutoInfraLabelName: infraID})
			var err error
			if object.GetObjectKind().GroupVersionKind().Kind == "HostedCluster" {
				err = client.Create(ctx, object)
			} else {
				err = client.Patch(ctx, object, crclient.Apply, crclient.ForceOwnership, crclient.FieldOwner("hypershift-cli"))
			}
			if err != nil {
				return fmt.Errorf("failed to apply object %q: %w", key, err)
			}
			log.Info("Applied Kube resource", "kind", object.GetObjectKind().GroupVersionKind().Kind, "namespace", key.Namespace, "name", key.Name)
		}
	}
	return nil
}
