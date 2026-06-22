package endpointresolver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
)

func newPod(name, namespace string, podLabels map[string]string, ip string, phase corev1.PodPhase, ready corev1.ConditionStatus) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    podLabels,
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

func newResolveRequest(t *testing.T, selector map[string]string) *http.Request {
	t.Helper()
	g := NewWithT(t)
	body, err := json.Marshal(ResolveRequest{Selector: selector})
	g.Expect(err).ToNot(HaveOccurred())
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, resolvePath, bytes.NewReader(body))
	g.Expect(err).ToNot(HaveOccurred())
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestResolverHandler(t *testing.T) {
	etcdLabels := map[string]string{"app": "etcd"}
	componentLabels := map[string]string{"app": "my-component"}

	tests := []struct {
		name         string
		selector     map[string]string
		pods         []*corev1.Pod
		expectedCode int
		expectedPods []PodEndpoint
	}{
		{
			name:     "When resolving with a selector matching multiple ready pods it should return all ready pods",
			selector: etcdLabels,
			pods: []*corev1.Pod{
				newPod("etcd-0", "test-namespace", etcdLabels, "10.0.1.5", corev1.PodRunning, corev1.ConditionTrue),
				newPod("etcd-1", "test-namespace", etcdLabels, "10.0.1.6", corev1.PodRunning, corev1.ConditionTrue),
			},
			expectedCode: http.StatusOK,
			expectedPods: []PodEndpoint{
				{Name: "etcd-0", IP: "10.0.1.5"},
				{Name: "etcd-1", IP: "10.0.1.6"},
			},
		},
		{
			name:         "When resolving with a selector matching no pods it should return 404",
			selector:     map[string]string{"app": "nonexistent"},
			pods:         []*corev1.Pod{},
			expectedCode: http.StatusNotFound,
		},
		{
			name:     "When resolving with a selector matching non-ready pods it should filter them out",
			selector: etcdLabels,
			pods: []*corev1.Pod{
				newPod("etcd-0", "test-namespace", etcdLabels, "10.0.1.5", corev1.PodRunning, corev1.ConditionTrue),
				newPod("etcd-1", "test-namespace", etcdLabels, "10.0.1.6", corev1.PodRunning, corev1.ConditionFalse),
			},
			expectedCode: http.StatusOK,
			expectedPods: []PodEndpoint{
				{Name: "etcd-0", IP: "10.0.1.5"},
			},
		},
		{
			name:     "When resolving with a selector where all pods are not ready it should return 404",
			selector: etcdLabels,
			pods: []*corev1.Pod{
				newPod("etcd-0", "test-namespace", etcdLabels, "10.0.1.5", corev1.PodRunning, corev1.ConditionFalse),
			},
			expectedCode: http.StatusNotFound,
		},
		{
			name:     "When resolving with a selector matching a pod not in Running phase it should filter it out",
			selector: etcdLabels,
			pods: []*corev1.Pod{
				newPod("etcd-0", "test-namespace", etcdLabels, "10.0.1.5", corev1.PodPending, corev1.ConditionFalse),
			},
			expectedCode: http.StatusNotFound,
		},
		{
			name:     "When resolving with a selector matching a pod without IP it should filter it out",
			selector: componentLabels,
			pods: []*corev1.Pod{
				newPod("my-component-0", "test-namespace", componentLabels, "", corev1.PodRunning, corev1.ConditionTrue),
			},
			expectedCode: http.StatusNotFound,
		},
		{
			name:     "When resolving with multiple labels it should only match pods with all labels",
			selector: map[string]string{"app": "etcd", "tier": "backend"},
			pods: []*corev1.Pod{
				newPod("etcd-0", "test-namespace", map[string]string{"app": "etcd", "tier": "backend"}, "10.0.1.5", corev1.PodRunning, corev1.ConditionTrue),
				newPod("etcd-1", "test-namespace", map[string]string{"app": "etcd"}, "10.0.1.6", corev1.PodRunning, corev1.ConditionTrue),
			},
			expectedCode: http.StatusOK,
			expectedPods: []PodEndpoint{
				{Name: "etcd-0", IP: "10.0.1.5"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			lister := newFakePodLister("test-namespace", tt.pods)
			handler := newResolverHandler(lister)
			req := newResolveRequest(t, tt.selector)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			g.Expect(rec.Code).To(Equal(tt.expectedCode))

			if tt.expectedPods != nil {
				var response ResolveResponse
				g.Expect(json.NewDecoder(rec.Body).Decode(&response)).To(Succeed())
				g.Expect(response.Pods).To(HaveLen(len(tt.expectedPods)))
				for i, expected := range tt.expectedPods {
					g.Expect(response.Pods[i].Name).To(Equal(expected.Name))
					g.Expect(response.Pods[i].IP).To(Equal(expected.IP))
				}
			}
		})
	}
}

func TestResolverHandlerMethodNotAllowed(t *testing.T) {
	t.Run("When sending GET request it should return 405", func(t *testing.T) {
		g := NewWithT(t)
		lister := newFakePodLister("test-namespace", nil)
		handler := newResolverHandler(lister)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, resolvePath, nil)
		g.Expect(err).ToNot(HaveOccurred())
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		g.Expect(rec.Code).To(Equal(http.StatusMethodNotAllowed))
	})
}

func TestResolverHandlerEmptySelector(t *testing.T) {
	t.Run("When selector is empty it should return 400", func(t *testing.T) {
		g := NewWithT(t)
		lister := newFakePodLister("test-namespace", nil)
		handler := newResolverHandler(lister)
		req := newResolveRequest(t, map[string]string{})
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		g.Expect(rec.Code).To(Equal(http.StatusBadRequest))
	})
}

func TestResolverHandlerInvalidBody(t *testing.T) {
	t.Run("When request body is invalid JSON it should return 400", func(t *testing.T) {
		g := NewWithT(t)
		lister := newFakePodLister("test-namespace", nil)
		handler := newResolverHandler(lister)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, resolvePath, bytes.NewReader([]byte("not json")))
		g.Expect(err).ToNot(HaveOccurred())
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		g.Expect(rec.Code).To(Equal(http.StatusBadRequest))
	})
}
