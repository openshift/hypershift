package karpenteroperator

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"
	"github.com/openshift/hypershift/support/rhobsmonitoring"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAdaptDeployment(t *testing.T) {
	testCases := []struct {
		name                      string
		platformType              hyperv1.PlatformType
		awsRegion                 string
		hyperShiftOperatorImage   string
		controlPlaneOperatorImage string
		ignitionEndpoint          string
		rhobsEnabled              bool
		validateFunc              func(t *testing.T, g Gomega, opts *KarpenterOperatorOptions, cpContext controlplanecomponent.WorkloadContext)
	}{
		{
			name:                    "When platform is AWS, it should configure AWS-specific volumes and environment",
			platformType:            hyperv1.AWSPlatform,
			awsRegion:               "us-west-2",
			hyperShiftOperatorImage: "quay.io/hypershift/operator:latest",
			ignitionEndpoint:        "https://ignition.example.com",
			validateFunc: func(t *testing.T, g Gomega, opts *KarpenterOperatorOptions, cpContext controlplanecomponent.WorkloadContext) {
				t.Helper()
				deploymentObj, err := assets.LoadDeploymentManifest(ComponentName)
				g.Expect(err).ToNot(HaveOccurred())

				err = opts.adaptDeployment(cpContext, deploymentObj)
				g.Expect(err).ToNot(HaveOccurred())

				// Verify provider-creds volume is added
				g.Expect(deploymentObj.Spec.Template.Spec.Volumes).To(ContainElement(
					WithTransform(func(vol corev1.Volume) string {
						return vol.Name
					}, Equal("provider-creds")),
				))

				// Verify the secret name for provider-creds
				providerCredsVolume := podspec.FindVolume("provider-creds", deploymentObj.Spec.Template.Spec.Volumes)
				g.Expect(providerCredsVolume).ToNot(BeNil())
				g.Expect(providerCredsVolume.VolumeSource.Secret).ToNot(BeNil())
				g.Expect(providerCredsVolume.VolumeSource.Secret.SecretName).To(Equal("karpenter-credentials"))

				// Verify container configuration
				container := podspec.FindContainer(ComponentName, deploymentObj.Spec.Template.Spec.Containers)
				g.Expect(container).ToNot(BeNil(), "container %s should exist", ComponentName)
				g.Expect(container.Image).To(Equal("quay.io/hypershift/operator:latest"))

				// Verify AWS environment variables
				g.Expect(container.Env).To(ContainElements(
					corev1.EnvVar{
						Name:  "AWS_SHARED_CREDENTIALS_FILE",
						Value: "/etc/provider/credentials",
					},
					corev1.EnvVar{
						Name:  "AWS_REGION",
						Value: "us-west-2",
					},
					corev1.EnvVar{
						Name:  "AWS_SDK_LOAD_CONFIG",
						Value: "true",
					},
				))

				// Verify volume mount
				g.Expect(container.VolumeMounts).To(ContainElement(
					corev1.VolumeMount{
						Name:      "provider-creds",
						MountPath: "/etc/provider",
					},
				))

				// Verify arguments
				g.Expect(container.Args).To(ContainElements(
					"--hypershift-operator-image=quay.io/hypershift/operator:latest",
					"--ignition-endpoint=https://ignition.example.com",
				))
			},
		},
		{
			name:                      "When platform is AWS with control plane operator image, it should include CPO image arg",
			platformType:              hyperv1.AWSPlatform,
			awsRegion:                 "eu-central-1",
			hyperShiftOperatorImage:   "quay.io/hypershift/operator:v1.0",
			controlPlaneOperatorImage: "quay.io/hypershift/cpo:v1.0",
			ignitionEndpoint:          "https://ignition.example.com",
			validateFunc: func(t *testing.T, g Gomega, opts *KarpenterOperatorOptions, cpContext controlplanecomponent.WorkloadContext) {
				t.Helper()
				deploymentObj, err := assets.LoadDeploymentManifest(ComponentName)
				g.Expect(err).ToNot(HaveOccurred())

				err = opts.adaptDeployment(cpContext, deploymentObj)
				g.Expect(err).ToNot(HaveOccurred())

				container := podspec.FindContainer(ComponentName, deploymentObj.Spec.Template.Spec.Containers)
				g.Expect(container).ToNot(BeNil(), "container %s should exist", ComponentName)
				g.Expect(container.Args).To(ContainElement("--control-plane-operator-image=quay.io/hypershift/cpo:v1.0"))
			},
		},
		{
			name:                    "When RHOBS monitoring is enabled on AWS, it should set environment variable",
			platformType:            hyperv1.AWSPlatform,
			awsRegion:               "us-east-1",
			hyperShiftOperatorImage: "quay.io/hypershift/operator:latest",
			ignitionEndpoint:        "https://ignition.example.com",
			rhobsEnabled:            true,
			validateFunc: func(t *testing.T, g Gomega, opts *KarpenterOperatorOptions, cpContext controlplanecomponent.WorkloadContext) {
				t.Helper()
				deploymentObj, err := assets.LoadDeploymentManifest(ComponentName)
				g.Expect(err).ToNot(HaveOccurred())

				err = opts.adaptDeployment(cpContext, deploymentObj)
				g.Expect(err).ToNot(HaveOccurred())

				container := podspec.FindContainer(ComponentName, deploymentObj.Spec.Template.Spec.Containers)
				g.Expect(container).ToNot(BeNil(), "container %s should exist", ComponentName)
				g.Expect(container.Env).To(ContainElement(
					corev1.EnvVar{
						Name:  rhobsmonitoring.EnvironmentVariable,
						Value: "1",
					},
				))
			},
		},
		{
			name:                    "When RHOBS monitoring is disabled on AWS, it should not set environment variable",
			platformType:            hyperv1.AWSPlatform,
			awsRegion:               "us-east-1",
			hyperShiftOperatorImage: "quay.io/hypershift/operator:latest",
			ignitionEndpoint:        "https://ignition.example.com",
			rhobsEnabled:            false,
			validateFunc: func(t *testing.T, g Gomega, opts *KarpenterOperatorOptions, cpContext controlplanecomponent.WorkloadContext) {
				t.Helper()
				deploymentObj, err := assets.LoadDeploymentManifest(ComponentName)
				g.Expect(err).ToNot(HaveOccurred())

				err = opts.adaptDeployment(cpContext, deploymentObj)
				g.Expect(err).ToNot(HaveOccurred())

				container := podspec.FindContainer(ComponentName, deploymentObj.Spec.Template.Spec.Containers)
				g.Expect(container).ToNot(BeNil(), "container %s should exist", ComponentName)
				g.Expect(podspec.FindEnvVar(rhobsmonitoring.EnvironmentVariable, container.Env)).To(BeNil())
			},
		},
		{
			name:                    "When platform is not AWS, it should only set basic configuration",
			platformType:            hyperv1.AzurePlatform,
			hyperShiftOperatorImage: "quay.io/hypershift/operator:latest",
			ignitionEndpoint:        "https://ignition.example.com",
			validateFunc: func(t *testing.T, g Gomega, opts *KarpenterOperatorOptions, cpContext controlplanecomponent.WorkloadContext) {
				t.Helper()
				deploymentObj, err := assets.LoadDeploymentManifest(ComponentName)
				g.Expect(err).ToNot(HaveOccurred())

				err = opts.adaptDeployment(cpContext, deploymentObj)
				g.Expect(err).ToNot(HaveOccurred())

				// Verify NO provider-creds volume is added for non-AWS
				g.Expect(podspec.FindVolume("provider-creds", deploymentObj.Spec.Template.Spec.Volumes)).To(BeNil())

				container := podspec.FindContainer(ComponentName, deploymentObj.Spec.Template.Spec.Containers)
				g.Expect(container).ToNot(BeNil(), "container %s should exist", ComponentName)
				g.Expect(container.Image).To(Equal("quay.io/hypershift/operator:latest"))

				// Verify AWS-specific env vars are NOT present
				g.Expect(podspec.FindEnvVar("AWS_SHARED_CREDENTIALS_FILE", container.Env)).To(BeNil())
				g.Expect(podspec.FindEnvVar("AWS_REGION", container.Env)).To(BeNil())
				g.Expect(podspec.FindEnvVar("AWS_SDK_LOAD_CONFIG", container.Env)).To(BeNil())

				// Verify basic args are set
				g.Expect(container.Args).To(ContainElements(
					"--hypershift-operator-image=quay.io/hypershift/operator:latest",
					"--ignition-endpoint=https://ignition.example.com",
				))
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			if tc.rhobsEnabled {
				t.Setenv(rhobsmonitoring.EnvironmentVariable, "1")
			} else {
				t.Setenv(rhobsmonitoring.EnvironmentVariable, "")
			}

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: tc.platformType,
					},
				},
			}

			if tc.platformType == hyperv1.AWSPlatform {
				hcp.Spec.Platform.AWS = &hyperv1.AWSPlatformSpec{
					Region: tc.awsRegion,
				}
			}

			opts := &KarpenterOperatorOptions{
				HyperShiftOperatorImage:   tc.hyperShiftOperatorImage,
				ControlPlaneOperatorImage: tc.controlPlaneOperatorImage,
				IgnitionEndpoint:          tc.ignitionEndpoint,
			}

			cpContext := controlplanecomponent.WorkloadContext{
				Context: t.Context(),
				HCP:     hcp,
			}

			tc.validateFunc(t, g, opts, cpContext)
		})
	}
}
