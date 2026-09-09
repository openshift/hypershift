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

func ValidateGCPWorkloadIdentityWebhookMutation(t testing.TB, ctx context.Context, hostedClusterClient crclient.Client) {
	g := NewWithT(t)

	nsName := fmt.Sprintf("gcp-wif-e2e-%d", time.Now().UnixNano())
	testNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: nsName}}
	g.Expect(hostedClusterClient.Create(ctx, testNamespace)).To(Succeed(), "failed to create test namespace")
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := hostedClusterClient.Delete(cleanupCtx, testNamespace); err != nil && !apierrors.IsNotFound(err) {
			t.Logf("failed to delete test namespace %s: %v", testNamespace.Name, err)
		}
	}()

	serviceAccount := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gcp-wif-test-sa",
			Namespace: nsName,
			Annotations: map[string]string{
				"cloud.google.com/workload-identity-provider": "projects/000000000000/locations/global/workloadIdentityPools/test/providers/test",
				"cloud.google.com/service-account-email":      "test@test-project.iam.gserviceaccount.com",
				"cloud.google.com/injection-mode":             "direct",
			},
		},
	}
	g.Expect(hostedClusterClient.Create(ctx, serviceAccount)).To(Succeed(), "failed to create test service account")

	podTemplate := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gcp-wif-webhook-test-pod",
			Namespace: nsName,
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

	// The WIF webhook is a MutatingAdmissionWebhook with FailurePolicy: Ignore
	// that only fires on pod CREATE. If the webhook sidecar isn't ready when
	// the pod is created, the pod is admitted without mutation and no amount
	// of GET polling can recover. Delete and recreate each iteration to
	// re-trigger admission until the webhook is ready.
	g.Eventually(func(g Gomega) {
		existing := &corev1.Pod{}
		err := hostedClusterClient.Get(ctx, types.NamespacedName{Name: podTemplate.Name, Namespace: podTemplate.Namespace}, existing)
		if err == nil {
			g.Expect(hostedClusterClient.Delete(ctx, existing)).To(Succeed(), "failed to delete pod for retry")
			g.Eventually(func() bool {
				err := hostedClusterClient.Get(ctx, types.NamespacedName{Name: podTemplate.Name, Namespace: podTemplate.Namespace}, &corev1.Pod{})
				return apierrors.IsNotFound(err)
			}).WithContext(ctx).WithTimeout(30*time.Second).WithPolling(time.Second).Should(BeTrue(), "pod should be deleted before retry")
		} else {
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "unexpected error getting existing pod: %v", err)
		}

		fresh := podTemplate.DeepCopy()
		g.Expect(hostedClusterClient.Create(ctx, fresh)).To(Succeed(), "failed to create pod for webhook mutation test")

		mutated := &corev1.Pod{}
		g.Expect(hostedClusterClient.Get(ctx, types.NamespacedName{Name: fresh.Name, Namespace: fresh.Namespace}, mutated)).To(Succeed(), "failed to get pod after webhook mutation")
		g.Expect(hasGCPProjectedTokenVolume(mutated.Spec.Volumes)).To(BeTrue(), "expected projected service account token volume to be injected")
		g.Expect(hasGCPCredentialEnv(mutated.Spec.Containers)).To(BeTrue(), "expected GOOGLE_APPLICATION_CREDENTIALS env var in pod containers")
	}).WithContext(ctx).WithTimeout(3*time.Minute).WithPolling(10*time.Second).Should(Succeed(), "pod should be mutated with GCP workload identity credentials")
}

func hasGCPProjectedTokenVolume(volumes []corev1.Volume) bool {
	for _, volume := range volumes {
		if volume.Name != "gcp-iam-token" || volume.Projected == nil {
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

func hasGCPCredentialEnv(containers []corev1.Container) bool {
	for _, container := range containers {
		for _, env := range container.Env {
			if env.Name == "GOOGLE_APPLICATION_CREDENTIALS" && strings.HasPrefix(env.Value, "/var/run/secrets/workload-identity/") {
				return true
			}
		}
	}
	return false
}
