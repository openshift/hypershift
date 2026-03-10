package karpenteroperator

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type fakeReleaseProvider struct{}

func (f *fakeReleaseProvider) GetImage(key string) string           { return "test-cpo-image" }
func (f *fakeReleaseProvider) ImageExist(key string) (string, bool) { return "", false }
func (f *fakeReleaseProvider) Version() string                      { return "4.17.0" }
func (f *fakeReleaseProvider) ComponentVersions() (map[string]string, error) {
	return nil, nil
}
func (f *fakeReleaseProvider) ComponentImages() map[string]string { return nil }

func TestAdaptDeploymentAWSCABundle(t *testing.T) {
	testCases := []struct {
		name            string
		platformType    hyperv1.PlatformType
		additionalTrust *corev1.LocalObjectReference
		expectCABundle  bool
	}{
		{
			name:            "When AWS platform with additional trust bundle it should add combined CA bundle",
			platformType:    hyperv1.AWSPlatform,
			additionalTrust: &corev1.LocalObjectReference{Name: "user-ca-bundle"},
			expectCABundle:  true,
		},
		{
			name:            "When AWS platform without additional trust bundle it should not add CA bundle",
			platformType:    hyperv1.AWSPlatform,
			additionalTrust: nil,
			expectCABundle:  false,
		},
		{
			name:            "When non-AWS platform with additional trust bundle it should not add CA bundle",
			platformType:    hyperv1.KubevirtPlatform,
			additionalTrust: &corev1.LocalObjectReference{Name: "user-ca-bundle"},
			expectCABundle:  false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			awsSpec := &hyperv1.AWSPlatformSpec{
				Region: "us-east-1",
			}
			if tc.platformType != hyperv1.AWSPlatform {
				awsSpec = nil
			}

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "clusters-test-cluster",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: tc.platformType,
						AWS:  awsSpec,
					},
					AdditionalTrustBundle: tc.additionalTrust,
				},
			}

			karp := &KarpenterOperatorOptions{
				HyperShiftOperatorImage:   "test-hypershift-operator-image",
				ControlPlaneOperatorImage: "test-cpo-image",
				IgnitionEndpoint:          "https://ignition.example.com",
			}

			cpContext := controlplanecomponent.WorkloadContext{
				Context:              t.Context(),
				HCP:                  hcp,
				ReleaseImageProvider: &fakeReleaseProvider{},
			}

			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())

			err = karp.adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			container := deployment.Spec.Template.Spec.Containers[0]
			volumes := deployment.Spec.Template.Spec.Volumes
			initContainers := deployment.Spec.Template.Spec.InitContainers

			// Verify user-ca-bundle ConfigMap volume.
			hasUserCAVolume := false
			for _, v := range volumes {
				if v.Name == "user-ca-bundle" {
					hasUserCAVolume = true
					g.Expect(v.VolumeSource.ConfigMap).ToNot(BeNil())
					g.Expect(v.VolumeSource.ConfigMap.Name).To(Equal("user-ca-bundle"))
					break
				}
			}
			g.Expect(hasUserCAVolume).To(Equal(tc.expectCABundle))

			// Verify aws-ca-bundle EmptyDir volume.
			hasCombinedVolume := false
			for _, v := range volumes {
				if v.Name == "aws-ca-bundle" {
					hasCombinedVolume = true
					g.Expect(v.VolumeSource.EmptyDir).ToNot(BeNil())
					break
				}
			}
			g.Expect(hasCombinedVolume).To(Equal(tc.expectCABundle))

			// Verify setup-aws-ca-bundle init container.
			hasInitContainer := false
			for _, ic := range initContainers {
				if ic.Name == "setup-aws-ca-bundle" {
					hasInitContainer = true
					g.Expect(ic.Image).To(Equal("test-cpo-image"))
					break
				}
			}
			g.Expect(hasInitContainer).To(Equal(tc.expectCABundle))

			// Verify main container volume mount.
			hasMount := false
			for _, vm := range container.VolumeMounts {
				if vm.Name == "aws-ca-bundle" {
					hasMount = true
					g.Expect(vm.MountPath).To(Equal("/etc/pki/ca-trust/extracted/hypershift"))
					g.Expect(vm.ReadOnly).To(BeTrue())
					break
				}
			}
			g.Expect(hasMount).To(Equal(tc.expectCABundle))

			// Verify AWS_CA_BUNDLE env var points to the combined CA file.
			hasEnvVar := false
			for _, env := range container.Env {
				if env.Name == "AWS_CA_BUNDLE" {
					hasEnvVar = true
					g.Expect(env.Value).To(Equal("/etc/pki/ca-trust/extracted/hypershift/combined-ca-bundle.pem"))
					break
				}
			}
			g.Expect(hasEnvVar).To(Equal(tc.expectCABundle))
		})
	}
}
