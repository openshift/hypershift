package aws

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	capiaws "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"

	"github.com/blang/semver"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcileAWSCluster(t *testing.T) {
	testCases := []struct {
		name              string
		initialAWSCluster *capiaws.AWSCluster
		hostedCluster     *hyperv1.HostedCluster

		expectedAWSCluster *capiaws.AWSCluster
	}{
		{
			name:              "When hosted cluster has resource tags it should copy them to the AWS cluster",
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
			name: "When AWS cluster has existing tags it should replace them with hosted cluster tags",
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
			name: "When hosted cluster has no tags it should clear existing AWS cluster tags",
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
			name:                                    "When both conditions are True, it should return Valid",
			ValidOIDCConfigurationConditionStatus:   ptr.To(metav1.ConditionTrue),
			ValidAWSIdentityProviderConditionStatus: ptr.To(metav1.ConditionTrue),
			expectedResult:                          CredentialStatusValid,
		},
		{
			name:                                    "When OIDC is False, it should return Invalid",
			ValidOIDCConfigurationConditionStatus:   ptr.To(metav1.ConditionFalse),
			ValidAWSIdentityProviderConditionStatus: ptr.To(metav1.ConditionTrue),
			expectedResult:                          CredentialStatusInvalid,
		},
		{
			name:                                    "When AWS Identity Provider is False, it should return Invalid",
			ValidOIDCConfigurationConditionStatus:   ptr.To(metav1.ConditionTrue),
			ValidAWSIdentityProviderConditionStatus: ptr.To(metav1.ConditionFalse),
			expectedResult:                          CredentialStatusInvalid,
		},
		{
			name:                                    "When both are False, it should return Invalid",
			ValidOIDCConfigurationConditionStatus:   ptr.To(metav1.ConditionFalse),
			ValidAWSIdentityProviderConditionStatus: ptr.To(metav1.ConditionFalse),
			expectedResult:                          CredentialStatusInvalid,
		},
		{
			name:                                    "When OIDC is Unknown, it should return Unknown",
			ValidOIDCConfigurationConditionStatus:   ptr.To(metav1.ConditionUnknown),
			ValidAWSIdentityProviderConditionStatus: ptr.To(metav1.ConditionTrue),
			expectedResult:                          CredentialStatusUnknown,
		},
		{
			name:                                    "When AWS Identity Provider is Unknown, it should return Unknown",
			ValidOIDCConfigurationConditionStatus:   ptr.To(metav1.ConditionTrue),
			ValidAWSIdentityProviderConditionStatus: ptr.To(metav1.ConditionUnknown),
			expectedResult:                          CredentialStatusUnknown,
		},
		{
			name:                                    "When both are Unknown, it should return Unknown",
			ValidOIDCConfigurationConditionStatus:   ptr.To(metav1.ConditionUnknown),
			ValidAWSIdentityProviderConditionStatus: ptr.To(metav1.ConditionUnknown),
			expectedResult:                          CredentialStatusUnknown,
		},
		{
			name:                                    "When OIDC condition is missing, it should return Unknown",
			ValidOIDCConfigurationConditionStatus:   nil,
			ValidAWSIdentityProviderConditionStatus: ptr.To(metav1.ConditionTrue),
			expectedResult:                          CredentialStatusUnknown,
		},
		{
			name:                                    "When AWS Identity Provider condition is missing, it should return Unknown",
			ValidOIDCConfigurationConditionStatus:   ptr.To(metav1.ConditionTrue),
			ValidAWSIdentityProviderConditionStatus: nil,
			expectedResult:                          CredentialStatusUnknown,
		},
		{
			name:                                    "When both conditions are missing, it should return Unknown",
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
			name: "When role ARN is empty, it should return an error",
			args: args{
				roleArn: "",
				region:  "us-east-1",
			},
			wantErr: true,
		},
		{
			name:    "When region is empty, it should return an error",
			wantErr: true,
			args: args{
				roleArn: "arn:aws:iam::123456789012:role/some-role",
				region:  "",
			},
		},
		{
			name:    "When role ARN and region are provided, it should return populated credentials template",
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
		"--feature-gates=EKS=false,ROSA=false",
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
		name           string
		hcp            *hyperv1.HostedControlPlane
		payloadVersion *semver.Version
		expectedImage  string
		expectedArgs   []string
	}{
		{
			name:           "When HostedControlPlane is nil it should not append TLS args",
			payloadVersion: ptr.To(semver.MustParse("4.23.0")),
			expectedImage:  defaultImage,
			expectedArgs:   defaultArgs,
		},
		{
			name: "When version is 4.22 and HCP has TLS profile it should not append TLS args",
			hcp: buildAWSHostedControlPlane(&configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileModernType,
			}),
			payloadVersion: ptr.To(semver.MustParse("4.22.0")),
			expectedImage:  defaultImage,
			expectedArgs:   defaultArgs,
		},
		{
			name: "When version is 4.23 and HCP has Modern TLS profile it should append min-version only",
			hcp: buildAWSHostedControlPlane(&configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileModernType,
			}),
			payloadVersion: ptr.To(semver.MustParse("4.23.0")),
			expectedImage:  defaultImage,
			expectedArgs: append(defaultArgs,
				"--tls-min-version=VersionTLS13",
			),
		},
		{
			name:           "When version is 5.0 and HCP has custom TLS profile it should append custom TLS args",
			hcp:            buildAWSHostedControlPlane(customTLSProfile),
			payloadVersion: ptr.To(semver.MustParse("5.0.0")),
			expectedImage:  defaultImage,
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
				payloadVersion:    tc.payloadVersion,
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

			if managerContainer.Image != tc.expectedImage {
				t.Errorf("expected image %s, got %s", tc.expectedImage, managerContainer.Image)
			}

			if diff := cmp.Diff(managerContainer.Args, tc.expectedArgs); diff != "" {
				t.Errorf("args differ (-got +want):\n%s", diff)
			}
		})
	}
}

