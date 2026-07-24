package nodepool

import (
	"bufio"
	"context"
	coreerrors "errors"
	"fmt"
	"io"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/releaseinfo"

	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	runtimeserializer "k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/util/yaml"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/blang/semver"
)

// ctrcfgDecoder is reused across calls to avoid allocating a new Scheme + CodecFactory
// on every invocation. api.Scheme already has MCO types registered.
var ctrcfgDecoder = runtimeserializer.NewCodecFactory(api.Scheme).UniversalDeserializer()

// usesRuncRuntime scans the NodePool's user-supplied ConfigMaps for any
// ContainerRuntimeConfig that sets defaultRuntime to "runc".
// Returns true if runc is explicitly requested by any config entry.
func usesRuncRuntime(ctx context.Context, c client.Client, nodePool *hyperv1.NodePool) (bool, error) {
	if len(nodePool.Spec.Config) == 0 {
		return false, nil
	}

	for _, ref := range nodePool.Spec.Config {
		cm := &corev1.ConfigMap{}
		if err := c.Get(ctx, client.ObjectKey{
			Namespace: nodePool.Namespace,
			Name:      ref.Name,
		}, cm); err != nil {
			if apierrors.IsNotFound(err) {
				// ConfigMap doesn't exist yet — validation catches this elsewhere.
				continue
			}
			return false, fmt.Errorf("failed to get ConfigMap %s/%s: %w", nodePool.Namespace, ref.Name, err)
		}
		payload := cm.Data[TokenSecretConfigKey]
		if payload == "" {
			continue
		}
		yamlReader := yaml.NewYAMLReader(bufio.NewReader(strings.NewReader(payload)))
		for {
			raw, err := yamlReader.Read()
			if err != nil && !coreerrors.Is(err, io.EOF) {
				return false, fmt.Errorf("failed to read YAML from ConfigMap %s/%s: %w", nodePool.Namespace, ref.Name, err)
			}
			if len(strings.TrimSpace(string(raw))) > 0 {
				obj, _, decodeErr := ctrcfgDecoder.Decode(raw, nil, nil)
				// Decode errors are expected for non-ContainerRuntimeConfig resources
				// (MachineConfig, KubeletConfig, etc.); only ContainerRuntimeConfig is relevant here.
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

// GetRHELStreamForBootImage returns the RHEL stream name to pass to
// StreamForName when resolving platform-specific boot images (AMIs, VHDs,
// GCE images, etc.).
//
// It always delegates to GetRHELStream for version-aware default
// resolution, validation, and runc constraint checking. When
// spec.osImageStream.Name is unset, GetRHELStream derives the default
// from the release version: rhel-9 for OCP < 5.0, rhel-10 for
// OCP >= 5.0. This matches the dual-stream RHEL NodePool enhancement:
// https://github.com/openshift/enhancements/blob/master/enhancements/hypershift/dual-stream-rhel-nodepool.md
//
// On upgrade to OCP 5.0+, existing NodePools with unset
// spec.osImageStream will transition from rhel-9 to rhel-10 boot
// images. This is the intended behavior per the enhancement:
// implicit-stream NodePools automatically adopt the new default.
func GetRHELStreamForBootImage(ctx context.Context, c client.Client, nodePool *hyperv1.NodePool, releaseImage *releaseinfo.ReleaseImage) (string, error) {
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
	if nodePool.Spec.OSImageStream.Name == "" {
		return nil
	}
	_, err := GetRHELStreamForBootImage(ctx, c, nodePool, releaseImage)
	return err
}
