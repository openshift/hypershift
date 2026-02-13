package assets

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

// assertContainersHaveResourceRequests validates that all containers have CPU and memory resource requests.
func assertContainersHaveResourceRequests(t *testing.T, containers []corev1.Container, deploymentName string) {
	t.Helper()
	for _, container := range containers {
		if container.Resources.Requests == nil {
			t.Errorf("container %s in %s deployment is missing resource requests", container.Name, deploymentName)
			continue
		}

		if _, hasCPU := container.Resources.Requests[corev1.ResourceCPU]; !hasCPU {
			t.Errorf("container %s in %s deployment is missing CPU resource request", container.Name, deploymentName)
		}

		if _, hasMemory := container.Resources.Requests[corev1.ResourceMemory]; !hasMemory {
			t.Errorf("container %s in %s deployment is missing memory resource request", container.Name, deploymentName)
		}
	}
}

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

	assertContainersHaveResourceRequests(t, deployment.Spec.Template.Spec.Containers, "HyperShift operator")
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

	assertContainersHaveResourceRequests(t, deployment.Spec.Template.Spec.Containers, "external-dns")
}
