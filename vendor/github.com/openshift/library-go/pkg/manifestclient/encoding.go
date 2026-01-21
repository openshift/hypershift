package manifestclient

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
)

func individualFromList(objList *unstructured.UnstructuredList, name string) (*unstructured.Unstructured, error) {
	individualKind := strings.TrimSuffix(objList.GetKind(), "List")

	for _, obj := range objList.Items {
		if obj.GetName() != name {
			continue
		}

		ret := obj.DeepCopy()
		ret.SetKind(individualKind)
		return ret, nil
	}

	return nil, fmt.Errorf("not found in this list")
}

func readListFile(sourceFS fs.FS, path string) (*unstructured.UnstructuredList, error) {
	content, err := fs.ReadFile(sourceFS, path)
	if err != nil {
		return nil, fmt.Errorf("unable to read %q: %w", path, err)
	}

	return decodeListObj(content)
}

func readIndividualFile(sourceFS fs.FS, path string) (*unstructured.Unstructured, error) {
	content, err := fs.ReadFile(sourceFS, path)
	if err != nil {
		return nil, fmt.Errorf("unable to read %q: %w", path, err)
	}

	return decodeIndividualObj(content)
}

var localScheme = runtime.NewScheme()
var codecs = serializer.NewCodecFactory(localScheme)

func decodeIndividualObj(content []byte) (*unstructured.Unstructured, error) {
	obj, _, err := codecs.UniversalDecoder().Decode(content, nil, &unstructured.Unstructured{})
	if err != nil {
		return nil, fmt.Errorf("unable to decode: %w", err)
	}
	return obj.(*unstructured.Unstructured), nil
}

func decodeListObj(content []byte) (*unstructured.UnstructuredList, error) {
	obj, _, err := codecs.UniversalDecoder().Decode(content, nil, &unstructured.UnstructuredList{})
	if err != nil {
		return nil, fmt.Errorf("unable to decode: %w", err)
	}
	return obj.(*unstructured.UnstructuredList), nil
}

func serializeIndividualObjToJSON(obj *unstructured.Unstructured) (string, error) {
	ret, err := json.MarshalIndent(obj.Object, "", "    ")
	if err != nil {
		return "", err
	}
	return string(ret) + "\n", nil
}

func serializeListObjToJSON(obj *unstructured.UnstructuredList) (string, error) {
	ret, err := json.MarshalIndent(obj, "", "    ")
	if err != nil {
		return "", err
	}
	return string(ret) + "\n", nil
}

func serializeAPIResourceListToJSON(obj *metav1.APIResourceList) (string, error) {
	ret, err := json.MarshalIndent(obj, "", "    ")
	if err != nil {
		return "", err
	}
	return string(ret) + "\n", nil
}
