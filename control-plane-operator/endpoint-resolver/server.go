package endpointresolver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	corev1listers "k8s.io/client-go/listers/core/v1"
)

// PodEndpoint represents a resolved pod endpoint with its name and IP address.
type PodEndpoint struct {
	Name string `json:"name"`
	IP   string `json:"ip"`
}

// ResolveResponse is the JSON response returned by the resolve endpoint.
type ResolveResponse struct {
	Pods []PodEndpoint `json:"pods"`
}

// ResolverHandler handles endpoint resolution requests by looking up Pods
// using the hypershift.openshift.io/control-plane-component label.
type ResolverHandler struct {
	podLister corev1listers.PodNamespaceLister
}

// NewResolverHandler creates a new ResolverHandler.
func NewResolverHandler(podLister corev1listers.PodNamespaceLister) *ResolverHandler {
	return &ResolverHandler{
		podLister: podLister,
	}
}

func (h *ResolverHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract component name from /resolve/<component-name>
	path := strings.TrimPrefix(r.URL.Path, "/resolve/")
	componentName := strings.TrimSuffix(path, "/")
	if componentName == "" {
		http.Error(w, "component name is required", http.StatusBadRequest)
		return
	}

	selector := labels.SelectorFromSet(labels.Set{
		hyperv1.ControlPlaneComponentLabel: componentName,
	})
	podList, err := h.podLister.List(selector)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to list pods: %v", err), http.StatusInternalServerError)
		return
	}

	var pods []PodEndpoint
	for _, pod := range podList {
		if !isPodReady(pod) {
			continue
		}
		if pod.Status.PodIP == "" {
			continue
		}
		pods = append(pods, PodEndpoint{
			Name: pod.Name,
			IP:   pod.Status.PodIP,
		})
	}

	if len(pods) == 0 {
		http.Error(w, fmt.Sprintf("no ready pods found for component %s", componentName), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(ResolveResponse{Pods: pods}); err != nil {
		http.Error(w, fmt.Sprintf("failed to encode response: %v", err), http.StatusInternalServerError)
	}
}

// isPodReady returns true if the pod is in Running phase and has the Ready condition set to True.
func isPodReady(pod *corev1.Pod) bool {
	if pod.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}
