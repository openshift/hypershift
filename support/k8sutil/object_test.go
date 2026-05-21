package k8sutil

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestCopyConfigMap(t *testing.T) {
	t.Run("When source has data it should deep copy all entries", func(t *testing.T) {
		g := NewWithT(t)
		source := &corev1.ConfigMap{
			Data: map[string]string{"key1": "val1", "key2": "val2"},
		}
		dest := &corev1.ConfigMap{}

		CopyConfigMap(dest, source)

		g.Expect(dest.Data).To(Equal(map[string]string{"key1": "val1", "key2": "val2"}))
		source.Data["key1"] = "mutated"
		g.Expect(dest.Data["key1"]).To(Equal("val1"))
	})

	t.Run("When source has empty data it should set empty map", func(t *testing.T) {
		g := NewWithT(t)
		source := &corev1.ConfigMap{Data: map[string]string{}}
		dest := &corev1.ConfigMap{}

		CopyConfigMap(dest, source)

		g.Expect(dest.Data).To(BeEmpty())
	})

	t.Run("When source has nil data it should set empty map", func(t *testing.T) {
		g := NewWithT(t)
		source := &corev1.ConfigMap{}
		dest := &corev1.ConfigMap{}

		CopyConfigMap(dest, source)

		g.Expect(dest.Data).To(BeEmpty())
	})
}

func TestDeleteIfNeededWithOptions(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	t.Run("When object exists it should delete and return exists=true", func(t *testing.T) {
		g := NewWithT(t)
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"}}
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm).Build()

		exists, err := DeleteIfNeededWithOptions(t.Context(), c, cm)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(exists).To(BeTrue())

		err = c.Get(t.Context(), crclient.ObjectKeyFromObject(cm), &corev1.ConfigMap{})
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	t.Run("When object does not exist it should return exists=false without error", func(t *testing.T) {
		g := NewWithT(t)
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "missing", Namespace: "default"}}
		c := fake.NewClientBuilder().WithScheme(scheme).Build()

		exists, err := DeleteIfNeededWithOptions(t.Context(), c, cm)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(exists).To(BeFalse())
	})

	t.Run("When object has a deletion timestamp it should return exists=true without deleting again", func(t *testing.T) {
		g := NewWithT(t)
		now := metav1.Now()
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
			Name: "deleting", Namespace: "default",
			DeletionTimestamp: &now,
			Finalizers:        []string{"test-finalizer"},
		}}
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm).Build()

		exists, err := DeleteIfNeededWithOptions(t.Context(), c, cm)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(exists).To(BeTrue())
	})

	t.Run("When Get returns a non-NotFound error it should propagate the error", func(t *testing.T) {
		g := NewWithT(t)
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"}}
		c := fake.NewClientBuilder().WithScheme(scheme).
			WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, client crclient.WithWatch, key crclient.ObjectKey, obj crclient.Object, opts ...crclient.GetOption) error {
					return fmt.Errorf("connection refused")
				},
			}).Build()

		exists, err := DeleteIfNeededWithOptions(t.Context(), c, cm)
		g.Expect(err).To(MatchError(ContainSubstring("connection refused")))
		g.Expect(exists).To(BeFalse())
	})

	t.Run("When Delete returns NotFound due to race it should return exists=false", func(t *testing.T) {
		g := NewWithT(t)
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"}}
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm).
			WithInterceptorFuncs(interceptor.Funcs{
				Delete: func(ctx context.Context, client crclient.WithWatch, obj crclient.Object, opts ...crclient.DeleteOption) error {
					return apierrors.NewNotFound(schema.GroupResource{Resource: "configmaps"}, "test")
				},
			}).Build()

		exists, err := DeleteIfNeededWithOptions(t.Context(), c, cm)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(exists).To(BeFalse())
	})

	t.Run("When Delete returns a non-NotFound error it should propagate the error", func(t *testing.T) {
		g := NewWithT(t)
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"}}
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm).
			WithInterceptorFuncs(interceptor.Funcs{
				Delete: func(ctx context.Context, client crclient.WithWatch, obj crclient.Object, opts ...crclient.DeleteOption) error {
					return fmt.Errorf("forbidden")
				},
			}).Build()

		exists, err := DeleteIfNeededWithOptions(t.Context(), c, cm)
		g.Expect(err).To(MatchError(ContainSubstring("forbidden")))
		g.Expect(exists).To(BeFalse())
	})
}

