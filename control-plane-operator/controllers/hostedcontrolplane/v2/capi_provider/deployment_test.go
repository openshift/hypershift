package capiprovider

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/k8sutil"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestAdaptDeployment(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                string
		deploymentSpec      *appsv1.DeploymentSpec
		hcpAnnotations      map[string]string
		expectedServiceAcct string
		expectedLabels      map[string]string
		expectedAnnotKey    string
	}{
		{
			name: "When deployment spec is provided, it should apply the spec to deployment",
			deploymentSpec: &appsv1.DeploymentSpec{
				Replicas: ptr.To[int32](3),
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:  "provider",
								Image: "test-image:v1.0.0",
							},
						},
					},
				},
			},
			expectedServiceAcct: "capi-provider",
			expectedLabels: map[string]string{
				"control-plane": "capi-provider-controller-manager",
				"app":           "capi-provider-controller-manager",
			},
		},
		{
			name: "When HCP has hosted cluster annotation, it should set deployment annotation",
			deploymentSpec: &appsv1.DeploymentSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:  "provider",
								Image: "test-image:v1.0.0",
							},
						},
					},
				},
			},
			hcpAnnotations: map[string]string{
				k8sutil.HostedClusterAnnotation: "test-namespace/test-cluster",
			},
			expectedServiceAcct: "capi-provider",
			expectedLabels: map[string]string{
				"control-plane": "capi-provider-controller-manager",
				"app":           "capi-provider-controller-manager",
			},
			expectedAnnotKey: k8sutil.HostedClusterAnnotation,
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

			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())

			capi := &CAPIProviderOptions{
				deploymentSpec: tc.deploymentSpec,
			}

			cpContext := component.WorkloadContext{
				Context: t.Context(),
				HCP:     hcp,
			}

			err = capi.adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			// Check that spec was applied
			if tc.deploymentSpec.Replicas != nil {
				g.Expect(deployment.Spec.Replicas).To(Equal(tc.deploymentSpec.Replicas))
			}

			// Check selector
			g.Expect(deployment.Spec.Selector).ToNot(BeNil())
			g.Expect(deployment.Spec.Selector.MatchLabels).To(Equal(tc.expectedLabels))

			// Check template labels
			g.Expect(deployment.Spec.Template.Labels).To(Equal(tc.expectedLabels))

			// Check service account
			g.Expect(deployment.Spec.Template.Spec.ServiceAccountName).To(Equal(tc.expectedServiceAcct))

			// Check annotations
			if tc.expectedAnnotKey != "" {
				g.Expect(deployment.Annotations).To(HaveKey(tc.expectedAnnotKey))
				g.Expect(deployment.Annotations[tc.expectedAnnotKey]).To(Equal(hcp.Annotations[tc.expectedAnnotKey]))
			}
		})
	}
}

func TestAdaptDeployment_WithProxyEnvVars(t *testing.T) {
	// Cannot use t.Parallel() because this test uses t.Setenv
	g := NewWithT(t)

	// Set proxy environment variables
	t.Setenv("HTTP_PROXY", "http://proxy.example.com:8080")
	t.Setenv("HTTPS_PROXY", "https://proxy.example.com:8443")
	t.Setenv("NO_PROXY", "localhost,127.0.0.1")

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "test-namespace",
		},
	}

	deployment, err := assets.LoadDeploymentManifest(ComponentName)
	g.Expect(err).ToNot(HaveOccurred())

	deploymentSpec := &appsv1.DeploymentSpec{
		Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "provider",
						Image: "test-image:v1.0.0",
						Env:   []corev1.EnvVar{},
					},
				},
			},
		},
	}

	capi := &CAPIProviderOptions{
		deploymentSpec: deploymentSpec,
	}

	cpContext := component.WorkloadContext{
		Context: t.Context(),
		HCP:     hcp,
	}

	err = capi.adaptDeployment(cpContext, deployment)
	g.Expect(err).ToNot(HaveOccurred())

	// Check that proxy env vars are set on the container
	container := deployment.Spec.Template.Spec.Containers[0]
	envNames := make([]string, 0, len(container.Env))
	for _, env := range container.Env {
		envNames = append(envNames, env.Name)
	}
	g.Expect(envNames).To(ContainElement("HTTP_PROXY"))
	g.Expect(envNames).To(ContainElement("HTTPS_PROXY"))
	g.Expect(envNames).To(ContainElement("NO_PROXY"))
}

func TestAdaptDeployment_WithNilAnnotations(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "test-namespace",
			Annotations: map[string]string{
				k8sutil.HostedClusterAnnotation: "test-namespace/test-cluster",
			},
		},
	}

	deployment, err := assets.LoadDeploymentManifest(ComponentName)
	g.Expect(err).ToNot(HaveOccurred())

	// Ensure deployment has nil annotations to test initialization
	deployment.Annotations = nil

	deploymentSpec := &appsv1.DeploymentSpec{
		Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "provider",
						Image: "test-image:v1.0.0",
					},
				},
			},
		},
	}

	capi := &CAPIProviderOptions{
		deploymentSpec: deploymentSpec,
	}

	cpContext := component.WorkloadContext{
		Context: t.Context(),
		HCP:     hcp,
	}

	err = capi.adaptDeployment(cpContext, deployment)
	g.Expect(err).ToNot(HaveOccurred())

	// Should create annotations map and set the annotation
	g.Expect(deployment.Annotations).ToNot(BeNil())
	g.Expect(deployment.Annotations[k8sutil.HostedClusterAnnotation]).To(Equal("test-namespace/test-cluster"))
}
