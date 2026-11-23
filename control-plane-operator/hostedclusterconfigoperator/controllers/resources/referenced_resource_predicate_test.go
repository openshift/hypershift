package resources

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/k8sutil"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/event"
)

func TestReferencedResourceConfigMapPredicate(t *testing.T) {
	testCases := []struct {
		name     string
		annos    map[string]string
		expected bool
	}{
		{
			name:     "When the configmap carries a referenced-resource annotation, it should match",
			annos:    map[string]string{k8sutil.ReferencedResourceAnnotationPrefix + "test-cluster": "true"},
			expected: true,
		},
		{
			name:     "When the configmap carries an unrelated hypershift annotation, it should not match",
			annos:    map[string]string{k8sutil.HostedClusterAnnotation: "clusters/test-cluster"},
			expected: false,
		},
		{
			name:     "When the configmap carries no annotations, it should not match",
			annos:    nil,
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			predicate := referencedResourceConfigMapPredicate()
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "some-ca", Namespace: "test-ns", Annotations: tc.annos},
			}
			g.Expect(predicate.Create(event.CreateEvent{Object: cm})).To(Equal(tc.expected))
			g.Expect(predicate.Update(event.UpdateEvent{ObjectNew: cm})).To(Equal(tc.expected))
		})
	}
}
