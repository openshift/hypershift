//go:build e2ev2

/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package util

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// ValidateGCPWorkloadIdentityWebhookMutation verifies that the GCP workload
// identity webhook injects the projected token volume and credential environment
// variable into a newly created pod.
func ValidateGCPWorkloadIdentityWebhookMutation(ctx context.Context, hostedClusterClient crclient.Client) error {
	nsName := fmt.Sprintf("gcp-wif-e2e-%d", time.Now().UnixNano())
	testNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: nsName}}
	if err := hostedClusterClient.Create(ctx, testNamespace); err != nil {
		return fmt.Errorf("failed to create test namespace: %w", err)
	}
	defer func() {
		_ = hostedClusterClient.Delete(context.Background(), testNamespace)
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
	if err := hostedClusterClient.Create(ctx, serviceAccount); err != nil {
		return fmt.Errorf("failed to create test service account: %w", err)
	}

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

	if err := waitForGCPWorkloadIdentityMutation(ctx, hostedClusterClient, podTemplate); err != nil {
		return err
	}
	return nil
}

func waitForGCPWorkloadIdentityMutation(ctx context.Context, hostedClusterClient crclient.Client, podTemplate *corev1.Pod) error {
	podKey := types.NamespacedName{Name: podTemplate.Name, Namespace: podTemplate.Namespace}
	err := wait.PollUntilContextTimeout(ctx, 10*time.Second, 3*time.Minute, true, func(ctx context.Context) (bool, error) {
		existing := &corev1.Pod{}
		err := hostedClusterClient.Get(ctx, podKey, existing)
		if err == nil {
			if err := hostedClusterClient.Delete(ctx, existing); err != nil {
				GinkgoWriter.Printf("failed to delete pod for retry: %v\n", err)
				return false, nil
			}
			if err := wait.PollUntilContextTimeout(ctx, time.Second, 30*time.Second, true, func(ctx context.Context) (bool, error) {
				err := hostedClusterClient.Get(ctx, podKey, &corev1.Pod{})
				return apierrors.IsNotFound(err), nil
			}); err != nil {
				GinkgoWriter.Printf("pod was not deleted before retry: %v\n", err)
				return false, nil
			}
		} else if !apierrors.IsNotFound(err) {
			GinkgoWriter.Printf("unexpected error getting existing pod: %v\n", err)
			return false, nil
		}

		fresh := podTemplate.DeepCopy()
		if err := hostedClusterClient.Create(ctx, fresh); err != nil {
			GinkgoWriter.Printf("failed to create pod for webhook mutation test: %v\n", err)
			return false, nil
		}

		mutated := &corev1.Pod{}
		if err := hostedClusterClient.Get(ctx, podKey, mutated); err != nil {
			GinkgoWriter.Printf("failed to get mutated pod: %v\n", err)
			return false, nil
		}
		if !hasGCPProjectedTokenVolume(mutated.Spec.Volumes) {
			GinkgoWriter.Println("expected projected service account token volume to be injected")
			return false, nil
		}
		if !hasGCPCredentialEnv(mutated.Spec.Containers) {
			GinkgoWriter.Println("expected GOOGLE_APPLICATION_CREDENTIALS env var in pod containers")
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("failed to validate GCP workload identity webhook mutation: %w", err)
	}
	return nil
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
