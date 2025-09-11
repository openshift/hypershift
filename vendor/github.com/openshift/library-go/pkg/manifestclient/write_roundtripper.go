package manifestclient

import (
	"bytes"
	"fmt"
	"io"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"strings"
	"sync"

	metainternalversionscheme "k8s.io/apimachinery/pkg/apis/meta/internalversion/scheme"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/apimachinery/pkg/util/sets"
	apirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/server"
	"net/http"
	"sigs.k8s.io/yaml"
)

// Saves all mutations for later serialization and/or inspection.
// In the case of updating the same thing multiple times, all mutations are stored and it's up to the caller to decide
// what to do.
type writeTrackingRoundTripper struct {
	// requestInfoResolver is the same type constructed the same way as the kube-apiserver
	requestInfoResolver *apirequest.RequestInfoFactory

	discoveryReader *discoveryReader

	lock              sync.RWMutex
	nextRequestNumber int
	actionTracker     *AllActionsTracker[TrackedSerializedRequest]
}

func newWriteRoundTripper(discoveryRoundTripper *discoveryReader) *writeTrackingRoundTripper {
	return &writeTrackingRoundTripper{
		nextRequestNumber: 1,
		actionTracker:     &AllActionsTracker[TrackedSerializedRequest]{},
		requestInfoResolver: server.NewRequestInfoResolver(&server.Config{
			LegacyAPIGroupPrefixes: sets.NewString(server.DefaultLegacyAPIPrefix),
		}),
		discoveryReader: discoveryRoundTripper,
	}
}

func (mrt *writeTrackingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	resp := &http.Response{}

	retJSONBytes, err := mrt.roundTrip(req)
	if err != nil {
		resp.StatusCode = http.StatusInternalServerError
		resp.Status = http.StatusText(resp.StatusCode)
		resp.Body = io.NopCloser(bytes.NewBufferString(err.Error()))
		return resp, nil
	}

	resp.StatusCode = http.StatusOK
	resp.Status = http.StatusText(resp.StatusCode)
	resp.Body = io.NopCloser(bytes.NewReader(retJSONBytes))
	// We always return application/json. Avoid clients expecting proto for built-ins.
	// this may or may not work for apply.  Guess we'll find out.
	resp.Header = make(http.Header)
	resp.Header.Set("Content-Type", "application/json")

	return resp, nil
}

