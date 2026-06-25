package nodepool

import (
	"bufio"
	"context"
	coreerrors "errors"
	"fmt"
	"io"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/releaseinfo"

	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	runtimeserializer "k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/util/yaml"

	"github.com/blang/semver"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// usesRuncRuntime scans the NodePool's user-supplied ConfigMaps for any
// ContainerRuntimeConfig that sets defaultRuntime to "runc".
// Returns true if runc is explicitly requested by any config entry.
func usesRuncRuntime(ctx context.Context, c client.Client, nodePool *hyperv1.NodePool) (bool, error) {
	if len(nodePool.Spec.Config) == 0 {
		return false, nil
	}

	scheme := runtime.NewScheme()
	_ = mcfgv1.Install(scheme)
	decoder := runtimeserializer.NewCodecFactory(scheme).UniversalDeserializer()

	for _, ref := range nodePool.Spec.Config {
		cm := &corev1.ConfigMap{}
		if err := c.Get(ctx, client.ObjectKey{
			Namespace: nodePool.Namespace,
			Name:      ref.Name,
		}, cm); err != nil {
			// If the ConfigMap doesn't exist, skip — validation catches this elsewhere.
			continue
		}
		payload := cm.Data[TokenSecretConfigKey]
		if payload == "" {
			continue
		}
		yamlReader := yaml.NewYAMLReader(bufio.NewReader(strings.NewReader(payload)))
		for {
			raw, err := yamlReader.Read()
			if err != nil && !coreerrors.Is(err, io.EOF) {
				break
			}
			if len(strings.TrimSpace(string(raw))) > 0 {
				obj, _, decodeErr := decoder.Decode(raw, nil, nil)
				if decodeErr == nil {
					if ctrcfg, ok := obj.(*mcfgv1.ContainerRuntimeConfig); ok {
						if ctrcfg.Spec.ContainerRuntimeConfig != nil &&
							ctrcfg.Spec.ContainerRuntimeConfig.DefaultRuntime == mcfgv1.ContainerRuntimeDefaultRuntimeRunc {
							return true, nil
						}
					}
				}
			}
			if coreerrors.Is(err, io.EOF) {
				break
			}
		}
	}
	return false, nil
}

// getRHELStream returns the effective RHEL stream for the NodePool.
// It delegates to GetRHELStream, which validates stream/version
// combinations and handles runc constraints.
// When spec.osImageStream.Name is unset, GetRHELStream returns the
// version-derived default (StreamRHEL9 for < 5.0, StreamRHEL10 for >= 5.0).
func getRHELStream(ctx context.Context, c client.Client, nodePool *hyperv1.NodePool, releaseImage *releaseinfo.ReleaseImage) (string, error) {
	version, err := semver.Parse(releaseImage.Version())
	if err != nil {
		return "", fmt.Errorf("failed to parse release image version %q: %w", releaseImage.Version(), err)
	}

	usesRunc, err := usesRuncRuntime(ctx, c, nodePool)
	if err != nil {
		return "", fmt.Errorf("failed to detect container runtime: %w", err)
	}

	return GetRHELStream(nodePool.Spec.OSImageStream.Name, version, usesRunc)
}

// validateOSImageStream checks that spec.osImageStream.Name, if set, is a
// valid stream for the given release version and container runtime
// configuration. Returns an error describing the problem or nil.
// It delegates to GetRHELStream for version-aware validation.
func validateOSImageStream(ctx context.Context, c client.Client, nodePool *hyperv1.NodePool, releaseImage *releaseinfo.ReleaseImage) error {
	name := nodePool.Spec.OSImageStream.Name
	if name == "" {
		return nil
	}
	version, err := semver.Parse(releaseImage.Version())
	if err != nil {
		return fmt.Errorf("failed to parse release image version %q: %w", releaseImage.Version(), err)
	}

	usesRunc, err := usesRuncRuntime(ctx, c, nodePool)
	if err != nil {
		return fmt.Errorf("failed to detect container runtime: %w", err)
	}

	_, err = GetRHELStream(name, version, usesRunc)
	return err
}
