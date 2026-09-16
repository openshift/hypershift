package ntotuning

import (
	"bytes"
	"context"
	coreerrors "errors"
	"fmt"
	"sort"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/k8sutil"

	performanceprofilev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	serializer "k8s.io/apimachinery/pkg/runtime/serializer/json"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GetTuningConfig loads tuning ConfigMaps and returns serialized Tuned and PerformanceProfile manifests.
func GetTuningConfig(
	ctx context.Context,
	c client.Client,
	configNamespace string,
	tuningConfigRefs []corev1.LocalObjectReference,
) (string, string, string, error) {
	var (
		configs                              []corev1.ConfigMap
		tunedAllConfigPlainText              []string
		performanceProfileConfigMapName      string
		performanceProfileAllConfigPlainText []string
		errors                               []error
	)

	for _, ref := range tuningConfigRefs {
		configConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      ref.Name,
				Namespace: configNamespace,
			},
		}
		if err := c.Get(ctx, client.ObjectKeyFromObject(configConfigMap), configConfigMap); err != nil {
			errors = append(errors, err)
			continue
		}
		configs = append(configs, *configConfigMap)
	}

	for _, config := range configs {
		manifestRaw, ok := config.Data[ConfigKey]
		if !ok {
			errors = append(errors, fmt.Errorf("no manifest found in configmap %q with key %q", config.Name, ConfigKey))
			continue
		}
		manifestTuned, manifestPerformanceProfile, err := ValidateTuningConfigManifest([]byte(manifestRaw))
		if err != nil {
			errors = append(errors, fmt.Errorf("configmap %q failed validation: %w", config.Name, err))
			continue
		}
		if manifestTuned != nil {
			tunedAllConfigPlainText = append(tunedAllConfigPlainText, string(manifestTuned))
		}
		if manifestPerformanceProfile != nil {
			performanceProfileConfigMapName = config.Name
			performanceProfileAllConfigPlainText = append(performanceProfileAllConfigPlainText, string(manifestPerformanceProfile))
		}
	}

	if len(performanceProfileAllConfigPlainText) > 1 {
		errors = append(errors, fmt.Errorf("there cannot be more than one PerformanceProfile per NodePool. found: %d", len(performanceProfileAllConfigPlainText)))
	}

	// Keep output deterministic to avoid unnecessary no-op changes to Tuned ConfigMap.
	sort.Strings(tunedAllConfigPlainText)
	sort.Strings(performanceProfileAllConfigPlainText)

	return strings.Join(tunedAllConfigPlainText, "\n---\n"),
		strings.Join(performanceProfileAllConfigPlainText, "\n---\n"),
		performanceProfileConfigMapName,
		utilerrors.NewAggregate(errors)
}

// ReconcileNodeTuningConfigMap writes a serialized tuning manifest into a control plane ConfigMap for NTO.
func ReconcileNodeTuningConfigMap(tuningConfigMap *corev1.ConfigMap, nodePool *hyperv1.NodePool, rawConfig string) error {
	tuningConfigMap.Immutable = ptr.To(false)
	if tuningConfigMap.Annotations == nil {
		tuningConfigMap.Annotations = make(map[string]string)
	}
	if tuningConfigMap.Labels == nil {
		tuningConfigMap.Labels = make(map[string]string)
	}

	tuningConfigMap.Annotations[hyperv1.NodePoolLabel] = client.ObjectKeyFromObject(nodePool).String()
	tuningConfigMap.Labels[hyperv1.NodePoolLabel] = nodePool.GetName()

	if tuningConfigMap.Data == nil {
		tuningConfigMap.Data = map[string]string{}
	}
	tuningConfigMap.Data[ConfigKey] = rawConfig

	return nil
}

// ReconcileTunedConfigMap writes a Tuned manifest into tunedConfigMap for NTO to mirror into the guest cluster.
func ReconcileTunedConfigMap(tunedConfigMap *corev1.ConfigMap, nodePool *hyperv1.NodePool, tunedConfig string) error {
	if err := ReconcileNodeTuningConfigMap(tunedConfigMap, nodePool, tunedConfig); err != nil {
		return err
	}
	tunedConfigMap.Labels[TunedConfigMapLabel] = "true"
	return nil
}

