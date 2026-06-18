package podspec

import (
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"
)

// initDeployment creates a base deployment for testing. When existing is non-empty,
// it populates volumes, init containers, volume mounts, and env vars using the
// string as a naming prefix. When empty, those fields are left uninitialized.
func initDeployment(existing string) *appsv1.Deployment {
	dep := &appsv1.Deployment{
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "main"},
					},
				},
			},
		},
	}
	if existing != "" {
		dep.Spec.Template.Spec.Volumes = []corev1.Volume{
			{Name: existing + "-volume"},
		}
		dep.Spec.Template.Spec.InitContainers = []corev1.Container{
			{Name: existing + "-init"},
		}
		dep.Spec.Template.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{
			{Name: existing + "-mount", MountPath: "/data"},
		}
		dep.Spec.Template.Spec.Containers[0].Env = []corev1.EnvVar{
			{Name: strings.ToUpper(existing) + "_VAR", Value: "value"},
		}
	}
	return dep
}

func TestDeploymentAddAWSCABundleVolume(t *testing.T) {
	testCases := []struct {
		name                 string
		trustBundleConfigMap *corev1.LocalObjectReference
		existing             string
		initContainerImage   string
	}{
		{
			name:                 "When a trust bundle ConfigMap is provided it should add volumes, init container, volume mount, and AWS_CA_BUNDLE env var",
			trustBundleConfigMap: &corev1.LocalObjectReference{Name: "my-trust-bundle"},
			existing:             "",
			initContainerImage:   "registry.example.com/cpo:latest",
		},
		{
			name:                 "When the deployment already has existing resources it should append without removing them",
			trustBundleConfigMap: &corev1.LocalObjectReference{Name: "custom-ca"},
			existing:             "existing",
			initContainerImage:   "registry.example.com/cpo:v2",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			deployment := initDeployment(tc.existing)
			existingVolumeCount := len(deployment.Spec.Template.Spec.Volumes)
			existingInitContainerCount := len(deployment.Spec.Template.Spec.InitContainers)
			existingVolumeMountCount := len(deployment.Spec.Template.Spec.Containers[0].VolumeMounts)
			existingEnvCount := len(deployment.Spec.Template.Spec.Containers[0].Env)

			DeploymentAddAWSCABundleVolume(tc.trustBundleConfigMap, deployment, tc.initContainerImage)

			spec := deployment.Spec.Template.Spec

			// It should add exactly two new volumes (user-ca-bundle and aws-ca-bundle).
			g.Expect(spec.Volumes).To(HaveLen(existingVolumeCount + 2))

			// Verify user-ca-bundle volume references the ConfigMap.
			var userCAVolume *corev1.Volume
			for i := range spec.Volumes {
				if spec.Volumes[i].Name == "user-ca-bundle" {
					userCAVolume = &spec.Volumes[i]
					break
				}
			}
			g.Expect(userCAVolume).NotTo(BeNil(), "user-ca-bundle volume should exist")
			g.Expect(userCAVolume.VolumeSource.ConfigMap).NotTo(BeNil())
			g.Expect(userCAVolume.VolumeSource.ConfigMap.LocalObjectReference.Name).To(Equal(tc.trustBundleConfigMap.Name))
			g.Expect(userCAVolume.VolumeSource.ConfigMap.Items).To(ConsistOf(
				corev1.KeyToPath{Key: "ca-bundle.crt", Path: "user-ca-bundle.pem"},
			))

			// Verify aws-ca-bundle volume is an EmptyDir.
			var combinedCAVolume *corev1.Volume
			for i := range spec.Volumes {
				if spec.Volumes[i].Name == "aws-ca-bundle" {
					combinedCAVolume = &spec.Volumes[i]
					break
				}
			}
			g.Expect(combinedCAVolume).NotTo(BeNil(), "aws-ca-bundle volume should exist")
			g.Expect(combinedCAVolume.VolumeSource.EmptyDir).NotTo(BeNil())

			// It should add exactly one init container.
			g.Expect(spec.InitContainers).To(HaveLen(existingInitContainerCount + 1))

			initContainer := spec.InitContainers[len(spec.InitContainers)-1]
			g.Expect(initContainer.Name).To(Equal("setup-aws-ca-bundle"))
			g.Expect(initContainer.Image).To(Equal(tc.initContainerImage))
			g.Expect(initContainer.Command).To(Equal([]string{
				"/bin/sh", "-c",
				"cat /etc/pki/tls/certs/ca-bundle.crt /user-ca/user-ca-bundle.pem > /etc/pki/ca-trust/extracted/hypershift/combined-ca-bundle.pem 2>/dev/null || cp /user-ca/user-ca-bundle.pem /etc/pki/ca-trust/extracted/hypershift/combined-ca-bundle.pem",
			}))
			g.Expect(initContainer.VolumeMounts).To(ConsistOf(
				corev1.VolumeMount{Name: "user-ca-bundle", MountPath: "/user-ca", ReadOnly: true},
				corev1.VolumeMount{Name: "aws-ca-bundle", MountPath: "/etc/pki/ca-trust/extracted/hypershift"},
			))

			// It should set resource requests on the init container.
			g.Expect(initContainer.Resources.Requests).To(HaveKeyWithValue(corev1.ResourceCPU, resource.MustParse("10m")))
			g.Expect(initContainer.Resources.Requests).To(HaveKeyWithValue(corev1.ResourceMemory, resource.MustParse("10Mi")))

			// It should set a restricted security context on the init container.
			g.Expect(initContainer.SecurityContext).NotTo(BeNil())
			g.Expect(initContainer.SecurityContext.AllowPrivilegeEscalation).To(Equal(ptr.To(false)))
			g.Expect(initContainer.SecurityContext.Capabilities).NotTo(BeNil())
			g.Expect(initContainer.SecurityContext.Capabilities.Drop).To(ConsistOf(corev1.Capability("ALL")))

			// It should add exactly one volume mount to the main container.
			g.Expect(spec.Containers[0].VolumeMounts).To(HaveLen(existingVolumeMountCount + 1))
			addedMount := spec.Containers[0].VolumeMounts[len(spec.Containers[0].VolumeMounts)-1]
			g.Expect(addedMount.Name).To(Equal("aws-ca-bundle"))
			g.Expect(addedMount.MountPath).To(Equal("/etc/pki/ca-trust/extracted/hypershift"))
			g.Expect(addedMount.ReadOnly).To(BeTrue())

			// It should set AWS_CA_BUNDLE env var on the main container.
			g.Expect(spec.Containers[0].Env).To(HaveLen(existingEnvCount + 1))
			addedEnv := spec.Containers[0].Env[len(spec.Containers[0].Env)-1]
			g.Expect(addedEnv.Name).To(Equal("AWS_CA_BUNDLE"))
			g.Expect(addedEnv.Value).To(Equal("/etc/pki/ca-trust/extracted/hypershift/combined-ca-bundle.pem"))
		})
	}
}
