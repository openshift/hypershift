package capiprovider

import (
	"slices"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/upsert"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestPredicate(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name           string
		hcpAnnotations map[string]string
		expected       bool
	}{
		{
			name:     "When HCP has no annotations, it should return true",
			expected: true,
		},
		{
			name: "When HCP has DisableMachineManagement annotation, it should return false",
			hcpAnnotations: map[string]string{
				hyperv1.DisableMachineManagement: "true",
			},
			expected: false,
		},
		{
			name: "When HCP has other annotations but not DisableMachineManagement, it should return true",
			hcpAnnotations: map[string]string{
				"some.other/annotation": "value",
			},
			expected: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-hcp",
					Namespace:   "test-namespace",
					Annotations: tc.hcpAnnotations,
				},
			}

			cpContext := component.WorkloadContext{
				Context: t.Context(),
				HCP:     hcp,
			}

			result, err := predicate(cpContext)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(tc.expected))
		})
	}
}

func TestCAPIProviderOptions_IsRequestServing(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	capi := &CAPIProviderOptions{}
	g.Expect(capi.IsRequestServing()).To(BeFalse())
}

func TestCAPIProviderOptions_MultiZoneSpread(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	capi := &CAPIProviderOptions{}
	g.Expect(capi.MultiZoneSpread()).To(BeFalse())
}

func TestCAPIProviderOptions_NeedsManagementKASAccess(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	capi := &CAPIProviderOptions{}
	g.Expect(capi.NeedsManagementKASAccess()).To(BeTrue())
}

func TestLabels(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	result := labels()

	g.Expect(result).To(HaveKeyWithValue("control-plane", "capi-provider-controller-manager"))
	g.Expect(result).To(HaveKeyWithValue("app", "capi-provider-controller-manager"))
	g.Expect(result).To(HaveLen(2))
}

