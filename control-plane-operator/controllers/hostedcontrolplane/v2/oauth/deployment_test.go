package oauth

import (
	"context"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestAdaptDeployment_IBMCloud_NOProxy(t *testing.T) {
	tests := []struct {
		name                     string
		platformType             hyperv1.PlatformType
		ibmCloudSpec             *hyperv1.IBMCloudPlatformSpec
		expectedNoProxyEndpoints []string
	}{
		{
			name:         "When IBM Cloud platform with no custom endpoints, it should set default NO_PROXY endpoints",
			platformType: hyperv1.IBMCloudPlatform,
			ibmCloudSpec: &hyperv1.IBMCloudPlatformSpec{},
			expectedNoProxyEndpoints: []string{
				manifests.KubeAPIServerService("").Name,
				config.AuditWebhookService,
				"iam.cloud.ibm.com",
				"iam.test.cloud.ibm.com",
			},
		},
		{
			name:         "When IBM Cloud platform with custom endpoints, it should append unique endpoints to defaults",
			platformType: hyperv1.IBMCloudPlatform,
			ibmCloudSpec: &hyperv1.IBMCloudPlatformSpec{
				OAuthNoProxyEndpoints: []string{
					"custom.endpoint.com",
					"another.endpoint.com",
				},
			},
			expectedNoProxyEndpoints: []string{
				manifests.KubeAPIServerService("").Name,
				config.AuditWebhookService,
				"iam.cloud.ibm.com",
				"iam.test.cloud.ibm.com",
				"custom.endpoint.com",
				"another.endpoint.com",
			},
		},
		{
			name:         "When IBM Cloud platform with duplicate endpoints, it should not add duplicates",
			platformType: hyperv1.IBMCloudPlatform,
			ibmCloudSpec: &hyperv1.IBMCloudPlatformSpec{
				OAuthNoProxyEndpoints: []string{
					"iam.cloud.ibm.com", // duplicate
					"custom.endpoint.com",
					manifests.KubeAPIServerService("").Name, // duplicate
				},
			},
			expectedNoProxyEndpoints: []string{
				manifests.KubeAPIServerService("").Name,
				config.AuditWebhookService,
				"iam.cloud.ibm.com",
				"iam.test.cloud.ibm.com",
				"custom.endpoint.com",
			},
		},
		{
			name:         "When IBM Cloud platform with empty string endpoints, it should filter them out",
			platformType: hyperv1.IBMCloudPlatform,
			ibmCloudSpec: &hyperv1.IBMCloudPlatformSpec{
				OAuthNoProxyEndpoints: []string{
					"",
					"custom.endpoint.com",
					"",
				},
			},
			expectedNoProxyEndpoints: []string{
				manifests.KubeAPIServerService("").Name,
				config.AuditWebhookService,
				"iam.cloud.ibm.com",
				"iam.test.cloud.ibm.com",
				"custom.endpoint.com",
			},
		},
		{
			name:         "When IBM Cloud platform with nil IBMCloud spec, it should set default NO_PROXY endpoints",
			platformType: hyperv1.IBMCloudPlatform,
			ibmCloudSpec: nil,
			expectedNoProxyEndpoints: []string{
				manifests.KubeAPIServerService("").Name,
				config.AuditWebhookService,
				"iam.cloud.ibm.com",
				"iam.test.cloud.ibm.com",
			},
		},
		{
			name:         "When IBM Cloud platform with empty OAuthNoProxyEndpoints slice, it should set default NO_PROXY endpoints",
			platformType: hyperv1.IBMCloudPlatform,
			ibmCloudSpec: &hyperv1.IBMCloudPlatformSpec{
				OAuthNoProxyEndpoints: []string{},
			},
			expectedNoProxyEndpoints: []string{
				manifests.KubeAPIServerService("").Name,
				config.AuditWebhookService,
				"iam.cloud.ibm.com",
				"iam.test.cloud.ibm.com",
			},
		},
		{
			name:                     "When non-IBM Cloud platform, it should not set NO_PROXY",
			platformType:             hyperv1.AWSPlatform,
			ibmCloudSpec:             nil,
			expectedNoProxyEndpoints: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			// Create a minimal deployment with the oauth container
			deployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "oauth-openshift",
					Namespace: "test-namespace",
				},
				Spec: appsv1.DeploymentSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  ComponentName,
									Image: "test-image",
								},
							},
						},
					},
				},
			}

			// Create a minimal HCP with the platform configuration
			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type:     tt.platformType,
						IBMCloud: tt.ibmCloudSpec,
					},
				},
			}

			// Create a fake client with the scheme
			scheme := runtime.NewScheme()
			_ = hyperv1.AddToScheme(scheme)
			_ = corev1.AddToScheme(scheme)
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(hcp).Build()

			// Create the workload context
			cpContext := component.WorkloadContext{
				Context: context.Background(),
				Client:  fakeClient,
				HCP:     hcp,
			}

			// Call adaptDeployment
			err := adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			// Verify NO_PROXY environment variable
			var noProxyValue string
			for _, container := range deployment.Spec.Template.Spec.Containers {
				if container.Name == ComponentName {
					for _, env := range container.Env {
						if env.Name == "NO_PROXY" {
							noProxyValue = env.Value
							break
						}
					}
					break
				}
			}

			if tt.expectedNoProxyEndpoints == nil {
				g.Expect(noProxyValue).To(BeEmpty(), "NO_PROXY should not be set for non-IBM Cloud platforms")
			} else {
				g.Expect(noProxyValue).ToNot(BeEmpty(), "NO_PROXY should be set for IBM Cloud platform")
				actualEndpoints := strings.Split(noProxyValue, ",")
				g.Expect(actualEndpoints).To(ConsistOf(tt.expectedNoProxyEndpoints))
			}
		})
	}
}