func (mrt *writeTrackingRoundTripper) roundTrip(req *http.Request) ([]byte, error) {
	requestInfo, err := mrt.requestInfoResolver.NewRequestInfo(req)
	if err != nil {
		return nil, fmt.Errorf("failed reading requestInfo: %w", err)
	}

	if !requestInfo.IsResourceRequest {
		return nil, fmt.Errorf("non-resource requests are not supported by this implementation")
	}
	if len(requestInfo.Subresource) != 0 && requestInfo.Subresource != "status" {
		return nil, fmt.Errorf("subresource %v is not supported by this implementation", requestInfo.Subresource)
	}

	patchType := ""
	var action Action
	switch {
	case requestInfo.Verb == "create" && len(requestInfo.Subresource) == 0:
		action = ActionCreate
	case requestInfo.Verb == "update" && len(requestInfo.Subresource) == 0:
		action = ActionUpdate
	case requestInfo.Verb == "update" && requestInfo.Subresource == "status":
		action = ActionUpdateStatus
	case requestInfo.Verb == "patch" && req.Header.Get("Content-Type") == string(types.ApplyPatchType) && len(requestInfo.Subresource) == 0:
		action = ActionApply
	case requestInfo.Verb == "patch" && req.Header.Get("Content-Type") == string(types.ApplyPatchType) && requestInfo.Subresource == "status":
		action = ActionApplyStatus
	case requestInfo.Verb == "patch" && len(requestInfo.Subresource) == 0:
		action = ActionPatch
		patchType = req.Header.Get("Content-Type")
	case requestInfo.Verb == "patch" && requestInfo.Subresource == "status":
		action = ActionPatchStatus
		patchType = req.Header.Get("Content-Type")
	case requestInfo.Verb == "delete" && len(requestInfo.Subresource) == 0:
		action = ActionDelete
	default:
		return nil, fmt.Errorf("verb %v is not supported by this implementation", requestInfo.Verb)
	}

	var opts runtime.Object
	switch action {
	case ActionPatch, ActionPatchStatus:
		opts = &metav1.PatchOptions{}
	case ActionApply, ActionApplyStatus:
		opts = &metav1.PatchOptions{}
	case ActionUpdate, ActionUpdateStatus:
		opts = &metav1.UpdateOptions{}
	case ActionCreate:
		opts = &metav1.CreateOptions{}
	case ActionDelete:
		opts = &metav1.DeleteOptions{}
	}
	if err := metainternalversionscheme.ParameterCodec.DecodeParameters(req.URL.Query(), metav1.SchemeGroupVersion, opts); err != nil {
		return nil, fmt.Errorf("unable to parse query parameters: %w", err)
	}

	optionsBytes, err := yaml.Marshal(opts)
	if err != nil {
		return nil, fmt.Errorf("unable to encode options: %w", err)
	}
	if strings.TrimSpace(string(optionsBytes)) == "{}" {
		optionsBytes = nil
	}

	bodyContent := []byte{}
	if req.Body != nil {
		bodyContent, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read body: %w", err)
		}
	}

	var bodyObj runtime.Object
	actionHasRuntimeObjectBody := action != ActionPatch && action != ActionPatchStatus
	if actionHasRuntimeObjectBody {
		bodyObj, err = runtime.Decode(unstructured.UnstructuredJSONScheme, bodyContent)
		if err != nil {
			return nil, fmt.Errorf("unable to decode body: %w", err)
		}
		if requestInfo.Namespace != bodyObj.(*unstructured.Unstructured).GetNamespace() {
			return nil, fmt.Errorf("request namespace %q does not equal body namespace %q", requestInfo.Namespace, bodyObj.(*unstructured.Unstructured).GetNamespace())
		}
		if action != ActionCreate && action != ActionDelete && requestInfo.Name != bodyObj.(*unstructured.Unstructured).GetName() {
			return nil, fmt.Errorf("request name %q does not equal body name %q", requestInfo.Namespace, bodyObj.(*unstructured.Unstructured).GetNamespace())
		}
	}

	gvr := schema.GroupVersionResource{
		Group:    requestInfo.APIGroup,
		Version:  requestInfo.APIVersion,
		Resource: requestInfo.Resource,
	}
	metadataName := requestInfo.Name
	if action == ActionCreate {
		// in this case, the name isn't in the URL, it's in the body
		metadataName = bodyObj.(*unstructured.Unstructured).GetName()
	}

	bodyOutputBytes := bodyContent
	generatedName := ""
	kindType := schema.GroupVersionKind{}
	if actionHasRuntimeObjectBody {
		bodyOutputBytes, err = yaml.Marshal(bodyObj.(*unstructured.Unstructured).Object)
		if err != nil {
			return nil, fmt.Errorf("unable to encode body: %w", err)
		}
		generatedName = bodyObj.(*unstructured.Unstructured).GetGenerateName()
		kindType = bodyObj.GetObjectKind().GroupVersionKind()
	} else if (action == ActionPatch || action == ActionPatchStatus) && patchType == string(types.JSONPatchType) {
		// the following code gives nice formatting for
		// JSON patches that will be stored in files.
		var jsonPatchOperations []map[string]interface{}
		err = json.Unmarshal(bodyOutputBytes, &jsonPatchOperations)
		if err != nil {
			return nil, fmt.Errorf("unable to decode JSONPatch body: %w", err)
		}
		bodyOutputBytes, err = yaml.Marshal(jsonPatchOperations)
		if err != nil {
			return nil, fmt.Errorf("unable to encode JSONPatch body: %w", err)
		}
	}

	fieldManagerName := ""
	if patchOptions, ok := opts.(*metav1.PatchOptions); ok {
		fieldManagerName = patchOptions.FieldManager
	}

	serializedRequest := SerializedRequest{
		ActionMetadata: ActionMetadata{
			Action: action,
			ResourceMetadata: ResourceMetadata{
				ResourceType: gvr,
				Namespace:    requestInfo.Namespace,
				Name:         metadataName,
				GenerateName: generatedName,
			},
			PatchType:              patchType,
			FieldManager:           fieldManagerName,
			ControllerInstanceName: ControllerInstanceNameFromContext(req.Context()),
		},
		KindType: kindType,
		Options:  optionsBytes,
		Body:     bodyOutputBytes,
	}

	// this lock also protects the access to actionTracker
	mrt.lock.Lock()
	defer mrt.lock.Unlock()
	trackedRequest := TrackedSerializedRequest{
		RequestNumber:     mrt.nextRequestNumber,
		SerializedRequest: serializedRequest,
	}
	mrt.nextRequestNumber++

	mrt.actionTracker.AddRequest(trackedRequest)

	// returning a value that will probably not cause the wrapping client to fail, but isn't very useful.
	// this keeps calling code from depending on the return value.
	ret := &unstructured.Unstructured{Object: map[string]interface{}{}}
	ret.SetName(serializedRequest.ActionMetadata.Name)
	ret.SetNamespace(serializedRequest.ActionMetadata.Namespace)
	if actionHasRuntimeObjectBody {
		ret.SetGroupVersionKind(bodyObj.GetObjectKind().GroupVersionKind())
	} else {
		kindForResource, err := mrt.discoveryReader.getKindForResource(gvr)
		if err != nil {
			return nil, err
		}
		ret.SetGroupVersionKind(kindForResource.kind)
	}
	retBytes, err := json.Marshal(ret.Object)
	if err != nil {
		return nil, fmt.Errorf("unable to encode body: %w", err)
	}
	return retBytes, nil
}

func (mrt *writeTrackingRoundTripper) GetMutations() *AllActionsTracker[TrackedSerializedRequest] {
	mrt.lock.Lock()
	defer mrt.lock.Unlock()

	return mrt.actionTracker.DeepCopy()
}

func setAnnotationFor(obj *unstructured.Unstructured, key, value string) {
	if obj == nil {
		return
	}

	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[key] = value
	obj.SetAnnotations(annotations)
}
