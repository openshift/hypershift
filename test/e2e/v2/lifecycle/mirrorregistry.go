//go:build e2ev2

package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/util/mirrorregistry"

	imagev1 "github.com/openshift/api/image/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

type mirrorRegistry interface {
	Setup(context.Context, mirrorregistry.MirrorRequest) (*mirrorregistry.Result, error)
}

// cliImage reads the imported latest tag, not the mutable ImageStream spec tag.
func cliImage(ctx context.Context, cl crclient.Client) (string, error) {
	stream := &imagev1.ImageStream{}
	if err := cl.Get(ctx, crclient.ObjectKey{Namespace: "openshift", Name: "cli"}, stream); err != nil {
		return "", fmt.Errorf("read ImageStream openshift/cli: %w", err)
	}
	for _, tag := range stream.Status.Tags {
		if tag.Tag == "latest" && len(tag.Items) > 0 {
			ref := tag.Items[0].DockerImageReference
			if strings.Contains(ref, "@sha256:") {
				return ref, nil
			}
		}
	}
	return "", fmt.Errorf("ImageStream openshift/cli has no resolved latest tag by digest")
}

func writeMirrorRegistryResult(sharedDir string, result *mirrorregistry.Result) error {
	if len(result.ImageContentSources) == 0 {
		return fmt.Errorf("mirror registry result has no image content sources")
	}
	ics, err := yaml.Marshal(result.ImageContentSources)
	if err != nil {
		return fmt.Errorf("marshal mirror image content sources: %w", err)
	}
	// Keep the Result field names, omitting the CA entirely from the JSON.
	data, err := json.MarshalIndent(struct {
		Host                string
		InternalHost        string
		ImageContentSources []hyperv1.ImageContentSource
		Images              []mirrorregistry.MirroredImage
	}{result.Host, result.InternalHost, result.ImageContentSources, result.Images}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal mirror registry result: %w", err)
	}
	for _, file := range []struct {
		name string
		data []byte
	}{
		{"mirror-registry-ca.pem", result.CABundlePEM},
		{"mirror-registry-ics.yaml", ics},
		{"mirror-registry.json", append(data, '\n')},
	} {
		path := filepath.Join(sharedDir, file.name)
		if err := os.WriteFile(path, file.data, 0600); err != nil {
			return fmt.Errorf("write mirror registry file %s: %w", path, err)
		}
	}
	return nil
}
