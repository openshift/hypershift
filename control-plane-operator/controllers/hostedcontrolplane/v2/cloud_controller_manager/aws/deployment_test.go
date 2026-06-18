package aws

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/testutil"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAdaptDeploymentTrustBundle(t *testing.T) {
	testCases := []struct {
		name            string
		additionalTrust *corev1.LocalObjectReference
		expectCABundle  bool
	}{
		{
			name:            "When no additional trust bundle is set it should not add CA bundle resources",
			additionalTrust: nil,
			expectCABundle:  false,
		},
		{
			name:            "When additional trust bundle is set it should add combined CA bundle with init container",
			additionalTrust: &corev1.LocalObjectReference{Name: "user-ca-bundle"},
			expectCABundle:  true,
		},
	}

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "clusters-test-cluster",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AWSPlatform,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			hcp.Spec.AdditionalTrustBundle = tc.additionalTrust

			cpContext := controlplanecomponent.WorkloadContext{
				Context:              t.Context(),
				HCP:                  hcp,
				ReleaseImageProvider: testutil.FakeImageProvider(),
			}

			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())

			err = adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			volumes := deployment.Spec.Template.Spec.Volumes
			initContainers := deployment.Spec.Template.Spec.InitContainers
			container := deployment.Spec.Template.Spec.Containers[0]

			if tc.expectCABundle {
				g.Expect(volumes).To(ContainElement(SatisfyAll(
					HaveField("Name", "user-ca-bundle"),
					HaveField("VolumeSource.ConfigMap.Name", "user-ca-bundle"),
				)))
				g.Expect(volumes).To(ContainElement(SatisfyAll(
					HaveField("Name", "aws-ca-bundle"),
					HaveField("VolumeSource.EmptyDir", Not(BeNil())),
				)))
				g.Expect(initContainers).To(ContainElement(SatisfyAll(
					HaveField("Name", "setup-aws-ca-bundle"),
					HaveField("Image", "controlplane-operator"),
				)))
				g.Expect(container.VolumeMounts).To(ContainElement(SatisfyAll(
					HaveField("Name", "aws-ca-bundle"),
					HaveField("MountPath", "/etc/pki/ca-trust/extracted/hypershift"),
					HaveField("ReadOnly", true),
				)))
				g.Expect(container.Env).To(ContainElement(SatisfyAll(
					HaveField("Name", "AWS_CA_BUNDLE"),
					HaveField("Value", "/etc/pki/ca-trust/extracted/hypershift/combined-ca-bundle.pem"),
				)))
			} else {
				g.Expect(volumes).ToNot(ContainElement(HaveField("Name", "user-ca-bundle")))
				g.Expect(volumes).ToNot(ContainElement(HaveField("Name", "aws-ca-bundle")))
				g.Expect(initContainers).ToNot(ContainElement(HaveField("Name", "setup-aws-ca-bundle")))
				g.Expect(container.VolumeMounts).ToNot(ContainElement(HaveField("Name", "aws-ca-bundle")))
				g.Expect(container.Env).ToNot(ContainElement(HaveField("Name", "AWS_CA_BUNDLE")))
			}
		})
	}
}
