package kas

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestApplyAzureWorkloadIdentityWebhookContainer(t *testing.T) {
	testCases := []struct {
		name        string
		hcp         *hyperv1.HostedControlPlane
		validatePod func(*GomegaWithT, *corev1.PodSpec)
	}{
		{
			name: "When applying the Azure webhook container it should add the sidecar with correct configuration",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-ns",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzurePlatformSpec{
							TenantID: "test-tenant-id",
							Cloud:    "AzurePublicCloud",
						},
					},
				},
			},
			validatePod: func(g *GomegaWithT, podSpec *corev1.PodSpec) {
				var webhookContainer *corev1.Container
				for i := range podSpec.Containers {
					if podSpec.Containers[i].Name == "azure-workload-identity-webhook" {
						webhookContainer = &podSpec.Containers[i]
						break
					}
				}
				g.Expect(webhookContainer).NotTo(BeNil(), "webhook container should exist")

				g.Expect(webhookContainer.Image).To(Equal("azure-workload-identity-webhook"))
				g.Expect(webhookContainer.Command).To(Equal([]string{"/usr/bin/azure-workload-identity-webhook"}))

				g.Expect(webhookContainer.Args).To(ContainElement("--webhook-cert-dir=/var/run/app/certs"))
				g.Expect(webhookContainer.Args).To(ContainElement("--health-addr=:9440"))
				g.Expect(webhookContainer.Args).To(ContainElement("--audience=api://AzureADTokenExchange"))
				g.Expect(webhookContainer.Args).To(ContainElement("--kubeconfig=/var/run/app/kubeconfig/kubeconfig"))
				g.Expect(webhookContainer.Args).To(ContainElement("--log-level=info"))
				g.Expect(webhookContainer.Args).To(ContainElement("--disable-cert-rotation"))
			},
		},
		{
			name: "When applying the Azure webhook container it should set AZURE_TENANT_ID and AZURE_ENVIRONMENT env vars",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-ns",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzurePlatformSpec{
							TenantID: "my-tenant-123",
							Cloud:    "AzureUSGovernmentCloud",
						},
					},
				},
			},
			validatePod: func(g *GomegaWithT, podSpec *corev1.PodSpec) {
				var webhookContainer *corev1.Container
				for i := range podSpec.Containers {
					if podSpec.Containers[i].Name == "azure-workload-identity-webhook" {
						webhookContainer = &podSpec.Containers[i]
						break
					}
				}
				g.Expect(webhookContainer).NotTo(BeNil())

				envMap := make(map[string]string)
				for _, e := range webhookContainer.Env {
					envMap[e.Name] = e.Value
				}
				g.Expect(envMap).To(HaveKeyWithValue("AZURE_TENANT_ID", "my-tenant-123"))
				g.Expect(envMap).To(HaveKeyWithValue("AZURE_ENVIRONMENT", "AzureUSGovernmentCloud"))
			},
		},
		{
			name: "When applying the Azure webhook container it should configure liveness and readiness probes",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-ns",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzurePlatformSpec{
							TenantID: "test-tenant",
							Cloud:    "AzurePublicCloud",
						},
					},
				},
			},
			validatePod: func(g *GomegaWithT, podSpec *corev1.PodSpec) {
				var webhookContainer *corev1.Container
				for i := range podSpec.Containers {
					if podSpec.Containers[i].Name == "azure-workload-identity-webhook" {
						webhookContainer = &podSpec.Containers[i]
						break
					}
				}
				g.Expect(webhookContainer).NotTo(BeNil())

				g.Expect(webhookContainer.LivenessProbe).NotTo(BeNil())
				g.Expect(webhookContainer.LivenessProbe.HTTPGet.Path).To(Equal("/healthz"))
				g.Expect(webhookContainer.LivenessProbe.HTTPGet.Port.IntValue()).To(Equal(9440))

				g.Expect(webhookContainer.ReadinessProbe).NotTo(BeNil())
				g.Expect(webhookContainer.ReadinessProbe.HTTPGet.Path).To(Equal("/readyz"))
				g.Expect(webhookContainer.ReadinessProbe.HTTPGet.Port.IntValue()).To(Equal(9440))
			},
		},
		{
			name: "When applying the Azure webhook container it should add serving cert and kubeconfig volumes",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-ns",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzurePlatformSpec{
							TenantID: "test-tenant",
							Cloud:    "AzurePublicCloud",
						},
					},
				},
			},
			validatePod: func(g *GomegaWithT, podSpec *corev1.PodSpec) {
				volumeNames := make(map[string]string)
				for _, v := range podSpec.Volumes {
					if v.Secret != nil {
						volumeNames[v.Name] = v.Secret.SecretName
					}
				}
				g.Expect(volumeNames).To(HaveKeyWithValue(
					azureWorkloadIdentityWebhookServingCertVolumeName,
					manifests.AzureWorkloadIdentityWebhookServingCert("").Name,
				))
				g.Expect(volumeNames).To(HaveKeyWithValue(
					azureWorkloadIdentityWebhookKubeconfigVolumeName,
					manifests.AzureWorkloadIdentityWebhookKubeconfig("").Name,
				))

				var webhookContainer *corev1.Container
				for i := range podSpec.Containers {
					if podSpec.Containers[i].Name == "azure-workload-identity-webhook" {
						webhookContainer = &podSpec.Containers[i]
						break
					}
				}
				g.Expect(webhookContainer).NotTo(BeNil())

				mountPaths := make(map[string]string)
				for _, vm := range webhookContainer.VolumeMounts {
					mountPaths[vm.Name] = vm.MountPath
				}
				g.Expect(mountPaths).To(HaveKeyWithValue(azureWorkloadIdentityWebhookServingCertVolumeName, "/var/run/app/certs"))
				g.Expect(mountPaths).To(HaveKeyWithValue(azureWorkloadIdentityWebhookKubeconfigVolumeName, "/var/run/app/kubeconfig"))
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			podSpec := &corev1.PodSpec{}
			applyAzureWorkloadIdentityWebhookContainer(podSpec, tc.hcp)
			tc.validatePod(g, podSpec)
		})
	}
}
