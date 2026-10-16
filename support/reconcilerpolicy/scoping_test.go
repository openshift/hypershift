package reconcilerpolicy

import (
	"context"
	"errors"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/k8sutil"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func TestPredicatesForHostedClusterAnnotationScoping(t *testing.T) {
	g := NewWithT(t)
	scheme := runtime.NewScheme()
	g.Expect(corev1.AddToScheme(scheme)).To(Succeed())
	g.Expect(hyperv1.AddToScheme(scheme)).To(Succeed())

	tests := []struct {
		name          string
		enabled       string
		operatorScope string
		object        client.Object
		readerObjects []client.Object
		readerError   error
		expected      bool
	}{
		{
			name:          "When annotation scoping is disabled and scopes differ, it should allow the event",
			enabled:       "false",
			operatorScope: "operator-b",
			object:        testHostedCluster("operator-a"),
			expected:      true,
		},
		{
			name:     "When annotation scoping is enabled and both scopes are empty, it should allow the event",
			enabled:  "true",
			object:   testHostedCluster(""),
			expected: true,
		},
		{
			name:          "When a HostedCluster scope matches the operator scope, it should allow the event",
			enabled:       "true",
			operatorScope: "operator-a",
			object:        testHostedCluster("operator-a"),
			expected:      true,
		},
		{
			name:          "When a HostedCluster scope differs from the operator scope, it should reject the event",
			enabled:       "true",
			operatorScope: "operator-b",
			object:        testHostedCluster("operator-a"),
			expected:      false,
		},
		{
			name:          "When a NodePool belongs to a HostedCluster with the operator scope, it should allow the event",
			enabled:       "true",
			operatorScope: "operator-a",
			object:        testNodePool(),
			readerObjects: []client.Object{testHostedCluster("operator-a")},
			expected:      true,
		},
		{
			name:          "When a NodePool belongs to a HostedCluster with another operator scope, it should reject the event",
			enabled:       "true",
			operatorScope: "operator-a",
			object:        testNodePool(),
			readerObjects: []client.Object{testHostedCluster("operator-b")},
			expected:      false,
		},
		{
			name:          "When a resource has a HostedCluster annotation with the operator scope, it should allow the event",
			enabled:       "true",
			operatorScope: "operator-a",
			object: &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
				Name:        "managed-resource",
				Namespace:   "clusters",
				Annotations: map[string]string{k8sutil.HostedClusterAnnotation: "clusters/example"},
			}},
			readerObjects: []client.Object{testHostedCluster("operator-a")},
			expected:      true,
		},
		{
			name:          "When a resource has a HostedCluster annotation with another operator scope, it should reject the event",
			enabled:       "true",
			operatorScope: "operator-a",
			object: &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
				Name:        "managed-resource",
				Namespace:   "clusters",
				Annotations: map[string]string{k8sutil.HostedClusterAnnotation: "clusters/example"},
			}},
			readerObjects: []client.Object{testHostedCluster("operator-b")},
			expected:      false,
		},
		{
			name:          "When a resource has a NodePool annotation linked to the operator scope, it should allow the event",
			enabled:       "true",
			operatorScope: "operator-a",
			object: &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
				Name:        "managed-resource",
				Namespace:   "clusters",
				Annotations: map[string]string{"hypershift.openshift.io/nodePool": "clusters/workers"},
			}},
			readerObjects: []client.Object{testNodePool(), testHostedCluster("operator-a")},
			expected:      true,
		},
		{
			name:          "When a resource has a NodePool annotation linked to another operator scope, it should reject the event",
			enabled:       "true",
			operatorScope: "operator-a",
			object: &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
				Name:        "managed-resource",
				Namespace:   "clusters",
				Annotations: map[string]string{"hypershift.openshift.io/nodePool": "clusters/workers"},
			}},
			readerObjects: []client.Object{testNodePool(), testHostedCluster("operator-b")},
			expected:      false,
		},
		{
			name:     "When a resource has no owner annotation and the operator scope is empty, it should allow the event",
			enabled:  "true",
			object:   &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "unowned-resource", Namespace: "clusters"}},
			expected: true,
		},
		{
			name:          "When a resource has no owner annotation and the operator scope is set, it should reject the event",
			enabled:       "true",
			operatorScope: "operator-a",
			object:        &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "unowned-resource", Namespace: "clusters"}},
			expected:      false,
		},
		{
			name:    "When the HostedCluster lookup fails and the operator scope is empty, it should allow the event",
			enabled: "true",
			object: &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
				Name:        "managed-resource",
				Namespace:   "clusters",
				Annotations: map[string]string{k8sutil.HostedClusterAnnotation: "clusters/example"},
			}},
			readerError: errors.New("reader unavailable"),
			expected:    true,
		},
		{
			name:          "When the NodePool lookup fails and the operator scope is set, it should reject the event",
			enabled:       "true",
			operatorScope: "operator-a",
			object: &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
				Name:        "managed-resource",
				Namespace:   "clusters",
				Annotations: map[string]string{"hypershift.openshift.io/nodePool": "clusters/workers"},
			}},
			readerError: errors.New("reader unavailable"),
			expected:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			t.Setenv(EnableHostedClustersAnnotationScopingEnv, tt.enabled)
			t.Setenv(HostedClustersScopeAnnotationEnv, tt.operatorScope)

			clientBuilder := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.readerObjects...)
			if tt.readerError != nil {
				clientBuilder = clientBuilder.WithInterceptorFuncs(interceptor.Funcs{
					Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
						return tt.readerError
					},
				})
			}
			predicate := PredicatesForHostedClusterAnnotationScoping(clientBuilder.Build())

			results := []struct {
				name    string
				allowed bool
			}{
				{name: "create", allowed: predicate.Create(event.CreateEvent{Object: tt.object})},
				{name: "update", allowed: predicate.Update(event.UpdateEvent{ObjectOld: tt.object, ObjectNew: tt.object})},
				{name: "delete", allowed: predicate.Delete(event.DeleteEvent{Object: tt.object})},
				{name: "generic", allowed: predicate.Generic(event.GenericEvent{Object: tt.object})},
			}
			for _, result := range results {
				g.Expect(result.allowed).To(Equal(tt.expected), "unexpected result for %s event", result.name)
			}
		})
	}
}

func testHostedCluster(scope string) *hyperv1.HostedCluster {
	annotations := map[string]string(nil)
	if scope != "" {
		annotations = map[string]string{HostedClustersScopeAnnotation: scope}
	}
	return &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "example",
			Namespace:   "clusters",
			Annotations: annotations,
		},
	}
}

func testNodePool() *hyperv1.NodePool {
	return &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "workers", Namespace: "clusters"},
		Spec:       hyperv1.NodePoolSpec{ClusterName: "example"},
	}
}
