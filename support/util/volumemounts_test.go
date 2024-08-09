package util

import (
	"slices"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	. "github.com/onsi/gomega"
)

func TestVolumeMounts(t *testing.T) {
	g := NewGomegaWithT(t)

	volumeMounts := Volumes{
		"config": Volume{
			VolumeSource: ConfigMapVolumeSource("configMapName"),
			VolumeMounts: map[string]string{
				"mainContainer":   "main/mount/path/config",
				"secondContainer": "second/mount/path/config",
				"initContainer":   "init/mount/path/config",
			},
		},
		"kubeconfig": Volume{
			VolumeSource: SecretVolumeSource("secretName"),
			VolumeMounts: map[string]string{
				"mainContainer": "/etc/kubeconfig",
			},
		},
	}

	deployment := appsv1.Deployment{
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					InitContainers: []corev1.Container{
						{
							Name: "initContainer",
						},
					},
					Containers: []corev1.Container{
						{
							Name: "mainContainer",
						},
						{
							Name: "secondContainer",
						},
					},
				},
			},
		},
	}
	volumeMounts.ApplyTo(&deployment.Spec.Template.Spec)

	g.Expect(len(deployment.Spec.Template.Spec.Volumes)).To(Equal(2))

	containsFunc := func(volumeName, mountPath string) func(v corev1.VolumeMount) bool {
		return func(v corev1.VolumeMount) bool {
			return v.Name == volumeName && v.MountPath == mountPath
		}
	}

	mainContainerVolumeMounts := deployment.Spec.Template.Spec.Containers[0].VolumeMounts
	g.Expect(slices.ContainsFunc(mainContainerVolumeMounts, containsFunc("config", "main/mount/path/config"))).To(BeTrue())
	g.Expect(slices.ContainsFunc(mainContainerVolumeMounts, containsFunc("kubeconfig", "/etc/kubeconfig"))).To(BeTrue())

	initContainerVoumeMounts := deployment.Spec.Template.Spec.InitContainers[0].VolumeMounts
	g.Expect(slices.ContainsFunc(initContainerVoumeMounts, containsFunc("config", "init/mount/path/config"))).To(BeTrue())
}
