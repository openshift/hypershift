package util

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestValidateGCPWorkloadIdentityWebhookMutation(t *testing.T) {
	t.Run("When admission injects credentials, it should succeed and clean up the namespace", func(t *testing.T) {
		g := NewWithT(t)
		scheme := runtime.NewScheme()
		g.Expect(corev1.AddToScheme(scheme)).To(Succeed())
		podCreated := false
		hostedClient := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if pod, ok := obj.(*corev1.Pod); ok {
					podCreated = true
					pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
						Name: "gcp-iam-token",
						VolumeSource: corev1.VolumeSource{Projected: &corev1.ProjectedVolumeSource{
							Sources: []corev1.VolumeProjection{{ServiceAccountToken: &corev1.ServiceAccountTokenProjection{
								Audience: "openshift", Path: "token",
							}}},
						}},
					})
					pod.Spec.Containers[0].Env = append(pod.Spec.Containers[0].Env, corev1.EnvVar{
						Name: "GOOGLE_APPLICATION_CREDENTIALS", Value: "/var/run/secrets/workload-identity/credentials.json",
					})
				}
				return c.Create(ctx, obj, opts...)
			},
		}).Build()

		ValidateGCPWorkloadIdentityWebhookMutation(t, g, t.Context(), hostedClient)

		g.Expect(podCreated).To(BeTrue(), "expected the helper to create a pod")
		namespaces := &corev1.NamespaceList{}
		g.Expect(hostedClient.List(t.Context(), namespaces)).To(Succeed())
		g.Expect(namespaces.Items).To(BeEmpty(), "expected cleanup to delete the test namespace")
	})
}
