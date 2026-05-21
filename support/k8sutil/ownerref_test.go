package k8sutil

import (
	"testing"

	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestEnsureOwnerRef(t *testing.T) {
	t.Run("When no owner refs exist it should add the new one", func(t *testing.T) {
		g := NewWithT(t)
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "test"}}
		ref := &metav1.OwnerReference{
			APIVersion: "v1",
			Kind:       "Namespace",
			Name:       "default",
			UID:        types.UID("abc"),
		}

		EnsureOwnerRef(cm, ref)
		g.Expect(cm.GetOwnerReferences()).To(HaveLen(1))
		g.Expect(cm.GetOwnerReferences()[0].Name).To(Equal("default"))
	})

	t.Run("When matching owner ref exists it should update it", func(t *testing.T) {
		g := NewWithT(t)
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test",
				OwnerReferences: []metav1.OwnerReference{
					{APIVersion: "v1", Kind: "Namespace", Name: "default", UID: types.UID("abc")},
				},
			},
		}
		ref := &metav1.OwnerReference{
			APIVersion: "v1",
			Kind:       "Namespace",
			Name:       "default",
			UID:        types.UID("abc"),
			Controller: boolPtr(true),
		}

		EnsureOwnerRef(cm, ref)
		g.Expect(cm.GetOwnerReferences()).To(HaveLen(1))
		g.Expect(*cm.GetOwnerReferences()[0].Controller).To(BeTrue())
	})

	t.Run("When ownerRef is nil it should be a no-op", func(t *testing.T) {
		g := NewWithT(t)
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "test"}}

		EnsureOwnerRef(cm, nil)
		g.Expect(cm.GetOwnerReferences()).To(BeEmpty())
	})

	t.Run("When owner ref is idempotent it should not duplicate", func(t *testing.T) {
		g := NewWithT(t)
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "test"}}
		ref := &metav1.OwnerReference{
			APIVersion: "v1",
			Kind:       "Namespace",
			Name:       "default",
			UID:        types.UID("abc"),
		}

		EnsureOwnerRef(cm, ref)
		EnsureOwnerRef(cm, ref)
		g.Expect(cm.GetOwnerReferences()).To(HaveLen(1))
	})
}

func boolPtr(b bool) *bool {
	return &b
}
