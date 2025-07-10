package nodepool

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/support/backwardcompat"
	"github.com/openshift/hypershift/support/globalconfig"
	"github.com/openshift/hypershift/support/releaseinfo"
	supportutil "github.com/openshift/hypershift/support/util"

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

// ConfigGenerator knows how to:
// - Generate a unique hash id for any NodePool API input that requires a NodePool rollout.
// - Generate a compressed and encoded artifact of the mco RawConfig that can be stored in a Secret
// and consumed by mco/local-ignition-provider to generate the final ignition config served to Nodes.
type ConfigGenerator struct {
	client.Client
	hostedCluster         *hyperv1.HostedCluster
	nodePool              *hyperv1.NodePool
	controlplaneNamespace string
	*rolloutConfig
}

// rolloutConfig is the canonical source for input that produces a unique hash id and causes a NodePool rollout.
// This can be grouped by two categories of input based on how it's consumed by the MCO:
// - Some fields from spec like hostedCluster.Spec.Config, pullSecretName, additionalTrustBundleName...
// - The mcoRawConfig, which is an MCO consumable version of NodePool.spec.config, tuneConfig and any hypershift core machineConfig.
type rolloutConfig struct {
	releaseImage              *releaseinfo.ReleaseImage
	pullSecretName            string
	additionalTrustBundleName string
	// globalConfig represents input from hostedCluster.spec.config that requires a NodePool rollout.
	globalConfig string
	// rawConfig is an mco consumable version of NodePool.spec.config, tuneConfig and any hypershift core machine config.
	mcoRawConfig string
	// TODO(alberto): consider let haproxyRawConfig be an implementation detail of ConfigGenerator.
	// For now, it's a required input to keep the haproxy business logic and files outside the scope of this initial refactor.
	haproxyRawConfig string
}

// NewConfigGenerator is the contract to create a new ConfigGenerator.
func NewConfigGenerator(ctx context.Context, client client.Client, hostedCluster *hyperv1.HostedCluster, nodePool *hyperv1.NodePool, releaseImage *releaseinfo.ReleaseImage, haproxyRawConfig string) (*ConfigGenerator, error) {
	if client == nil {
		return nil, fmt.Errorf("client can't be nil")
	}

	if releaseImage == nil {
		return nil, fmt.Errorf("release image can't be nil")
	}

	globalConfig, err := globalConfigString(hostedCluster)
	if err != nil {
		return nil, err
	}

	cg := &ConfigGenerator{
		Client:                client,
		hostedCluster:         hostedCluster,
		nodePool:              nodePool,
		controlplaneNamespace: manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name),
		rolloutConfig: &rolloutConfig{
			releaseImage:     releaseImage,
			pullSecretName:   hostedCluster.Spec.PullSecret.Name,
			globalConfig:     globalConfig,
			haproxyRawConfig: haproxyRawConfig,
		},
	}

	if hostedCluster.Spec.AdditionalTrustBundle != nil {
		cg.rolloutConfig.additionalTrustBundleName = hostedCluster.Spec.AdditionalTrustBundle.Name
	}

	mcoRawConfig, err := cg.generateMCORawConfig(ctx)
	if err != nil {
		return nil, err
	}
	cg.rolloutConfig.mcoRawConfig = mcoRawConfig

	return cg, nil
}

// Compressed returns a gzipped artifact of the rawconfig.
// Prefer CompressedAndEncoded unless the CPO/your decompressor doesn't know how to handle base64 encoded data.
func (cg *ConfigGenerator) Compressed() (*bytes.Buffer, error) {
	return supportutil.Compress([]byte(cg.mcoRawConfig))
}

// CompressedAndEncoded returns a gzipped and base-64 encodesd artifact of the raw config.
func (cg *ConfigGenerator) CompressedAndEncoded() (*bytes.Buffer, error) {
	return supportutil.CompressAndEncode([]byte(cg.mcoRawConfig))
}

// Hash returns a unique hash id for any NodePool API input that requires a NodePool rollout, i.e. the rolloutConfig struct.
// TODO(alberto): hash the struct directly instead of the string representation field by field.
// This is kept like this for now to contain the scope of the refactor and avoid backward compatibility issues.
func (cg *ConfigGenerator) Hash() string {
	return supportutil.HashSimple(cg.mcoRawConfig + cg.releaseImage.Version() + cg.pullSecretName + cg.additionalTrustBundleName + cg.globalConfig)
}

