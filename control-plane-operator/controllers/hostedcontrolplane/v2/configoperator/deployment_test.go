package configoperator

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	"github.com/openshift/hypershift/support/config"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/upsert"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type fakeReleaseImageProvider struct{}

func (fakeReleaseImageProvider) GetImage(string) string {
	return "quay.io/example/image:latest"
}

func (fakeReleaseImageProvider) ImageExist(string) (string, bool) {
	return "quay.io/example/image:latest", true
}

func (fakeReleaseImageProvider) Version() string {
	return "test-version"
}

func (fakeReleaseImageProvider) ComponentVersions() (map[string]string, error) {
	return map[string]string{"kubernetes": "test-kubernetes-version"}, nil
}

func (fakeReleaseImageProvider) ComponentImages() map[string]string {
	return map[string]string{}
}

func TestAdaptDeploymentIBMCloudControllerSelection(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	deployment := &appsv1.Deployment{
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: ComponentName}},
				},
			},
		},
	}
	hcp := &hyperv1.HostedControlPlane{
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{Type: hyperv1.IBMCloudPlatform},
		},
	}

	err := (&hcco{}).adaptDeployment(controlplanecomponent.WorkloadContext{
		HCP:                  hcp,
		ReleaseImageProvider: fakeReleaseImageProvider{},
	}, deployment)
	g.Expect(err).ToNot(HaveOccurred())

	g.Expect(deployment.Spec.Template.Spec.Containers[0].Command).To(ContainElement(
		"--controllers=controller-manager-ca,resources,user-ca-bundle,inplaceupgrader,drainer,hcpstatus",
	))
}

func TestNewComponent(t *testing.T) {
	t.Parallel()

	g := NewWithT(t)
	component := NewComponent(nil, nil, nil)
	g.Expect(component).ToNot(BeNil())
	g.Expect(component.Name()).To(Equal(ComponentName))

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform:     hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform},
			ReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.16.10-x86_64",
		},
	}
	kubeAPIServerComponent := &hyperv1.ControlPlaneComponent{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-apiserver",
			Namespace: hcp.Namespace,
		},
		Status: hyperv1.ControlPlaneComponentStatus{
			Version: "test-version",
			Conditions: []metav1.Condition{
				{Type: string(hyperv1.ControlPlaneComponentAvailable), Status: metav1.ConditionTrue},
				{Type: string(hyperv1.ControlPlaneComponentRolloutComplete), Status: metav1.ConditionTrue},
			},
		},
	}
	cpContext := controlplanecomponent.ControlPlaneContext{
		Context:                        t.Context(),
		HCP:                            hcp,
		Client:                         fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(kubeAPIServerComponent).Build(),
		ApplyProvider:                  upsert.NewApplyProvider(false),
		ReleaseImageProvider:           fakeReleaseImageProvider{},
		SkipPredicate:                  true,
		SkipCertificateSigning:         true,
		OmitOwnerReference:             true,
		NativeSidecarContainersEnabled: true,
	}
	g.Expect(component.Reconcile(cpContext)).To(Succeed())

	deployment := &appsv1.Deployment{}
	g.Expect(cpContext.Client.Get(t.Context(), client.ObjectKey{Namespace: hcp.Namespace, Name: ComponentName}, deployment)).To(Succeed())

	var tokenMinter *corev1.Container
	for i := range deployment.Spec.Template.Spec.InitContainers {
		if deployment.Spec.Template.Spec.InitContainers[i].Name == "cloud-token-minter" {
			tokenMinter = &deployment.Spec.Template.Spec.InitContainers[i]
			break
		}
	}
	g.Expect(tokenMinter).ToNot(BeNil())
	g.Expect(tokenMinter.VolumeMounts).To(ContainElement(corev1.VolumeMount{
		Name:      "cloud-token",
		MountPath: config.CloudTokenMountPath,
	}))

	var cloudTokenVolume *corev1.Volume
	for i := range deployment.Spec.Template.Spec.Volumes {
		if deployment.Spec.Template.Spec.Volumes[i].Name == "cloud-token" {
			cloudTokenVolume = &deployment.Spec.Template.Spec.Volumes[i]
			break
		}
	}
	g.Expect(cloudTokenVolume).ToNot(BeNil())
	g.Expect(cloudTokenVolume.EmptyDir).ToNot(BeNil())
	g.Expect(cloudTokenVolume.EmptyDir.Medium).To(Equal(corev1.StorageMediumMemory))
	g.Expect(deployment.Spec.Template.Spec.Containers[0].VolumeMounts).To(ContainElement(corev1.VolumeMount{
		Name:      "cloud-token",
		MountPath: config.CloudTokenMountPath,
	}))
	g.Expect(deployment.Spec.Template.Annotations["cluster-autoscaler.kubernetes.io/safe-to-evict-local-volumes"]).To(Equal("tmp-dir"))
}

func TestIsExternalInfraKubevirt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		hcp      *hyperv1.HostedControlPlane
		expected bool
	}{
		{
			name: "When HCP has no kubevirt platform, it should return false",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Kubevirt: nil,
					},
				},
			},
			expected: false,
		},
		{
			name: "When kubevirt platform has no credentials, it should return false",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Kubevirt: &hyperv1.KubevirtPlatformSpec{
							Credentials: nil,
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "When kubevirt credentials have no InfraKubeConfigSecret, it should return false",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Kubevirt: &hyperv1.KubevirtPlatformSpec{
							Credentials: &hyperv1.KubevirtPlatformCredentials{
								InfraKubeConfigSecret: nil,
								InfraNamespace:        "infra-ns",
							},
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "When kubevirt credentials have InfraKubeConfigSecret but empty InfraNamespace, it should return false",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Kubevirt: &hyperv1.KubevirtPlatformSpec{
							Credentials: &hyperv1.KubevirtPlatformCredentials{
								InfraKubeConfigSecret: &hyperv1.KubeconfigSecretRef{
									Name: "infra-kubeconfig",
									Key:  "kubeconfig",
								},
								InfraNamespace: "",
							},
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "When kubevirt credentials have both InfraKubeConfigSecret and InfraNamespace, it should return true",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Kubevirt: &hyperv1.KubevirtPlatformSpec{
							Credentials: &hyperv1.KubevirtPlatformCredentials{
								InfraKubeConfigSecret: &hyperv1.KubeconfigSecretRef{
									Name: "infra-kubeconfig",
									Key:  "kubeconfig",
								},
								InfraNamespace: "infra-ns",
							},
						},
					},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			result := isExternalInfraKubevirt(tt.hcp)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}
