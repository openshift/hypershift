package hostedcluster

import (
	"testing"

	"github.com/openshift/hypershift/support/upsert"
	hyperutil "github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/google/go-cmp/cmp"
)

func TestCreateOrUpdateWithAnnotationFactory(t *testing.T) {
	req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "clusters", Name: "example"}}
	annotationValue := req.String()
	testCases := []struct {
		name     string
		obj      crclient.Object
		expected crclient.Object
		mutateFN func(crclient.Object) controllerutil.MutateFn
	}{
		{
			name: "No annotations",
			obj: &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ConfigMap",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "foo",
					Namespace: "bar",
				},
			},
			mutateFN: func(_ crclient.Object) controllerutil.MutateFn {
				return func() error { return nil }
			},
			expected: &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ConfigMap",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "foo",
					Namespace: "bar",
					Annotations: map[string]string{
						hyperutil.HostedClusterAnnotation: annotationValue,
					},
				},
			},
		},
		{
			name: "Existing annotations are kept",
			obj: &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ConfigMap",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "foo",
					Namespace: "bar",
				},
			},
			mutateFN: func(o crclient.Object) controllerutil.MutateFn {
				return func() error {
					o.SetAnnotations(map[string]string{
						"foo": "bar",
					})
					return nil
				}
			},
			expected: &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ConfigMap",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "foo",
					Namespace: "bar",
					Annotations: map[string]string{
						hyperutil.HostedClusterAnnotation: annotationValue,
						"foo":                             "bar",
					},
				},
			},
		},
		{
			name: "Do not annotate cluster scoped resources",
			obj: &corev1.Namespace{
				TypeMeta: metav1.TypeMeta{
					Kind:       "Namespace",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "foo",
				},
			},
			mutateFN: func(_ crclient.Object) controllerutil.MutateFn {
				return func() error { return nil }
			},
			expected: &corev1.Namespace{
				TypeMeta: metav1.TypeMeta{
					Kind:       "Namespace",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "foo",
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			obj := tc.obj
			client := fake.NewClientBuilder().WithObjects(obj).Build()
			providerFactory := createOrUpdateWithAnnotationFactory(upsert.New(false))
			if _, err := providerFactory(req)(t.Context(), client, obj, tc.mutateFN(obj)); err != nil {
				t.Fatalf("CreateOrUpdate failed: %v", err)
			}

			if err := client.Get(t.Context(), crclient.ObjectKeyFromObject(obj), obj); err != nil {
				t.Fatalf("failed to get object from client after running CreateOrUpdate: %v", err)
			}
			actualMeta := obj.(metav1.Object)
			actualMeta.SetResourceVersion("")
			if diff := cmp.Diff(tc.expected, obj); diff != "" {
				t.Errorf("actual differs from expected: %v", diff)
			}
		})
	}
}