func TestNewComponent(t *testing.T) {
	t.Parallel()

	const (
		hcpNamespace   = "clusters-example"
		tokenDir       = "/var/run/secrets/openshift/serviceaccount"
		tokenFile      = tokenDir + "/token"
		minterName     = "cloud-token-minter"
		utilitiesImage = "quay.io/openshift/origin-hypershift:4.18.0"
	)

	testCases := []struct {
		name                 string
		platformType         hyperv1.PlatformType
		nativeSidecars       bool
		expectMinter         bool
		expectedMinterArgs   []string
		expectedMinterMounts []corev1.VolumeMount
	}{
		{
			name:           "When the platform is AWS and native sidecars are supported, it should run the shared cloud token minter as a native sidecar after the availability prober",
			platformType:   hyperv1.AWSPlatform,
			nativeSidecars: true,
			expectMinter:   true,
			expectedMinterArgs: []string{
				"--token-audience=openshift",
				"--service-account-namespace=kube-system",
				"--service-account-name=capa-controller-manager",
				"--token-file=" + tokenFile,
				"--kubeconfig=/etc/kubernetes/kubeconfig",
			},
			expectedMinterMounts: []corev1.VolumeMount{
				{Name: "cloud-token", MountPath: tokenDir},
				{Name: "svc-kubeconfig", MountPath: "/etc/kubernetes"},
				{Name: "tmp-dir", MountPath: "/tmp"},
			},
		},
		{
			name:           "When the platform is AWS and native sidecars are not supported, it should run the shared cloud token minter as a regular sidecar",
			platformType:   hyperv1.AWSPlatform,
			nativeSidecars: false,
			expectMinter:   true,
			expectedMinterArgs: []string{
				"--token-audience=openshift",
				"--service-account-namespace=kube-system",
				"--service-account-name=capa-controller-manager",
				"--token-file=" + tokenFile,
				"--kubeconfig=/etc/kubernetes/kubeconfig",
			},
			expectedMinterMounts: []corev1.VolumeMount{
				{Name: "cloud-token", MountPath: tokenDir},
				{Name: "svc-kubeconfig", MountPath: "/etc/kubernetes"},
				{Name: "tmp-dir", MountPath: "/tmp"},
			},
		},
		{
			name:           "When the platform is GCP and native sidecars are supported, it should run the cloud token minter as a native sidecar after the availability prober",
			platformType:   hyperv1.GCPPlatform,
			nativeSidecars: true,
			expectMinter:   true,
			expectedMinterArgs: []string{
				"--token-audience=openshift",
				"--service-account-namespace=kube-system",
				"--service-account-name=capi-gcp-controller-manager",
				"--token-file=" + tokenFile,
				"--kubeconfig-secret-namespace=" + hcpNamespace,
				"--kubeconfig-secret-name=service-network-admin-kubeconfig",
			},
			expectedMinterMounts: []corev1.VolumeMount{
				{Name: "cloud-token", MountPath: tokenDir},
				{Name: "tmp-dir", MountPath: "/tmp"},
			},
		},
		{
			name:           "When the platform is GCP and native sidecars are not supported, it should run the cloud token minter as a regular sidecar",
			platformType:   hyperv1.GCPPlatform,
			nativeSidecars: false,
			expectMinter:   true,
			expectedMinterArgs: []string{
				"--token-audience=openshift",
				"--service-account-namespace=kube-system",
				"--service-account-name=capi-gcp-controller-manager",
				"--token-file=" + tokenFile,
				"--kubeconfig-secret-namespace=" + hcpNamespace,
				"--kubeconfig-secret-name=service-network-admin-kubeconfig",
			},
			expectedMinterMounts: []corev1.VolumeMount{
				{Name: "cloud-token", MountPath: tokenDir},
				{Name: "tmp-dir", MountPath: "/tmp"},
			},
		},
		{
			name:           "When the platform does not use cloud tokens, it should not inject a cloud token minter",
			platformType:   hyperv1.AgentPlatform,
			nativeSidecars: true,
			expectMinter:   false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "example",
					Namespace: hcpNamespace,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: tc.platformType,
					},
				},
			}
			cpContext := component.ControlPlaneContext{
				Context:       t.Context(),
				Client:        fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
				ApplyProvider: upsert.NewApplyProvider(false),
				HCP:           hcp,
				ReleaseImageProvider: testutil.FakeImageProvider(testutil.WithImages(map[string]string{
					"token-minter":        utilitiesImage,
					"availability-prober": utilitiesImage,
				})),
				OmitOwnerReference:             true,
				NativeSidecarContainersEnabled: tc.nativeSidecars,
			}

			capiProvider := NewComponent(testCAPIProviderDeploymentSpec(tc.platformType), nil, tc.platformType)
			// Reconcile twice to make sure repeated runs do not accumulate injected mounts or containers.
			for range 2 {
				g.Expect(capiProvider.Reconcile(cpContext)).To(Succeed())
			}

			deployment := &appsv1.Deployment{}
			g.Expect(cpContext.Client.Get(t.Context(), client.ObjectKey{Namespace: hcpNamespace, Name: ComponentName}, deployment)).To(Succeed())
			podSpec := deployment.Spec.Template.Spec

			g.Expect(podSpec.Containers).ToNot(BeEmpty())
			manager := podSpec.Containers[0]
			g.Expect(manager.Name).To(Equal("manager"))
			for _, c := range append(slices.Clone(podSpec.InitContainers), podSpec.Containers...) {
				g.Expect(c.Name).ToNot(Equal("token-minter"), "the hand-written token minter should not be rendered")
				g.Expect(countMountsAt(c, "/tmp")).To(BeNumerically("<=", 1), "container %s should mount /tmp at most once", c.Name)
			}

			if !tc.expectMinter {
				g.Expect(containerNames(podSpec.InitContainers)).To(Equal([]string{"availability-prober"}))
				g.Expect(containerNames(podSpec.Containers)).To(Equal([]string{"manager"}))
				g.Expect(volumeNames(podSpec.Volumes)).ToNot(ContainElement("cloud-token"))
				return
			}

			var minter corev1.Container
			if tc.nativeSidecars {
				g.Expect(containerNames(podSpec.InitContainers)).To(Equal([]string{"availability-prober", minterName}))
				g.Expect(containerNames(podSpec.Containers)).To(Equal([]string{"manager"}))
				minter = podSpec.InitContainers[1]
				g.Expect(minter.RestartPolicy).To(HaveValue(Equal(corev1.ContainerRestartPolicyAlways)))
				g.Expect(minter.StartupProbe).ToNot(BeNil())
				g.Expect(minter.StartupProbe.Exec).ToNot(BeNil())
				g.Expect(minter.StartupProbe.Exec.Command).To(Equal([]string{"test", "-f", tokenFile}))
			} else {
				g.Expect(containerNames(podSpec.InitContainers)).To(Equal([]string{"availability-prober"}))
				g.Expect(containerNames(podSpec.Containers)).To(Equal([]string{"manager", minterName}))
				minter = podSpec.Containers[1]
				g.Expect(minter.RestartPolicy).To(BeNil())
				g.Expect(minter.StartupProbe).To(BeNil())
			}

			g.Expect(minter.Image).To(Equal(utilitiesImage))
			g.Expect(minter.Command).To(Equal([]string{"/usr/bin/control-plane-operator", "token-minter"}))
			g.Expect(minter.Args).To(ConsistOf(tc.expectedMinterArgs))
			g.Expect(minter.VolumeMounts).To(Equal(tc.expectedMinterMounts))
			g.Expect(minter.Resources.Requests).To(Equal(corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10m"),
				corev1.ResourceMemory: resource.MustParse("30Mi"),
			}))
			g.Expect(minter.SecurityContext).ToNot(BeNil())
			g.Expect(minter.SecurityContext.ReadOnlyRootFilesystem).To(HaveValue(BeTrue()))
			if tc.platformType == hyperv1.GCPPlatform {
				g.Expect(minter.SecurityContext.AllowPrivilegeEscalation).To(HaveValue(BeFalse()))
				g.Expect(minter.SecurityContext.RunAsNonRoot).To(HaveValue(BeTrue()))
			}

			// The manager reads the minted token through the shared volume at the path its credentials point to.
			g.Expect(manager.VolumeMounts).To(ContainElement(corev1.VolumeMount{Name: "cloud-token", MountPath: tokenDir}))
			g.Expect(countMountsAt(manager, tokenDir)).To(Equal(1))
			g.Expect(manager.VolumeMounts).To(ContainElements(testCAPIProviderDeploymentSpec(tc.platformType).Template.Spec.Containers[0].VolumeMounts))

			g.Expect(volumeNames(podSpec.Volumes)).ToNot(ContainElement("token"))
			tokenVolumes := 0
			for _, v := range podSpec.Volumes {
				if v.Name == "cloud-token" {
					tokenVolumes++
					g.Expect(v.EmptyDir).ToNot(BeNil())
					g.Expect(v.EmptyDir.Medium).To(Equal(corev1.StorageMediumMemory))
				}
			}
			g.Expect(tokenVolumes).To(Equal(1))
		})
	}
}

