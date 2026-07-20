package aws

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	capiaws "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"

	"github.com/google/go-cmp/cmp"
)

func TestReconcileAWSCluster(t *testing.T) {
	testCases := []struct {
		name              string
		initialAWSCluster *capiaws.AWSCluster
		hostedCluster     *hyperv1.HostedCluster

		expectedAWSCluster *capiaws.AWSCluster
	}{
		{
			name:              "Tags get copied over",
			initialAWSCluster: &capiaws.AWSCluster{},
			hostedCluster: &hyperv1.HostedCluster{Spec: hyperv1.HostedClusterSpec{Platform: hyperv1.PlatformSpec{AWS: &hyperv1.AWSPlatformSpec{
				ResourceTags: []hyperv1.AWSResourceTag{
					{Key: "foo", Value: "bar"},
				},
			}}}},

			expectedAWSCluster: &capiaws.AWSCluster{
				ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
					"cluster.x-k8s.io/managed-by": "external",
				}},
				Spec: capiaws.AWSClusterSpec{
					AdditionalTags: capiaws.Tags{"foo": "bar"},
				},
				Status: capiaws.AWSClusterStatus{
					Ready: true,
				},
			},
		},
		{
			name: "Existing tags get removed",
			initialAWSCluster: &capiaws.AWSCluster{Spec: capiaws.AWSClusterSpec{AdditionalTags: capiaws.Tags{
				"to-be-removed": "value",
			}}},
			hostedCluster: &hyperv1.HostedCluster{Spec: hyperv1.HostedClusterSpec{Platform: hyperv1.PlatformSpec{AWS: &hyperv1.AWSPlatformSpec{
				ResourceTags: []hyperv1.AWSResourceTag{
					{Key: "foo", Value: "bar"},
				},
			}}}},

			expectedAWSCluster: &capiaws.AWSCluster{
				ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
					"cluster.x-k8s.io/managed-by": "external",
				}},
				Spec: capiaws.AWSClusterSpec{
					AdditionalTags: capiaws.Tags{"foo": "bar"},
				},
				Status: capiaws.AWSClusterStatus{
					Ready: true,
				},
			},
		},
		{
			name: "No tags on hostedcluster clears existing awscluster tags",
			initialAWSCluster: &capiaws.AWSCluster{Spec: capiaws.AWSClusterSpec{AdditionalTags: capiaws.Tags{
				"to-be-removed": "value",
			}}},
			hostedCluster: &hyperv1.HostedCluster{},

			expectedAWSCluster: &capiaws.AWSCluster{
				ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
					"cluster.x-k8s.io/managed-by": "external",
				}},
				Status: capiaws.AWSClusterStatus{
					Ready: true,
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if err := reconcileAWSCluster(tc.initialAWSCluster, tc.hostedCluster, hyperv1.APIEndpoint{}, nil); err != nil {
				t.Fatalf("reconcileAWSCluster failed: %v", err)
			}
			if diff := cmp.Diff(tc.initialAWSCluster, tc.expectedAWSCluster); diff != "" {
				t.Errorf("reconciled AWS cluster differs from expected AWS cluster: %s", diff)
			}
		})
	}
}

