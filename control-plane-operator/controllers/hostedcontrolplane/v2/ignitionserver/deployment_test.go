package ignitionserver

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/releaseinfo"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	crfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"go.uber.org/mock/gomock"
)

// TestAdaptDeployment verifies that adaptDeployment does not set the MIRRORED_RELEASE_IMAGE
// environment variable. This addresses OCPBUGS-60185 where that env var caused deployment
// flapping because SeekOverride returned non-deterministic mirror URLs during network issues,
// and the variable was not consumed by the ignition-server binary at runtime.
func TestAdaptDeployment(t *testing.T) {
	tests := []struct {
		name                   string
		imageRegistryOverrides map[string][]string
		setupEnv               func(t *testing.T)
	}{
		{
			name: "When called it should not set MIRRORED_RELEASE_IMAGE",
		},
		{
			name: "When proxy env vars are set it should not set MIRRORED_RELEASE_IMAGE",
			setupEnv: func(t *testing.T) {
				t.Setenv("HTTP_PROXY", "http://proxy.example.com:3128")
				t.Setenv("HTTPS_PROXY", "https://proxy.example.com:3128")
				t.Setenv("NO_PROXY", "localhost,127.0.0.1,.svc,.cluster.local")
			},
		},
		{
			name: "When mirror overrides are configured it should not set MIRRORED_RELEASE_IMAGE",
			imageRegistryOverrides: map[string][]string{
				"quay.io":                            {"mirror-registry.example.com", "backup-mirror.example.com"},
				"registry.redhat.io":                 {"mirror-registry.example.com"},
				"registry.ci.openshift.org/ocp/4.18": {"internal-mirror.corp.example.com"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			if tt.setupEnv != nil {
				tt.setupEnv(t)
			}

			overrides := tt.imageRegistryOverrides
			if overrides == nil {
				overrides = map[string][]string{}
			}

			ctrl := gomock.NewController(t)

			mockRelease := releaseinfo.NewMockProviderWithOpenShiftImageRegistryOverrides(ctrl)
			mockRelease.EXPECT().GetOpenShiftImageRegistryOverrides().Return(overrides).AnyTimes()
			mockRelease.EXPECT().GetRegistryOverrides().Return(map[string]string{}).AnyTimes()
			mockRelease.EXPECT().GetMirroredReleaseImage().Return("").AnyTimes()

			mockImageProvider := imageprovider.NewMockReleaseImageProvider(ctrl)
			mockImageProvider.EXPECT().GetImage(gomock.Any()).DoAndReturn(func(name string) string {
				return "test-registry.example.com/" + name + ":latest"
			}).AnyTimes()

			client := crfake.NewClientBuilder().WithScheme(api.Scheme).Build()

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					ReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.18.18-x86_64",
				},
			}

			pullSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pull-secret",
					Namespace: hcp.Namespace,
				},
				Data: map[string][]byte{
					corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`),
				},
			}
			err := client.Create(t.Context(), pullSecret)
			g.Expect(err).ToNot(HaveOccurred())

			cpContext := component.WorkloadContext{
				Context:              t.Context(),
				Client:               client,
				HCP:                  hcp,
				ReleaseImageProvider: mockImageProvider,
			}

			ign := &ignitionServer{releaseProvider: mockRelease}

			deployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ComponentName,
					Namespace: hcp.Namespace,
				},
				Spec: appsv1.DeploymentSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{Name: ComponentName},
							},
						},
					},
				},
			}

			err = ign.adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			for _, container := range deployment.Spec.Template.Spec.Containers {
				if container.Name == ComponentName {
					for _, env := range container.Env {
						g.Expect(env.Name).ToNot(Equal("MIRRORED_RELEASE_IMAGE"),
							"MIRRORED_RELEASE_IMAGE should not be set — it is dead code that caused flapping (OCPBUGS-60185)")
					}
				}
			}
		})
	}
}
