package assets

import (
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestHyperShiftOperatorDeployment_Build(t *testing.T) {
	testNamespace := "hypershift"
	testOperatorImage := "myimage"
	tests := map[string]struct {
		inputBuildParameters HyperShiftOperatorDeployment
		expectedVolumeMounts []corev1.VolumeMount
		expectedVolumes      []corev1.Volume
		expectedArgs         []string
		expectedEnvVars      []corev1.EnvVar
	}{
		"empty oidc parameters result in no volume mounts": {
			inputBuildParameters: HyperShiftOperatorDeployment{
				Namespace: &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: testNamespace,
					},
				},
				OperatorImage: testOperatorImage,
				ServiceAccount: &corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name: "hypershift",
					},
				},
				Replicas:        3,
				PrivatePlatform: string(hyperv1.NonePlatform),
			},
			expectedVolumeMounts: nil,
			expectedVolumes:      nil,
			expectedArgs: []string{
				"run",
				"--namespace=$(MY_NAMESPACE)",
				"--pod-name=$(MY_NAME)",
				"--metrics-addr=:9000",
				fmt.Sprintf("--enable-dedicated-request-serving-isolation=%t", false),
				fmt.Sprintf("--enable-ocp-cluster-monitoring=%t", false),
				fmt.Sprintf("--enable-ci-debug-output=%t", false),
				fmt.Sprintf("--private-platform=%s", string(hyperv1.NonePlatform)),
			},
		},
		"additional-trust-bundle parameter mounts ca bundle volume": {
			inputBuildParameters: HyperShiftOperatorDeployment{
				Namespace: &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: testNamespace,
					},
				},
				OperatorImage: testOperatorImage,
				ServiceAccount: &corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name: "hypershift",
					},
				},
				Replicas:        3,
				PrivatePlatform: string(hyperv1.NonePlatform),
				AdditionalTrustBundle: &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name: "user-ca-bundle",
					},
				},
			},
			expectedVolumeMounts: []corev1.VolumeMount{
				{
					Name:      "trusted-ca",
					ReadOnly:  true,
					MountPath: "/etc/pki/tls/certs",
				},
			},
			expectedVolumes: []corev1.Volume{
				{
					Name: "trusted-ca",
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{Name: "user-ca-bundle"},
							Items:                []corev1.KeyToPath{{Key: "ca-bundle.crt", Path: "user-ca-bundle.pem"}},
						},
					},
				},
			},
			expectedArgs: []string{
				"run",
				"--namespace=$(MY_NAMESPACE)",
				"--pod-name=$(MY_NAME)",
				"--metrics-addr=:9000",
				fmt.Sprintf("--enable-dedicated-request-serving-isolation=%t", false),
				fmt.Sprintf("--enable-ocp-cluster-monitoring=%t", false),
				fmt.Sprintf("--enable-ci-debug-output=%t", false),
				fmt.Sprintf("--private-platform=%s", string(hyperv1.NonePlatform)),
			},
		},
		"specify oidc parameters result in appropriate volumes and volumeMounts": {
			inputBuildParameters: HyperShiftOperatorDeployment{
				Namespace: &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: testNamespace,
					},
				},
				OperatorImage: testOperatorImage,
				ServiceAccount: &corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name: "hypershift",
					},
				},
				Replicas:         3,
				PrivatePlatform:  string(hyperv1.NonePlatform),
				OIDCBucketRegion: "us-east-1",
				OIDCStorageProviderS3Secret: &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name: "oidc-s3-secret",
					},
				},
				OIDCBucketName:                 "oidc-bucket",
				OIDCStorageProviderS3SecretKey: "mykey",
			},
			expectedArgs: []string{
				"run",
				"--namespace=$(MY_NAMESPACE)",
				"--pod-name=$(MY_NAME)",
				"--metrics-addr=:9000",
				fmt.Sprintf("--enable-dedicated-request-serving-isolation=%t", false),
				fmt.Sprintf("--enable-ocp-cluster-monitoring=%t", false),
				fmt.Sprintf("--enable-ci-debug-output=%t", false),
				fmt.Sprintf("--private-platform=%s", string(hyperv1.NonePlatform)),
				"--oidc-storage-provider-s3-bucket-name=" + "oidc-bucket",
				"--oidc-storage-provider-s3-region=" + "us-east-1",
				"--oidc-storage-provider-s3-credentials=/etc/oidc-storage-provider-s3-creds/" + "mykey",
			},
			expectedVolumeMounts: []corev1.VolumeMount{
				{
					Name:      "oidc-storage-provider-s3-creds",
					MountPath: "/etc/oidc-storage-provider-s3-creds",
				},
			},
			expectedVolumes: []corev1.Volume{
				{
					Name: "oidc-storage-provider-s3-creds",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: "oidc-s3-secret",
						},
					},
				},
			},
		},
		"specify aws private creds and oidc parameters result in appropriate volumes and volumeMounts": {
			inputBuildParameters: HyperShiftOperatorDeployment{
				Namespace: &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: testNamespace,
					},
				},
				OperatorImage: testOperatorImage,
				ServiceAccount: &corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name: "hypershift",
					},
				},
				Replicas:         3,
				PrivatePlatform:  string(hyperv1.AWSPlatform),
				AWSPrivateRegion: "us-east-1",
				AWSPrivateSecret: &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name: awsCredsSecretName,
					},
				},
				AWSPrivateSecretKey: "mykey",
				OIDCBucketRegion:    "us-east-1",
				OIDCStorageProviderS3Secret: &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name: "oidc-s3-secret",
					},
				},
				OIDCBucketName:                 "oidc-bucket",
				OIDCStorageProviderS3SecretKey: "mykey",
			},
			expectedArgs: []string{
				"run",
				"--namespace=$(MY_NAMESPACE)",
				"--pod-name=$(MY_NAME)",
				"--metrics-addr=:9000",
				fmt.Sprintf("--enable-dedicated-request-serving-isolation=%t", false),
				fmt.Sprintf("--enable-ocp-cluster-monitoring=%t", false),
				fmt.Sprintf("--enable-ci-debug-output=%t", false),
				fmt.Sprintf("--private-platform=%s", string(hyperv1.AWSPlatform)),
				"--oidc-storage-provider-s3-bucket-name=" + "oidc-bucket",
				"--oidc-storage-provider-s3-region=" + "us-east-1",
				"--oidc-storage-provider-s3-credentials=/etc/oidc-storage-provider-s3-creds/" + "mykey",
			},
			expectedVolumeMounts: []corev1.VolumeMount{
				{
					Name:      "oidc-storage-provider-s3-creds",
					MountPath: "/etc/oidc-storage-provider-s3-creds",
				},
				{
					Name:      "credentials",
					MountPath: "/etc/provider",
				},
				{
					Name:      "token",
					MountPath: "/var/run/secrets/openshift/serviceaccount",
				},
			},
			expectedVolumes: []corev1.Volume{
				{
					Name: "oidc-storage-provider-s3-creds",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: "oidc-s3-secret",
						},
					},
				},
				{
					Name: "credentials",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: awsCredsSecretName,
						},
					},
				},
				{
					Name: "token",
					VolumeSource: corev1.VolumeSource{
						Projected: &corev1.ProjectedVolumeSource{
							Sources: []corev1.VolumeProjection{
								{
									ServiceAccountToken: &corev1.ServiceAccountTokenProjection{
										Audience: "openshift",
										Path:     "token",
									},
								},
							},
						},
					},
				},
			},
		},
		"When GCP private platform is specified it should set GCP_PROJECT and GCP_REGION env vars": {
			inputBuildParameters: HyperShiftOperatorDeployment{
				Namespace: &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: testNamespace,
					},
				},
				OperatorImage: testOperatorImage,
				ServiceAccount: &corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name: "hypershift",
					},
				},
				Replicas:        3,
				PrivatePlatform: string(hyperv1.GCPPlatform),
				GCPProject:      "my-gcp-project",
				GCPRegion:       "us-central1",
			},
			expectedVolumeMounts: nil,
			expectedVolumes:      nil,
			expectedArgs: []string{
				"run",
				"--namespace=$(MY_NAMESPACE)",
				"--pod-name=$(MY_NAME)",
				"--metrics-addr=:9000",
				fmt.Sprintf("--enable-dedicated-request-serving-isolation=%t", false),
				fmt.Sprintf("--enable-ocp-cluster-monitoring=%t", false),
				fmt.Sprintf("--enable-ci-debug-output=%t", false),
				fmt.Sprintf("--private-platform=%s", string(hyperv1.GCPPlatform)),
			},
			expectedEnvVars: []corev1.EnvVar{
				{
					Name:  "GCP_PROJECT",
					Value: "my-gcp-project",
				},
				{
					Name:  "GCP_REGION",
					Value: "us-central1",
				},
			},
		},
		"When Azure private platform is specified, it should mount credentials and set AZURE_CREDENTIALS_FILE env var": {
			inputBuildParameters: HyperShiftOperatorDeployment{
				Namespace: &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: testNamespace,
					},
				},
				OperatorImage: testOperatorImage,
				ServiceAccount: &corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name: "hypershift",
					},
				},
				Replicas:              3,
				PrivatePlatform:       string(hyperv1.AzurePlatform),
				AzurePLSResourceGroup: "rg-mgmt",
				AzurePrivateSecret: &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name: azureCredsSecretName,
					},
				},
				AzurePrivateSecretKey: "credentials",
			},
			expectedVolumeMounts: []corev1.VolumeMount{
				{
					Name:      "azure-credentials",
					MountPath: "/etc/azure-provider",
					ReadOnly:  true,
				},
			},
			expectedVolumes: []corev1.Volume{
				{
					Name: "azure-credentials",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: azureCredsSecretName,
						},
					},
				},
			},
			expectedArgs: []string{
				"run",
				"--namespace=$(MY_NAMESPACE)",
				"--pod-name=$(MY_NAME)",
				"--metrics-addr=:9000",
				fmt.Sprintf("--enable-dedicated-request-serving-isolation=%t", false),
				fmt.Sprintf("--enable-ocp-cluster-monitoring=%t", false),
				fmt.Sprintf("--enable-ci-debug-output=%t", false),
				fmt.Sprintf("--private-platform=%s", string(hyperv1.AzurePlatform)),
			},
			expectedEnvVars: []corev1.EnvVar{
				{
					Name:  "AZURE_RESOURCE_GROUP",
					Value: "rg-mgmt",
				},
				{
					Name:  "AZURE_CREDENTIALS_FILE",
					Value: "/etc/azure-provider/credentials",
				},
			},
		},
		"When Azure private platform is specified with managed identity, it should set AZURE_PLS_CLIENT_ID and AZURE_SUBSCRIPTION_ID env vars and workload identity pod label": {
			inputBuildParameters: HyperShiftOperatorDeployment{
				Namespace: &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: testNamespace,
					},
				},
				OperatorImage: testOperatorImage,
				ServiceAccount: &corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name: "hypershift",
					},
				},
				Replicas:                        3,
				PrivatePlatform:                 string(hyperv1.AzurePlatform),
				AzurePLSManagedIdentityClientID: "00000000-0000-0000-0000-000000000001",
				AzurePLSSubscriptionID:          "00000000-0000-0000-0000-000000000002",
				AzurePLSResourceGroup:           "rg-mgmt",
			},
			expectedVolumeMounts: nil,
			expectedVolumes:      nil,
			expectedArgs: []string{
				"run",
				"--namespace=$(MY_NAMESPACE)",
				"--pod-name=$(MY_NAME)",
				"--metrics-addr=:9000",
				fmt.Sprintf("--enable-dedicated-request-serving-isolation=%t", false),
				fmt.Sprintf("--enable-ocp-cluster-monitoring=%t", false),
				fmt.Sprintf("--enable-ci-debug-output=%t", false),
				fmt.Sprintf("--private-platform=%s", string(hyperv1.AzurePlatform)),
			},
			expectedEnvVars: []corev1.EnvVar{
				{
					Name:  "AZURE_RESOURCE_GROUP",
					Value: "rg-mgmt",
				},
				{
					Name:  "AZURE_PLS_CLIENT_ID",
					Value: "00000000-0000-0000-0000-000000000001",
				},
				{
					Name:  "AZURE_SUBSCRIPTION_ID",
					Value: "00000000-0000-0000-0000-000000000002",
				},
			},
		},
		"specify dedicated request serving isolation parameter (true) result in appropriate arguments": {
			inputBuildParameters: HyperShiftOperatorDeployment{
				Namespace: &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: testNamespace,
					},
				},
				OperatorImage: testOperatorImage,
				ServiceAccount: &corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name: "hypershift",
					},
				},
				Replicas:                               3,
				PrivatePlatform:                        string(hyperv1.NonePlatform),
				EnableDedicatedRequestServingIsolation: true,
			},
			expectedVolumeMounts: nil,
			expectedVolumes:      nil,
			expectedArgs: []string{
				"run",
				"--namespace=$(MY_NAMESPACE)",
				"--pod-name=$(MY_NAME)",
				"--metrics-addr=:9000",
				fmt.Sprintf("--enable-dedicated-request-serving-isolation=%t", true),
				fmt.Sprintf("--enable-ocp-cluster-monitoring=%t", false),
				fmt.Sprintf("--enable-ci-debug-output=%t", false),
				fmt.Sprintf("--private-platform=%s", string(hyperv1.NonePlatform)),
			},
		},
		"specify dedicated request serving isolation parameter (false) result in appropriate arguments": {
			inputBuildParameters: HyperShiftOperatorDeployment{
				Namespace: &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: testNamespace,
					},
				},
				OperatorImage: testOperatorImage,
				ServiceAccount: &corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name: "hypershift",
					},
				},
				Replicas:                               3,
				PrivatePlatform:                        string(hyperv1.NonePlatform),
				EnableDedicatedRequestServingIsolation: false,
			},
			expectedVolumeMounts: nil,
			expectedVolumes:      nil,
			expectedArgs: []string{
				"run",
				"--namespace=$(MY_NAMESPACE)",
				"--pod-name=$(MY_NAME)",
				"--metrics-addr=:9000",
				fmt.Sprintf("--enable-dedicated-request-serving-isolation=%t", false),
				fmt.Sprintf("--enable-ocp-cluster-monitoring=%t", false),
				fmt.Sprintf("--enable-ci-debug-output=%t", false),
				fmt.Sprintf("--private-platform=%s", string(hyperv1.NonePlatform)),
			},
		},
		"When TechPreviewNoUpgrade it results in appropriate arguments for the HO": {
			inputBuildParameters: HyperShiftOperatorDeployment{
				Namespace: &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: testNamespace,
					},
				},
				OperatorImage: testOperatorImage,
				ServiceAccount: &corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name: "hypershift",
					},
				},
				Replicas:                               3,
				PrivatePlatform:                        string(hyperv1.NonePlatform),
				EnableDedicatedRequestServingIsolation: false,
				TechPreviewNoUpgrade:                   true,
			},
			expectedVolumeMounts: nil,
			expectedVolumes:      nil,
			expectedArgs: []string{
				"run",
				"--namespace=$(MY_NAMESPACE)",
				"--pod-name=$(MY_NAME)",
				"--metrics-addr=:9000",
				fmt.Sprintf("--enable-dedicated-request-serving-isolation=%t", false),
				fmt.Sprintf("--enable-ocp-cluster-monitoring=%t", false),
				fmt.Sprintf("--enable-ci-debug-output=%t", false),
				fmt.Sprintf("--private-platform=%s", string(hyperv1.NonePlatform)),
			},
		},
		"When AdditionalEnvironmentVariables are set, they are included as env vars in the HO deployment": {
			inputBuildParameters: HyperShiftOperatorDeployment{
				Namespace: &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: testNamespace,
					},
				},
				OperatorImage: testOperatorImage,
				ServiceAccount: &corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name: "hypershift",
					},
				},
				Replicas:                               3,
				PrivatePlatform:                        string(hyperv1.NonePlatform),
				EnableDedicatedRequestServingIsolation: false,
				AdditionalOperatorEnvVars: map[string]string{
					"TEST1": "value1",
					"TEST2": "value2",
				},
			},
			expectedEnvVars: []corev1.EnvVar{
				{
					Name:  "TEST1",
					Value: "value1",
				},
				{
					Name:  "TEST2",
					Value: "value2",
				},
			},
			expectedArgs: []string{
				"run",
				"--namespace=$(MY_NAMESPACE)",
				"--pod-name=$(MY_NAME)",
				"--metrics-addr=:9000",
				fmt.Sprintf("--enable-dedicated-request-serving-isolation=%t", false),
				fmt.Sprintf("--enable-ocp-cluster-monitoring=%t", false),
				fmt.Sprintf("--enable-ci-debug-output=%t", false),
				fmt.Sprintf("--private-platform=%s", string(hyperv1.NonePlatform)),
			},
		},
		"AdditionalEnvironmentVariables dont overwrite existing environment variables set": {
			inputBuildParameters: HyperShiftOperatorDeployment{
				Namespace: &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: testNamespace,
					},
				},
				OperatorImage: testOperatorImage,
				ServiceAccount: &corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name: "hypershift",
					},
				},
				Replicas:                               3,
				PrivatePlatform:                        string(hyperv1.NonePlatform),
				EnableDedicatedRequestServingIsolation: false,
				AdditionalOperatorEnvVars: map[string]string{
					"MY_NAMESPACE": "testnamespace",
					"MY_NAME":      "testname",
				},
			},
			// These are the existing environment variables on the deployment that should not be overwritten.
			expectedEnvVars: []corev1.EnvVar{
				{
					Name: "MY_NAMESPACE",
					ValueFrom: &corev1.EnvVarSource{
						FieldRef: &corev1.ObjectFieldSelector{
							FieldPath: "metadata.namespace",
						},
					},
				},
				{
					Name: "MY_NAME",
					ValueFrom: &corev1.EnvVarSource{
						FieldRef: &corev1.ObjectFieldSelector{
							FieldPath: "metadata.name",
						},
					},
				},
			},
			expectedArgs: []string{
				"run",
				"--namespace=$(MY_NAMESPACE)",
				"--pod-name=$(MY_NAME)",
				"--metrics-addr=:9000",
				fmt.Sprintf("--enable-dedicated-request-serving-isolation=%t", false),
				fmt.Sprintf("--enable-ocp-cluster-monitoring=%t", false),
				fmt.Sprintf("--enable-ci-debug-output=%t", false),
				fmt.Sprintf("--private-platform=%s", string(hyperv1.NonePlatform)),
			},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			deployment := test.inputBuildParameters.Build()
			g.Expect(deployment.Spec.Template.Spec.Containers[0].Args).To(BeEquivalentTo(test.expectedArgs))
			g.Expect(deployment.Spec.Template.Spec.Volumes).To(BeEquivalentTo(test.expectedVolumes))
			g.Expect(deployment.Spec.Template.Spec.Containers[0].VolumeMounts).To(BeEquivalentTo(test.expectedVolumeMounts))
			g.Expect(deployment.Spec.Template.Spec.Containers[0].Env).To(ContainElements(test.expectedEnvVars))
		})
	}
}

