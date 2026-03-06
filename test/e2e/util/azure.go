package util

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func EnsureAzureWorkloadIdentityWebhookMutation(t *testing.T, ctx context.Context, guestClient crclient.Client) {
	t.Helper()
	g := NewWithT(t)

	nsName := fmt.Sprintf("azure-wi-e2e-%d", time.Now().UnixNano())
	testNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: nsName}}
	g.Expect(guestClient.Create(ctx, testNamespace)).To(Succeed(), "failed to create test namespace")

	serviceAccount := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "azure-wi-test-sa",
			Namespace: nsName,
			Annotations: map[string]string{
				"azure.workload.identity/client-id": "00000000-0000-0000-0000-000000000000",
			},
		},
	}
	g.Expect(guestClient.Create(ctx, serviceAccount)).To(Succeed(), "failed to create test service account")

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "azure-wi-webhook-test-pod",
			Namespace: nsName,
			Labels: map[string]string{
				"azure.workload.identity/use": "true",
			},
		},
		Spec: corev1.PodSpec{
			ServiceAccountName: serviceAccount.Name,
			Containers: []corev1.Container{
				{
					Name:    "app",
					Image:   "registry.k8s.io/pause:3.10",
					Command: []string{"/pause"},
				},
			},
			RestartPolicy: corev1.RestartPolicyNever,
		},
	}
	g.Expect(guestClient.Create(ctx, pod)).To(Succeed(), "failed to create pod for webhook mutation test")

	EventuallyObject(
		t,
		ctx,
		"Azure workload identity webhook to mutate test pod",
		func(ctx context.Context) (*corev1.Pod, error) {
			mutatedPod := &corev1.Pod{}
			err := guestClient.Get(ctx, types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace}, mutatedPod)
			return mutatedPod, err
		},
		[]Predicate[*corev1.Pod]{
			func(mutatedPod *corev1.Pod) (bool, string, error) {
				if hasProjectedTokenVolume(mutatedPod.Spec.Volumes) {
					return true, "", nil
				}
				return false, "expected projected service account token volume to be injected", nil
			},
			func(mutatedPod *corev1.Pod) (bool, string, error) {
				if hasAzureFederatedTokenEnv(mutatedPod.Spec.Containers) {
					return true, "", nil
				}
				return false, "expected AZURE_FEDERATED_TOKEN_FILE env var in pod containers", nil
			},
		},
		WithTimeout(3*time.Minute),
		WithInterval(5*time.Second),
	)
}

func hasProjectedTokenVolume(volumes []corev1.Volume) bool {
	for _, volume := range volumes {
		if volume.Projected == nil {
			continue
		}
		for _, source := range volume.Projected.Sources {
			if source.ServiceAccountToken != nil {
				return true
			}
		}
	}
	return false
}

func hasAzureFederatedTokenEnv(containers []corev1.Container) bool {
	for _, container := range containers {
		for _, env := range container.Env {
			if env.Name == "AZURE_FEDERATED_TOKEN_FILE" && strings.HasPrefix(env.Value, "/var/run/secrets/azure/tokens/") {
				return true
			}
		}
	}
	return false
}
