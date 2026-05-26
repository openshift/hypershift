package ignitionserverproxy

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAdaptDeployment(t *testing.T) {
	testCases := []struct {
		name                    string
		additionalTrustBundle   *corev1.LocalObjectReference
		expectTrustBundleVolume bool
		expectTrustBundleMount  bool
		expectedVolumeName      string
		expectedConfigMapName   string
	}{
		{
			name:                    "When no additional trust bundle is set, it should not add trust bundle volume",
			additionalTrustBundle:   nil,
			expectTrustBundleVolume: false,
			expectTrustBundleMount:  false,
		},
		{
			name: "When additional trust bundle is set, it should add trust bundle volume and mount",
			additionalTrustBundle: &corev1.LocalObjectReference{
				Name: "custom-ca-bundle",
			},
			expectTrustBundleVolume: true,
			expectTrustBundleMount:  true,
			expectedVolumeName:      "trusted-ca",
			expectedConfigMapName:   "custom-ca-bundle",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					AdditionalTrustBundle: tc.additionalTrustBundle,
				},
			}

			cpContext := component.WorkloadContext{
				HCP: hcp,
			}

			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())

			err = adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			// Verify trust bundle volume configuration
			if tc.expectTrustBundleVolume {
				volume := podspec.FindVolume(tc.expectedVolumeName, deployment.Spec.Template.Spec.Volumes)
				g.Expect(volume).ToNot(BeNil(), "trust bundle volume should exist")
				g.Expect(volume.ConfigMap).ToNot(BeNil(), "volume should use ConfigMap source")
				g.Expect(volume.ConfigMap.Name).To(Equal(tc.expectedConfigMapName))
				g.Expect(volume.ConfigMap.Items).To(HaveLen(1))
				g.Expect(volume.ConfigMap.Items[0].Key).To(Equal("ca-bundle.crt"))
				g.Expect(volume.ConfigMap.Items[0].Path).To(Equal("user-ca-bundle.pem"))
			} else {
				// Verify trust bundle volume is NOT present when not configured
				g.Expect(podspec.FindVolume("trusted-ca", deployment.Spec.Template.Spec.Volumes)).To(BeNil(), "trust bundle volume should not exist")
			}

			// Verify trust bundle mount on first container (DeploymentAddTrustBundleVolume adds to first container)
			if tc.expectTrustBundleMount {
				g.Expect(deployment.Spec.Template.Spec.Containers).ToNot(BeEmpty(), "deployment should have containers")

				firstContainer := &deployment.Spec.Template.Spec.Containers[0]
				mount := podspec.FindVolumeMount(tc.expectedVolumeName, firstContainer.VolumeMounts)
				g.Expect(mount).ToNot(BeNil(), "trust bundle volume mount should exist on first container")
				g.Expect(mount.MountPath).To(Equal("/etc/pki/tls/certs"))
				g.Expect(mount.ReadOnly).To(BeTrue())
			} else {
				// Verify trust bundle mount is NOT present
				if len(deployment.Spec.Template.Spec.Containers) > 0 {
					firstContainer := &deployment.Spec.Template.Spec.Containers[0]
					g.Expect(podspec.FindVolumeMount("trusted-ca", firstContainer.VolumeMounts)).To(BeNil(), "trust bundle volume mount should not exist")
				}
			}
		})
	}
}

func TestAdaptDeploymentWithProxyEnvVars(t *testing.T) {
	g := NewWithT(t)

	// Set proxy environment variables for this test
	t.Setenv("HTTP_PROXY", "http://proxy.example.com:8080")
	t.Setenv("HTTPS_PROXY", "https://proxy.example.com:8443")
	t.Setenv("NO_PROXY", "localhost,127.0.0.1")

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{},
	}

	cpContext := component.WorkloadContext{
		HCP: hcp,
	}

	deployment, err := assets.LoadDeploymentManifest(ComponentName)
	g.Expect(err).ToNot(HaveOccurred())

	err = adaptDeployment(cpContext, deployment)
	g.Expect(err).ToNot(HaveOccurred())

	// Find haproxy container
	haproxyContainer := podspec.FindContainer("haproxy", deployment.Spec.Template.Spec.Containers)
	g.Expect(haproxyContainer).ToNot(BeNil(), "haproxy container should exist")

	// Verify proxy env vars are set
	httpProxy := podspec.FindEnvVar("HTTP_PROXY", haproxyContainer.Env)
	g.Expect(httpProxy).ToNot(BeNil())
	g.Expect(httpProxy.Value).To(Equal("http://proxy.example.com:8080"))

	httpsProxy := podspec.FindEnvVar("HTTPS_PROXY", haproxyContainer.Env)
	g.Expect(httpsProxy).ToNot(BeNil())
	g.Expect(httpsProxy.Value).To(Equal("https://proxy.example.com:8443"))

	noProxy := podspec.FindEnvVar("NO_PROXY", haproxyContainer.Env)
	g.Expect(noProxy).ToNot(BeNil())
	g.Expect(noProxy.Value).To(ContainSubstring("localhost"))
	g.Expect(noProxy.Value).To(ContainSubstring("kube-apiserver"))
}
