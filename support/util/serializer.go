package util

import (
	"bytes"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"

	apisupport "github.com/openshift/hypershift/support/api"
)

func DeserializeResource(data string, resource runtime.Object, objectTyper runtime.ObjectTyper) error {
	gvks, _, err := objectTyper.ObjectKinds(resource)
	if err != nil || len(gvks) == 0 {
		return fmt.Errorf("cannot determine GVK of resource of type %T: %w", resource, err)
	}
	_, _, err = apisupport.YamlSerializer.Decode([]byte(data), &gvks[0], resource)
	return err
}

func SerializeResource(resource runtime.Object, objectTyper runtime.ObjectTyper) (string, error) {
	out := &bytes.Buffer{}
	gvks, _, err := objectTyper.ObjectKinds(resource)
	if err != nil || len(gvks) == 0 {
		return "", fmt.Errorf("cannot determine GVK of resource of type %T: %w", resource, err)
	}
	resource.GetObjectKind().SetGroupVersionKind(gvks[0])
	if err = apisupport.YamlSerializer.Encode(resource, out); err != nil {
		return "", err
	}
	return out.String(), nil
}