func TestExternalDNSDeployment_Build(t *testing.T) {
	baseDeployment := func() ExternalDNSDeployment {
		return ExternalDNSDeployment{
			Namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "hypershift",
				},
			},
			Image: "external-dns:latest",
			ServiceAccount: &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name: "external-dns",
				},
			},
			Provider:     AWSExternalDNSProvider,
			DomainFilter: "example.com",
			CredentialsSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "dns-creds",
				},
			},
			TxtOwnerId: "test-owner",
		}
	}

	tests := map[string]struct {
		modify     func(*ExternalDNSDeployment)
		assertArgs func(*GomegaWithT, []string)
		assertEnv  func(*GomegaWithT, []corev1.EnvVar)
	}{
		"When no interval is specified it should use the default 1m interval": {
			modify: func(d *ExternalDNSDeployment) {},
			assertArgs: func(g *GomegaWithT, args []string) {
				g.Expect(args).To(ContainElement("--interval=" + DefaultExternalDNSInterval))
			},
		},
		"When a custom interval is specified it should use the custom interval": {
			modify: func(d *ExternalDNSDeployment) {
				d.Interval = "5m"
			},
			assertArgs: func(g *GomegaWithT, args []string) {
				g.Expect(args).To(ContainElement("--interval=5m"))
				g.Expect(args).NotTo(ContainElement("--interval=" + DefaultExternalDNSInterval))
			},
		},
		"When no AWS zones cache duration is specified it should use the default 1h for AWS provider": {
			modify: func(d *ExternalDNSDeployment) {},
			assertArgs: func(g *GomegaWithT, args []string) {
				g.Expect(args).To(ContainElement("--aws-zones-cache-duration=" + DefaultExternalDNSAWSZonesCacheDuration))
			},
		},
		"When a custom AWS zones cache duration is specified it should use the custom value": {
			modify: func(d *ExternalDNSDeployment) {
				d.AWSZonesCacheDuration = "10m"
			},
			assertArgs: func(g *GomegaWithT, args []string) {
				g.Expect(args).To(ContainElement("--aws-zones-cache-duration=10m"))
				g.Expect(args).NotTo(ContainElement("--aws-zones-cache-duration=" + DefaultExternalDNSAWSZonesCacheDuration))
			},
		},
		"When both custom interval and AWS zones cache duration are specified it should use both custom values": {
			modify: func(d *ExternalDNSDeployment) {
				d.Interval = "10m"
				d.AWSZonesCacheDuration = "30m"
			},
			assertArgs: func(g *GomegaWithT, args []string) {
				g.Expect(args).To(ContainElement("--interval=10m"))
				g.Expect(args).To(ContainElement("--aws-zones-cache-duration=30m"))
			},
		},
		"When Azure provider is used it should not include AWS zones cache duration arg": {
			modify: func(d *ExternalDNSDeployment) {
				d.Provider = AzureExternalDNSProvider
			},
			assertArgs: func(g *GomegaWithT, args []string) {
				g.Expect(args).To(ContainElement("--interval=" + DefaultExternalDNSInterval))
				for _, arg := range args {
					g.Expect(arg).NotTo(HavePrefix("--aws-zones-cache-duration"))
				}
			},
			assertEnv: func(g *GomegaWithT, envVars []corev1.EnvVar) {
				g.Expect(envVars).To(ContainElement(corev1.EnvVar{Name: "AZURE_SDK_MAX_RETRIES", Value: "5"}))
			},
		},
		"When GCP provider is used it should not include AWS zones cache duration arg": {
			modify: func(d *ExternalDNSDeployment) {
				d.Provider = GCPExternalDNSProvider
			},
			assertArgs: func(g *GomegaWithT, args []string) {
				g.Expect(args).To(ContainElement("--interval=" + DefaultExternalDNSInterval))
				for _, arg := range args {
					g.Expect(arg).NotTo(HavePrefix("--aws-zones-cache-duration"))
				}
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			d := baseDeployment()
			test.modify(&d)
			deployment := d.Build()
			test.assertArgs(g, deployment.Spec.Template.Spec.Containers[0].Args)
			if test.assertEnv != nil {
				test.assertEnv(g, deployment.Spec.Template.Spec.Containers[0].Env)
			}
		})
	}
}

