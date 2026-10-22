package karpenteroperator

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	controlplaneoperatoroverrides "github.com/openshift/hypershift/hypershift-operator/controlplaneoperator-overrides"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/images"
	karpenterutil "github.com/openshift/hypershift/support/karpenter"
	"github.com/openshift/hypershift/support/podspec"
	"github.com/openshift/hypershift/support/rhobsmonitoring"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type fakeReleaseImageProvider struct {
	images map[string]string
}

func TestAdaptStandaloneDeploymentEndpoint(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		host string
		port int32
		want string
	}{
		{name: "When the endpoint is raw IPv6, it should bracket the host", host: "2001:db8::1", port: 6443, want: "https://[2001:db8::1]:6443"},
		{name: "When the endpoint is IPv4, it should preserve the authority", host: "192.0.2.1", port: 6443, want: "https://192.0.2.1:6443"},
		{name: "When the endpoint is DNS, it should preserve the authority", host: "api.example.test", port: 6443, want: "https://api.example.test:6443"},
		{name: "When the endpoint uses an alternate port, it should retain the port", host: "2001:db8::1", port: 443, want: "https://[2001:db8::1]:443"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred(), "load the production deployment manifest")
			hcp := &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform, AWS: &hyperv1.AWSPlatformSpec{Region: "us-east-1"}},
				},
				Status: hyperv1.HostedControlPlaneStatus{ControlPlaneEndpoint: hyperv1.APIEndpoint{Host: test.host, Port: test.port}},
			}
			opts := &KarpenterOperatorOptions{StandaloneKarpenterOperatorEnabled: true}
			err = opts.adaptDeployment(controlplanecomponent.WorkloadContext{
				Context: t.Context(), HCP: hcp, ReleaseImageProvider: &fakeReleaseImageProvider{},
			}, deployment)
			g.Expect(err).ToNot(HaveOccurred(), "adapt standalone deployment for %q", test.host)
			container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
			g.Expect(container).ToNot(BeNil(), "standalone operator container must exist")
			g.Expect(container.Env).To(ContainElement(corev1.EnvVar{Name: "CLUSTER_ENDPOINT", Value: test.want}), "deployment must carry the exact endpoint URL")
		})
	}
}

func (f *fakeReleaseImageProvider) GetImage(name string) string {
	return f.images[name]
}

func (f *fakeReleaseImageProvider) ImageExist(name string) (string, bool) {
	img, ok := f.images[name]
	return img, ok
}

func (f *fakeReleaseImageProvider) Version() string {
	return "4.18.0"
}

func (f *fakeReleaseImageProvider) ComponentVersions() (map[string]string, error) {
	return nil, nil
}

func (f *fakeReleaseImageProvider) ComponentImages() map[string]string {
	return f.images
}

