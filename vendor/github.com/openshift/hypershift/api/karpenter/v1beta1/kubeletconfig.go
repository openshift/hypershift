package v1beta1

import (
	"encoding/json"
	"fmt"
)

// ToKubeletConfigManifest converts the KubeletConfiguration spec into a KubeletConfig
// machineconfiguration.openshift.io/v1 YAML manifest string suitable for injection
// into a ConfigMap "config" key. Returns empty string if KubeletConfiguration is nil.
// The name argument is used as the KubeletConfig CR name.
func (kc *KubeletConfiguration) ToKubeletConfigManifest(name string) (string, error) {
	if kc == nil {
		return "", nil
	}

	// Marshal the KubeletConfig struct directly — the JSON tags and omitempty on each field
	// handle nil/empty filtering, and metav1.Duration marshals as a duration string automatically.
	// This mirrors how upstream Karpenter serializes KubeletConfiguration in nodeadm.go.
	kubeletConfigJSON, err := json.Marshal(kc)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf(`apiVersion: machineconfiguration.openshift.io/v1
kind: KubeletConfig
metadata:
  name: %s
spec:
  kubeletConfig: %s`, name, string(kubeletConfigJSON)), nil
}

// ToKubeletConfigManifestWithTaints is like ToKubeletConfigManifest but also injects
// the karpenter.sh/unregistered taint into registerWithTaints. Use this instead of
// ToKubeletConfigManifest when the set-karpenter-taint ConfigMap is being omitted from
// the NodePool configRefs (i.e. when Spec.Kubelet != nil), so that only a single
// KubeletConfig targets the worker MachineConfigPool in the ignition payload.
func (kc *KubeletConfiguration) ToKubeletConfigManifestWithTaints(name string) (string, error) {
	if kc == nil {
		return "", nil
	}

	raw, err := json.Marshal(kc)
	if err != nil {
		return "", err
	}
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", err
	}
	m["registerWithTaints"] = []interface{}{
		map[string]interface{}{
			"key":    "karpenter.sh/unregistered",
			"value":  "true",
			"effect": "NoExecute",
		},
	}
	merged, err := json.Marshal(m)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf(`apiVersion: machineconfiguration.openshift.io/v1
kind: KubeletConfig
metadata:
  name: %s
spec:
  kubeletConfig: %s`, name, string(merged)), nil
}