func TestHyperShiftOperatorDeployment_Build_WorkloadIdentityLabel(t *testing.T) {
	tests := map[string]struct {
		input       HyperShiftOperatorDeployment
		expectLabel bool
	}{
		"When Azure managed identity is specified, it should add workload identity pod label": {
			input: HyperShiftOperatorDeployment{
				Namespace: &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{Name: "hypershift"},
				},
				OperatorImage: "myimage",
				ServiceAccount: &corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{Name: "hypershift"},
				},
				Replicas:                        3,
				PrivatePlatform:                 string(hyperv1.AzurePlatform),
				AzurePLSManagedIdentityClientID: "test-client-id",
				AzurePLSSubscriptionID:          "test-sub-id",
			},
			expectLabel: true,
		},
		"When Azure credential file is specified, it should not add workload identity pod label": {
			input: HyperShiftOperatorDeployment{
				Namespace: &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{Name: "hypershift"},
				},
				OperatorImage: "myimage",
				ServiceAccount: &corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{Name: "hypershift"},
				},
				Replicas:        3,
				PrivatePlatform: string(hyperv1.AzurePlatform),
				AzurePrivateSecret: &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "my-creds"},
				},
				AzurePrivateSecretKey: "credentials",
			},
			expectLabel: false,
		},
		"When no Azure config is specified, it should not add workload identity pod label": {
			input: HyperShiftOperatorDeployment{
				Namespace: &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{Name: "hypershift"},
				},
				OperatorImage: "myimage",
				ServiceAccount: &corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{Name: "hypershift"},
				},
				Replicas:        3,
				PrivatePlatform: string(hyperv1.NonePlatform),
			},
			expectLabel: false,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			deployment := test.input.Build()
			labels := deployment.Spec.Template.Labels
			if test.expectLabel {
				g.Expect(labels).To(HaveKeyWithValue("azure.workload.identity/use", "true"),
					"should have workload identity pod label when managed identity is configured")
			} else {
				g.Expect(labels).ToNot(HaveKey("azure.workload.identity/use"),
					"should not have workload identity pod label")
			}
		})
	}
}

func TestHyperShiftOperatorClusterRole_WebhookRBAC(t *testing.T) {
	t.Parallel()
	clusterRole := HyperShiftOperatorClusterRole{}.Build()

	t.Run("When building the ClusterRole it should include cluster-scoped RBAC for webhook configurations", func(t *testing.T) {
		t.Parallel()
		g := NewGomegaWithT(t)
		g.Expect(clusterRole.Rules).To(ContainElement(Equal(rbacv1.PolicyRule{
			APIGroups: []string{"admissionregistration.k8s.io"},
			Resources: []string{"mutatingwebhookconfigurations", "validatingwebhookconfigurations"},
			Verbs:     []string{"get", "list", "watch", "update"},
		})))
	})

	t.Run("When building the ClusterRole it should include scoped delete for validatingwebhookconfigurations", func(t *testing.T) {
		t.Parallel()
		g := NewGomegaWithT(t)
		g.Expect(clusterRole.Rules).To(ContainElement(Equal(rbacv1.PolicyRule{
			APIGroups:     []string{"admissionregistration.k8s.io"},
			Resources:     []string{"validatingwebhookconfigurations"},
			Verbs:         []string{"delete"},
			ResourceNames: []string{hyperv1.GroupVersion.Group},
		})))
	})
}
