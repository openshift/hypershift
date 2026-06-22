package manifestclient

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	apirequest "k8s.io/apiserver/pkg/endpoints/request"
)

// must-gather has a few different ways to store resources
// 1. cluster-scoped-resource/group/resource/<name>.yaml
// 2. cluster-scoped-resource/group/resource.yaml
// 3. namespaces/<namespace>/group/resource/<name>.yaml
// 4. namespaces/<namespace>/group/resource.yaml
// we have to choose which to prefer and we should always prefer the #2 if it's available.
// Keep in mind that to produce a cluster-scoped list of namespaced resources, you can need to navigate many namespaces.
func (mrt *manifestRoundTripper) list(requestInfo *apirequest.RequestInfo) ([]byte, error) {
	// TODO post-filter for label selectors
	return mrt.listAll(requestInfo)
}

func (mrt *manifestRoundTripper) listAll(requestInfo *apirequest.RequestInfo) ([]byte, error) {
	var retList *unstructured.UnstructuredList

	// namespaces are special.
	if len(requestInfo.APIGroup) == 0 &&
		requestInfo.APIVersion == "v1" &&
		requestInfo.Resource == "namespaces" &&
		len(requestInfo.Subresource) == 0 {

		return mrt.listAllNamespaces()
	}

	gvr := schema.GroupVersionResource{
		Group:    requestInfo.APIGroup,
		Version:  requestInfo.APIVersion,
		Resource: requestInfo.Resource,
	}

	kind, err := mrt.discoveryReader.getKindForResource(gvr)
	if err != nil {
		return nil, fmt.Errorf("unable to determine list kind: %w", err)
	}
	possibleListFiles, err := allPossibleListFileLocations(mrt.sourceFS, requestInfo)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// continue to see if something else is present to return
	case err != nil:
		return nil, fmt.Errorf("unable to determine list file locations: %w", err)
	}
	for _, listFile := range possibleListFiles {
		currList, err := readListFile(mrt.sourceFS, listFile)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			// do nothing, it's possible, not guaranteed
			continue
		case err != nil:
			return nil, fmt.Errorf("unable to determine read list file %v: %w", listFile, err)
		}

		if retList == nil {
			retList = currList
			continue
		}
		for i := range currList.Items {
			retList.Items = append(retList.Items, currList.Items[i])
		}
	}
	if retList != nil {
		if retList.GroupVersionKind() != kind.listKind {
			return nil, fmt.Errorf("inconsistent list kind: got %v, expected %v", retList.GroupVersionKind(), kind.listKind)
		}
		retList, err := filterByLabelSelector(retList, requestInfo.LabelSelector)
		if err != nil {
			return nil, fmt.Errorf("failed to filter by labelSelector %s: %w", requestInfo.LabelSelector, err)
		}
		ret, err := serializeListObjToJSON(retList)
		if err != nil {
			return nil, fmt.Errorf("failed to serialize: %v", err)
		}
		return []byte(ret), nil
	}

	retList = &unstructured.UnstructuredList{
		Object: map[string]interface{}{},
		Items:  nil,
	}
	retList.SetGroupVersionKind(kind.listKind)
	individualFiles, err := allIndividualFileLocations(mrt.sourceFS, requestInfo)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// continue to see if something else is present to return
	case err != nil:
		return nil, fmt.Errorf("unable to determine individual file locations: %w", err)
	}
	for _, individualFile := range individualFiles {
		currInstance, err := readIndividualFile(mrt.sourceFS, individualFile)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			// do nothing, it's possible, not guaranteed
			continue
		case err != nil:
			return nil, fmt.Errorf("unable to determine read list file %v: %w", individualFile, err)
		}

		retList.Items = append(retList.Items, *currInstance)
	}
	if len(retList.Items) > 0 {
		if retList.Items[0].GroupVersionKind() != kind.kind {
			return nil, fmt.Errorf("inconsistent item kind: got %v, expected %v", retList.Items[0].GroupVersionKind(), kind.kind)
		}
		retList, err := filterByLabelSelector(retList, requestInfo.LabelSelector)
		if err != nil {
			return nil, fmt.Errorf("failed to filter by labelSelector %s: %w", requestInfo.LabelSelector, err)
		}
		ret, err := serializeListObjToJSON(retList)
		if err != nil {
			return nil, fmt.Errorf("failed to serialize: %v", err)
		}
		return []byte(ret), nil
	}

	// if we get here, there is no list file and no individual files.
	// the namespace must exist or we would have returned long ago. Return an empty list.
	ret, err := serializeListObjToJSON(retList)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize: %v", err)
	}

	return []byte(ret), nil
}

func (mrt *manifestRoundTripper) listAllNamespaces() ([]byte, error) {
	possibleNamespaceFiles, err := allPossibleNamespaceFiles(mrt.sourceFS)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, fmt.Errorf("unable to determine list file alternative individual files: %w", err)
	}

	namespaces := []unstructured.Unstructured{}
	for _, individualFile := range possibleNamespaceFiles {
		currNamespace, err := readIndividualFile(mrt.sourceFS, individualFile)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			// do nothing, it's possible, not guaranteed
			continue
		case err != nil:
			return nil, fmt.Errorf("unable to determine read namespace individual file %v: %w", individualFile, err)
		}
		namespaces = append(namespaces, *currNamespace)

	}

	retList := &unstructured.UnstructuredList{
		Object: map[string]interface{}{},
		Items:  namespaces,
	}
	retList.SetKind("NamespaceList")
	retList.SetAPIVersion("v1")

	ret, err := serializeListObjToJSON(retList)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize: %v", err)
	}

	return []byte(ret), nil
}

