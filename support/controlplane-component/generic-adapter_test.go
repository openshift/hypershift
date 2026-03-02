package controlplanecomponent

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/upsert"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = hyperv1.AddToScheme(s)
	return s
}

func testCPContext(t *testing.T, checker GVKAccessChecker, objects ...client.Object) ControlPlaneContext {
	t.Helper()
	return ControlPlaneContext{
		Context:       t.Context(),
		ApplyProvider: upsert.NewApplyProvider(false),
		HCP: &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-hcp",
				Namespace: "test-ns",
			},
		},
		Client: fake.NewClientBuilder().WithScheme(testScheme()).
			WithObjects(objects...).Build(),
		GVKAccessChecker: checker,
	}
}

func testObjWithGVK(gvk schema.GroupVersionKind) client.Object {
	obj := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-resource",
			Namespace: "test-ns",
		},
	}
	obj.SetGroupVersionKind(gvk)
	return obj
}

var inaccessibleGVK = schema.GroupVersionKind{Group: "secrets-store.csi.x-k8s.io", Version: "v1", Kind: "SecretProviderClass"}

func TestGenericAdapterReconcile(t *testing.T) {
	t.Run("When predicate is false and GVK is inaccessible it should skip without error", func(t *testing.T) {
		g := NewWithT(t)

		reader := &fakeReader{err: apierrors.NewForbidden(
			schema.GroupResource{Group: inaccessibleGVK.Group, Resource: "secretproviderclasses"},
			"test-resource", fmt.Errorf("forbidden"),
		)}
		checker := NewGVKAccessCache(reader)

		cpCtx := testCPContext(t, checker)

		ga := &genericAdapter{
			predicate: func(_ WorkloadContext) bool { return false },
		}

		obj := testObjWithGVK(inaccessibleGVK)
		err := ga.reconcile(cpCtx, obj)
		g.Expect(err).ToNot(HaveOccurred())
		// Probe should have been called exactly once.
		g.Expect(reader.callCount.Load()).To(Equal(int32(1)))
	})

	t.Run("When predicate is false and GVK is accessible it should attempt cleanup", func(t *testing.T) {
		g := NewWithT(t)

		// NotFound means GVK is accessible (CRD exists, resource just doesn't exist yet).
		reader := &fakeReader{err: apierrors.NewNotFound(
			schema.GroupResource{Group: "apps", Resource: "deployments"},
			"test-resource",
		)}
		checker := NewGVKAccessCache(reader)

		cpCtx := testCPContext(t, checker)

		ga := &genericAdapter{
			predicate: func(_ WorkloadContext) bool { return false },
		}

		// Use a ConfigMap with a known GVK (accessible). The object doesn't exist
		// on the fake client so the Get in the predicate-false path returns NotFound → returns nil.
		obj := testObjWithGVK(schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"})
		err := ga.reconcile(cpCtx, obj)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(reader.callCount.Load()).To(Equal(int32(1)))
	})

	t.Run("When predicate is false and GVK checker is nil it should proceed with existing logic", func(t *testing.T) {
		g := NewWithT(t)

		// No checker — backward compatibility.
		cpCtx := testCPContext(t, nil)

		ga := &genericAdapter{
			predicate: func(_ WorkloadContext) bool { return false },
		}

		obj := testObjWithGVK(inaccessibleGVK)
		// Without a checker the code falls through to Client.Get which will
		// return an error or NotFound depending on the fake client setup.
		// Since the object doesn't exist and the GVK is registered (ConfigMap used in fake),
		// we use a ConfigMap to avoid scheme issues.
		cmObj := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-resource",
				Namespace: "test-ns",
			},
		}
		_ = obj // unused in this path
		err := ga.reconcile(cpCtx, cmObj)
		// Should succeed (object not found → no deletion needed).
		g.Expect(err).ToNot(HaveOccurred())
	})

	t.Run("When predicate is false and GVK probe returns transient error it should propagate error", func(t *testing.T) {
		g := NewWithT(t)

		reader := &fakeReader{err: fmt.Errorf("connection refused")}
		checker := NewGVKAccessCache(reader)

		cpCtx := testCPContext(t, checker)

		ga := &genericAdapter{
			predicate: func(_ WorkloadContext) bool { return false },
		}

		obj := testObjWithGVK(inaccessibleGVK)
		err := ga.reconcile(cpCtx, obj)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("connection refused"))
	})

	t.Run("When predicate is false and GVK is NoMatch it should skip without error", func(t *testing.T) {
		g := NewWithT(t)

		reader := &fakeReader{err: &meta.NoKindMatchError{
			GroupKind: schema.GroupKind{Group: inaccessibleGVK.Group, Kind: inaccessibleGVK.Kind},
		}}
		checker := NewGVKAccessCache(reader)

		cpCtx := testCPContext(t, checker)

		ga := &genericAdapter{
			predicate: func(_ WorkloadContext) bool { return false },
		}

		obj := testObjWithGVK(inaccessibleGVK)
		err := ga.reconcile(cpCtx, obj)
		g.Expect(err).ToNot(HaveOccurred())
	})

	t.Run("When predicate is true it should not check GVK access", func(t *testing.T) {
		g := NewWithT(t)

		reader := &fakeReader{err: fmt.Errorf("should not be called")}
		checker := NewGVKAccessCache(reader)

		cpCtx := testCPContext(t, checker)

		adapted := false
		ga := &genericAdapter{
			predicate: func(_ WorkloadContext) bool { return true },
			adapt: func(_ WorkloadContext, _ client.Object) error {
				adapted = true
				return nil
			},
		}

		obj := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-resource",
				Namespace: "test-ns",
			},
		}
		err := ga.reconcile(cpCtx, obj)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(adapted).To(BeTrue())
		// Reader should not have been called since predicate was true.
		g.Expect(reader.callCount.Load()).To(Equal(int32(0)))
	})

	t.Run("When predicate is false and accessible resource has HCP owner ref it should delete it", func(t *testing.T) {
		g := NewWithT(t)

		reader := &fakeReader{err: nil} // OK → accessible
		checker := NewGVKAccessCache(reader)

		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-hcp",
				Namespace: "test-ns",
				UID:       "test-uid",
			},
		}

		// Create an existing resource with HCP owner reference.
		existingCM := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-resource",
				Namespace: "test-ns",
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: hyperv1.GroupVersion.String(),
						Kind:       "HostedControlPlane",
						Name:       hcp.Name,
						UID:        hcp.UID,
					},
				},
			},
		}

		cpCtx := ControlPlaneContext{
			Context:          t.Context(),
			ApplyProvider:    upsert.NewApplyProvider(false),
			HCP:              hcp,
			Client:           fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(existingCM).Build(),
			GVKAccessChecker: checker,
		}

		ga := &genericAdapter{
			predicate: func(_ WorkloadContext) bool { return false },
		}

		obj := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-resource",
				Namespace: "test-ns",
			},
		}
		obj.SetGroupVersionKind(schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"})

		err := ga.reconcile(cpCtx, obj)
		g.Expect(err).ToNot(HaveOccurred())

		// Verify the resource was deleted.
		got := &corev1.ConfigMap{}
		err = cpCtx.Client.Get(context.Background(), client.ObjectKeyFromObject(obj), got)
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})
}
