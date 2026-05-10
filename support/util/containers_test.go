package util

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

func TestIsPodReady(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name     string
		pod      *corev1.Pod
		expected bool
	}{
		{
			name:     "When pod is nil it should return false",
			pod:      nil,
			expected: false,
		},
		{
			name: "When pod has Ready=True it should return true",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{{
						Type:   corev1.PodReady,
						Status: corev1.ConditionTrue,
					}},
				},
			},
			expected: true,
		},
		{
			name: "When pod has Ready=False it should return false",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{{
						Type:   corev1.PodReady,
						Status: corev1.ConditionFalse,
					}},
				},
			},
			expected: false,
		},
		{
			name: "When pod has no Ready condition it should return false",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{{
						Type:   corev1.PodInitialized,
						Status: corev1.ConditionTrue,
					}},
				},
			},
			expected: false,
		},
		{
			name: "When pod has no conditions it should return false",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{},
			},
			expected: false,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			g.Expect(IsPodReady(testCase.pod)).To(Equal(testCase.expected))
		})
	}
}

func TestContainerPort(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name         string
		pod          *corev1.Pod
		portName     string
		defaultPort  int32
		expectedPort int32
	}{
		{
			name: "When named port is found it should return the container port",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Ports: []corev1.ContainerPort{{
							Name:          "client",
							ContainerPort: 6443,
						}},
					}},
				},
			},
			portName:     "client",
			defaultPort:  8443,
			expectedPort: 6443,
		},
		{
			name: "When named port is missing it should return the default port",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Ports: []corev1.ContainerPort{{
							Name:          "metrics",
							ContainerPort: 9090,
						}},
					}},
				},
			},
			portName:     "client",
			defaultPort:  6443,
			expectedPort: 6443,
		},
		{
			name: "When container has no ports it should return the default port",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name: "kube-apiserver",
					}},
				},
			},
			portName:     "client",
			defaultPort:  6443,
			expectedPort: 6443,
		},
		{
			name: "When port is in a non-first container it should return the container port",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "sidecar",
							Ports: []corev1.ContainerPort{{
								Name:          "metrics",
								ContainerPort: 9090,
							}},
						},
						{
							Name: "kube-apiserver",
							Ports: []corev1.ContainerPort{{
								Name:          "client",
								ContainerPort: 7443,
							}},
						},
					},
				},
			},
			portName:     "client",
			defaultPort:  6443,
			expectedPort: 7443,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			g.Expect(ContainerPort(testCase.pod, testCase.portName, testCase.defaultPort)).To(Equal(testCase.expectedPort))
		})
	}
}
