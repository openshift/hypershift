package k8sutil

import (
	"bytes"
	"fmt"

	hyperapi "github.com/openshift/hypershift/support/api"

	"k8s.io/apimachinery/pkg/runtime"
)

func DeserializeResource(data string, resource runtime.Object, objectTyper runtime.ObjectTyper) error {
	gvks, _, err := objectTyper.ObjectKinds(resource)
	if err != nil || len(gvks) == 0 {
		return fmt.Errorf("cannot determine GVK of resource of type %T: %w", resource, err)
	}
	_, _, err = hyperapi.YamlSerializer.Decode([]byte(data), &gvks[0], resource)
	if err != nil {
		return fmt.Errorf("failed to decode resource: %w", err)
	}
	return nil
}

func SerializeResource(resource runtime.Object, objectTyper runtime.ObjectTyper) (string, error) {
	out := &bytes.Buffer{}
	gvks, _, err := objectTyper.ObjectKinds(resource)
	if err != nil || len(gvks) == 0 {
		return "", fmt.Errorf("cannot determine GVK of resource of type %T: %w", resource, err)
	}
	resource.GetObjectKind().SetGroupVersionKind(gvks[0])
	if err = hyperapi.YamlSerializer.Encode(resource, out); err != nil {
		return "", fmt.Errorf("failed to encode resource: %w", err)
	}
	return out.String(), nil
}
