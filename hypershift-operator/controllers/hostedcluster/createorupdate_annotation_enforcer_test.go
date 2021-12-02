package hostedcluster

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/openshift/hypershift/support/upsert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestCreateOrUpdateWithAnnotationFactory(t *testing.T) {

	testCases := []struct {
		name     string
		mutateFN func(m *metav1.ObjectMeta) controllerutil.MutateFn
		verify   func(*testing.T, *corev1.ConfigMap)
	}{
		{
			name: "No annotations",
			mutateFN: func(m *metav1.ObjectMeta) controllerutil.MutateFn {
				return func() error { return nil }
			},
		},
		{
			name: "Existing annotations are kept",
			mutateFN: func(m *metav1.ObjectMeta) controllerutil.MutateFn {
				return func() error {
					m.Annotations = map[string]string{
						"foo": "bar",
					}
					return nil
				}
			},
			verify: func(t *testing.T, cm *corev1.ConfigMap) {
				if cm.Annotations["foo"] != "bar" {
					t.Errorf("expected 'foo' annotation to be kept, but it's gone. Annotations: %+v", cm.Annotations)
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			obj := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: "foo",
				},
			}

			// expectedObj is the original object + mutateFN + the annotation
			expectedObj := obj.DeepCopy()
			tc.mutateFN(&expectedObj.ObjectMeta)()
			if expectedObj.Annotations == nil {
				expectedObj.Annotations = map[string]string{}
			}
			expectedObj.Annotations[hostedClusterAnnotation] = "/foo"

			client := fake.NewClientBuilder().WithObjects(obj).Build()
			providerFactory := createOrUpdateWithAnnotationFactory(upsert.New(false))
			req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: obj.Namespace, Name: obj.Name}}

			if _, err := providerFactory(req)(context.Background(), client, obj, tc.mutateFN(&obj.ObjectMeta)); err != nil {
				t.Fatalf("CreateOrUpdate failed: %v", err)
			}

			actual := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "foo"}}
			if err := client.Get(context.Background(), crclient.ObjectKeyFromObject(actual), actual); err != nil {
				t.Fatalf("failed to get object from client after running CreateOrUpdate: %v", err)
			}
			actual.ResourceVersion = ""
			actual.TypeMeta = metav1.TypeMeta{}
			if diff := cmp.Diff(actual, expectedObj); diff != "" {
				t.Errorf("actual differs from expected: %v", diff)
			}

			if tc.verify != nil {
				tc.verify(t, actual)
			}
		})
	}
}
