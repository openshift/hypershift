package endpointresolver

import (
	"encoding/json"
	"fmt"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	corev1listers "k8s.io/client-go/listers/core/v1"
)

const resolvePath = "/resolve"

// ResolveRequest is the JSON request body for the resolve endpoint.
type ResolveRequest struct {
	Selector map[string]string `json:"selector"`
}

// PodEndpoint represents a resolved pod endpoint with its name and IP address.
type PodEndpoint struct {
	Name string `json:"name"`
	IP   string `json:"ip"`
}

// ResolveResponse is the JSON response returned by the resolve endpoint.
type ResolveResponse struct {
	Pods []PodEndpoint `json:"pods"`
}

// resolverHandler handles endpoint resolution requests by looking up Pods
// matching the provided label selector.
type resolverHandler struct {
	podLister corev1listers.PodNamespaceLister
}

// newResolverHandler creates a new resolverHandler.
func newResolverHandler(podLister corev1listers.PodNamespaceLister) *resolverHandler {
	return &resolverHandler{
		podLister: podLister,
	}
}

func (h *resolverHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ResolveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("failed to decode request body: %v", err), http.StatusBadRequest)
		return
	}
	if len(req.Selector) == 0 {
		http.Error(w, "selector is required and must not be empty", http.StatusBadRequest)
		return
	}

	selector := labels.SelectorFromSet(labels.Set(req.Selector))
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
		http.Error(w, "no ready pods found matching selector", http.StatusNotFound)
		return
	}

	buf, err := json.Marshal(ResolveResponse{Pods: pods})
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to encode response: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(buf)
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
