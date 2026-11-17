package karpenterignition

import (
	"fmt"

	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type nodeClass struct {
	object *unstructured.Unstructured
}

type nodeClassStatus struct {
	Conditions   []metav1.Condition `json:"conditions,omitempty"`
	ReleaseImage string             `json:"releaseImage,omitempty"`
	Version      string             `json:"version,omitempty"`
}

func newNodeClass(gvk schema.GroupVersionKind) *nodeClass {
	object := &unstructured.Unstructured{}
	object.SetGroupVersionKind(gvk)
	return &nodeClass{object: object}
}

func newNodeClassList(gvk schema.GroupVersionKind) *unstructured.UnstructuredList {
	nodeClassList := &unstructured.UnstructuredList{}
	gvk.Kind += "List"
	nodeClassList.SetGroupVersionKind(gvk)
	return nodeClassList
}

func (n *nodeClass) specVersion() (string, error) {
	version, _, err := unstructured.NestedString(n.object.Object, "spec", "version")
	if err != nil {
		return "", fmt.Errorf("failed to read NodeClass spec.version: %w", err)
	}
	return version, nil
}

func (n *nodeClass) statusVersion() (string, error) {
	version, _, err := unstructured.NestedString(n.object.Object, "status", "version")
	if err != nil {
		return "", fmt.Errorf("failed to read NodeClass status.version: %w", err)
	}
	return version, nil
}

func (n *nodeClass) kubeletConfig() (hyperkarpenterv1.KubeletConfiguration, error) {
	kubeletConfig, _, err := unstructured.NestedMap(n.object.Object, "spec", "kubelet")
	if err != nil {
		return hyperkarpenterv1.KubeletConfiguration{}, fmt.Errorf("failed to read NodeClass spec.kubelet: %w", err)
	}

	var parsed hyperkarpenterv1.KubeletConfiguration
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(kubeletConfig, &parsed); err != nil {
		return hyperkarpenterv1.KubeletConfiguration{}, fmt.Errorf("failed to decode NodeClass spec.kubelet: %w", err)
	}

	return parsed, nil
}

func (n *nodeClass) status() (nodeClassStatus, error) {
	version, err := n.statusVersion()
	if err != nil {
		return nodeClassStatus{}, err
	}
	releaseImage, _, err := unstructured.NestedString(n.object.Object, "status", "releaseImage")
	if err != nil {
		return nodeClassStatus{}, fmt.Errorf("failed to read NodeClass status.releaseImage: %w", err)
	}
	rawConditions, _, err := unstructured.NestedSlice(n.object.Object, "status", "conditions")
	if err != nil {
		return nodeClassStatus{}, fmt.Errorf("failed to read NodeClass status.conditions: %w", err)
	}

	conditions := make([]metav1.Condition, len(rawConditions))
	for i, rawCondition := range rawConditions {
		conditionMap, ok := rawCondition.(map[string]interface{})
		if !ok {
			return nodeClassStatus{}, fmt.Errorf("NodeClass status.conditions[%d] must be an object, got %T", i, rawCondition)
		}
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(conditionMap, &conditions[i]); err != nil {
			return nodeClassStatus{}, fmt.Errorf("failed to decode NodeClass status.conditions[%d]: %w", i, err)
		}
	}
	return nodeClassStatus{
		Conditions:   conditions,
		ReleaseImage: releaseImage,
		Version:      version,
	}, nil
}

// setStatus writes the common status fields without replacing provider-owned fields.
func (n *nodeClass) setStatus(status nodeClassStatus) error {
	encoded, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&status)
	if err != nil {
		return fmt.Errorf("failed to encode NodeClass status: %w", err)
	}
	current, _, err := unstructured.NestedMap(n.object.Object, "status")
	if err != nil {
		return fmt.Errorf("failed to read NodeClass status: %w", err)
	}
	if current == nil {
		current = make(map[string]interface{})
	}
	// Omitted fields must also be removed from the object to retain omitempty behavior.
	for _, field := range []string{"conditions", "releaseImage", "version"} {
		if value, ok := encoded[field]; ok {
			current[field] = value
		} else {
			delete(current, field)
		}
	}
	if err := unstructured.SetNestedMap(n.object.Object, current, "status"); err != nil {
		return fmt.Errorf("failed to set NodeClass status: %w", err)
	}
	return nil
}
