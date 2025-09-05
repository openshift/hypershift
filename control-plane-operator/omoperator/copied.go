package omoperator

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"

	"github.com/openshift/multi-operator-manager/pkg/library/libraryinputresources"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/util/sets"
)

// copied from mom repo
func unstructuredToMustGatherFormat(in []*libraryinputresources.Resource) ([]*libraryinputresources.Resource, error) {
	type mustGatherKeyType struct {
		gk        schema.GroupKind
		namespace string
	}

	versionsByGroupKind := map[schema.GroupKind]sets.Set[string]{}
	groupKindToResource := map[schema.GroupKind]schema.GroupVersionResource{}
	byGroupKind := map[mustGatherKeyType]*unstructured.UnstructuredList{}
	for _, curr := range in {
		gvk := curr.Content.GroupVersionKind()
		groupKind := curr.Content.GroupVersionKind().GroupKind()
		existingVersions, ok := versionsByGroupKind[groupKind]
		if !ok {
			existingVersions = sets.New[string]()
			versionsByGroupKind[groupKind] = existingVersions
		}
		existingVersions.Insert(gvk.Version)
		groupKindToResource[groupKind] = curr.ResourceType

		mustGatherKey := mustGatherKeyType{
			gk:        groupKind,
			namespace: curr.Content.GetNamespace(),
		}
		existing, ok := byGroupKind[mustGatherKey]
		if !ok {
			existing = &unstructured.UnstructuredList{
				Object: map[string]interface{}{},
			}
			listGVK := guessListKind(curr.Content)
			existing.GetObjectKind().SetGroupVersionKind(listGVK)
			byGroupKind[mustGatherKey] = existing
		}
		existing.Items = append(existing.Items, *curr.Content.DeepCopy())
	}

	errs := []error{}
	for groupKind, currVersions := range versionsByGroupKind {
		if len(currVersions) == 1 {
			continue
		}
		errs = append(errs, fmt.Errorf("groupKind=%v has multiple versions: %v, which prevents serialization", groupKind, sets.List(currVersions)))
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	ret := []*libraryinputresources.Resource{}
	for mustGatherKey, list := range byGroupKind {
		namespacedString := "REPLACE_ME"
		if len(mustGatherKey.namespace) > 0 {
			namespacedString = "namespaces"
		} else {
			namespacedString = "cluster-scoped-resources"
		}

		groupString := mustGatherKey.gk.Group
		if len(groupString) == 0 {
			groupString = "core"
		}
		listAsUnstructured := &unstructured.Unstructured{Object: list.UnstructuredContent()}
		resourceType := groupKindToResource[mustGatherKey.gk]
		ret = append(ret, &libraryinputresources.Resource{
			Filename:     path.Join(namespacedString, mustGatherKey.namespace, groupString, fmt.Sprintf("%s.yaml", resourceType.Resource)),
			Content:      listAsUnstructured,
			ResourceType: resourceType,
		})
	}

	return ret, nil
}

func guessListKind(in *unstructured.Unstructured) schema.GroupVersionKind {
	return schema.GroupVersionKind{
		Group:   in.GroupVersionKind().Group,
		Version: in.GroupVersionKind().Version,
		Kind:    in.GroupVersionKind().Kind + "List",
	}
}

// end of // copied from mom repo

// copied from library-go/manifestclient
var localScheme = runtime.NewScheme()
var codecs = serializer.NewCodecFactory(localScheme)

func decodeIndividualObj(content []byte) (*unstructured.Unstructured, error) {
	obj, _, err := codecs.UniversalDecoder().Decode(content, nil, &unstructured.Unstructured{})
	if err != nil {
		return nil, fmt.Errorf("unable to decode: %w", err)
	}
	return obj.(*unstructured.Unstructured), nil
}

// TODO: i've changed json.MarshalIndent to json.Marshal
func serializeIndividualObjToJSON(obj *unstructured.Unstructured) (string, error) {
	ret, err := json.Marshal(obj.Object)
	if err != nil {
		return "", err
	}
	return string(ret), nil
}

// end of copied from library-go/manifestclient