func TestAppendUnique(t *testing.T) {
	tests := []struct {
		name     string
		existing []string
		toAdd    []string
		expected []string
	}{
		{
			name:     "When adding to empty slice, all non-empty items should be added",
			existing: []string{},
			toAdd:    []string{"a", "b", "c"},
			expected: []string{"a", "b", "c"},
		},
		{
			name:     "When adding items with no duplicates, all should be appended",
			existing: []string{"a", "b"},
			toAdd:    []string{"c", "d"},
			expected: []string{"a", "b", "c", "d"},
		},
		{
			name:     "When adding items with duplicates, only unique items should be appended",
			existing: []string{"a", "b", "c"},
			toAdd:    []string{"b", "d", "c", "e"},
			expected: []string{"a", "b", "c", "d", "e"},
		},
		{
			name:     "When adding all duplicates, nothing should be appended",
			existing: []string{"a", "b", "c"},
			toAdd:    []string{"a", "b", "c"},
			expected: []string{"a", "b", "c"},
		},
		{
			name:     "When adding empty slice, existing should remain unchanged",
			existing: []string{"a", "b"},
			toAdd:    []string{},
			expected: []string{"a", "b"},
		},
		{
			name:     "When both slices are empty, result should be empty",
			existing: []string{},
			toAdd:    []string{},
			expected: []string{},
		},
		{
			name:     "When adding items with empty strings, empty strings should be filtered out",
			existing: []string{"a", "b"},
			toAdd:    []string{"", "c", ""},
			expected: []string{"a", "b", "c"},
		},
		{
			name:     "When adding only empty strings, nothing should be appended",
			existing: []string{"a", "b"},
			toAdd:    []string{"", "", ""},
			expected: []string{"a", "b"},
		},
		{
			name:     "When adding items with special characters, deduplication should work",
			existing: []string{"*.example.com", ".example.com"},
			toAdd:    []string{"*.example.com", "192.168.1.0/24", ".example.com"},
			expected: []string{"*.example.com", ".example.com", "192.168.1.0/24"},
		},
		{
			name:     "When adding mix of empty, duplicates, and unique items, only unique non-empty should be added",
			existing: []string{"a", "b"},
			toAdd:    []string{"", "a", "c", "", "b", "d", ""},
			expected: []string{"a", "b", "c", "d"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			result := appendUnique(tt.existing, tt.toAdd...)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}
