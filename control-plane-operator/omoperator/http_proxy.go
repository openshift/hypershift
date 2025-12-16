package omoperator

import (
	"context"
	stdjson "encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metainternalversionscheme "k8s.io/apimachinery/pkg/apis/meta/internalversion/scheme"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utiljson "k8s.io/apimachinery/pkg/util/json"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/watch"
	apirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/server/httplog"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
)

const omStandaloneNamespaceAnnotationKey = "synthetic.om.openshift.io/standalone-namespace"

type server struct {
	requestInfoResolver *apirequest.RequestInfoFactory

	// in prod we need a client per operator
	// reverseProxy could be used by a one that
	// doesn't terminate TLS
	//
	// instead of a client maybe we could use
	// httputil.ReverseProxy
	managementClusterClient dynamic.Interface

	hostedControlPlaneNamespace string
}

func newServer(requestInfoResolver *apirequest.RequestInfoFactory, managementClusterConfig *rest.Config, hostedControlPlaneNamespace string) (*server, error) {
	managementClusterClient, err := dynamic.NewForConfig(managementClusterConfig)
	if err != nil {
		return nil, err
	}
	return &server{
		requestInfoResolver:         requestInfoResolver,
		managementClusterClient:     managementClusterClient,
		hostedControlPlaneNamespace: hostedControlPlaneNamespace,
	}, nil
}

func (s *server) Start(ctx context.Context) error {
	addr := ":8084"
	klog.Infof("Starting Openshift Manager HTTP Proxy server. Listening on: %v", addr)
	defer klog.Infof("Shutting down Openshift Manager HTTP Proxy server.")

	return http.ListenAndServe(addr, httplog.WithLogging(s, httplog.DefaultStacktracePred))
}

