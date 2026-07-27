package podspec

import (
	"testing"

	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
)

func TestFindContainer(t *testing.T) {
	t.Parallel()
	containers := []corev1.Container{
		{Name: "first"},
		{Name: "second"},
		{Name: "third"},
	}

	t.Run("When the container exists, it should return a pointer to it", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		result := FindContainer("second", containers)
		g.Expect(result).ToNot(BeNil())
		g.Expect(result.Name).To(Equal("second"))
	})

	t.Run("When the container does not exist, it should return nil", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		g.Expect(FindContainer("nonexistent", containers)).To(BeNil())
	})

	t.Run("When the slice is empty, it should return nil", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		g.Expect(FindContainer("any", nil)).To(BeNil())
	})
}

func TestFindEnvVar(t *testing.T) {
	t.Parallel()
	envVars := []corev1.EnvVar{
		{Name: "FOO", Value: "bar"},
		{Name: "BAZ", Value: "qux"},
	}

	t.Run("When the env var exists, it should return a pointer to it", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		result := FindEnvVar("BAZ", envVars)
		g.Expect(result).ToNot(BeNil())
		g.Expect(result.Value).To(Equal("qux"))
	})

	t.Run("When the env var does not exist, it should return nil", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		g.Expect(FindEnvVar("MISSING", envVars)).To(BeNil())
	})

	t.Run("When the slice is empty, it should return nil", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		g.Expect(FindEnvVar("FOO", nil)).To(BeNil())
	})
}
