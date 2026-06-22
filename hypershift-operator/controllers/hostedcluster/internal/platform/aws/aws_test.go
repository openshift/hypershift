package aws

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

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
