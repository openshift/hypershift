package k8sutil

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperapi "github.com/openshift/hypershift/support/api"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestSerializeDeserializeRoundTrip(t *testing.T) {
	t.Run("When serializing and deserializing a ConfigMap it should round-trip", func(t *testing.T) {
		g := NewWithT(t)
		original := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Data:       map[string]string{"key": "value"},
		}

		data, err := SerializeResource(original, hyperapi.Scheme)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(data).ToNot(BeEmpty())

		restored := &corev1.ConfigMap{}
		err = DeserializeResource(data, restored, hyperapi.Scheme)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(restored.Name).To(Equal("test"))
		g.Expect(restored.Data["key"]).To(Equal("value"))
	})
}

func TestSerializeResource(t *testing.T) {
	t.Run("When resource is valid it should produce YAML output", func(t *testing.T) {
		g := NewWithT(t)
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "test"},
		}

		data, err := SerializeResource(cm, hyperapi.Scheme)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(data).To(ContainSubstring("kind: ConfigMap"))
		g.Expect(data).To(ContainSubstring("name: test"))
	})

	t.Run("When resource type is unregistered it should return a GVK error", func(t *testing.T) {
		g := NewWithT(t)
		emptyScheme := runtime.NewScheme()
		cm := &corev1.ConfigMap{}
		_, err := SerializeResource(cm, emptyScheme)
		g.Expect(err).To(MatchError(ContainSubstring("cannot determine GVK")))
	})
}

func TestDeserializeResource(t *testing.T) {
	t.Run("When YAML is invalid it should return an error", func(t *testing.T) {
		g := NewWithT(t)
		cm := &corev1.ConfigMap{}
		err := DeserializeResource("not valid yaml: {{{", cm, hyperapi.Scheme)
		g.Expect(err).To(HaveOccurred())
	})

	t.Run("When resource type is unregistered it should return a GVK error", func(t *testing.T) {
		g := NewWithT(t)
		emptyScheme := runtime.NewScheme()
		cm := &corev1.ConfigMap{}
		err := DeserializeResource("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test", cm, emptyScheme)
		g.Expect(err).To(MatchError(ContainSubstring("cannot determine GVK")))
	})
}