// works only with unstructured data
// TODO: client auth
// TODO: use httputil.ReverseProxy ?
func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestInfo, err := s.requestInfoResolver.NewRequestInfo(r)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed reading requestInfo: %v", err), http.StatusBadRequest)
	}

	switch requestInfoToResourceID(requestInfo) {
	case newResourceID("", "v1", "configmaps", "openshift-authentication", "foo"):
		switch requestInfo.Verb {
		case "update":
			opts, err := prepareUpdateOptions(r)
			if err != nil {
				http.Error(w, fmt.Sprintf("failed to prepare options: %v", err), http.StatusBadRequest)
				return
			}
			obj, err := prepareUnstructuredFromBodyWithErrHandling(w, r, s.hostedControlPlaneNamespace)
			if err != nil {
				return
			}
			updatedObj, err := s.managementClusterClient.Resource(coreConfigMapGVR).Namespace(s.hostedControlPlaneNamespace).Update(r.Context(), obj, opts)
			if err != nil {
				http.Error(w, fmt.Sprintf("failed to update resource: %v", err), http.StatusInternalServerError)
				return
			}
			err = finalizeResponseWithErrHandling(w, updatedObj)
		default:
			http.Error(w, fmt.Sprintf("unknown verb: %q", requestInfo.Verb), http.StatusBadRequest)
		}
	case newResourceID("", "v1", "configmaps", "openshift-authentication", ""):
		switch requestInfo.Verb {
		case "create":
			opts, err := prepareCreateOptions(r)
			if err != nil {
				http.Error(w, fmt.Sprintf("failed to prepare create options: %v", err), http.StatusBadRequest)
				return
			}
			defer r.Body.Close()
			data, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, fmt.Sprintf("failed to read body: %v", err), http.StatusBadRequest)
				return
			}
			rawObj, _, err := unstructured.UnstructuredJSONScheme.Decode(data, nil, nil)
			if err != nil {
				http.Error(w, fmt.Sprintf("failed to decode request body: %v", err), http.StatusBadRequest)
				return
			}
			obj := rawObj.(*unstructured.Unstructured)

			addWellKnownAnnotationsFor(obj)
			objNewName := hcpNameForNamespacedStandaloneResource(obj.GetNamespace(), obj.GetName())
			obj.SetName(objNewName)
			obj.SetNamespace(s.hostedControlPlaneNamespace)

			createdObj, err := s.managementClusterClient.Resource(coreConfigMapGVR).Namespace(s.hostedControlPlaneNamespace).Create(r.Context(), obj, opts)
			if err != nil {
				http.Error(w, fmt.Sprintf("failed to create resource: %v", err), http.StatusInternalServerError)
				return
			}
			standaloneNamespace, standaloneName, err := parseHCPNameForNamespacedStandaloneResource(createdObj.GetName())
			if err != nil {
				http.Error(w, fmt.Sprintf("failed to parse standalone resource name: %v", err), http.StatusInternalServerError)
				return
			}
			createdObj.SetName(standaloneName)
			createdObj.SetNamespace(standaloneNamespace)
			if err = writeJSON(w, createdObj); err != nil {
				http.Error(w, fmt.Sprintf("failed to write response: %v", err), http.StatusInternalServerError)
				return
			}
		case "list":
			opts, err := prepareListOptions(r)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			unstructuredRsp, err := s.managementClusterClient.Resource(coreConfigMapGVR).Namespace(s.hostedControlPlaneNamespace).List(r.Context(), opts)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
			var filteredItems []unstructured.Unstructured
			for _, item := range unstructuredRsp.Items {
				if item.GetAnnotations()[omStandaloneNamespaceAnnotationKey] == requestInfo.Namespace {
					standaloneNamespace, standaloneName, err := parseHCPNameForNamespacedStandaloneResource(item.GetName())
					if err != nil {
						http.Error(w, fmt.Sprintf("failed to parse standalone resource name: %v", err), http.StatusInternalServerError)
						return
					}
					item.SetName(standaloneName)
					item.SetNamespace(standaloneNamespace)
					filteredItems = append(filteredItems, item)
				}
			}
			unstructuredRsp.Items = filteredItems
			if err = writeJSON(w, unstructuredRsp); err != nil {
				klog.Errorf("Failed to write response: %v", err)
			}
		case "watch":
			opts, err := prepareListOptions(r)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			watcher, err := s.managementClusterClient.Resource(coreConfigMapGVR).Namespace(s.hostedControlPlaneNamespace).Watch(r.Context(), opts)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			defer watcher.Stop()
			w.Header().Set("Content-Type", "application/json")

			enc := utiljson.NewEncoder(w)
			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "streaming unsupported by server", http.StatusInternalServerError)
				return
			}

			w.WriteHeader(http.StatusOK)
			flusher.Flush()

			for {
				select {
				case <-r.Context().Done():
					return
				case ev, ok := <-watcher.ResultChan():
					if !ok {
						return
					}
					unstructuredObj, ok := ev.Object.(*unstructured.Unstructured)
					if !ok {
						klog.Errorf("WATCH: received unexpected type: %T, obj: %v", ev.Object, ev.Object)
						continue
					}
					if unstructuredObj.GetAnnotations()[omStandaloneNamespaceAnnotationKey] != requestInfo.Namespace {
						continue
					}

					standaloneNamespace, standaloneName, err := parseHCPNameForNamespacedStandaloneResource(unstructuredObj.GetName())
					if err != nil {
						http.Error(w, fmt.Sprintf("failed to parse standalone resource name: %v", err), http.StatusInternalServerError)
						return
					}
					unstructuredObj.SetName(standaloneName)
					unstructuredObj.SetNamespace(standaloneNamespace)

					ev.Object = unstructuredObj
					if err := encodeWatchEvent(enc, ev); err != nil {
						klog.Errorf("Failed to write WATCH response: %v", err)
						return
					}
					flusher.Flush()
				}
			}
		default:
			http.Error(w, fmt.Sprintf("invalid request method/verb: %s", requestInfo.Verb), http.StatusBadRequest)
		}
	default:
		http.Error(w, fmt.Sprintf("unknown resource id: %v", requestInfoToResourceID(requestInfo)), http.StatusBadRequest)
	}
}

type resourceID struct {
	Group     string
	Version   string
	Resource  string
	Namespace string
	Name      string
}

func requestInfoToResourceID(requestInfo *apirequest.RequestInfo) resourceID {
	return resourceID{
		Group:     requestInfo.APIGroup,
		Version:   requestInfo.APIVersion,
		Resource:  requestInfo.Resource,
		Namespace: requestInfo.Namespace,
		Name:      requestInfo.Name,
	}
}

