package machineapprover

import (
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/testutil"

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAdaptDeployment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		namespace         string
		initialContainers []corev1.Container
		validate          func(*testing.T, *appsv1.Deployment)
	}{
		{
			name:      "When deployment has machine-approver container, it should add machine-namespace arg",
			namespace: "test-namespace",
			initialContainers: []corev1.Container{
				{
					Name: ComponentName,
					Args: []string{"--existing-arg=value"},
				},
			},
			validate: func(t *testing.T, deployment *appsv1.Deployment) {
				g := NewWithT(t)
				g.Expect(deployment.Spec.Template.Spec.Containers).To(HaveLen(1))
				container := deployment.Spec.Template.Spec.Containers[0]
				g.Expect(container.Name).To(Equal(ComponentName))
				g.Expect(container.Args).To(ContainElement("--existing-arg=value"))
				g.Expect(container.Args).To(ContainElement("--machine-namespace=test-namespace"))
			},
		},
		{
			name:      "When deployment has machine-approver container with no args, it should add machine-namespace arg",
			namespace: "another-namespace",
			initialContainers: []corev1.Container{
				{
					Name: ComponentName,
				},
			},
			validate: func(t *testing.T, deployment *appsv1.Deployment) {
				g := NewWithT(t)
				g.Expect(deployment.Spec.Template.Spec.Containers).To(HaveLen(1))
				container := deployment.Spec.Template.Spec.Containers[0]
				g.Expect(container.Name).To(Equal(ComponentName))
				g.Expect(container.Args).To(ContainElement("--machine-namespace=another-namespace"))
			},
		},
		{
			name:      "When deployment has multiple containers, it should only modify machine-approver container",
			namespace: "test-namespace",
			initialContainers: []corev1.Container{
				{
					Name: "other-container",
					Args: []string{"--other-arg=value"},
				},
				{
					Name: ComponentName,
					Args: []string{"--existing-arg=value"},
				},
				{
					Name: "another-container",
					Args: []string{"--another-arg=value"},
				},
			},
			validate: func(t *testing.T, deployment *appsv1.Deployment) {
				g := NewWithT(t)
				g.Expect(deployment.Spec.Template.Spec.Containers).To(HaveLen(3))

				// First container should be unchanged
				g.Expect(deployment.Spec.Template.Spec.Containers[0].Name).To(Equal("other-container"))
				g.Expect(deployment.Spec.Template.Spec.Containers[0].Args).To(Equal([]string{"--other-arg=value"}))

				// Machine approver container should have the new arg
				g.Expect(deployment.Spec.Template.Spec.Containers[1].Name).To(Equal(ComponentName))
				g.Expect(deployment.Spec.Template.Spec.Containers[1].Args).To(ContainElement("--existing-arg=value"))
				g.Expect(deployment.Spec.Template.Spec.Containers[1].Args).To(ContainElement("--machine-namespace=test-namespace"))

				// Third container should be unchanged
				g.Expect(deployment.Spec.Template.Spec.Containers[2].Name).To(Equal("another-container"))
				g.Expect(deployment.Spec.Template.Spec.Containers[2].Args).To(Equal([]string{"--another-arg=value"}))
			},
		},
		{
			name:      "When deployment has no machine-approver container, it should not modify any containers",
			namespace: "test-namespace",
			initialContainers: []corev1.Container{
				{
					Name: "other-container",
					Args: []string{"--other-arg=value"},
				},
			},
			validate: func(t *testing.T, deployment *appsv1.Deployment) {
				g := NewWithT(t)
				g.Expect(deployment.Spec.Template.Spec.Containers).To(HaveLen(1))
				g.Expect(deployment.Spec.Template.Spec.Containers[0].Name).To(Equal("other-container"))
				g.Expect(deployment.Spec.Template.Spec.Containers[0].Args).To(Equal([]string{"--other-arg=value"}))
			},
		},
		{
			name:      "When deployment has empty namespace, it should add empty machine-namespace arg",
			namespace: "",
			initialContainers: []corev1.Container{
				{
					Name: ComponentName,
				},
			},
			validate: func(t *testing.T, deployment *appsv1.Deployment) {
				g := NewWithT(t)
				g.Expect(deployment.Spec.Template.Spec.Containers).To(HaveLen(1))
				container := deployment.Spec.Template.Spec.Containers[0]
				g.Expect(container.Args).To(ContainElement("--machine-namespace="))
			},
		},
		{
			name:      "When namespace has special characters, it should correctly format the arg",
			namespace: "test-namespace-with-dashes_and_underscores",
			initialContainers: []corev1.Container{
				{
					Name: ComponentName,
				},
			},
			validate: func(t *testing.T, deployment *appsv1.Deployment) {
				g := NewWithT(t)
				g.Expect(deployment.Spec.Template.Spec.Containers).To(HaveLen(1))
				container := deployment.Spec.Template.Spec.Containers[0]
				g.Expect(container.Args).To(ContainElement("--machine-namespace=test-namespace-with-dashes_and_underscores"))
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: tc.namespace,
				},
			}

			containers := make([]corev1.Container, len(tc.initialContainers))
			for i := range tc.initialContainers {
				containers[i] = *tc.initialContainers[i].DeepCopy()
			}

			deployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-deployment",
					Namespace: tc.namespace,
				},
				Spec: appsv1.DeploymentSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: containers,
						},
					},
				},
			}

			cpContext := component.WorkloadContext{
				HCP:                  hcp,
				ReleaseImageProvider: testutil.FakeImageProvider(testutil.WithVersion("4.23.0")),
			}

			err := adaptDeployment(cpContext, deployment)

			g.Expect(err).ToNot(HaveOccurred())
			tc.validate(t, deployment)
		})
	}
}

func TestAdaptDeploymentTLSArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                  string
		releaseVersion        string
		profile               *configv1.TLSSecurityProfile
		expectedTLSMinVersion string
		expectedCipherSuites  string
		expectedError         string
	}{
		{
			name:           "When release is 4.21 multi and TLS profile is nil, it should omit unsupported TLS flags",
			releaseVersion: "4.21.0-multi",
		},
		{
			name:           "When release is 4.21 multi and TLS profile is explicit, it should omit unsupported TLS flags",
			releaseVersion: "4.21.0-multi",
			profile:        &configv1.TLSSecurityProfile{Type: configv1.TLSProfileModernType},
		},
		{
			name:           "When release is 4.22 multi and TLS profile is nil, it should omit unsupported TLS flags",
			releaseVersion: "4.22.0-multi",
		},
		{
			name:           "When release is 4.22 multi and TLS profile is explicit, it should omit unsupported TLS flags",
			releaseVersion: "4.22.0-multi",
			profile:        &configv1.TLSSecurityProfile{Type: configv1.TLSProfileIntermediateType},
		},
		{
			name:                  "When release is 4.23 multi and TLS profile is nil, it should add Intermediate TLS flags",
			releaseVersion:        "4.23.0-multi",
			expectedTLSMinVersion: "--tls-min-version=VersionTLS12",
			expectedCipherSuites: "--tls-cipher-suites=" + strings.Join(
				config.OpenSSLToIANACipherSuites(configv1.TLSProfiles[configv1.TLSProfileIntermediateType].Ciphers), ","),
		},
		{
			name:                  "When release is 5.0 and TLS profile is nil, it should add Intermediate TLS flags",
			releaseVersion:        "5.0.0",
			expectedTLSMinVersion: "--tls-min-version=VersionTLS12",
			expectedCipherSuites: "--tls-cipher-suites=" + strings.Join(
				config.OpenSSLToIANACipherSuites(configv1.TLSProfiles[configv1.TLSProfileIntermediateType].Ciphers), ","),
		},
		{
			name:           "When release is 4.23 and TLS profile is Custom, it should add exact custom TLS flags",
			releaseVersion: "4.23.0",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						MinTLSVersion: configv1.VersionTLS12,
						Ciphers:       []string{"ECDHE-RSA-AES128-GCM-SHA256"},
					},
				},
			},
			expectedTLSMinVersion: "--tls-min-version=VersionTLS12",
			expectedCipherSuites:  "--tls-cipher-suites=TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
		},
		{
			name:                  "When release is 5.0 and TLS profile is Modern, it should add only the minimum version",
			releaseVersion:        "5.0.0",
			profile:               &configv1.TLSSecurityProfile{Type: configv1.TLSProfileModernType},
			expectedTLSMinVersion: "--tls-min-version=VersionTLS13",
		},
		{
			name:           "When release version is invalid, it should return a contextual error",
			releaseVersion: "invalid-version",
			expectedError:  "failed to parse machine approver release version \"invalid-version\"",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "test-hcp", Namespace: "test-namespace"},
			}
			if tc.profile != nil {
				hcp.Spec.Configuration = &hyperv1.ClusterConfiguration{
					APIServer: &configv1.APIServerSpec{TLSSecurityProfile: tc.profile},
				}
			}

			deployment := &appsv1.Deployment{
				Spec: appsv1.DeploymentSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{{Name: ComponentName}},
						},
					},
				},
			}
			cpContext := component.WorkloadContext{
				HCP:                  hcp,
				ReleaseImageProvider: testutil.FakeImageProvider(testutil.WithVersion(tc.releaseVersion)),
			}

			err := adaptDeployment(cpContext, deployment)
			if tc.expectedError != "" {
				g.Expect(err).To(MatchError(ContainSubstring(tc.expectedError)))
				return
			}
			g.Expect(err).ToNot(HaveOccurred())

			container := deployment.Spec.Template.Spec.Containers[0]
			if tc.expectedTLSMinVersion == "" {
				g.Expect(container.Args).ToNot(ContainElement(ContainSubstring("--tls-min-version")))
			} else {
				g.Expect(container.Args).To(ContainElement(tc.expectedTLSMinVersion))
			}
			if tc.expectedCipherSuites == "" {
				g.Expect(container.Args).ToNot(ContainElement(ContainSubstring("--tls-cipher-suites")))
			} else {
				g.Expect(container.Args).To(ContainElement(tc.expectedCipherSuites))
			}
		})
	}
}
