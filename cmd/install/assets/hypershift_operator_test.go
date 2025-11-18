package assets

import (
	"fmt"
	"reflect"
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
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			deployment := test.inputBuildParameters.Build()
			g.Expect(deployment.Spec.Template.Spec.Containers[0].Args).To(BeEquivalentTo(test.expectedArgs))
			g.Expect(deployment.Spec.Template.Spec.Volumes).To(BeEquivalentTo(test.expectedVolumes))
			g.Expect(deployment.Spec.Template.Spec.Containers[0].VolumeMounts).To(BeEquivalentTo(test.expectedVolumeMounts))
		})
	}
}

// TestHyperShiftOperatorClusterRole_SecretProviderClassRBAC verifies that SecretProviderClass RBAC
// permissions are granted unconditionally, regardless of the ManagedService setting.
// This is a regression test for OCPBUGS-65687 where the RBAC was previously only granted for AroHCP,
// causing "reflector forbidden" errors on other platforms when the Secrets Store CSI Driver CRD was installed.
func TestHyperShiftOperatorClusterRole_SecretProviderClassRBAC(t *testing.T) {
	tests := map[string]struct {
		managedService string
	}{
		"When ManagedService is empty it should include SecretProviderClass RBAC": {
			managedService: "",
		},
		"When ManagedService is AroHCP it should include SecretProviderClass RBAC": {
			managedService: hyperv1.AroHCP,
		},
		"When ManagedService is another value it should include SecretProviderClass RBAC": {
			managedService: "some-other-service",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			clusterRole := HyperShiftOperatorClusterRole{
				ManagedService: test.managedService,
			}.Build()

			// Verify SecretProviderClass RBAC rule exists
			expectedRule := rbacv1.PolicyRule{
				APIGroups: []string{"secrets-store.csi.x-k8s.io"},
				Resources: []string{"secretproviderclasses"},
				Verbs:     []string{"get", "list", "create", "update", "watch"},
			}

			found := false
			for _, rule := range clusterRole.Rules {
				if reflect.DeepEqual(rule, expectedRule) {
					found = true
					break
				}
			}

			g.Expect(found).To(BeTrue(),
				"SecretProviderClass RBAC rule should be present unconditionally to prevent 'reflector forbidden' errors when Secrets Store CSI Driver CRD is installed (OCPBUGS-65687)")
		})
	}
}
