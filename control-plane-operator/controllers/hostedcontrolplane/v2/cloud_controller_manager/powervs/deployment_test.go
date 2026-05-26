package powervs

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAdaptDeployment(t *testing.T) {
	t.Parallel()

	t.Run("When cloud controller creds secret name is set, it should update volume secret name", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "test-namespace",
			},
			Spec: hyperv1.HostedControlPlaneSpec{
				Platform: hyperv1.PlatformSpec{
					Type: hyperv1.PowerVSPlatform,
					PowerVS: &hyperv1.PowerVSPlatformSpec{
						KubeCloudControllerCreds: corev1.LocalObjectReference{
							Name: "my-cloud-creds-secret",
						},
					},
				},
			},
		}

		deployment := &appsv1.Deployment{
			Spec: appsv1.DeploymentSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Volumes: []corev1.Volume{
							{
								Name: cloudCredsVolumeName,
								VolumeSource: corev1.VolumeSource{
									Secret: &corev1.SecretVolumeSource{
										SecretName: "original-secret",
									},
								},
							},
						},
					},
				},
			},
		}

		cpContext := component.WorkloadContext{
			HCP: hcp,
		}
		err := adaptDeployment(cpContext, deployment)
		g.Expect(err).ToNot(HaveOccurred())

		cloudCredsVol := podspec.FindVolume(cloudCredsVolumeName, deployment.Spec.Template.Spec.Volumes)
		g.Expect(cloudCredsVol).ToNot(BeNil(), "cloud-creds volume should exist")
		g.Expect(cloudCredsVol.Secret.SecretName).To(Equal("my-cloud-creds-secret"))
	})

	t.Run("When deployment has multiple volumes, it should only update cloud-creds volume", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "test-namespace",
			},
			Spec: hyperv1.HostedControlPlaneSpec{
				Platform: hyperv1.PlatformSpec{
					Type: hyperv1.PowerVSPlatform,
					PowerVS: &hyperv1.PowerVSPlatformSpec{
						KubeCloudControllerCreds: corev1.LocalObjectReference{
							Name: "updated-creds",
						},
					},
				},
			},
		}

		deployment := &appsv1.Deployment{
			Spec: appsv1.DeploymentSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Volumes: []corev1.Volume{
							{
								Name: "other-volume",
								VolumeSource: corev1.VolumeSource{
									Secret: &corev1.SecretVolumeSource{
										SecretName: "other-secret",
									},
								},
							},
							{
								Name: cloudCredsVolumeName,
								VolumeSource: corev1.VolumeSource{
									Secret: &corev1.SecretVolumeSource{
										SecretName: "old-secret",
									},
								},
							},
							{
								Name: "yet-another-volume",
								VolumeSource: corev1.VolumeSource{
									ConfigMap: &corev1.ConfigMapVolumeSource{
										LocalObjectReference: corev1.LocalObjectReference{
											Name: "some-configmap",
										},
									},
								},
							},
						},
					},
				},
			},
		}

		cpContext := component.WorkloadContext{
			HCP: hcp,
		}
		err := adaptDeployment(cpContext, deployment)
		g.Expect(err).ToNot(HaveOccurred())

		g.Expect(deployment.Spec.Template.Spec.Volumes).To(HaveLen(3))

		otherVol := podspec.FindVolume("other-volume", deployment.Spec.Template.Spec.Volumes)
		g.Expect(otherVol).ToNot(BeNil(), "other-volume should exist")
		g.Expect(otherVol.Secret.SecretName).To(Equal("other-secret"))

		cloudCredsVol := podspec.FindVolume(cloudCredsVolumeName, deployment.Spec.Template.Spec.Volumes)
		g.Expect(cloudCredsVol).ToNot(BeNil(), "cloud-creds volume should exist")
		g.Expect(cloudCredsVol.Secret.SecretName).To(Equal("updated-creds"))

		anotherVol := podspec.FindVolume("yet-another-volume", deployment.Spec.Template.Spec.Volumes)
		g.Expect(anotherVol).ToNot(BeNil(), "yet-another-volume should exist")
		g.Expect(anotherVol.ConfigMap.Name).To(Equal("some-configmap"))
	})
}

func TestAdaptDeploymentErrorStates(t *testing.T) {
	t.Parallel()

	t.Run("When PowerVS platform is nil, it should return error", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "test-namespace",
			},
			Spec: hyperv1.HostedControlPlaneSpec{
				Platform: hyperv1.PlatformSpec{
					Type:    hyperv1.PowerVSPlatform,
					PowerVS: nil,
				},
			},
		}

		deployment := &appsv1.Deployment{
			Spec: appsv1.DeploymentSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Volumes: []corev1.Volume{
							{
								Name: cloudCredsVolumeName,
								VolumeSource: corev1.VolumeSource{
									Secret: &corev1.SecretVolumeSource{
										SecretName: "original-secret",
									},
								},
							},
						},
					},
				},
			},
		}

		cpContext := component.WorkloadContext{
			HCP: hcp,
		}
		err := adaptDeployment(cpContext, deployment)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring(".spec.platform.powervs is not defined"))
	})
}

func TestAdaptDeploymentWithAssets(t *testing.T) {
	t.Parallel()

	t.Run("When deployment is loaded from assets, it should adapt cloud-creds volume", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "test-namespace",
			},
			Spec: hyperv1.HostedControlPlaneSpec{
				Platform: hyperv1.PlatformSpec{
					Type: hyperv1.PowerVSPlatform,
					PowerVS: &hyperv1.PowerVSPlatformSpec{
						KubeCloudControllerCreds: corev1.LocalObjectReference{
							Name: "asset-test-creds",
						},
					},
				},
			},
		}

		deployment := &appsv1.Deployment{}
		_, _, err := assets.LoadManifestInto(ComponentName, "deployment.yaml", deployment)
		g.Expect(err).ToNot(HaveOccurred())

		cpContext := component.WorkloadContext{
			HCP: hcp,
		}
		err = adaptDeployment(cpContext, deployment)
		g.Expect(err).ToNot(HaveOccurred())

		// Find the cloud-creds volume and verify it was updated
		cloudCredsVol := podspec.FindVolume(cloudCredsVolumeName, deployment.Spec.Template.Spec.Volumes)
		g.Expect(cloudCredsVol).ToNot(BeNil(), "cloud-creds volume should exist in deployment")
		g.Expect(cloudCredsVol.Secret.SecretName).To(Equal("asset-test-creds"))
	})
}

func TestCloudCredsVolumeNameConstant(t *testing.T) {
	t.Parallel()

	t.Run("When cloudCredsVolumeName is used, it should match expected value", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		g.Expect(cloudCredsVolumeName).To(Equal("cloud-creds"))
	})
}
