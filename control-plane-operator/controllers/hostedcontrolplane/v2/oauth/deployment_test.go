package oauth

import (
	"testing"

	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

// TestRemoveIDPVolumes verifies that removeIDPVolumes filters out only IDP-related volumes
// while keeping all other volumes intact.
func TestRemoveIDPVolumes(t *testing.T) {
	t.Run("When deployment has mixed IDP and non-IDP volumes, it should keep only non-IDP volumes", func(t *testing.T) {
		g := NewWithT(t)

		deployment := &appsv1.Deployment{
			Spec: appsv1.DeploymentSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Volumes: []corev1.Volume{
							{Name: "idp-secret-0-file-data"},
							{Name: "idp-cm-0-ca"},
							{Name: "non-idp-volume"},
						},
					},
				},
			},
		}

		removeIDPVolumes(deployment)

		g.Expect(deployment.Spec.Template.Spec.Volumes).To(HaveLen(1))
		g.Expect(deployment.Spec.Template.Spec.Volumes[0].Name).To(Equal("non-idp-volume"))
	})

	t.Run("When deployment has no IDP volumes, it should keep all volumes unchanged", func(t *testing.T) {
		g := NewWithT(t)

		deployment := &appsv1.Deployment{
			Spec: appsv1.DeploymentSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Volumes: []corev1.Volume{
							{Name: "config-volume"},
							{Name: "audit-config"},
						},
					},
				},
			},
		}

		removeIDPVolumes(deployment)

		g.Expect(deployment.Spec.Template.Spec.Volumes).To(HaveLen(2))
		g.Expect(deployment.Spec.Template.Spec.Volumes[0].Name).To(Equal("config-volume"))
		g.Expect(deployment.Spec.Template.Spec.Volumes[1].Name).To(Equal("audit-config"))
	})
}

// TestRemoveIDPVolumeMounts verifies that removeIDPVolumeMounts filters out only IDP-related
// volume mounts from the oauth-openshift container while keeping all other mounts intact.
func TestRemoveIDPVolumeMounts(t *testing.T) {
	t.Run("When container has mixed IDP and non-IDP volume mounts, it should keep only non-IDP mounts", func(t *testing.T) {
		g := NewWithT(t)

		deployment := &appsv1.Deployment{
			Spec: appsv1.DeploymentSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name: ComponentName,
								VolumeMounts: []corev1.VolumeMount{
									{Name: "idp-secret-0-file-data"},
									{Name: "idp-cm-0-ca"},
									{Name: "config-volume"},
								},
							},
							{
								// Another container to ensure we only filter the oauth-openshift container
								Name: "sidecar",
								VolumeMounts: []corev1.VolumeMount{
									{Name: "idp-secret-0-file-data"},
									{Name: "sidecar-volume"},
								},
							},
						},
					},
				},
			},
		}

		removeIDPVolumeMounts(deployment)

		containers := deployment.Spec.Template.Spec.Containers
		g.Expect(containers).To(HaveLen(2))

		// oauth-openshift container should have only non-IDP mounts
		oauthMounts := containers[0].VolumeMounts
		g.Expect(oauthMounts).To(HaveLen(1))
		g.Expect(oauthMounts[0].Name).To(Equal("config-volume"))

		// sidecar container mounts should be untouched
		sidecarMounts := containers[1].VolumeMounts
		g.Expect(sidecarMounts).To(HaveLen(2))
		g.Expect(sidecarMounts[0].Name).To(Equal("idp-secret-0-file-data"))
		g.Expect(sidecarMounts[1].Name).To(Equal("sidecar-volume"))
	})

	t.Run("When oauth-openshift container has no IDP mounts, it should keep all mounts unchanged", func(t *testing.T) {
		g := NewWithT(t)

		deployment := &appsv1.Deployment{
			Spec: appsv1.DeploymentSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name: ComponentName,
								VolumeMounts: []corev1.VolumeMount{
									{Name: "config-volume"},
									{Name: "audit-config"},
								},
							},
						},
					},
				},
			},
		}

		removeIDPVolumeMounts(deployment)

		oauthMounts := deployment.Spec.Template.Spec.Containers[0].VolumeMounts
		g.Expect(oauthMounts).To(HaveLen(2))
		g.Expect(oauthMounts[0].Name).To(Equal("config-volume"))
		g.Expect(oauthMounts[1].Name).To(Equal("audit-config"))
	})
}