func TestAdaptDeployment(t *testing.T) {
	testCases := []struct {
		name                      string
		platformType              hyperv1.PlatformType
		awsRegion                 string
		hyperShiftOperatorImage   string
		controlPlaneOperatorImage string
		ignitionEndpoint          string
		rhobsEnabled              bool
		setDefaultSecurityContext bool
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
				g.Expect(podspec.FindContainer("karpenter-adapter", deploymentObj.Spec.Template.Spec.Containers)).To(BeNil())

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
		{
			name:                    "When the HyperShift operator has IMAGE_SHARED_INGRESS_HAPROXY set, it should pass it to the container",
			platformType:            hyperv1.AzurePlatform,
			hyperShiftOperatorImage: "quay.io/hypershift/operator:latest",
			ignitionEndpoint:        "https://ignition.example.com",
			validateFunc: func(t *testing.T, g Gomega, opts *KarpenterOperatorOptions, cpContext controlplanecomponent.WorkloadContext) {
				t.Helper()
				t.Setenv(images.SharedIngressHAProxyEnvVar, "quay.io/redhat-user-workloads/crt-redhat-acm-tenant/hypershift-shared-ingress-main@sha256:1af59b7a29432314bde54e8977fa45fa92dc48885efbf0df601418ec0912f472")
				deploymentObj, err := assets.LoadDeploymentManifest(ComponentName)
				g.Expect(err).ToNot(HaveOccurred())

				err = opts.adaptDeployment(cpContext, deploymentObj)
				g.Expect(err).ToNot(HaveOccurred())

				container := podspec.FindContainer(ComponentName, deploymentObj.Spec.Template.Spec.Containers)
				g.Expect(container).ToNot(BeNil(), "container %s should exist", ComponentName)
				g.Expect(container.Env).To(ContainElement(corev1.EnvVar{Name: images.SharedIngressHAProxyEnvVar, Value: "quay.io/redhat-user-workloads/crt-redhat-acm-tenant/hypershift-shared-ingress-main@sha256:1af59b7a29432314bde54e8977fa45fa92dc48885efbf0df601418ec0912f472"}))
			},
		},
		{
			name:                      "When standalone mode is enabled with SCC, it should omit DEFAULT_SECURITY_CONTEXT_UID from the operator and adapter",
			platformType:              hyperv1.AWSPlatform,
			awsRegion:                 "us-west-2",
			hyperShiftOperatorImage:   "quay.io/hypershift/operator:latest",
			controlPlaneOperatorImage: "quay.io/hypershift/cpo:latest",
			ignitionEndpoint:          "https://ignition.example.com",
			validateFunc: func(t *testing.T, g Gomega, opts *KarpenterOperatorOptions, cpContext controlplanecomponent.WorkloadContext) {
				t.Helper()
				deploymentObj, err := assets.LoadDeploymentManifest(ComponentName)
				g.Expect(err).ToNot(HaveOccurred())

				opts.StandaloneKarpenterOperatorEnabled = true
				cpContext.ReleaseImageProvider = &fakeReleaseImageProvider{images: map[string]string{
					karpenterutil.KarpenterOperatorImageName:    "quay.io/openshift/karpenter-operator:latest",
					karpenterutil.KarpenterProviderAWSImageName: "quay.io/openshift/karpenter-aws:latest",
					"token-minter": "quay.io/openshift/token-minter:latest",
				}}
				err = opts.adaptDeployment(cpContext, deploymentObj)
				g.Expect(err).ToNot(HaveOccurred())

				g.Expect(podspec.FindContainer("karpenter-adapter", deploymentObj.Spec.Template.Spec.Containers)).ToNot(BeNil())
				for _, container := range deploymentObj.Spec.Template.Spec.Containers {
					g.Expect(podspec.FindEnvVar("DEFAULT_SECURITY_CONTEXT_UID", container.Env)).To(BeNil())
				}
			},
		},
		{
			name:                      "When the legacy operator runs without SCC, it should omit DEFAULT_SECURITY_CONTEXT_UID",
			platformType:              hyperv1.AWSPlatform,
			awsRegion:                 "us-west-2",
			setDefaultSecurityContext: true,
			validateFunc: func(t *testing.T, g Gomega, opts *KarpenterOperatorOptions, cpContext controlplanecomponent.WorkloadContext) {
				t.Helper()
				deploymentObj, err := assets.LoadDeploymentManifest(ComponentName)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(opts.adaptDeployment(cpContext, deploymentObj)).To(Succeed())

				container := podspec.FindContainer(ComponentName, deploymentObj.Spec.Template.Spec.Containers)
				g.Expect(container).ToNot(BeNil())
				g.Expect(podspec.FindEnvVar("DEFAULT_SECURITY_CONTEXT_UID", container.Env)).To(BeNil())
			},
		},
		{
			name:                      "When standalone mode is enabled without SCC, it should pass DEFAULT_SECURITY_CONTEXT_UID only to the operator",
			platformType:              hyperv1.AWSPlatform,
			awsRegion:                 "us-west-2",
			setDefaultSecurityContext: true,
			validateFunc: func(t *testing.T, g Gomega, opts *KarpenterOperatorOptions, cpContext controlplanecomponent.WorkloadContext) {
				t.Helper()
				deploymentObj, err := assets.LoadDeploymentManifest(ComponentName)
				g.Expect(err).ToNot(HaveOccurred())
				opts.StandaloneKarpenterOperatorEnabled = true
				cpContext.ReleaseImageProvider = &fakeReleaseImageProvider{images: map[string]string{
					karpenterutil.KarpenterOperatorImageName:    "quay.io/openshift/karpenter-operator:latest",
					karpenterutil.KarpenterProviderAWSImageName: "quay.io/openshift/karpenter-aws:latest",
					"token-minter": "quay.io/openshift/token-minter:latest",
				}}
				g.Expect(opts.adaptDeployment(cpContext, deploymentObj)).To(Succeed())

				container := podspec.FindContainer(ComponentName, deploymentObj.Spec.Template.Spec.Containers)
				g.Expect(container).ToNot(BeNil())
				g.Expect(container.Env).To(ContainElement(corev1.EnvVar{Name: "DEFAULT_SECURITY_CONTEXT_UID", Value: "1001230000"}))
				adapter := podspec.FindContainer(AdapterContainerName, deploymentObj.Spec.Template.Spec.Containers)
				g.Expect(adapter).ToNot(BeNil())
				g.Expect(podspec.FindEnvVar("DEFAULT_SECURITY_CONTEXT_UID", adapter.Env)).To(BeNil())
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
				Context:                   t.Context(),
				HCP:                       hcp,
				SetDefaultSecurityContext: tc.setDefaultSecurityContext,
				DefaultSecurityContextUID: 1001230000,
			}

			tc.validateFunc(t, g, opts, cpContext)
		})
	}
}