func TestGetCredentialStatus(t *testing.T) {
	testCases := []struct {
		name                                    string
		ValidOIDCConfigurationConditionStatus   *metav1.ConditionStatus // nil means condition not present
		ValidAWSIdentityProviderConditionStatus *metav1.ConditionStatus // nil means condition not present

		expectedResult CredentialStatus
	}{
		{
			name:                                    "When both conditions are True, return Valid",
			ValidOIDCConfigurationConditionStatus:   &[]metav1.ConditionStatus{metav1.ConditionTrue}[0],
			ValidAWSIdentityProviderConditionStatus: &[]metav1.ConditionStatus{metav1.ConditionTrue}[0],
			expectedResult:                          CredentialStatusValid,
		},
		{
			name:                                    "When OIDC is False, return Invalid",
			ValidOIDCConfigurationConditionStatus:   &[]metav1.ConditionStatus{metav1.ConditionFalse}[0],
			ValidAWSIdentityProviderConditionStatus: &[]metav1.ConditionStatus{metav1.ConditionTrue}[0],
			expectedResult:                          CredentialStatusInvalid,
		},
		{
			name:                                    "When AWS Identity Provider is False, return Invalid",
			ValidOIDCConfigurationConditionStatus:   &[]metav1.ConditionStatus{metav1.ConditionTrue}[0],
			ValidAWSIdentityProviderConditionStatus: &[]metav1.ConditionStatus{metav1.ConditionFalse}[0],
			expectedResult:                          CredentialStatusInvalid,
		},
		{
			name:                                    "When both are False, return Invalid",
			ValidOIDCConfigurationConditionStatus:   &[]metav1.ConditionStatus{metav1.ConditionFalse}[0],
			ValidAWSIdentityProviderConditionStatus: &[]metav1.ConditionStatus{metav1.ConditionFalse}[0],
			expectedResult:                          CredentialStatusInvalid,
		},
		{
			name:                                    "When OIDC is Unknown, return Unknown",
			ValidOIDCConfigurationConditionStatus:   &[]metav1.ConditionStatus{metav1.ConditionUnknown}[0],
			ValidAWSIdentityProviderConditionStatus: &[]metav1.ConditionStatus{metav1.ConditionTrue}[0],
			expectedResult:                          CredentialStatusUnknown,
		},
		{
			name:                                    "When AWS Identity Provider is Unknown, return Unknown",
			ValidOIDCConfigurationConditionStatus:   &[]metav1.ConditionStatus{metav1.ConditionTrue}[0],
			ValidAWSIdentityProviderConditionStatus: &[]metav1.ConditionStatus{metav1.ConditionUnknown}[0],
			expectedResult:                          CredentialStatusUnknown,
		},
		{
			name:                                    "When both are Unknown, return Unknown",
			ValidOIDCConfigurationConditionStatus:   &[]metav1.ConditionStatus{metav1.ConditionUnknown}[0],
			ValidAWSIdentityProviderConditionStatus: &[]metav1.ConditionStatus{metav1.ConditionUnknown}[0],
			expectedResult:                          CredentialStatusUnknown,
		},
		{
			name:                                    "When OIDC condition is missing, return Unknown",
			ValidOIDCConfigurationConditionStatus:   nil,
			ValidAWSIdentityProviderConditionStatus: &[]metav1.ConditionStatus{metav1.ConditionTrue}[0],
			expectedResult:                          CredentialStatusUnknown,
		},
		{
			name:                                    "When AWS Identity Provider condition is missing, return Unknown",
			ValidOIDCConfigurationConditionStatus:   &[]metav1.ConditionStatus{metav1.ConditionTrue}[0],
			ValidAWSIdentityProviderConditionStatus: nil,
			expectedResult:                          CredentialStatusUnknown,
		},
		{
			name:                                    "When both conditions are missing, return Unknown",
			ValidOIDCConfigurationConditionStatus:   nil,
			ValidAWSIdentityProviderConditionStatus: nil,
			expectedResult:                          CredentialStatusUnknown,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			hc := hyperv1.HostedCluster{}

			// Set OIDC condition if provided
			if tc.ValidOIDCConfigurationConditionStatus != nil {
				meta.SetStatusCondition(&hc.Status.Conditions, metav1.Condition{
					Type:   string(hyperv1.ValidOIDCConfiguration),
					Status: *tc.ValidOIDCConfigurationConditionStatus,
				})
			}

			// Set AWS Identity Provider condition if provided
			if tc.ValidAWSIdentityProviderConditionStatus != nil {
				meta.SetStatusCondition(&hc.Status.Conditions, metav1.Condition{
					Type:   string(hyperv1.ValidAWSIdentityProvider),
					Status: *tc.ValidAWSIdentityProviderConditionStatus,
				})
			}

			result := GetCredentialStatus(&hc)
			if tc.expectedResult != result {
				t.Errorf("GetCredentialStatus returned %v, expected %v", result, tc.expectedResult)
			}
		})
	}
}

