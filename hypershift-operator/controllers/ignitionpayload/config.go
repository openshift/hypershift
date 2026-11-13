package ignitionpayload

import (
	"bufio"
	"context"
	coreerrors "errors"
	"fmt"
	"io"
	"sort"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/backwardcompat"

	configv1 "github.com/openshift/api/config/v1"
	configv1alpha1 "github.com/openshift/api/config/v1alpha1"
	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"
	"github.com/openshift/api/operator/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	serializer "k8s.io/apimachinery/pkg/runtime/serializer/json"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/yaml"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// configDataKey is the ConfigMap data key under which an ignition config-map
// payload is stored. Matches the NodePool controller's TokenSecretConfigKey.
const configDataKey = "config"

// ResolvedConfig is the validated, deterministic output of reading a CR's ConfigMaps.
type ResolvedConfig struct {
	// RolloutManifests is the sorted, validated manifest set from spec.rolloutConfigMaps
	// (user, core, NTO), joined with "\n---\n". Feeds the rollout hash and the payload.
	RolloutManifests string
	// ManagementManifests is the sorted, validated manifest set from spec.managementConfigMaps
	// (the apiserver-HAProxy config). Excluded from the rollout hash; included in the
	// rendered payload and the payload-identity hash.
	ManagementManifests string
}

// resolveAndValidate reads the ConfigMaps named in the CR spec from the CR's namespace,
// splits each into manifests, defaults+validates them, and returns the deterministic
// rollout and mgmt manifest strings. On a validation failure it returns an error naming
// the offending source ConfigMap.
func resolveAndValidate(ctx context.Context, c client.Client, cr *hyperv1.IgnitionPayload) (ResolvedConfig, error) {
	rolloutCMs, err := getConfigMaps(ctx, c, cr.Namespace, cr.Spec.RolloutConfigMaps)
	if err != nil {
		return ResolvedConfig{}, err
	}
	mgmtCMs, err := getConfigMaps(ctx, c, cr.Namespace, cr.Spec.ManagementConfigMaps)
	if err != nil {
		return ResolvedConfig{}, err
	}

	rollout, err := parse(rolloutCMs)
	if err != nil {
		return ResolvedConfig{}, &validationError{err}
	}
	mgmt, err := parse(mgmtCMs)
	if err != nil {
		return ResolvedConfig{}, &validationError{err}
	}
	return ResolvedConfig{RolloutManifests: rollout, ManagementManifests: mgmt}, nil
}

// validationError marks a config-manifest validation failure, as distinct from a transient
// read error (e.g. a ConfigMap that is briefly absent). The controller sets PayloadGenerated=False
// for a validationError and requeues for any other error.
type validationError struct{ err error }

func (e *validationError) Error() string { return e.err.Error() }
func (e *validationError) Unwrap() error { return e.err }

// getConfigMaps fetches each named ConfigMap from namespace ns, in the CR's namespace.
func getConfigMaps(ctx context.Context, c client.Client, ns string, refs []hyperv1.ConfigMapReference) ([]corev1.ConfigMap, error) {
	out := make([]corev1.ConfigMap, 0, len(refs))
	for _, ref := range refs {
		cm := &corev1.ConfigMap{}
		if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: ref.Name}, cm); err != nil {
			return nil, fmt.Errorf("failed to get configmap %q: %w", ref.Name, err)
		}
		out = append(out, *cm)
	}
	return out, nil
}

// parse loops over a slice of ConfigMaps and returns the concatenated content of their
// MCO-consumable manifests, defaulted/validated and sorted for a deterministic hash input.
func parse(configs []corev1.ConfigMap) (string, error) {
	var errors []error
	var allConfigPlainText []string

	for _, config := range configs {
		cmPayload := config.Data[configDataKey]
		// an ignition config-map payload may contain multiple manifests
		yamlReader := yaml.NewYAMLReader(bufio.NewReader(strings.NewReader(cmPayload)))
		for {
			manifestRaw, err := yamlReader.Read()
			if err != nil && !coreerrors.Is(err, io.EOF) {
				errors = append(errors, fmt.Errorf("configmap %q contains invalid yaml: %w", config.Name, err))
				continue
			}
			if len(manifestRaw) != 0 && strings.TrimSpace(string(manifestRaw)) != "" {
				manifest, err := defaultAndValidateConfigManifest(manifestRaw)
				if err != nil {
					errors = append(errors, fmt.Errorf("configmap %q yaml document failed validation: %w", config.Name, err))
					continue
				}
				allConfigPlainText = append(allConfigPlainText, string(manifest))
			}
			if coreerrors.Is(err, io.EOF) {
				break
			}
		}
	}

	// These configs are a hash input, so the output must be deterministic.
	sort.Strings(allConfigPlainText)
	return strings.Join(allConfigPlainText, "\n---\n"), utilerrors.NewAggregate(errors)
}

// defaultAndValidateConfigManifest validates that a manifest is an MCO-consumable supported
// API and defaults core labels/selectors. Ported from the NodePool ConfigGenerator.
func defaultAndValidateConfigManifest(manifest []byte) ([]byte, error) {
	scheme := runtime.NewScheme()
	_ = mcfgv1.Install(scheme)
	_ = v1alpha1.Install(scheme)
	_ = configv1.Install(scheme)
	_ = configv1alpha1.Install(scheme)

	manifest = backwardcompat.NormalizeV1Alpha1ClusterImagePolicy(manifest)

	yamlSerializer := serializer.NewSerializerWithOptions(
		serializer.DefaultMetaFactory, scheme, scheme,
		serializer.SerializerOptions{Yaml: true, Pretty: true, Strict: false},
	)

	cr, _, err := yamlSerializer.Decode(manifest, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("error decoding config: %w", err)
	}

	switch obj := cr.(type) {
	case *mcfgv1.MachineConfig:
		if obj.Labels == nil {
			obj.Labels = map[string]string{}
		}
		obj.Labels["machineconfiguration.openshift.io/role"] = "worker"
		manifest, err = api.CompatibleYAMLEncode(cr, yamlSerializer)
		if err != nil {
			return nil, fmt.Errorf("failed to encode machine config after defaulting it: %w", err)
		}
	case *v1alpha1.ImageContentSourcePolicy:
	case *configv1.ImageDigestMirrorSet:
	case *configv1.ClusterImagePolicy:
	case *mcfgv1.KubeletConfig:
		obj.Spec.MachineConfigPoolSelector = &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"machineconfiguration.openshift.io/mco-built-in": "",
			},
		}
		manifest, err = api.CompatibleYAMLEncode(cr, yamlSerializer)
		if err != nil {
			return nil, fmt.Errorf("failed to encode kubelet config after setting built-in MCP selector: %w", err)
		}
	case *mcfgv1.ContainerRuntimeConfig:
		obj.Spec.MachineConfigPoolSelector = &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"machineconfiguration.openshift.io/mco-built-in": "",
			},
		}
		manifest, err = api.CompatibleYAMLEncode(cr, yamlSerializer)
		if err != nil {
			return nil, fmt.Errorf("failed to encode container runtime config after setting built-in MCP selector: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported config type: %T", obj)
	}
	return manifest, err
}