func TestSetIgnitionEnvVars(t *testing.T) {
	testCases := map[string]struct {
		envVars     map[string]string
		expectedEnv []corev1.EnvVar
	}{
		"When the HyperShift operator has no ignition env vars set, it should not add env vars": {
			expectedEnv: []corev1.EnvVar{},
		},
		"When the HyperShift operator has IMAGE_SHARED_INGRESS_HAPROXY set, it should pass it on": {
			envVars: map[string]string{
				images.SharedIngressHAProxyEnvVar: "quay.io/redhat-user-workloads/crt-redhat-acm-tenant/hypershift-shared-ingress-main@sha256:1af59b7a29432314bde54e8977fa45fa92dc48885efbf0df601418ec0912f472",
			},
			expectedEnv: []corev1.EnvVar{
				{
					Name:  images.SharedIngressHAProxyEnvVar,
					Value: "quay.io/redhat-user-workloads/crt-redhat-acm-tenant/hypershift-shared-ingress-main@sha256:1af59b7a29432314bde54e8977fa45fa92dc48885efbf0df601418ec0912f472",
				},
			},
		},
		"When the HyperShift operator has ENABLE_CPO_OVERRIDES set, it should pass it on": {
			envVars:     map[string]string{controlplaneoperatoroverrides.CPOOverridesEnvVar: "1"},
			expectedEnv: []corev1.EnvVar{{Name: controlplaneoperatoroverrides.CPOOverridesEnvVar, Value: "1"}},
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)
			t.Setenv(images.SharedIngressHAProxyEnvVar, "")
			t.Setenv(controlplaneoperatoroverrides.CPOOverridesEnvVar, "")
			for envVarName, value := range tc.envVars {
				t.Setenv(envVarName, value)
			}

			var env []corev1.EnvVar
			setIgnitionEnvVars(&env)
			g.Expect(env).To(ConsistOf(tc.expectedEnv))
		})
	}
}

