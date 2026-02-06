package assets

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

// TestHyperShiftOperatorDeployment_HasResourceRequests verifies that the HyperShift operator deployment
// has CPU and memory resource requests defined for all containers.
func TestHyperShiftOperatorDeployment_HasResourceRequests(t *testing.T) {
	namespace := HyperShiftNamespace{Name: "hypershift"}.Build()
	serviceAccount := HyperShiftOperatorServiceAccount{Namespace: namespace}.Build()

	operatorDeployment := HyperShiftOperatorDeployment{
		Namespace:      namespace,
		OperatorImage:  "test-image",
		ServiceAccount: serviceAccount,
		Replicas:       1,
	}

	deployment := operatorDeployment.Build()

	if len(deployment.Spec.Template.Spec.Containers) == 0 {
		t.Fatal("HyperShift operator deployment has no containers")
	}

	for _, container := range deployment.Spec.Template.Spec.Containers {
		if container.Resources.Requests == nil {
			t.Errorf("container %s in HyperShift operator deployment missing resource requests", container.Name)
			continue
		}

		if _, hasCPU := container.Resources.Requests[corev1.ResourceCPU]; !hasCPU {
			t.Errorf("container %s in HyperShift operator deployment missing CPU resource request", container.Name)
		}

		if _, hasMemory := container.Resources.Requests[corev1.ResourceMemory]; !hasMemory {
			t.Errorf("container %s in HyperShift operator deployment missing memory resource request", container.Name)
		}
	}
}

// TestExternalDNSDeployment_HasResourceRequests verifies that the external-dns deployment
// has CPU and memory resource requests defined for all containers.
func TestExternalDNSDeployment_HasResourceRequests(t *testing.T) {
	namespace := HyperShiftNamespace{Name: "hypershift"}.Build()
	serviceAccount := ExternalDNSServiceAccount{Namespace: namespace}.Build()

	externalDNS := ExternalDNSDeployment{
		Namespace:      namespace,
		Image:          "test-image",
		ServiceAccount: serviceAccount,
		Provider:       AWSExternalDNSProvider,
		DomainFilter:   "example.com",
	}

	deployment := externalDNS.Build()

	if len(deployment.Spec.Template.Spec.Containers) == 0 {
		t.Fatal("external-dns deployment has no containers")
	}

	for _, container := range deployment.Spec.Template.Spec.Containers {
		if container.Resources.Requests == nil {
			t.Errorf("container %s in external-dns deployment missing resource requests", container.Name)
			continue
		}

		if _, hasCPU := container.Resources.Requests[corev1.ResourceCPU]; !hasCPU {
			t.Errorf("container %s in external-dns deployment missing CPU resource request", container.Name)
		}

		if _, hasMemory := container.Resources.Requests[corev1.ResourceMemory]; !hasMemory {
			t.Errorf("container %s in external-dns deployment missing memory resource request", container.Name)
		}
	}
}