// HashWithOutVersion is like Hash but doesn't compute the release version.
// This is only used to signal if a rollout is driven by a new release or by something else.
// TODO(alberto): This was left inconsistent in https://github.com/openshift/hypershift/pull/3795/files. It should also contain cg.globalConfig.
// This is kept like this for now to contain the scope of the refactor and avoid backward compatibility issues.
func (cg *ConfigGenerator) HashWithoutVersion() string {
	return supportutil.HashSimple(cg.mcoRawConfig + cg.pullSecretName + cg.additionalTrustBundleName)
}

func (cg *ConfigGenerator) Version() string {
	return cg.releaseImage.Version()
}

// generateMCORawConfig generates a mco consumable artifact of the mco Config.
func (cg *ConfigGenerator) generateMCORawConfig(ctx context.Context) (configsRaw string, err error) {
	var configs []corev1.ConfigMap

	// Look for core ignition configs in the control plane namespace.
	coreConfigs, err := cg.getCoreConfigs(ctx)
	if err != nil {
		return "", err
	}
	configs = append(configs, coreConfigs...)

	userConfig, err := cg.getUserConfigs(ctx)
	if err != nil {
		return "", err
	}
	configs = append(configs, userConfig...)

	// Look for NTO generated MachineConfigs from the hosted control plane namespace
	nodeTuningGeneratedConfigs, err := getNTOGeneratedConfig(ctx, cg)
	if err != nil {
		return "", err
	}
	configs = append(configs, nodeTuningGeneratedConfigs...)

	return cg.parse(configs)
}

// getUserConfigs returns a slice with all the configMaps in nodePool.Spec.Config.
func (cg *ConfigGenerator) getUserConfigs(ctx context.Context) ([]corev1.ConfigMap, error) {
	var errors []error
	var configs []corev1.ConfigMap
	for _, config := range cg.nodePool.Spec.Config {
		configConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      config.Name,
				Namespace: cg.nodePool.Namespace,
			},
		}
		if err := cg.Get(ctx, client.ObjectKeyFromObject(configConfigMap), configConfigMap); err != nil {
			errors = append(errors, err)
			continue
		}
		configs = append(configs, *configConfigMap)
	}
	return configs, utilerrors.NewAggregate(errors)
}

// getCoreConfigs returns a slice with all the configMaps containing MachineConfigs managed by the CPO
// and necessary for the node pool to function.
func (cg *ConfigGenerator) getCoreConfigs(ctx context.Context) ([]corev1.ConfigMap, error) {
	// Generic core config resources: fips, ssh, haproxy for old cpo releases and optionally ImageContentSources.
	// TODO (alberto): consider moving the expectedCoreConfigResources check
	// into the token Secret controller so we don't block Machine infra creation on this.
	expectedCoreConfigResources := 3
	if len(cg.hostedCluster.Spec.ImageContentSources) > 0 {
		// additional core config resource created when image content source specified.
		expectedCoreConfigResources += 1
	}
	if cg.haproxyRawConfig != "" {
		expectedCoreConfigResources--
	}

	var errors []error
	coreConfigMapList := &corev1.ConfigMapList{}
	if err := cg.List(ctx, coreConfigMapList, client.MatchingLabels{
		nodePoolCoreIgnitionConfigLabel: "true",
	}, client.InNamespace(cg.controlplaneNamespace)); err != nil {
		errors = append(errors, err)
	}

	if len(coreConfigMapList.Items) != expectedCoreConfigResources {
		return coreConfigMapList.Items, &MissingCoreConfigError{
			Got:      len(coreConfigMapList.Items),
			Expected: expectedCoreConfigResources,
		}
	}

	return coreConfigMapList.Items, utilerrors.NewAggregate(errors)
}

type MissingCoreConfigError struct {
	Expected int
	Got      int
}

func (e *MissingCoreConfigError) Error() string {
	return fmt.Sprintf("expected %d core ignition configs, found %d", e.Expected, e.Got)
}