func TestBuildAWSWebIdentityCredentials(t *testing.T) {
	type args struct {
		roleArn string
		region  string
	}
	type test struct {
		name    string
		args    args
		wantErr bool
		want    string
	}
	tests := []test{
		{
			name: "should fail if the role ARN is empty",
			args: args{
				roleArn: "",
				region:  "us-east-1",
			},
			wantErr: true,
		},
		{
			name:    "should fail if the region is empty",
			wantErr: true,
			args: args{
				roleArn: "arn:aws:iam::123456789012:role/some-role",
				region:  "",
			},
		},
		{
			name:    "should succeed and return the creds template populated with role arn and region otherwise",
			wantErr: false,
			args: args{
				roleArn: "arn:aws:iam::123456789012:role/some-role",
				region:  "us-east-1",
			},
			want: `[default]
role_arn = arn:aws:iam::123456789012:role/some-role
web_identity_token_file = /var/run/secrets/openshift/serviceaccount/token
sts_regional_endpoints = regional
region = us-east-1
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			creds, err := buildAWSWebIdentityCredentials(tt.args.roleArn, tt.args.region)
			if (err != nil) != tt.wantErr {
				t.Errorf("buildAWSWebIdentityCredentials err = %v, wantErr = %v", err, tt.wantErr)
				return
			}
			if creds != tt.want {
				t.Errorf("expected creds:\n%s, but got:\n%s", tt.want, creds)
			}
		})
	}
}

// Helper function to create a HostedControlPlane with TLS profile for testing
func buildAWSHostedControlPlane(tlsProfile *configv1.TLSSecurityProfile) *hyperv1.HostedControlPlane {
	return &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			Configuration: &hyperv1.ClusterConfiguration{
				APIServer: &configv1.APIServerSpec{
					TLSSecurityProfile: tlsProfile,
				},
			},
		},
	}
}

func TestCAPIProviderDeploymentSpec(t *testing.T) {
	defaultArgs := []string{
		"--namespace", "$(MY_NAMESPACE)",
		"--v=4",
		"--leader-elect=true",
		"--feature-gates=EKS=false",
	}

	defaultImage := "test-capi-image"

	customTLSProfile := &configv1.TLSSecurityProfile{
		Type: configv1.TLSProfileCustomType,
		Custom: &configv1.CustomTLSProfile{
			TLSProfileSpec: configv1.TLSProfileSpec{
				MinTLSVersion: configv1.VersionTLS12,
				Ciphers: []string{
					"ECDHE-ECDSA-AES128-GCM-SHA256",
					"ECDHE-RSA-AES128-GCM-SHA256",
				},
			},
		},
	}

	testCases := []struct {
		name          string
		hcp           *hyperv1.HostedControlPlane
		expectedImage string
		expectedArgs  []string
	}{
		{
			name:          "When HostedControlPlane is nil it should not append TLS args",
			expectedImage: defaultImage,
			expectedArgs:  defaultArgs,
		},
		{
			name: "When HostedControlPlane is provided with Modern TLS profile it should append min-version only",
			hcp: buildAWSHostedControlPlane(&configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileModernType,
			}),
			expectedImage: defaultImage,
			expectedArgs: append(defaultArgs,
				"--tls-min-version=VersionTLS13",
			),
		},
		{
			name:          "When HostedControlPlane is provided with custom TLS profile it should append custom TLS args",
			hcp:           buildAWSHostedControlPlane(customTLSProfile),
			expectedImage: defaultImage,
			expectedArgs: append(defaultArgs,
				"--tls-min-version=VersionTLS12",
				"--tls-cipher-suites=TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
			),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			platform := AWS{
				capiProviderImage: tc.expectedImage,
			}
			spec, err := platform.CAPIProviderDeploymentSpec(&hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
					},
				},
			}, tc.hcp)

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if spec == nil {
				t.Fatal("expected deployment spec, got nil")
			}
			if len(spec.Template.Spec.Containers) == 0 {
				t.Fatal("expected at least 1 container, got 0")
			}

			// Find the manager container
			var managerContainer *corev1.Container
			for i := range spec.Template.Spec.Containers {
				if spec.Template.Spec.Containers[i].Name == "manager" {
					managerContainer = &spec.Template.Spec.Containers[i]
					break
				}
			}
			if managerContainer == nil {
				t.Fatal("manager container not found")
			}

			// Verify image
			if managerContainer.Image != tc.expectedImage {
				t.Errorf("expected image %s, got %s", tc.expectedImage, managerContainer.Image)
			}

			// Verify args
			if diff := cmp.Diff(managerContainer.Args, tc.expectedArgs); diff != "" {
				t.Errorf("args differ (-got +want):\n%s", diff)
			}
		})
	}
}