// testCAPIProviderDeploymentSpec returns a minimal provider deployment spec shaped like the ones the platforms render.
func testCAPIProviderDeploymentSpec(platformType hyperv1.PlatformType) *appsv1.DeploymentSpec {
	manager := corev1.Container{
		Name:  "manager",
		Image: "quay.io/openshift/origin-cluster-api-provider:4.18.0",
	}
	var volumes []corev1.Volume
	switch platformType {
	case hyperv1.AWSPlatform:
		manager.VolumeMounts = []corev1.VolumeMount{{Name: "credentials", MountPath: "/home/.aws"}}
		manager.Env = []corev1.EnvVar{{Name: "AWS_SHARED_CREDENTIALS_FILE", Value: "/home/.aws/credentials"}}
		volumes = []corev1.Volume{
			{Name: "credentials", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "node-management-creds"}}},
			{Name: "svc-kubeconfig", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "service-network-admin-kubeconfig"}}},
		}
	case hyperv1.GCPPlatform:
		manager.VolumeMounts = []corev1.VolumeMount{{Name: "credentials", MountPath: "/home/.gcp"}}
		volumes = []corev1.Volume{
			{Name: "credentials", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "capg-manager-bootstrap-credentials"}}},
		}
	}
	return &appsv1.DeploymentSpec{
		Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{manager},
				Volumes:    volumes,
			},
		},
	}
}

func containerNames(containers []corev1.Container) []string {
	names := make([]string, 0, len(containers))
	for _, c := range containers {
		names = append(names, c.Name)
	}
	return names
}

func volumeNames(volumes []corev1.Volume) []string {
	names := make([]string, 0, len(volumes))
	for _, v := range volumes {
		names = append(names, v.Name)
	}
	return names
}

func countMountsAt(container corev1.Container, mountPath string) int {
	count := 0
	for _, m := range container.VolumeMounts {
		if m.MountPath == mountPath {
			count++
		}
	}
	return count
}
