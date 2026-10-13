package libraryinputresources

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"

	"github.com/PaesslerAG/gval"
	"github.com/PaesslerAG/jsonpath"
	"github.com/openshift/library-go/pkg/manifestclient"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

func WriteRequiredInputResourcesFromMustGather(ctx context.Context, inputResources *InputResources, mustGatherDir, targetDir string) error {
	actualResources, err := GetRequiredInputResourcesFromMustGather(ctx, inputResources, mustGatherDir)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("unable to create %q: %w", targetDir, err)
	}

	errs := []error{}
	for _, currResource := range actualResources {
		if err := WriteResource(currResource, targetDir); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func GetRequiredInputResourcesFromMustGather(ctx context.Context, inputResources *InputResources, mustGatherDir string) ([]*Resource, error) {
	dynamicClient, err := NewDynamicClientFromMustGather(mustGatherDir)
	if err != nil {
		return nil, err
	}

	pertinentUnstructureds, err := GetRequiredInputResourcesForResourceList(ctx, inputResources.ApplyConfigurationResources, dynamicClient)
	if err != nil {
		return nil, err
	}

	return unstructuredToMustGatherFormat(pertinentUnstructureds)
}

func NewDynamicClientFromMustGather(mustGatherDir string) (dynamic.Interface, error) {
	httpClient := newHTTPClientFromMustGather(mustGatherDir)
	dynamicClient, err := dynamic.NewForConfigAndClient(&rest.Config{}, httpClient)
	if err != nil {
		return nil, fmt.Errorf("failure creating dynamicClient for NewDynamicClientFromMustGather: %w", err)
	}
	return dynamicClient, nil
}

func NewDiscoveryClientFromMustGather(mustGatherDir string) (discovery.AggregatedDiscoveryInterface, error) {
	httpClient := newHTTPClientFromMustGather(mustGatherDir)
	discoveryClient, err := discovery.NewDiscoveryClientForConfigAndClient(manifestclient.RecommendedRESTConfig(), httpClient)
	if err != nil {
		return nil, fmt.Errorf("failure creating discoveryClient for NewDiscoveryClientFromMustGather: %w", err)
	}
	return discoveryClient, nil
}

func newHTTPClientFromMustGather(mustGatherDir string) *http.Client {
	roundTripper := manifestclient.NewRoundTripper(mustGatherDir)
	return &http.Client{
		Transport: roundTripper,
	}
}

var builder = gval.Full(jsonpath.Language())

func GetRequiredInputResourcesForResourceList(ctx context.Context, resourceList ResourceList, dynamicClient dynamic.Interface) ([]*Resource, error) {
	instances := NewUniqueResourceSet()
	errs := []error{}

	for _, currResource := range resourceList.ExactResources {
		resourceInstance, err := getExactResource(ctx, dynamicClient, currResource)
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		instances.Insert(resourceInstance)
	}

	for _, currResource := range resourceList.LabelSelectedResources {
		resourceList, err := getResourcesByLabelSelector(ctx, dynamicClient, currResource)
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		instances.Insert(resourceList...)
	}

	path := field.NewPath(".")
	for i, currResourceRef := range resourceList.ResourceReferences {
		currFieldPath := path.Child("resourceReference").Index(i)

		referringResourceInstance, err := getExactResource(ctx, dynamicClient, currResourceRef.ReferringResource)
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("failed reading referringResource [%v] %#v: %w", currFieldPath, currResourceRef.ReferringResource, err))
			continue
		}
		instances.Insert(referringResourceInstance)

		switch {
		case currResourceRef.ImplicitNamespacedReference != nil:
			fieldPathEvaluator, err := builder.NewEvaluable(currResourceRef.ImplicitNamespacedReference.NameJSONPath)
			if err != nil {
				errs = append(errs, fmt.Errorf("error parsing [%v]: %q: %w", currFieldPath, currResourceRef.ImplicitNamespacedReference.NameJSONPath, err))
				continue
			}

			results, err := fieldPathEvaluator(ctx, referringResourceInstance.Content.UnstructuredContent())
			if err != nil {
				errs = append(errs, fmt.Errorf("unexpected error finding value for %v from %v with jsonPath: %w", currFieldPath, "TODO", err))
				continue
			}

			var resultStrings []string
			switch cast := results.(type) {
			case string:
				resultStrings = []string{cast}
			case []string:
				resultStrings = cast
			case []interface{}:
				for _, curr := range cast {
					resultStrings = append(resultStrings, fmt.Sprintf("%v", curr))
				}
			default:
				errs = append(errs, fmt.Errorf("[%v] unexpected error type %T for %#v", currFieldPath, results, results))
			}

			for _, targetResourceName := range resultStrings {
				targetRef := ExactResourceID{
					InputResourceTypeIdentifier: currResourceRef.ImplicitNamespacedReference.InputResourceTypeIdentifier,
					Namespace:                   currResourceRef.ImplicitNamespacedReference.Namespace,
					Name:                        targetResourceName,
				}

				resourceInstance, err := getExactResource(ctx, dynamicClient, targetRef)
				if apierrors.IsNotFound(err) {
					continue
				}
				if err != nil {
					errs = append(errs, err)
					continue
				}

				instances.Insert(resourceInstance)
			}
		}
	}

	return instances.List(), errors.Join(errs...)
}

func getExactResource(ctx context.Context, dynamicClient dynamic.Interface, resourceReference ExactResourceID) (*Resource, error) {
	gvr := schema.GroupVersionResource{Group: resourceReference.Group, Version: resourceReference.Version, Resource: resourceReference.Resource}
	unstructuredInstance, err := dynamicClient.Resource(gvr).Namespace(resourceReference.Namespace).Get(ctx, resourceReference.Name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed getting %v: %w", IdentifierForExactResourceRef(&resourceReference), err)
	}

	resourceInstance := &Resource{
		ResourceType: gvr,
		Content:      unstructuredInstance,
	}
	return resourceInstance, nil
}

func getResourcesByLabelSelector(ctx context.Context, dynamicClient dynamic.Interface, labelSelectedResource LabelSelectedResource) ([]*Resource, error) {
	gvr := schema.GroupVersionResource{
		Group:    labelSelectedResource.Group,
		Version:  labelSelectedResource.Version,
		Resource: labelSelectedResource.Resource,
	}

	selector, err := metav1.LabelSelectorAsSelector(&labelSelectedResource.LabelSelector)
	if err != nil {
		return nil, err
	}

	namespace := labelSelectedResource.Namespace
	if namespace == "" {
		namespace = metav1.NamespaceAll
	}

	unstructuredList, err := dynamicClient.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector.String()})
	if err != nil {
		return nil, fmt.Errorf("failed getting list of resources with labelSelector %q: %w", selector, err)
	}

	var resources []*Resource
	for _, item := range unstructuredList.Items {
		resourceInstance := &Resource{
			ResourceType: gvr,
			Content:      &item,
		}
		resources = append(resources, resourceInstance)
	}

	return resources, nil
}

func IdentifierForExactResourceRef(resourceReference *ExactResourceID) string {
	return fmt.Sprintf("%s.%s.%s/%s[%s]", resourceReference.Resource, resourceReference.Version, resourceReference.Group, resourceReference.Name, resourceReference.Namespace)
}

func unstructuredToMustGatherFormat(in []*Resource) ([]*Resource, error) {
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

	ret := []*Resource{}
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
		ret = append(ret, &Resource{
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
