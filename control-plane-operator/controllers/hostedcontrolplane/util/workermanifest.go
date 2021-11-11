package util

import (
	"bytes"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"

	hyperapi "github.com/openshift/hypershift/control-plane-operator/api"
)

const (
	UserDataKey = "data"
)

func ReconcileWorkerManifest(cm *corev1.ConfigMap, resource client.Object) error {
	content, err := SerializeResource(resource, hyperapi.Scheme)
	if err != nil {
		return fmt.Errorf("failed to serialize resource of type %T: %w", resource, err)
	}
	return ReconcileWorkerManifestString(cm, content)
}

func ReconcileWorkerManifestString(cm *corev1.ConfigMap, content string) error {
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	if cm.Labels == nil {
		cm.Labels = map[string]string{}
	}
	cm.Labels["worker-manifest"] = "true"
	cm.Data[UserDataKey] = content
	return nil
}

func SerializeResource(resource client.Object, objectTyper runtime.ObjectTyper) (string, error) {
	out := &bytes.Buffer{}
	gvks, _, err := objectTyper.ObjectKinds(resource)
	if err != nil || len(gvks) == 0 {
		return "", fmt.Errorf("cannot determine GVK of resource of type %T: %w", resource, err)
	}
	resource.GetObjectKind().SetGroupVersionKind(gvks[0])
	if err = hyperapi.YamlSerializer.Encode(resource, out); err != nil {
		return "", err
	}
	return out.String(), nil
}
