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

func TestValidCredentials(t *testing.T) {
	testCases := []struct {
		name                                    string
		ValidOIDCConfigurationConditionStatus   metav1.ConditionStatus
		ValidAWSIdentityProviderConditionStatus metav1.ConditionStatus

		expectedResult bool
	}{
		{
			name:                                    "When ValidOIDCConfigurationCondition status False, return False",
			ValidOIDCConfigurationConditionStatus:   metav1.ConditionFalse,
			ValidAWSIdentityProviderConditionStatus: metav1.ConditionTrue,
			expectedResult:                          false,
		},
		{
			name:                                    "When ValidOIDCConfigurationCondition status Unknown, return True",
			ValidOIDCConfigurationConditionStatus:   metav1.ConditionUnknown,
			ValidAWSIdentityProviderConditionStatus: metav1.ConditionTrue,
			expectedResult:                          true,
		},
		{
			name:                                    "When ValidAWSIdentityProviderCondition status False, return False",
			ValidOIDCConfigurationConditionStatus:   metav1.ConditionTrue,
			ValidAWSIdentityProviderConditionStatus: metav1.ConditionFalse,
			expectedResult:                          false,
		},
		{
			name:                                    "When ValidAWSIdentityProviderCondition status Unknown, return False",
			ValidOIDCConfigurationConditionStatus:   metav1.ConditionTrue,
			ValidAWSIdentityProviderConditionStatus: metav1.ConditionUnknown,
			expectedResult:                          false,
		},
		{
			name:                                    "When both ValidAWSIdentityProviderCondition and ValidOIDCConfigurationCondition status True, return True",
			ValidOIDCConfigurationConditionStatus:   metav1.ConditionTrue,
			ValidAWSIdentityProviderConditionStatus: metav1.ConditionTrue,
			expectedResult:                          true,
		},
	}

	hc := hyperv1.HostedCluster{}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			meta.SetStatusCondition(&hc.Status.Conditions, metav1.Condition{
				Type:   string(hyperv1.ValidOIDCConfiguration),
				Status: tc.ValidOIDCConfigurationConditionStatus,
			})
			meta.SetStatusCondition(&hc.Status.Conditions, metav1.Condition{
				Type:   string(hyperv1.ValidAWSIdentityProvider),
				Status: tc.ValidAWSIdentityProviderConditionStatus,
			})

			result := ValidCredentials(&hc)
			if tc.expectedResult != result {
				t.Errorf("ValidCredentials returned %v, expected %v", result, tc.expectedResult)
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
