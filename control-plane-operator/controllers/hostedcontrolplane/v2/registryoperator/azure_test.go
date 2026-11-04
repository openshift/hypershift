package registryoperator

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/upsert"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	secretsstorev1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
)

func TestManagedAzureImageRegistryIdentity(t *testing.T) {
	t.Parallel()
	identity := hyperv1.ManagedIdentity{
		CredentialsSecretName: "image-registry-credentials",
		ObjectEncoding:        "utf-8",
	}
	tests := []struct {
		name        string
		hcp         *hyperv1.HostedControlPlane
		expectError string
	}{
		{
			name: "When the managed identity is configured it should return it",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Azure: &hyperv1.AzurePlatformSpec{
							AzureAuthenticationConfig: hyperv1.AzureAuthenticationConfiguration{
								ManagedIdentities: &hyperv1.AzureResourceManagedIdentities{
									ControlPlane: hyperv1.ControlPlaneManagedIdentities{ImageRegistry: identity},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "When the registry identity is absent, it should return an error instead of panicking",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Azure: &hyperv1.AzurePlatformSpec{
							AzureAuthenticationConfig: hyperv1.AzureAuthenticationConfiguration{
								ManagedIdentities: &hyperv1.AzureResourceManagedIdentities{},
							},
						},
					},
				},
			},
			expectError: "azure image registry managed identity is required when the ImageRegistry capability is enabled",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			actual, err := managedAzureImageRegistryIdentity(test.hcp)
			if test.expectError != "" {
				g.Expect(err).To(MatchError(test.expectError))
				g.Expect(actual).To(BeNil())
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(actual).To(HaveValue(Equal(identity)))
		})
	}
}

func TestNewComponent(t *testing.T) {
	g := NewWithT(t)
	g.Expect(secretsstorev1.AddToScheme(api.Scheme)).To(Succeed())
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{Name: "hcp", Namespace: "clusters-example"},
		Spec: hyperv1.HostedControlPlaneSpec{
			Capabilities: &hyperv1.Capabilities{
				Disabled: []hyperv1.OptionalCapability{hyperv1.ImageRegistryCapability},
			},
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AzurePlatform,
				Azure: &hyperv1.AzurePlatformSpec{
					AzureAuthenticationConfig: hyperv1.AzureAuthenticationConfiguration{
						AzureAuthenticationConfigType: hyperv1.AzureAuthenticationTypeManagedIdentities,
						ManagedIdentities:             &hyperv1.AzureResourceManagedIdentities{},
					},
				},
			},
		},
	}
	c := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
	cpContext := component.ControlPlaneContext{
		Context:       t.Context(),
		ApplyProvider: upsert.NewApplyProvider(false),
		Client:        c,
		HCP:           hcp,
	}

	g.Expect(NewComponent().Reconcile(cpContext)).To(Succeed())

	deployment := &appsv1.Deployment{}
	err := c.Get(t.Context(), client.ObjectKey{Namespace: hcp.Namespace, Name: ComponentName}, deployment)
	g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
}

// aroHCPMissingRegistryIdentity returns an ARO-HCP HostedControlPlane (managed-identities
// authentication) whose control-plane image registry identity is unset, so the shared
// managedAzureImageRegistryIdentity lookup fails.
func aroHCPMissingRegistryIdentity() *hyperv1.HostedControlPlane {
	return &hyperv1.HostedControlPlane{
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AzurePlatform,
				Azure: &hyperv1.AzurePlatformSpec{
					AzureAuthenticationConfig: hyperv1.AzureAuthenticationConfiguration{
						AzureAuthenticationConfigType: hyperv1.AzureAuthenticationTypeManagedIdentities,
						ManagedIdentities:             &hyperv1.AzureResourceManagedIdentities{},
					},
				},
			},
		},
	}
}

// TestAdaptAzureSecretProvider covers the SecretProviderClass adapter's error path: when the
// image registry identity lookup fails, the adapter must propagate the error and leave the
// output object unmodified.
func TestAdaptAzureSecretProvider(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	secretProvider := &secretsstorev1.SecretProviderClass{}
	original := secretProvider.DeepCopy()

	cpContext := component.WorkloadContext{
		Context: t.Context(),
		HCP:     aroHCPMissingRegistryIdentity(),
	}

	err := adaptAzureSecretProvider(cpContext, secretProvider)
	g.Expect(err).To(MatchError("azure image registry managed identity is required when the ImageRegistry capability is enabled"))
	g.Expect(secretProvider).To(Equal(original), "the SecretProviderClass must not be mutated when identity lookup fails")
}

// TestAdaptDeployment covers the deployment adapter's error path: for an ARO-HCP cluster whose
// image registry identity lookup fails, the adapter must propagate the error without mutating
// the deployment.
func TestAdaptDeployment(t *testing.T) {
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
	original := deployment.DeepCopy()

	cpContext := component.WorkloadContext{
		Context: t.Context(),
		HCP:     aroHCPMissingRegistryIdentity(),
	}

	err := adaptDeployment(cpContext, deployment)
	g.Expect(err).To(MatchError("azure image registry managed identity is required when the ImageRegistry capability is enabled"))
	g.Expect(deployment).To(Equal(original), "the Deployment must not be mutated when identity lookup fails")
}
