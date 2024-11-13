package config

import (
	"bytes"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/api"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	err := api.YamlSerializer.Encode(deployment, b)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Before applying: \n%s\n", b.String())
	spec.ApplyTo(&deployment.Spec.Template.Spec)
	b = &bytes.Buffer{}
	err = api.YamlSerializer.Encode(deployment, b)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("After applying: \n%s\n", b.String())

	if len(deployment.Spec.Template.Spec.Containers[0].Resources.Requests) == 0 {
		t.Errorf("did not get any resource requests applied\n")
	}

}

func TestApplyResourceRequestOverrides(t *testing.T) {

	const deploymentName = "test-deployment"
	const anotherDeploymentName = "another-deployment"

	tests := []struct {
		name      string
		input     corev1.PodSpec
		overrides ResourceOverrides
		expected  corev1.PodSpec
	}{
		{
			name: "simple memory override",
			input: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "foo",
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceMemory: resource.MustParse("1Gi"),
							},
						},
					},
				},
			},
			overrides: ResourceOverrides{
				deploymentName: ResourcesSpec{
					"foo": corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse("2Gi"),
						},
					},
				},
			},
			expected: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "foo",
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceMemory: resource.MustParse("2Gi"),
							},
						},
					},
				},
			},
		},
		{
			name: "simple memory override, does not affect other settings",
			input: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "foo",
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceMemory: resource.MustParse("1Gi"),
								corev1.ResourceCPU:    resource.MustParse("100m"),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceMemory: resource.MustParse("5Gi"),
								corev1.ResourceCPU:    resource.MustParse("1000m"),
							},
						},
					},
				},
			},
			overrides: ResourceOverrides{
				deploymentName: ResourcesSpec{
					"foo": corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse("2Gi"),
						},
					},
				},
			},
			expected: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "foo",
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceMemory: resource.MustParse("2Gi"),
								corev1.ResourceCPU:    resource.MustParse("100m"),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceMemory: resource.MustParse("5Gi"),
								corev1.ResourceCPU:    resource.MustParse("1000m"),
							},
						},
					},
				},
			},
		},
		{
			name: "overrides to multiple containers",
			input: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "foo",
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceMemory: resource.MustParse("1Gi"),
								corev1.ResourceCPU:    resource.MustParse("100m"),
							},
						},
					},
					{
						Name: "bar",
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceMemory: resource.MustParse("3Gi"),
								corev1.ResourceCPU:    resource.MustParse("200m"),
							},
						},
					},
				},
				InitContainers: []corev1.Container{
					{
						Name: "init",
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceMemory: resource.MustParse("100Mi"),
								corev1.ResourceCPU:    resource.MustParse("50m"),
							},
						},
					},
				},
			},
			overrides: ResourceOverrides{
				deploymentName: ResourcesSpec{
					"foo": corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse("2Gi"),
						},
					},
					"bar": corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("100m"),
						},
					},
					"init": corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse("200Mi"),
							corev1.ResourceCPU:    resource.MustParse("100m"),
						},
					},
				},
			},
			expected: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "foo",
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceMemory: resource.MustParse("2Gi"),
								corev1.ResourceCPU:    resource.MustParse("100m"),
							},
						},
					},
					{
						Name: "bar",
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceMemory: resource.MustParse("3Gi"),
								corev1.ResourceCPU:    resource.MustParse("100m"),
							},
						},
					},
				},
				InitContainers: []corev1.Container{
					{
						Name: "init",
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceMemory: resource.MustParse("200Mi"),
								corev1.ResourceCPU:    resource.MustParse("100m"),
							},
						},
					},
				},
			},
		},
		{
			name: "different deployment",
			input: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "foo",
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceMemory: resource.MustParse("1Gi"),
							},
						},
					},
				},
			},
			overrides: ResourceOverrides{
				anotherDeploymentName: ResourcesSpec{
					"foo": corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse("2Gi"),
						},
					},
				},
			},
			expected: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "foo",
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceMemory: resource.MustParse("1Gi"),
							},
						},
					},
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			input := test.input
			test.overrides.ApplyRequestsTo(deploymentName, &input)
			g.Expect(input).To(Equal(test.expected))
		})
	}
}
