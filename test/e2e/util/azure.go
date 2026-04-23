package util

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func ValidateAzureWorkloadIdentityWebhookMutation(t testing.TB, ctx context.Context, guestClient crclient.Client) {
	g := NewWithT(t)

	nsName := fmt.Sprintf("azure-wi-e2e-%d", time.Now().UnixNano())
	testNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: nsName}}
	g.Expect(guestClient.Create(ctx, testNamespace)).To(Succeed(), "failed to create test namespace")
	defer func() {
		_ = guestClient.Delete(context.Background(), testNamespace)
	}()

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

	podTemplate := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "azure-wi-webhook-test-pod",
			Namespace: nsName,
			Labels: map[string]string{
				"azure.workload.identity/use": "true",
			},
		},
		Spec: corev1.PodSpec{
			ServiceAccountName: serviceAccount.Name,
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot: ptr.To(true),
				SeccompProfile: &corev1.SeccompProfile{
					Type: corev1.SeccompProfileTypeRuntimeDefault,
				},
			},
			Containers: []corev1.Container{
				{
					Name:    "app",
					Image:   "registry.k8s.io/pause:3.10",
					Command: []string{"/pause"},
					SecurityContext: &corev1.SecurityContext{
						AllowPrivilegeEscalation: ptr.To(false),
						Capabilities: &corev1.Capabilities{
							Drop: []corev1.Capability{"ALL"},
						},
					},
				},
			},
			RestartPolicy: corev1.RestartPolicyNever,
		},
	}

	// The WI webhook is a MutatingAdmissionWebhook with FailurePolicy: Ignore
	// that only fires on pod CREATE. If the webhook sidecar isn't ready when
	// the pod is created, the pod is admitted without mutation and no amount
	// of GET polling can recover. Delete and recreate each iteration to
	// re-trigger admission until the webhook is ready.
	g.Eventually(func(g Gomega) {
		existing := &corev1.Pod{}
		err := guestClient.Get(ctx, types.NamespacedName{Name: podTemplate.Name, Namespace: podTemplate.Namespace}, existing)
		if err == nil {
			g.Expect(guestClient.Delete(ctx, existing)).To(Succeed(), "failed to delete pod for retry")
			g.Eventually(func() bool {
				err := guestClient.Get(ctx, types.NamespacedName{Name: podTemplate.Name, Namespace: podTemplate.Namespace}, &corev1.Pod{})
				return apierrors.IsNotFound(err)
			}).WithContext(ctx).WithTimeout(30*time.Second).WithPolling(time.Second).Should(BeTrue(), "pod should be deleted before retry")
		} else {
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "unexpected error getting existing pod: %v", err)
		}

		fresh := podTemplate.DeepCopy()
		g.Expect(guestClient.Create(ctx, fresh)).To(Succeed(), "failed to create pod for webhook mutation test")

		mutated := &corev1.Pod{}
		g.Expect(guestClient.Get(ctx, types.NamespacedName{Name: fresh.Name, Namespace: fresh.Namespace}, mutated)).To(Succeed())
		g.Expect(hasProjectedTokenVolume(mutated.Spec.Volumes)).To(BeTrue(), "expected projected service account token volume to be injected")
		g.Expect(hasAzureFederatedTokenEnv(mutated.Spec.Containers)).To(BeTrue(), "expected AZURE_FEDERATED_TOKEN_FILE env var in pod containers")
	}).WithContext(ctx).WithTimeout(3 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
}

func EnsureAzureWorkloadIdentityWebhookMutation(t *testing.T, ctx context.Context, guestClient crclient.Client) {
	t.Run("EnsureAzureWorkloadIdentityWebhookMutation", func(t *testing.T) {
		AtLeast(t, Version420)
		ValidateAzureWorkloadIdentityWebhookMutation(t, ctx, guestClient)
	})
}

func hasProjectedTokenVolume(volumes []corev1.Volume) bool {
	for _, volume := range volumes {
		if volume.Name != "azure-identity-token" || volume.Projected == nil {
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