func TestDeleteIfNeeded(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	t.Run("When object exists it should delete it", func(t *testing.T) {
		g := NewWithT(t)
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"}}
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm).Build()

		exists, err := DeleteIfNeeded(t.Context(), c, cm)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(exists).To(BeTrue())
	})

	t.Run("When object does not exist it should return false", func(t *testing.T) {
		g := NewWithT(t)
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "missing", Namespace: "default"}}
		c := fake.NewClientBuilder().WithScheme(scheme).Build()

		exists, err := DeleteIfNeeded(t.Context(), c, cm)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(exists).To(BeFalse())
	})
}

func TestDeleteIfNeededWithPredicate(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	t.Run("When predicate returns true it should delete", func(t *testing.T) {
		g := NewWithT(t)
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"}}
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm).Build()

		exists, err := DeleteIfNeededWithPredicate(t.Context(), c, cm, func(*corev1.ConfigMap) bool { return true })
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(exists).To(BeTrue())

		err = c.Get(t.Context(), crclient.ObjectKeyFromObject(cm), &corev1.ConfigMap{})
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	t.Run("When predicate returns false it should not delete", func(t *testing.T) {
		g := NewWithT(t)
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"}}
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm).Build()

		exists, err := DeleteIfNeededWithPredicate(t.Context(), c, cm, func(*corev1.ConfigMap) bool { return false })
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(exists).To(BeTrue())

		err = c.Get(t.Context(), crclient.ObjectKeyFromObject(cm), &corev1.ConfigMap{})
		g.Expect(err).ToNot(HaveOccurred())
	})

	t.Run("When object does not exist it should return false", func(t *testing.T) {
		g := NewWithT(t)
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "missing", Namespace: "default"}}
		c := fake.NewClientBuilder().WithScheme(scheme).Build()

		exists, err := DeleteIfNeededWithPredicate(t.Context(), c, cm, func(*corev1.ConfigMap) bool { return true })
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(exists).To(BeFalse())
	})

	t.Run("When object has a deletion timestamp it should return exists=true without deleting", func(t *testing.T) {
		g := NewWithT(t)
		now := metav1.Now()
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
			Name: "deleting", Namespace: "default",
			DeletionTimestamp: &now,
			Finalizers:        []string{"test-finalizer"},
		}}
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm).Build()

		exists, err := DeleteIfNeededWithPredicate(t.Context(), c, cm, func(*corev1.ConfigMap) bool { return true })
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(exists).To(BeTrue())
	})

	t.Run("When Get returns a non-NotFound error it should propagate the error", func(t *testing.T) {
		g := NewWithT(t)
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"}}
		c := fake.NewClientBuilder().WithScheme(scheme).
			WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, client crclient.WithWatch, key crclient.ObjectKey, obj crclient.Object, opts ...crclient.GetOption) error {
					return fmt.Errorf("connection refused")
				},
			}).Build()

		exists, err := DeleteIfNeededWithPredicate(t.Context(), c, cm, func(*corev1.ConfigMap) bool { return true })
		g.Expect(err).To(MatchError(ContainSubstring("connection refused")))
		g.Expect(exists).To(BeFalse())
	})

	t.Run("When Delete returns NotFound due to race it should return exists=false", func(t *testing.T) {
		g := NewWithT(t)
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"}}
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm).
			WithInterceptorFuncs(interceptor.Funcs{
				Delete: func(ctx context.Context, client crclient.WithWatch, obj crclient.Object, opts ...crclient.DeleteOption) error {
					return apierrors.NewNotFound(schema.GroupResource{Resource: "configmaps"}, "test")
				},
			}).Build()

		exists, err := DeleteIfNeededWithPredicate(t.Context(), c, cm, func(*corev1.ConfigMap) bool { return true })
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(exists).To(BeFalse())
	})

	t.Run("When Delete returns a non-NotFound error it should propagate the error", func(t *testing.T) {
		g := NewWithT(t)
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"}}
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm).
			WithInterceptorFuncs(interceptor.Funcs{
				Delete: func(ctx context.Context, client crclient.WithWatch, obj crclient.Object, opts ...crclient.DeleteOption) error {
					return fmt.Errorf("forbidden")
				},
			}).Build()

		exists, err := DeleteIfNeededWithPredicate(t.Context(), c, cm, func(*corev1.ConfigMap) bool { return true })
		g.Expect(err).To(MatchError(ContainSubstring("forbidden")))
		g.Expect(exists).To(BeFalse())
	})
}

