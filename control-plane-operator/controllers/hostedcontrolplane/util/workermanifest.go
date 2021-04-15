package util

import (
	"bytes"
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"

	hyperapi "github.com/openshift/hypershift/api"
)

const (
	userDataKey = "data"
)

func ReconcileWorkerManifest(cm *corev1.ConfigMap, resource client.Object) error {
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	if cm.Labels == nil {
		cm.Labels = map[string]string{}
	}
	cm.Labels["worker-manifest"] = "true"
	serialized, err := serializeResource(resource)
	if err != nil {
		return fmt.Errorf("failed to serialize resource of type %T: %w", resource, err)
	}
	cm.Data[userDataKey] = serialized
	return nil
}

func serializeResource(resource client.Object) (string, error) {
	out := &bytes.Buffer{}
	gvks, _, err := hyperapi.Scheme.ObjectKinds(resource)
	if err != nil || len(gvks) == 0 {
		return "", fmt.Errorf("cannot determine GVK of resource of type %T: %w", resource, err)
	}
	resource.GetObjectKind().SetGroupVersionKind(gvks[0])
	if err = hyperapi.YamlSerializer.Encode(resource, out); err != nil {
		return "", err
	}
	return out.String(), nil
}