// parse loops over a slice of configMaps and returns a string with the concatenated content if they are MCO consumable APIs.
func (cg *ConfigGenerator) parse(configs []corev1.ConfigMap) (string, error) {
	var errors []error
	var allConfigPlainText []string

	if cg.haproxyRawConfig != "" {
		allConfigPlainText = append(allConfigPlainText, cg.haproxyRawConfig)
	}

	for _, config := range configs {
		cmPayload := config.Data[TokenSecretConfigKey]
		// ignition config-map payload may contain multiple manifests
		yamlReader := yaml.NewYAMLReader(bufio.NewReader(strings.NewReader(cmPayload)))
		for {
			manifestRaw, err := yamlReader.Read()
			if err != nil && err != io.EOF {
				errors = append(errors, fmt.Errorf("configmap %q contains invalid yaml: %w", config.Name, err))
				continue
			}
			if len(manifestRaw) != 0 && strings.TrimSpace(string(manifestRaw)) != "" {
				manifest, err := cg.defaultAndValidateConfigManifest(manifestRaw)
				if err != nil {
					errors = append(errors, fmt.Errorf("configmap %q yaml document failed validation: %w", config.Name, err))
					continue
				}
				allConfigPlainText = append(allConfigPlainText, string(manifest))
			}
			if err == io.EOF {
				break
			}
		}
	}

	// These configs are the input to a hash func whose output is used as part of the name of the user-data secret,
	// so our output must be deterministic.
	sort.Strings(allConfigPlainText)
	return strings.Join(allConfigPlainText, "\n---\n"), utilerrors.NewAggregate(errors)
}

// defaultAndValidateConfigManifest validates a manifest is a MCO consumabled supported API
// and default core labels.
func (cg *ConfigGenerator) defaultAndValidateConfigManifest(manifest []byte) ([]byte, error) {
	scheme := runtime.NewScheme()
	_ = mcfgv1.Install(scheme)
	_ = v1alpha1.Install(scheme)
	_ = configv1.Install(scheme)
	_ = configv1alpha1.Install(scheme)

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
		manifest, err = encode(cr, yamlSerializer)
		if err != nil {
			return nil, fmt.Errorf("failed to encode machine config after defaulting it: %w", err)
		}
	case *v1alpha1.ImageContentSourcePolicy:
	case *configv1.ImageDigestMirrorSet:
	case *configv1alpha1.ClusterImagePolicy:
	case *mcfgv1.KubeletConfig:
		obj.Spec.MachineConfigPoolSelector = &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"machineconfiguration.openshift.io/mco-built-in": "",
			},
		}
		manifest, err = encode(cr, yamlSerializer)
		if err != nil {
			return nil, fmt.Errorf("failed to encode kubelet config after setting built-in MCP selector: %w", err)
		}
	case *mcfgv1.ContainerRuntimeConfig:
		obj.Spec.MachineConfigPoolSelector = &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"machineconfiguration.openshift.io/mco-built-in": "",
			},
		}
		manifest, err = encode(cr, yamlSerializer)
		if err != nil {
			return nil, fmt.Errorf("failed to encode container runtime config after setting built-in MCP selector: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported config type: %T", obj)
	}
	return manifest, err
}

func encode(obj runtime.Object, ser *serializer.Serializer) ([]byte, error) {
	buff := bytes.Buffer{}
	if err := ser.Encode(obj, &buff); err != nil {
		return nil, err
	}
	return buff.Bytes(), nil
}

func globalConfigString(hcluster *hyperv1.HostedCluster) (string, error) {
	// 1. - Reconcile conditions according to current state of the world.
	proxy := globalconfig.ProxyConfig()
	globalconfig.ReconcileProxyConfigWithStatusFromHostedCluster(proxy, hcluster)

	// NOTE: The image global config is not injected via userdata or NodePool ignition config.
	// It is included directly by the ignition server.  However, we need to detect the change
	// here to trigger a nodepool update.
	image := globalconfig.ImageConfig()
	globalconfig.ReconcileImageConfigFromHostedCluster(image, hcluster)

	// Serialize proxy and image into a single string to use in the token secret hash.
	globalConfigBytes := bytes.NewBuffer(nil)

	enc := json.NewEncoder(globalConfigBytes)
	if err := enc.Encode(proxy); err != nil {
		return "", fmt.Errorf("failed to encode proxy global config: %w", err)
	}

	if err := enc.Encode(image); err != nil {
		return "", fmt.Errorf("failed to encode image global config: %w", err)
	}

	// Some fields in the ClusterConfiguration have changes that are not backwards compatible with older versions of the CPO.
	return backwardcompat.GetBackwardCompatibleConfigString(globalConfigBytes.String()), nil
}
