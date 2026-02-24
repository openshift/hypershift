package endpointresolver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"

	corev1listers "k8s.io/client-go/listers/core/v1"
)

func newPod(name, namespace, componentLabel, ip string, phase corev1.PodPhase, ready corev1.ConditionStatus) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"hypershift.openshift.io/control-plane-component": componentLabel,
			},
		},
		Status: corev1.PodStatus{
			Phase: phase,
			PodIP: ip,
			Conditions: []corev1.PodCondition{
				{
					Type:   corev1.PodReady,
					Status: ready,
				},
			},
		},
	}
}

func newFakePodLister(namespace string, pods []*corev1.Pod) corev1listers.PodNamespaceLister {
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	for _, pod := range pods {
		_ = indexer.Add(pod)
	}
	return corev1listers.NewPodLister(indexer).Pods(namespace)
}

func TestResolverHandler(t *testing.T) {
	tests := []struct {
		name          string
		componentName string
		pods          []*corev1.Pod
		expectedCode  int
		expectedPods  []PodEndpoint
	}{
		{
			name:          "When resolving a component with multiple ready pods it should return all ready pods",
			componentName: "etcd",
			pods: []*corev1.Pod{
				newPod("etcd-0", "test-namespace", "etcd", "10.0.1.5", corev1.PodRunning, corev1.ConditionTrue),
				newPod("etcd-1", "test-namespace", "etcd", "10.0.1.6", corev1.PodRunning, corev1.ConditionTrue),
			},
			expectedCode: http.StatusOK,
			expectedPods: []PodEndpoint{
				{Name: "etcd-0", IP: "10.0.1.5"},
				{Name: "etcd-1", IP: "10.0.1.6"},
			},
		},
		{
			name:          "When resolving a component with no pods it should return 404",
			componentName: "nonexistent",
			pods:          []*corev1.Pod{},
			expectedCode:  http.StatusNotFound,
		},
		{
			name:          "When resolving a component with non-ready pods it should filter them out",
			componentName: "etcd",
			pods: []*corev1.Pod{
				newPod("etcd-0", "test-namespace", "etcd", "10.0.1.5", corev1.PodRunning, corev1.ConditionTrue),
				newPod("etcd-1", "test-namespace", "etcd", "10.0.1.6", corev1.PodRunning, corev1.ConditionFalse),
			},
			expectedCode: http.StatusOK,
			expectedPods: []PodEndpoint{
				{Name: "etcd-0", IP: "10.0.1.5"},
			},
		},
		{
			name:          "When resolving a component where all pods are not ready it should return 404",
			componentName: "etcd",
			pods: []*corev1.Pod{
				newPod("etcd-0", "test-namespace", "etcd", "10.0.1.5", corev1.PodRunning, corev1.ConditionFalse),
			},
			expectedCode: http.StatusNotFound,
		},
		{
			name:          "When resolving a component with a pod not in Running phase it should filter it out",
			componentName: "etcd",
			pods: []*corev1.Pod{
				newPod("etcd-0", "test-namespace", "etcd", "10.0.1.5", corev1.PodPending, corev1.ConditionFalse),
			},
			expectedCode: http.StatusNotFound,
		},
		{
			name:          "When resolving a component with a pod without IP it should filter it out",
			componentName: "my-component",
			pods: []*corev1.Pod{
				newPod("my-component-0", "test-namespace", "my-component", "", corev1.PodRunning, corev1.ConditionTrue),
			},
			expectedCode: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lister := newFakePodLister("test-namespace", tt.pods)
			handler := NewResolverHandler(lister)
			req := httptest.NewRequest(http.MethodGet, "/resolve/"+tt.componentName, nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.expectedCode {
				t.Errorf("expected status code %d, got %d: %s", tt.expectedCode, rec.Code, rec.Body.String())
			}

			if tt.expectedPods != nil {
				var response ResolveResponse
				if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
					t.Fatalf("failed to decode response: %v", err)
				}
				if len(response.Pods) != len(tt.expectedPods) {
					t.Fatalf("expected %d pods, got %d", len(tt.expectedPods), len(response.Pods))
				}
				for i, expected := range tt.expectedPods {
					if response.Pods[i].Name != expected.Name || response.Pods[i].IP != expected.IP {
						t.Errorf("pod %d: expected %+v, got %+v", i, expected, response.Pods[i])
					}
				}
			}
		})
	}
}

func TestResolverHandlerMethodNotAllowed(t *testing.T) {
	t.Run("When sending POST request it should return 405", func(t *testing.T) {
		lister := newFakePodLister("test-namespace", nil)
		handler := NewResolverHandler(lister)
		req := httptest.NewRequest(http.MethodPost, "/resolve/etcd", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected status code %d, got %d", http.StatusMethodNotAllowed, rec.Code)
		}
	})
}

func TestResolverHandlerMissingComponentName(t *testing.T) {
	t.Run("When component name is missing it should return 400", func(t *testing.T) {
		lister := newFakePodLister("test-namespace", nil)
		handler := NewResolverHandler(lister)
		req := httptest.NewRequest(http.MethodGet, "/resolve/", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected status code %d, got %d", rec.Code, http.StatusBadRequest)
		}
	})
}
