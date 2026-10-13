package config

import (
	"context"
	"fmt"

	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/util"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	FeatureGateConfigMapName = "feature-gate"
	FeatureGateConfigKey     = "feature-gate.yaml"
)

func FeatureGateConfigMap(ctx context.Context, c client.Reader, ns string) (*corev1.ConfigMap, error) {
	cm := &corev1.ConfigMap{}
	cm.Name = FeatureGateConfigMapName
	cm.Namespace = ns
	if err := c.Get(ctx, client.ObjectKeyFromObject(cm), cm); err != nil {
		return nil, err
	}
	return cm, nil
}

func ParseFeatureGates(cm *corev1.ConfigMap) (*configv1.FeatureGate, error) {
	fg := &configv1.FeatureGate{}
	manifest := cm.Data[FeatureGateConfigKey]
	if len(manifest) == 0 {
		return nil, fmt.Errorf("empty featuregate manifest")
	}
	if err := util.DeserializeResource(manifest, fg, api.Scheme); err != nil {
		return nil, fmt.Errorf("failed to deserialize feature gate resource: %w", err)
	}
	return fg, nil
}

func FeatureGatesFromConfigMap(ctx context.Context, c client.Reader, ns string) ([]string, error) {
	cm, err := FeatureGateConfigMap(ctx, c, ns)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch feature-gate configmap: %w", err)
	}
	fg, err := ParseFeatureGates(cm)
	if err != nil {
		return nil, fmt.Errorf("failed to parse feature gates manifest: %w", err)
	}
	return featureGatesFromResource(fg), nil
}

func featureGatesFromResource(fg *configv1.FeatureGate) []string {
	if len(fg.Status.FeatureGates) == 0 {
		return nil
	}
	var result []string
	for _, e := range fg.Status.FeatureGates[0].Enabled {
		result = append(result, fmt.Sprintf("%s=true", e.Name))
	}
	for _, d := range fg.Status.FeatureGates[0].Disabled {
		result = append(result, fmt.Sprintf("%s=false", d.Name))
	}
	return result
}
