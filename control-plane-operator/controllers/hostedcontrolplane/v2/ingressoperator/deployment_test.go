package ingressoperator

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"
	"github.com/openshift/hypershift/support/testutil"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAdaptDeployment(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		fips     bool
		validate func(*WithT, *corev1.Container)
	}{
		{
			name: "When FIPS is enabled, it should set FIPS_ENABLED env var to true",
			fips: true,
			validate: func(g *WithT, container *corev1.Container) {
				g.Expect(container.Env).To(ContainElement(corev1.EnvVar{
					Name:  "FIPS_ENABLED",
					Value: "true",
				}))
			},
		},
		{
			name: "When FIPS is not enabled, it should not set FIPS_ENABLED env var",
			fips: false,
			validate: func(g *WithT, container *corev1.Container) {
				for _, env := range container.Env {
					g.Expect(env.Name).ToNot(Equal("FIPS_ENABLED"))
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					FIPS: tc.fips,
				},
			}

			cpContext := component.WorkloadContext{
				HCP:                      hcp,
				UserReleaseImageProvider: testutil.FakeImageProvider(),
			}

			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())

			err = adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
			g.Expect(container).ToNot(BeNil(), "ingress-operator container should exist")

			tc.validate(g, container)
		})
	}
}

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

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "clusters-test-cluster",
		},
		Spec: hyperv1.HostedControlPlaneSpec{},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			hcp.Spec.Platform.Type = tc.platformType
			hcp.Spec.AdditionalTrustBundle = tc.additionalTrust

			cpContext := component.WorkloadContext{
				Context:                  t.Context(),
				HCP:                      hcp,
				ReleaseImageProvider:     testutil.FakeImageProvider(),
				UserReleaseImageProvider: testutil.FakeImageProvider(),
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