func hostedClusterWithCredentialConditions(oidcStatus, awsIdpStatus metav1.ConditionStatus) *hyperv1.HostedCluster {
	hc := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "clusters"},
	}
	meta.SetStatusCondition(&hc.Status.Conditions, metav1.Condition{
		Type:   string(hyperv1.ValidOIDCConfiguration),
		Status: oidcStatus,
		Reason: "test",
	})
	meta.SetStatusCondition(&hc.Status.Conditions, metav1.Condition{
		Type:   string(hyperv1.ValidAWSIdentityProvider),
		Status: awsIdpStatus,
		Reason: "test",
	})
	return hc
}

func TestDeleteOrphanedMachines(t *testing.T) {
	namespace := "clusters-test"
	deletionTime := metav1.Now()

	tests := []struct {
		name                    string
		hc                      *hyperv1.HostedCluster
		machines                []capiaws.AWSMachine
		expectErr               bool
		expectFinalizersCleared bool
	}{
		{
			name: "When credentials are valid, it should skip cleanup",
			hc:   hostedClusterWithCredentialConditions(metav1.ConditionTrue, metav1.ConditionTrue),
			machines: []capiaws.AWSMachine{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "machine-1",
						Namespace:         namespace,
						DeletionTimestamp: &deletionTime,
						Finalizers:        []string{"test-finalizer"},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "machine-2",
						Namespace:  namespace,
						Finalizers: []string{"test-finalizer"},
					},
				},
			},
			expectFinalizersCleared: false,
		},
		{
			name: "When credentials are invalid and machines have deletion timestamps, it should clear finalizers",
			hc:   hostedClusterWithCredentialConditions(metav1.ConditionFalse, metav1.ConditionTrue),
			machines: []capiaws.AWSMachine{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "machine-1",
						Namespace:         namespace,
						DeletionTimestamp: &deletionTime,
						Finalizers:        []string{"test-finalizer"},
					},
				},
			},
			expectFinalizersCleared: true,
		},
		{
			name: "When credentials are invalid and machines have no deletion timestamps, it should not modify them",
			hc:   hostedClusterWithCredentialConditions(metav1.ConditionFalse, metav1.ConditionTrue),
			machines: []capiaws.AWSMachine{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "machine-1",
						Namespace:  namespace,
						Finalizers: []string{"test-finalizer"},
					},
				},
			},
			expectFinalizersCleared: false,
		},
		{
			name:                    "When no AWSMachines exist, it should return nil",
			hc:                      hostedClusterWithCredentialConditions(metav1.ConditionFalse, metav1.ConditionTrue),
			machines:                []capiaws.AWSMachine{},
			expectFinalizersCleared: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			objects := []crclient.Object{}
			for i := range tc.machines {
				objects = append(objects, &tc.machines[i])
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(objects...).
				Build()

			a := AWS{}
			err := a.DeleteOrphanedMachines(t.Context(), fakeClient, tc.hc, namespace)

			if tc.expectErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}

			if tc.expectFinalizersCleared {
				var machineList capiaws.AWSMachineList
				g.Expect(fakeClient.List(t.Context(), &machineList, crclient.InNamespace(namespace))).To(Succeed())
				for _, m := range machineList.Items {
					g.Expect(m.Finalizers).To(BeEmpty(), "expected finalizers to be cleared on machine %s", m.Name)
				}
			}

			if !tc.expectFinalizersCleared && len(tc.machines) > 0 {
				var machineList capiaws.AWSMachineList
				g.Expect(fakeClient.List(t.Context(), &machineList, crclient.InNamespace(namespace))).To(Succeed())
				for _, m := range machineList.Items {
					g.Expect(m.Finalizers).To(ContainElement("test-finalizer"), "expected finalizers to be preserved on machine %s", m.Name)
				}
			}
		})
	}
}