func TestDeleteAllIfNeeded(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	t.Run("When all objects exist it should delete them without error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cm1 := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cm-1", Namespace: "default"}}
		cm2 := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cm-2", Namespace: "default"}}
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm1, cm2).Build()

		err := DeleteAllIfNeeded(t.Context(), c, cm1, cm2)
		g.Expect(err).ToNot(HaveOccurred())

		err = c.Get(t.Context(), crclient.ObjectKeyFromObject(cm1), &corev1.ConfigMap{})
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "cm1 should be deleted")
		err = c.Get(t.Context(), crclient.ObjectKeyFromObject(cm2), &corev1.ConfigMap{})
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "cm2 should be deleted")
	})

	t.Run("When no objects are provided it should return nil", func(t *testing.T) {
		g := NewGomegaWithT(t)
		c := fake.NewClientBuilder().WithScheme(scheme).Build()

		err := DeleteAllIfNeeded(t.Context(), c)
		g.Expect(err).ToNot(HaveOccurred())
	})

	t.Run("When an object does not exist it should not return an error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "nonexistent", Namespace: "default"}}
		c := fake.NewClientBuilder().WithScheme(scheme).Build()

		err := DeleteAllIfNeeded(t.Context(), c, cm)
		g.Expect(err).ToNot(HaveOccurred())
	})

	t.Run("When Get fails for multiple objects it should aggregate errors", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cm1 := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cm-1", Namespace: "default"}}
		cm2 := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cm-2", Namespace: "default"}}
		c := fake.NewClientBuilder().WithScheme(scheme).
			WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, client crclient.WithWatch, key crclient.ObjectKey, obj crclient.Object, opts ...crclient.GetOption) error {
					return fmt.Errorf("api unavailable")
				},
			}).Build()

		err := DeleteAllIfNeeded(t.Context(), c, cm1, cm2)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("api unavailable"))
	})
}

func TestUpdateObject(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	t.Run("When mutation succeeds it should patch the object", func(t *testing.T) {
		g := NewWithT(t)
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Data:       map[string]string{"key": "old"},
		}
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm).Build()

		err := UpdateObject(t.Context(), c, cm, func() error {
			cm.Data["key"] = "new"
			return nil
		})
		g.Expect(err).ToNot(HaveOccurred())

		updated := &corev1.ConfigMap{}
		err = c.Get(t.Context(), crclient.ObjectKeyFromObject(cm), updated)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(updated.Data["key"]).To(Equal("new"))
	})

	t.Run("When mutate returns an error it should propagate the error", func(t *testing.T) {
		g := NewWithT(t)
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Data:       map[string]string{"key": "old"},
		}
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm).Build()

		err := UpdateObject(t.Context(), c, cm, func() error {
			return fmt.Errorf("mutation failed")
		})
		g.Expect(err).To(MatchError(ContainSubstring("mutation failed")))

		unchanged := &corev1.ConfigMap{}
		err = c.Get(t.Context(), crclient.ObjectKeyFromObject(cm), unchanged)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(unchanged.Data["key"]).To(Equal("old"))
	})

	t.Run("When object does not exist it should return a not-found error", func(t *testing.T) {
		g := NewWithT(t)
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "missing", Namespace: "default"},
		}
		c := fake.NewClientBuilder().WithScheme(scheme).Build()

		err := UpdateObject(t.Context(), c, cm, func() error {
			return nil
		})
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	t.Run("When patch returns a conflict it should retry and succeed", func(t *testing.T) {
		g := NewWithT(t)
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Data:       map[string]string{"key": "old"},
		}

		var patchCalls atomic.Int32
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm).
			WithInterceptorFuncs(interceptor.Funcs{
				Patch: func(ctx context.Context, client crclient.WithWatch, obj crclient.Object, patch crclient.Patch, opts ...crclient.PatchOption) error {
					if patchCalls.Add(1) == 1 {
						return apierrors.NewConflict(schema.GroupResource{Resource: "configmaps"}, "test", fmt.Errorf("conflict"))
					}
					return client.Patch(ctx, obj, patch, opts...)
				},
			}).Build()

		err := UpdateObject(t.Context(), c, cm, func() error {
			cm.Data["key"] = "new"
			return nil
		})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(patchCalls.Load()).To(BeNumerically(">=", int32(2)))

		updated := &corev1.ConfigMap{}
		err = c.Get(t.Context(), crclient.ObjectKeyFromObject(cm), updated)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(updated.Data["key"]).To(Equal("new"))
	})
}