func newResourceID(group, version, resource, namespace, name string) resourceID {
	return resourceID{
		Group:     group,
		Version:   version,
		Resource:  resource,
		Namespace: namespace,
		Name:      name,
	}
}

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(metainternalversion.AddToScheme(scheme))
}

func prepareListOptions(r *http.Request) (metav1.ListOptions, error) {
	var internal metainternalversion.ListOptions
	if err := metainternalversionscheme.ParameterCodec.DecodeParameters(r.URL.Query(), metav1.SchemeGroupVersion, &internal); err != nil {
		return metav1.ListOptions{}, err
	}

	var listOptions metav1.ListOptions
	if err := scheme.Convert(&internal, &listOptions, nil); err != nil {
		return metav1.ListOptions{}, err
	}

	return listOptions, nil
}

func prepareCreateOptions(r *http.Request) (metav1.CreateOptions, error) {
	options := metav1.CreateOptions{}
	if err := metainternalversionscheme.ParameterCodec.DecodeParameters(r.URL.Query(), metav1.SchemeGroupVersion, &options); err != nil {
		return metav1.CreateOptions{}, err
	}
	return options, nil
}

func prepareUpdateOptions(r *http.Request) (metav1.UpdateOptions, error) {
	options := metav1.UpdateOptions{}
	if err := metainternalversionscheme.ParameterCodec.DecodeParameters(r.URL.Query(), metav1.SchemeGroupVersion, &options); err != nil {
		return metav1.UpdateOptions{}, err
	}
	return options, nil
}

func writeJSON(w http.ResponseWriter, obj runtime.Object) error {
	w.Header().Set("Content-Type", "application/json")
	return utiljson.NewEncoder(w).Encode(obj)
}

func addWellKnownAnnotationsFor(obj *unstructured.Unstructured) {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}

	annotations[omStandaloneNamespaceAnnotationKey] = obj.GetNamespace()

	obj.SetAnnotations(annotations)
}

func parseHCPNameForNamespacedStandaloneResource(encodedHCPName string) (namespace, name string, err error) {
	parts := strings.SplitN(encodedHCPName, "--", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid encoded name format: %q", encodedHCPName)
	}
	return parts[0], parts[1], nil
}

func prepareUnstructuredFromBodyWithErrHandling(w http.ResponseWriter, r *http.Request, hostedControlPlaneNamespace string) (*unstructured.Unstructured, error) {
	defer r.Body.Close()

	data, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to read body: %v", err), http.StatusBadRequest)
		return nil, err
	}

	rawObj, _, err := unstructured.UnstructuredJSONScheme.Decode(data, nil, nil)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to decode request body: %v", err), http.StatusBadRequest)
		return nil, err
	}

	obj := rawObj.(*unstructured.Unstructured)

	addWellKnownAnnotationsFor(obj)

	objNewName := hcpNameForNamespacedStandaloneResource(obj.GetNamespace(), obj.GetName())
	obj.SetName(objNewName)
	obj.SetNamespace(hostedControlPlaneNamespace)

	return obj, nil
}

func finalizeResponseWithErrHandling(w http.ResponseWriter, obj *unstructured.Unstructured) error {
	standaloneNamespace, standaloneName, err := parseHCPNameForNamespacedStandaloneResource(obj.GetName())
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to parse standalone resource name: %v", err), http.StatusInternalServerError)
		return err
	}

	obj.SetName(standaloneName)
	obj.SetNamespace(standaloneNamespace)

	if err = writeJSON(w, obj); err != nil {
		http.Error(w, fmt.Sprintf("failed to write response: %v", err), http.StatusInternalServerError)
		return err
	}

	return nil
}

func encodeWatchEvent(enc *stdjson.Encoder, ev watch.Event) error {
	raw, err := utiljson.Marshal(ev.Object)
	if err != nil {
		return err
	}

	watchEvent := &metav1.WatchEvent{
		Type: string(ev.Type),
		Object: runtime.RawExtension{
			Raw:    raw,
			Object: ev.Object,
		},
	}

	return enc.Encode(watchEvent)
}
