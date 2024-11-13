package hostedcontrolplane

import (
	"context"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/upsert"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/google/go-cmp/cmp"
)

func TestCreateOrUpdateWithOwnerRefFactory(t *testing.T) {

	hcp := &hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: "bar", Name: "foo"}}
	ownerRef := config.OwnerRefFrom(hcp)
	testCases := []struct {
		name     string
		obj      crclient.Object
		expected []metav1.OwnerReference
		mutateFN func(crclient.Object) controllerutil.MutateFn
	}{
		{
			name: "Owner ref is added",
			obj: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "foo",
					Namespace: "bar",
				},
			},
			expected: []metav1.OwnerReference{*ownerRef.Reference},
		},
		{
			name: "Adding takes precedence",
			obj: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "foo",
					Namespace: "bar",
				},
			},
			mutateFN: func(o crclient.Object) controllerutil.MutateFn {
				return func() error {
					o.SetOwnerReferences(nil)
					return nil
				}
			},
			expected: []metav1.OwnerReference{*ownerRef.Reference},
		},
		{
			name: "Do not add ownerRef to cluster scoped resources",
			obj: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "foo",
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			obj := tc.obj
			if tc.mutateFN == nil {
				tc.mutateFN = func(crclient.Object) controllerutil.MutateFn {
					return func() error { return nil }
				}
			}
			client := fake.NewClientBuilder().WithObjects(obj).Build()
			providerFactory := createOrUpdateWithOwnerRefFactory(upsert.New(false).CreateOrUpdate)
			if _, err := providerFactory(hcp)(context.Background(), client, obj, tc.mutateFN(obj)); err != nil {
				t.Fatalf("CreateOrUpdate failed: %v", err)
			}

			if err := client.Get(context.Background(), crclient.ObjectKeyFromObject(obj), obj); err != nil {
				t.Fatalf("failed to get object from client after running CreateOrUpdate: %v", err)
			}
			if diff := cmp.Diff(tc.expected, obj.GetOwnerReferences()); diff != "" {
				t.Errorf("actual differs from expected: %v", diff)
			}
		})
	}
}