func TestParseNodeSelector(t *testing.T) {
	tests := []struct {
		name string
		str  string
		want map[string]string
	}{
		{
			name: "When input has multiple key=value pairs it should return all entries",
			str:  "key1=value1,key2=value2,key3=value3",
			want: map[string]string{
				"key1": "value1",
				"key2": "value2",
				"key3": "value3",
			},
		},
		{
			name: "When entries have empty values it should skip them",
			str:  "key1=,key2=value2,key3=",
			want: map[string]string{
				"key2": "value2",
			},
		},
		{
			name: "When entries have empty keys it should skip them",
			str:  "=value1,key2=value2,=value3",
			want: map[string]string{
				"key2": "value2",
			},
		},
		{
			name: "When input is empty it should return nil",
			str:  "",
			want: nil,
		},
		{
			name: "When entries lack an = separator it should skip them",
			str:  "key1=value1,key2,key3=value3",
			want: map[string]string{
				"key1": "value1",
				"key3": "value3",
			},
		},
		{
			name: "When values contain = characters it should preserve them",
			str:  "key1=value1=one,key2,key3=value3=three",
			want: map[string]string{
				"key1": "value1=one",
				"key3": "value3=three",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			got := ParseNodeSelector(tt.str)
			g.Expect(got).To(Equal(tt.want))
		})
	}
}

func TestApplyAWSLoadBalancerTargetNodesAnnotation(t *testing.T) {
	t.Run("When platform is AWS and annotation is present it should set service annotation", func(t *testing.T) {
		g := NewWithT(t)
		svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "test"}}
		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					hyperv1.AWSLoadBalancerTargetNodesAnnotation: "node.kubernetes.io/role=worker",
				},
			},
			Spec: hyperv1.HostedControlPlaneSpec{
				Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform},
			},
		}

		ApplyAWSLoadBalancerTargetNodesAnnotation(svc, hcp)
		g.Expect(svc.Annotations["service.beta.kubernetes.io/aws-load-balancer-target-node-labels"]).To(Equal("node.kubernetes.io/role=worker"))
	})

	t.Run("When platform is not AWS it should not modify annotations", func(t *testing.T) {
		g := NewWithT(t)
		svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "test"}}
		hcp := &hyperv1.HostedControlPlane{
			Spec: hyperv1.HostedControlPlaneSpec{
				Platform: hyperv1.PlatformSpec{Type: hyperv1.AzurePlatform},
			},
		}

		ApplyAWSLoadBalancerTargetNodesAnnotation(svc, hcp)
		g.Expect(svc.Annotations).To(BeNil())
	})

	t.Run("When platform is AWS but annotation is absent it should not set service annotation", func(t *testing.T) {
		g := NewWithT(t)
		svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "test"}}
		hcp := &hyperv1.HostedControlPlane{
			Spec: hyperv1.HostedControlPlaneSpec{
				Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform},
			},
		}

		ApplyAWSLoadBalancerTargetNodesAnnotation(svc, hcp)
		g.Expect(svc.Annotations).ToNot(HaveKey("service.beta.kubernetes.io/aws-load-balancer-target-node-labels"))
	})
}