// ReconcilePerformanceProfileConfigMap writes a PerformanceProfile manifest into performanceProfileConfigMap for NTO.
func ReconcilePerformanceProfileConfigMap(performanceProfileConfigMap *corev1.ConfigMap, nodePool *hyperv1.NodePool, performanceProfileConfig string) error {
	if err := ReconcileNodeTuningConfigMap(performanceProfileConfigMap, nodePool, performanceProfileConfig); err != nil {
		return err
	}
	performanceProfileConfigMap.Labels[PerformanceProfileConfigMapLabel] = "true"
	return nil
}

// ValidateTuningConfigManifest decodes and normalizes a Tuned or PerformanceProfile manifest.
func ValidateTuningConfigManifest(manifest []byte) ([]byte, []byte, error) {
	scheme := runtime.NewScheme()
	_ = tunedv1.AddToScheme(scheme)
	_ = performanceprofilev2.AddToScheme(scheme)

	yamlSerializer := serializer.NewSerializerWithOptions(
		serializer.DefaultMetaFactory, scheme, scheme,
		serializer.SerializerOptions{Yaml: true, Pretty: true, Strict: true},
	)
	cr, _, err := yamlSerializer.Decode(manifest, nil, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("error decoding config: %w", err)
	}

	switch obj := cr.(type) {
	case *tunedv1.Tuned:
		creationTimestamp := obj.GetCreationTimestamp()
		if creationTimestamp.IsZero() {
			obj.SetCreationTimestamp(metav1.NewTime(time.Unix(0, 0)))
		}
		buff := bytes.Buffer{}
		if err := yamlSerializer.Encode(obj, &buff); err != nil {
			return nil, nil, fmt.Errorf("failed to encode Tuned object: %w", err)
		}
		result := buff.Bytes()
		resultStr := strings.ReplaceAll(string(result), `creationTimestamp: "1970-01-01T00:00:00Z"`, `creationTimestamp: null`)
		manifest = []byte(resultStr)
		return manifest, nil, nil

	case *performanceprofilev2.PerformanceProfile:
		validationErrors := obj.ValidateBasicFields()
		if len(validationErrors) > 0 {
			return nil, nil, fmt.Errorf("PerformanceProfile validation failed pp:%s : %w", obj.Name, coreerrors.Join(validationErrors.ToAggregate().Errors()...))
		}

		creationTimestamp := obj.GetCreationTimestamp()
		if creationTimestamp.IsZero() {
			obj.SetCreationTimestamp(metav1.NewTime(time.Unix(0, 0)))
		}
		buff := bytes.Buffer{}
		if err := yamlSerializer.Encode(obj, &buff); err != nil {
			return nil, nil, fmt.Errorf("failed to encode performance profile after defaulting it: %w", err)
		}
		result := buff.Bytes()
		resultStr := strings.ReplaceAll(string(result), `creationTimestamp: "1970-01-01T00:00:00Z"`, `creationTimestamp: null`)
		manifest = []byte(resultStr)
		return nil, manifest, nil

	default:
		return nil, nil, fmt.Errorf("unsupported tuningConfig object type: %T", obj)
	}
}

// DeleteConfigMapsByLabel deletes ConfigMaps in controlPlaneNamespace matching lbl.
func DeleteConfigMapsByLabel(ctx context.Context, c client.Client, lbl map[string]string, controlPlaneNamespace string) error {
	cmList := &corev1.ConfigMapList{}
	if err := c.List(ctx, cmList, &client.ListOptions{
		LabelSelector: labels.SelectorFromSet(lbl),
		Namespace:     controlPlaneNamespace,
	}); err != nil {
		return err
	}
	for i := range cmList.Items {
		cm := &cmList.Items[i]
		if _, err := k8sutil.DeleteIfNeeded(ctx, c, cm); err != nil {
			return err
		}
	}
	return nil
}
