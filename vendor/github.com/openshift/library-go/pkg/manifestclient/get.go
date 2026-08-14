package manifestclient

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	apirequest "k8s.io/apiserver/pkg/endpoints/request"
)

// must-gather has a few different ways to store resources
// 1. cluster-scoped-resource/group/resource/<name>.yaml
// 2. cluster-scoped-resource/group/resource.yaml
// 3. namespaces/<namespace>/group/resource/<name>.yaml
// 4. namespaces/<namespace>/group/resource.yaml
// we have to choose which to prefer and we should always prefer the #2 if it's available.
// Keep in mind that to produce a cluster-scoped list of namespaced resources, you can need to navigate many namespaces.
func (mrt *manifestRoundTripper) get(requestInfo *apirequest.RequestInfo) ([]byte, error) {
	if len(requestInfo.Name) == 0 {
		return nil, fmt.Errorf("name required for GET")
	}
	if len(requestInfo.Resource) == 0 {
		return nil, fmt.Errorf("resource required for GET")
	}
	requiredAPIVersion := fmt.Sprintf("%s/%s", requestInfo.APIGroup, requestInfo.APIVersion)
	if len(requestInfo.APIGroup) == 0 {
		requiredAPIVersion = fmt.Sprintf("%s", requestInfo.APIVersion)
	}

	individualFilePath := individualGetFileLocation(requestInfo)
	individualObj, individualErr := readIndividualFile(mrt.sourceFS, individualFilePath)
	switch {
	case errors.Is(individualErr, fs.ErrNotExist):
		// try for the list
	case individualErr != nil:
		return nil, fmt.Errorf("unable to read file: %w", individualErr)
	default:
		if individualObj.GetAPIVersion() != requiredAPIVersion {
			return nil, fmt.Errorf("actual version %v does not match request %v", individualObj.GetAPIVersion(), requiredAPIVersion)
		}
		ret, err := serializeIndividualObjToJSON(individualObj)
		if err != nil {
			return nil, fmt.Errorf("failed to serialize %v: %v", individualFilePath, err)
		}
		return []byte(ret), nil
	}

	listFilePath := listGetFileLocation(requestInfo)
	listObj, listErr := readListFile(mrt.sourceFS, listFilePath)
	switch {
	case errors.Is(listErr, fs.ErrNotExist):
		// we need this to be a not-found when sent back
		return nil, newNotFound(requestInfo)

	case listErr != nil:
		return nil, fmt.Errorf("unable to read file: %w", listErr)
	default:
		obj, err := individualFromList(listObj, requestInfo.Name)
		if obj == nil {
			return nil, newNotFound(requestInfo)
		}
		if obj.GetAPIVersion() != requiredAPIVersion {
			return nil, fmt.Errorf("actual version %v does not match request %v", obj.GetAPIVersion(), requiredAPIVersion)
		}

		ret, err := serializeIndividualObjToJSON(obj)
		if err != nil {
			return nil, fmt.Errorf("failed to serialize %v: %v", listFilePath, err)
		}
		return []byte(ret), nil
	}
}

func individualGetFileLocation(requestInfo *apirequest.RequestInfo) string {
	fileParts := []string{}

	if len(requestInfo.APIGroup) == 0 &&
		requestInfo.APIVersion == "v1" &&
		requestInfo.Resource == "namespaces" &&
		len(requestInfo.Subresource) == 0 &&
		requestInfo.Namespace == requestInfo.Name { // namespaces are weird. They list their own namespace in requestInfo.namespace

		fileParts = append(fileParts, "namespaces", requestInfo.Name, requestInfo.Name+".yaml")
		return filepath.Join(fileParts...)
	}

	if len(requestInfo.Namespace) > 0 {
		fileParts = append(fileParts, "namespaces", requestInfo.Namespace)
	} else {
		fileParts = append(fileParts, "cluster-scoped-resources")
	}

	if len(requestInfo.APIGroup) > 0 {
		fileParts = append(fileParts, requestInfo.APIGroup)
	} else {
		fileParts = append(fileParts, "core")
	}

	fileParts = append(fileParts, requestInfo.Resource, fmt.Sprintf("%s.yaml", requestInfo.Name))

	return filepath.Join(fileParts...)
}

func listGetFileLocation(requestInfo *apirequest.RequestInfo) string {
	fileParts := []string{}

	if len(requestInfo.Namespace) > 0 {
		fileParts = append(fileParts, "namespaces", requestInfo.Namespace)
	} else {
		fileParts = append(fileParts, "cluster-scoped-resources")
	}

	if len(requestInfo.APIGroup) > 0 {
		fileParts = append(fileParts, requestInfo.APIGroup)
	} else {
		fileParts = append(fileParts, "core")
	}

	fileParts = append(fileParts, fmt.Sprintf("%s.yaml", requestInfo.Resource))

	return filepath.Join(fileParts...)
}