func TestAddStandaloneAdapterContainer(t *testing.T) {
	g := NewWithT(t)
	t.Setenv(images.SharedIngressHAProxyEnvVar, "quay.io/redhat-user-workloads/crt-redhat-acm-tenant/hypershift-shared-ingress-main@sha256:1af59b7a29432314bde54e8977fa45fa92dc48885efbf0df601418ec0912f472")
	deploymentObj, err := assets.LoadDeploymentManifest(ComponentName)
	g.Expect(err).ToNot(HaveOccurred())

	opts := &KarpenterOperatorOptions{
		HyperShiftOperatorImage:   "quay.io/hypershift/operator:latest",
		ControlPlaneOperatorImage: "quay.io/hypershift/cpo:latest",
		IgnitionEndpoint:          "https://ignition.example.com",
	}
	err = opts.addStandaloneAdapterContainer(deploymentObj)
	g.Expect(err).ToNot(HaveOccurred())

	adapter := podspec.FindContainer("karpenter-adapter", deploymentObj.Spec.Template.Spec.Containers)
	g.Expect(adapter).ToNot(BeNil())
	g.Expect(adapter.Image).To(Equal("quay.io/hypershift/operator:latest"))
	g.Expect(adapter.Command).To(Equal([]string{"/usr/bin/karpenter-operator"}))
	g.Expect(adapter.Ports).To(BeEmpty())
	g.Expect(adapter.Args).To(ContainElements(
		"--enable-standalone-karpenter-operator",
		"--control-plane-operator-image=quay.io/hypershift/cpo:latest",
		"--hypershift-operator-image=quay.io/hypershift/operator:latest",
		"--ignition-endpoint=https://ignition.example.com",
	))
	g.Expect(adapter.VolumeMounts).To(ContainElement(
		corev1.VolumeMount{Name: "target-kubeconfig", MountPath: "/mnt/kubeconfig"},
	))
	g.Expect(adapter.VolumeMounts).ToNot(ContainElement(
		corev1.VolumeMount{Name: "provider-creds", MountPath: "/etc/provider"},
	))
	g.Expect(adapter.Env).To(ContainElement(corev1.EnvVar{Name: images.SharedIngressHAProxyEnvVar, Value: "quay.io/redhat-user-workloads/crt-redhat-acm-tenant/hypershift-shared-ingress-main@sha256:1af59b7a29432314bde54e8977fa45fa92dc48885efbf0df601418ec0912f472"}))
}