func allIndividualFileLocations(sourceFS fs.FS, requestInfo *apirequest.RequestInfo) ([]string, error) {
	resourceDirectoryParts := []string{}
	if len(requestInfo.APIGroup) > 0 {
		resourceDirectoryParts = append(resourceDirectoryParts, requestInfo.APIGroup)
	} else {
		resourceDirectoryParts = append(resourceDirectoryParts, "core")
	}
	resourceDirectoryParts = append(resourceDirectoryParts, requestInfo.Resource)

	resourceDirectoriesToCheckForIndividualFiles := []string{}
	if len(requestInfo.Namespace) > 0 {
		parts := append([]string{"namespaces", requestInfo.Namespace}, resourceDirectoryParts...)
		resourceDirectoriesToCheckForIndividualFiles = append(resourceDirectoriesToCheckForIndividualFiles, filepath.Join(parts...))

	} else {
		clusterParts := append([]string{"cluster-scoped-resources"}, resourceDirectoryParts...)
		resourceDirectoriesToCheckForIndividualFiles = append(resourceDirectoriesToCheckForIndividualFiles, filepath.Join(clusterParts...))

		namespaces, err := allNamespacesWithData(sourceFS)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			// do nothing and continue
		case err != nil:
			return nil, fmt.Errorf("unable to read namespaces: %w", err)
		}
		for _, ns := range namespaces {
			nsParts := append([]string{"namespaces", ns}, resourceDirectoryParts...)
			resourceDirectoriesToCheckForIndividualFiles = append(resourceDirectoriesToCheckForIndividualFiles, filepath.Join(nsParts...))
		}
	}

	allIndividualFilePaths := []string{}
	for _, resourceDirectory := range resourceDirectoriesToCheckForIndividualFiles {
		individualFiles, err := fs.ReadDir(sourceFS, resourceDirectory)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			return nil, fmt.Errorf("unable to read resourceDir")
		}

		for _, curr := range individualFiles {
			allIndividualFilePaths = append(allIndividualFilePaths, filepath.Join(resourceDirectory, curr.Name()))
		}
	}

	return allIndividualFilePaths, nil
}

func allPossibleListFileLocations(sourceFS fs.FS, requestInfo *apirequest.RequestInfo) ([]string, error) {
	resourceListFileParts := []string{}
	if len(requestInfo.APIGroup) > 0 {
		resourceListFileParts = append(resourceListFileParts, requestInfo.APIGroup)
	} else {
		resourceListFileParts = append(resourceListFileParts, "core")
	}
	resourceListFileParts = append(resourceListFileParts, fmt.Sprintf("%s.yaml", requestInfo.Resource))

	allPossibleListFileLocations := []string{}
	if len(requestInfo.Namespace) > 0 {
		parts := append([]string{"namespaces", requestInfo.Namespace}, resourceListFileParts...)
		allPossibleListFileLocations = append(allPossibleListFileLocations, filepath.Join(parts...))

	} else {
		clusterParts := append([]string{"cluster-scoped-resources"}, resourceListFileParts...)
		allPossibleListFileLocations = append(allPossibleListFileLocations, filepath.Join(clusterParts...))

		namespaces, err := allNamespacesWithData(sourceFS)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return allPossibleListFileLocations, nil
		case err != nil:
			return nil, fmt.Errorf("unable to read namespaces: %w", err)
		}
		for _, ns := range namespaces {
			nsParts := append([]string{"namespaces", ns}, resourceListFileParts...)
			allPossibleListFileLocations = append(allPossibleListFileLocations, filepath.Join(nsParts...))
		}
	}

	return allPossibleListFileLocations, nil
}

func allNamespacesWithData(sourceFS fs.FS) ([]string, error) {
	nsDirs, err := fs.ReadDir(sourceFS, "namespaces")
	if err != nil {
		return nil, fmt.Errorf("failed to read allNamespacesWithData: %w", err)
	}

	ret := []string{}
	for _, curr := range nsDirs {
		ret = append(ret, curr.Name())
	}

	return ret, nil
}

func allPossibleNamespaceFiles(sourceFS fs.FS) ([]string, error) {
	allPossibleListFileLocations := []string{}
	namespaces, err := allNamespacesWithData(sourceFS)
	if err != nil {
		return nil, fmt.Errorf("unable to read namespaces: %w", err)
	}

	for _, namespace := range namespaces {
		allPossibleListFileLocations = append(allPossibleListFileLocations, filepath.Join("namespaces", namespace, namespace+".yaml"))
	}

	return allPossibleListFileLocations, nil
}

func filterByLabelSelector(list *unstructured.UnstructuredList, labelSelector string) (*unstructured.UnstructuredList, error) {
	if labelSelector == "" {
		return list, nil
	}

	parsedSelector, err := labels.Parse(labelSelector)
	if err != nil {
		return nil, err
	}

	var filteredItems []unstructured.Unstructured
	for _, item := range list.Items {
		if parsedSelector.Matches(labels.Set(item.GetLabels())) {
			filteredItems = append(filteredItems, item)
		}
	}

	return &unstructured.UnstructuredList{
		Object: list.Object,
		Items:  filteredItems,
	}, nil
}
