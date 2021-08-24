package config

import (
	"bytes"

	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift/hypershift/control-plane-operator/api"
)

func TestSetResources(t *testing.T) {
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "foo",
			Namespace: "bar",
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "test1",
							Image: "foo/bar",
						},
						{
							Name:  "test2",
							Image: "foo/bar",
						},
					},
				},
			},
		},
	}
	spec := ResourcesSpec{
		"test1": corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100m"),
				corev1.ResourceMemory: resource.MustParse("1Gi"),
			},
		},
		"test2": corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("200m"),
				corev1.ResourceMemory: resource.MustParse("2Gi"),
			},
		},
	}

	b := &bytes.Buffer{}
	api.YamlSerializer.Encode(deployment, b)
	t.Logf("Before applying: \n%s\n", b.String())
	spec.ApplyTo(&deployment.Spec.Template.Spec)
	b = &bytes.Buffer{}
	api.YamlSerializer.Encode(deployment, b)
	t.Logf("After applying: \n%s\n", b.String())

	if len(deployment.Spec.Template.Spec.Containers[0].Resources.Requests) == 0 {
		t.Errorf("did not get any resource requests applied\n")
	}

}