func TestAdaptStandaloneDeployment(t *testing.T) {
	testCases := []struct {
		name           string
		platformType   hyperv1.PlatformType
		awsRegion      string
		azureLocation  string
		infraID        string
		rhobsEnabled   bool
		hcpAnnotations map[string]string
		images         map[string]string
		validateFunc   func(t *testing.T, g Gomega, opts *KarpenterOperatorOptions, cpContext controlplanecomponent.WorkloadContext)
	}{
		{
			name:         "When platform is AWS, it should configure AWS-specific env vars and karpenter image",
			platformType: hyperv1.AWSPlatform,
			awsRegion:    "us-west-2",
			infraID:      "test-infra-123",
			images: map[string]string{
				karpenterutil.KarpenterProviderAWSImageName: "quay.io/openshift/karpenter-aws:latest",
				"token-minter": "quay.io/openshift/token-minter:latest",
			},
			validateFunc: func(t *testing.T, g Gomega, opts *KarpenterOperatorOptions, cpContext controlplanecomponent.WorkloadContext) {
				t.Helper()
				deploymentObj, err := assets.LoadDeploymentManifest(ComponentName)
				g.Expect(err).ToNot(HaveOccurred())

				err = opts.adaptStandaloneDeployment(cpContext, deploymentObj)
				g.Expect(err).ToNot(HaveOccurred())

				container := podspec.FindContainer(ComponentName, deploymentObj.Spec.Template.Spec.Containers)
				g.Expect(container).ToNot(BeNil())

				g.Expect(container.Env).To(ContainElements(
					corev1.EnvVar{Name: "CLUSTER_NAME", Value: "test-infra-123"},
					corev1.EnvVar{Name: "CLUSTER_ENDPOINT", Value: "https://api.test-cluster.example.com:6443"},
					corev1.EnvVar{Name: "PLATFORM", Value: "AWS"},
					corev1.EnvVar{Name: "REGION", Value: "us-west-2"},
					corev1.EnvVar{Name: "AWS_SHARED_CREDENTIALS_FILE", Value: "/etc/provider/credentials"},
					corev1.EnvVar{Name: "AWS_SDK_LOAD_CONFIG", Value: "true"},
					corev1.EnvVar{Name: KarpenterImageAWSEnvVar, Value: "quay.io/openshift/karpenter-aws:latest"},
					corev1.EnvVar{Name: ManagementClusterEnvVar, Value: "true"},
					corev1.EnvVar{Name: TokenMinterImageEnvVar, Value: "quay.io/openshift/token-minter:latest"},
				))

				g.Expect(container.Args).To(ContainElement(ContainSubstring("--target-kubeconfig=")))

				g.Expect(container.VolumeMounts).To(ContainElement(
					corev1.VolumeMount{Name: "provider-creds", MountPath: "/etc/provider"},
				))

				providerCredsVolume := podspec.FindVolume("provider-creds", deploymentObj.Spec.Template.Spec.Volumes)
				g.Expect(providerCredsVolume).ToNot(BeNil())
				g.Expect(providerCredsVolume.VolumeSource.Secret.SecretName).To(Equal("karpenter-credentials"))
			},
		},
		{
			name:          "When platform is Azure, it should configure Azure-specific env vars and karpenter image",
			platformType:  hyperv1.AzurePlatform,
			azureLocation: "eastus",
			infraID:       "test-azure-456",
			images: map[string]string{
				karpenterutil.KarpenterProviderAzureImageName: "quay.io/openshift/karpenter-provider-azure:latest",
				"token-minter": "quay.io/openshift/token-minter:latest",
			},
			validateFunc: func(t *testing.T, g Gomega, opts *KarpenterOperatorOptions, cpContext controlplanecomponent.WorkloadContext) {
				t.Helper()
				deploymentObj, err := assets.LoadDeploymentManifest(ComponentName)
				g.Expect(err).ToNot(HaveOccurred())

				err = opts.adaptStandaloneDeployment(cpContext, deploymentObj)
				g.Expect(err).ToNot(HaveOccurred())

				container := podspec.FindContainer(ComponentName, deploymentObj.Spec.Template.Spec.Containers)
				g.Expect(container).ToNot(BeNil())

				g.Expect(container.Env).To(ContainElements(
					corev1.EnvVar{Name: "CLUSTER_NAME", Value: "test-azure-456"},
					corev1.EnvVar{Name: "PLATFORM", Value: "Azure"},
					corev1.EnvVar{Name: "REGION", Value: "eastus"},
					corev1.EnvVar{Name: KarpenterImageAzureEnvVar, Value: "quay.io/openshift/karpenter-provider-azure:latest"},
					corev1.EnvVar{Name: ManagementClusterEnvVar, Value: "true"},
					corev1.EnvVar{Name: TokenMinterImageEnvVar, Value: "quay.io/openshift/token-minter:latest"},
					corev1.EnvVar{Name: "AZURE_CLIENT_ID", Value: "12345678-1234-1234-1234-123456789012"},
					corev1.EnvVar{Name: "AZURE_TENANT_ID", Value: "tenant-id"},
					corev1.EnvVar{Name: "AZURE_SUBSCRIPTION_ID", Value: "subscription-id"},
					corev1.EnvVar{Name: "AZURE_FEDERATED_TOKEN_FILE", Value: "/var/run/secrets/openshift/serviceaccount/token"},
					corev1.EnvVar{Name: "VNET_SUBNET_ID", Value: "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet"},
					corev1.EnvVar{Name: "AZURE_NODE_RESOURCE_GROUP", Value: "test-rg"},
				))

				// Azure should not have AWS-specific env vars
				g.Expect(podspec.FindEnvVar(KarpenterImageAWSEnvVar, container.Env)).To(BeNil())
				g.Expect(podspec.FindEnvVar("AWS_SHARED_CREDENTIALS_FILE", container.Env)).To(BeNil())
				g.Expect(podspec.FindEnvVar("AWS_SDK_LOAD_CONFIG", container.Env)).To(BeNil())
				g.Expect(podspec.FindEnvVar("SSH_PUBLIC_KEY", container.Env)).To(BeNil())
				g.Expect(podspec.FindEnvVar("KUBELET_BOOTSTRAP_TOKEN", container.Env)).To(BeNil())
				g.Expect(podspec.FindVolume("provider-creds", deploymentObj.Spec.Template.Spec.Volumes)).To(BeNil())
				g.Expect(podspec.FindVolumeMount("provider-creds", container.VolumeMounts)).To(BeNil())
			},
		},
		{
			name:         "When RHOBS monitoring is enabled, it should set the env var",
			platformType: hyperv1.AWSPlatform,
			awsRegion:    "us-east-1",
			infraID:      "test-rhobs",
			rhobsEnabled: true,
			images: map[string]string{
				karpenterutil.KarpenterProviderAWSImageName: "quay.io/openshift/karpenter-aws:latest",
				"token-minter": "quay.io/openshift/token-minter:latest",
			},
			validateFunc: func(t *testing.T, g Gomega, opts *KarpenterOperatorOptions, cpContext controlplanecomponent.WorkloadContext) {
				t.Helper()
				deploymentObj, err := assets.LoadDeploymentManifest(ComponentName)
				g.Expect(err).ToNot(HaveOccurred())

				err = opts.adaptStandaloneDeployment(cpContext, deploymentObj)
				g.Expect(err).ToNot(HaveOccurred())

				container := podspec.FindContainer(ComponentName, deploymentObj.Spec.Template.Spec.Containers)
				g.Expect(container).ToNot(BeNil())
				g.Expect(container.Env).To(ContainElement(
					corev1.EnvVar{Name: rhobsmonitoring.EnvironmentVariable, Value: "1"},
				))
			},
		},
		{
			name:         "When RHOBS monitoring is disabled, it should not set the env var",
			platformType: hyperv1.AWSPlatform,
			awsRegion:    "us-east-1",
			infraID:      "test-no-rhobs",
			rhobsEnabled: false,
			images: map[string]string{
				karpenterutil.KarpenterProviderAWSImageName: "quay.io/openshift/karpenter-aws:latest",
				"token-minter": "quay.io/openshift/token-minter:latest",
			},
			validateFunc: func(t *testing.T, g Gomega, opts *KarpenterOperatorOptions, cpContext controlplanecomponent.WorkloadContext) {
				t.Helper()
				deploymentObj, err := assets.LoadDeploymentManifest(ComponentName)
				g.Expect(err).ToNot(HaveOccurred())

				err = opts.adaptStandaloneDeployment(cpContext, deploymentObj)
				g.Expect(err).ToNot(HaveOccurred())

				container := podspec.FindContainer(ComponentName, deploymentObj.Spec.Template.Spec.Containers)
				g.Expect(container).ToNot(BeNil())
				g.Expect(podspec.FindEnvVar(rhobsmonitoring.EnvironmentVariable, container.Env)).To(BeNil())
			},
		},
		{
			name:         "When HCP has KarpenterOperatorImage annotation, it should override the image",
			platformType: hyperv1.AWSPlatform,
			awsRegion:    "us-west-2",
			infraID:      "test-infra-override",
			hcpAnnotations: map[string]string{
				hyperkarpenterv1.KarpenterOperatorImage: "quay.io/custom/karpenter-operator:test",
			},
			images: map[string]string{
				karpenterutil.KarpenterOperatorImageName:    "quay.io/openshift/karpenter-operator:latest",
				karpenterutil.KarpenterProviderAWSImageName: "quay.io/openshift/karpenter-aws:latest",
				"token-minter": "quay.io/openshift/token-minter:latest",
			},
			validateFunc: func(t *testing.T, g Gomega, opts *KarpenterOperatorOptions, cpContext controlplanecomponent.WorkloadContext) {
				t.Helper()
				deploymentObj, err := assets.LoadDeploymentManifest(ComponentName)
				g.Expect(err).ToNot(HaveOccurred())

				err = opts.adaptStandaloneDeployment(cpContext, deploymentObj)
				g.Expect(err).ToNot(HaveOccurred())

				container := podspec.FindContainer(ComponentName, deploymentObj.Spec.Template.Spec.Containers)
				g.Expect(container).ToNot(BeNil())
				g.Expect(container.Image).To(Equal("quay.io/custom/karpenter-operator:test"))
			},
		},
		{
			name:         "When HCP has no KarpenterOperatorImage annotation, it should use the default image",
			platformType: hyperv1.AWSPlatform,
			awsRegion:    "us-west-2",
			infraID:      "test-infra-default",
			images: map[string]string{
				karpenterutil.KarpenterOperatorImageName:    "quay.io/openshift/karpenter-operator:latest",
				karpenterutil.KarpenterProviderAWSImageName: "quay.io/openshift/karpenter-aws:latest",
				"token-minter": "quay.io/openshift/token-minter:latest",
			},
			validateFunc: func(t *testing.T, g Gomega, opts *KarpenterOperatorOptions, cpContext controlplanecomponent.WorkloadContext) {
				t.Helper()
				deploymentObj, err := assets.LoadDeploymentManifest(ComponentName)
				g.Expect(err).ToNot(HaveOccurred())

				err = opts.adaptStandaloneDeployment(cpContext, deploymentObj)
				g.Expect(err).ToNot(HaveOccurred())

				container := podspec.FindContainer(ComponentName, deploymentObj.Spec.Template.Spec.Containers)
				g.Expect(container).ToNot(BeNil())
				g.Expect(container.Image).To(Equal("quay.io/openshift/karpenter-operator:latest"))
			},
		},
		{
			name:         "When HCP has empty KarpenterOperatorImage annotation, it should use the default image",
			platformType: hyperv1.AWSPlatform,
			awsRegion:    "us-west-2",
			infraID:      "test-infra-empty-override",
			hcpAnnotations: map[string]string{
				hyperkarpenterv1.KarpenterOperatorImage: "",
			},
			images: map[string]string{
				karpenterutil.KarpenterOperatorImageName:    "quay.io/openshift/karpenter-operator:latest",
				karpenterutil.KarpenterProviderAWSImageName: "quay.io/openshift/karpenter-aws:latest",
				"token-minter": "quay.io/openshift/token-minter:latest",
			},
			validateFunc: func(t *testing.T, g Gomega, opts *KarpenterOperatorOptions, cpContext controlplanecomponent.WorkloadContext) {
				t.Helper()
				deploymentObj, err := assets.LoadDeploymentManifest(ComponentName)
				g.Expect(err).ToNot(HaveOccurred())

				err = opts.adaptStandaloneDeployment(cpContext, deploymentObj)
				g.Expect(err).ToNot(HaveOccurred())

				container := podspec.FindContainer(ComponentName, deploymentObj.Spec.Template.Spec.Containers)
				g.Expect(container).ToNot(BeNil())
				g.Expect(container.Image).To(Equal("quay.io/openshift/karpenter-operator:latest"))
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
					Name:        "test-hcp",
					Namespace:   "test-namespace",
					Annotations: tc.hcpAnnotations,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID: tc.infraID,
					Platform: hyperv1.PlatformSpec{
						Type: tc.platformType,
					},
				},
				Status: hyperv1.HostedControlPlaneStatus{
					ControlPlaneEndpoint: hyperv1.APIEndpoint{
						Host: "api.test-cluster.example.com",
						Port: 6443,
					},
				},
			}

			if tc.platformType == hyperv1.AWSPlatform {
				hcp.Spec.Platform.AWS = &hyperv1.AWSPlatformSpec{
					Region: tc.awsRegion,
				}
			}
			if tc.platformType == hyperv1.AzurePlatform {
				hcp.Spec.Platform.Azure = &hyperv1.AzurePlatformSpec{
					Location:          tc.azureLocation,
					TenantID:          "tenant-id",
					SubscriptionID:    "subscription-id",
					ResourceGroupName: "test-rg",
					SubnetID:          "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet",
				}
				hcp.Spec.AutoNode = hyperv1.AutoNode{
					Provisioner: hyperv1.ProvisionerConfig{
						Name: hyperv1.ProvisionerKarpenter,
						Karpenter: hyperv1.KarpenterConfig{
							Platform: hyperv1.AzurePlatform,
							Azure: hyperv1.KarpenterAzureConfig{
								ClientID: "12345678-1234-1234-1234-123456789012",
							},
						},
					},
				}
			}

			opts := &KarpenterOperatorOptions{
				HyperShiftOperatorImage:            "quay.io/hypershift/operator:latest",
				ControlPlaneOperatorImage:          "quay.io/hypershift/cpo:latest",
				IgnitionEndpoint:                   "https://ignition.example.com",
				StandaloneKarpenterOperatorEnabled: true,
			}

			cpContext := controlplanecomponent.WorkloadContext{
				Context:              t.Context(),
				HCP:                  hcp,
				ReleaseImageProvider: &fakeReleaseImageProvider{images: tc.images},
			}

			tc.validateFunc(t, g, opts, cpContext)
		})
	}
}
