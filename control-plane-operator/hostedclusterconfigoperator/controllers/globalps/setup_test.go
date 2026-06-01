package globalps

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func Test_kubeSystemSecretPredicateFunc(t *testing.T) {
	tests := []struct {
		name   string
		object *corev1.Secret
		want   bool
	}{
		{
			name: "When secret is in kube-system it should return true",
			object: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Namespace: "kube-system", Name: "any-secret"},
			},
			want: true,
		},
		{
			name: "When secret is in a different namespace it should return false",
			object: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Namespace: "openshift-config", Name: "pull-secret"},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(kubeSystemSecretPredicateFunc(tt.object)).To(Equal(tt.want))
		})
	}
}

func Test_namespacedNamePredicateFunc(t *testing.T) {
	predicate := namespacedNamePredicateFunc("my-hcp-namespace", "pull-secret")

	tests := []struct {
		name   string
		object *corev1.Secret
		want   bool
	}{
		{
			name: "When namespace and name match it should return true",
			object: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Namespace: "my-hcp-namespace", Name: "pull-secret"},
			},
			want: true,
		},
		{
			name: "When namespace differs it should return false",
			object: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Namespace: "other-namespace", Name: "pull-secret"},
			},
			want: false,
		},
		{
			name: "When name differs it should return false",
			object: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Namespace: "my-hcp-namespace", Name: "other-secret"},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(predicate(tt.object)).To(Equal(tt.want))
		})
	}
}

func Test_staticReconcileMapper(t *testing.T) {
	t.Run("When called it should return a single empty reconcile request", func(t *testing.T) {
		g := NewWithT(t)
		requests := staticReconcileMapper(context.Background(), &corev1.Secret{})
		g.Expect(requests).To(HaveLen(1))
		g.Expect(requests[0].NamespacedName.String()).To(Equal("/"))
	})
}
