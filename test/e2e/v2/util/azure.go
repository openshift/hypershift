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

// ValidateAzureWorkloadIdentityWebhookMutation verifies that the Azure workload
// identity webhook injects the projected token volume and federated-token
// environment variable into a newly created pod.
func ValidateAzureWorkloadIdentityWebhookMutation(ctx context.Context, guestClient crclient.Client) error {
	nsName := fmt.Sprintf("azure-wi-e2e-%d", time.Now().UnixNano())
	testNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: nsName}}
	if err := guestClient.Create(ctx, testNamespace); err != nil {
		return fmt.Errorf("failed to create test namespace: %w", err)
	}
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
	if err := guestClient.Create(ctx, serviceAccount); err != nil {
		return fmt.Errorf("failed to create test service account: %w", err)
	}

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
	err := waitForAzureWorkloadIdentityMutation(ctx, guestClient, podTemplate)
	if err != nil {
		return err
	}
	return nil
}

func waitForAzureWorkloadIdentityMutation(ctx context.Context, guestClient crclient.Client, podTemplate *corev1.Pod) error {
	err := wait.PollUntilContextTimeout(ctx, 10*time.Second, 3*time.Minute, true, func(ctx context.Context) (bool, error) {
		existing := &corev1.Pod{}
		err := guestClient.Get(ctx, types.NamespacedName{Name: podTemplate.Name, Namespace: podTemplate.Namespace}, existing)
		if err == nil {
			if err := guestClient.Delete(ctx, existing); err != nil {
				GinkgoWriter.Printf("failed to delete pod for retry: %v\n", err)
				return false, nil
			}
			if err := wait.PollUntilContextTimeout(ctx, time.Second, 30*time.Second, true, func(ctx context.Context) (bool, error) {
				err := guestClient.Get(ctx, types.NamespacedName{Name: podTemplate.Name, Namespace: podTemplate.Namespace}, &corev1.Pod{})
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
		if err := guestClient.Create(ctx, fresh); err != nil {
			GinkgoWriter.Printf("failed to create pod for webhook mutation test: %v\n", err)
			return false, nil
		}

		mutated := &corev1.Pod{}
		if err := guestClient.Get(ctx, types.NamespacedName{Name: fresh.Name, Namespace: fresh.Namespace}, mutated); err != nil {
			GinkgoWriter.Printf("failed to get mutated pod: %v\n", err)
			return false, nil
		}
		if !hasProjectedTokenVolume(mutated.Spec.Volumes) {
			GinkgoWriter.Println("expected projected service account token volume to be injected")
			return false, nil
		}
		if !hasAzureFederatedTokenEnv(mutated.Spec.Containers) {
			GinkgoWriter.Println("expected AZURE_FEDERATED_TOKEN_FILE env var in pod containers")
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("failed to validate Azure workload identity webhook mutation: %w", err)
	}
	return nil
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
