package aws

import (
	"context"
	"errors"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/awsapi"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"github.com/go-logr/logr"
	"go.uber.org/mock/gomock"
)

const testIssuerURL = "https://s3.example.com/" + testInfraID

// createIAMOptions returns a CreateIAMOptions with shared test defaults.
func createIAMOptions() *CreateIAMOptions {
	return &CreateIAMOptions{
		InfraID:   testInfraID,
		IssuerURL: testIssuerURL,
	}
}

func TestCreateRole(t *testing.T) {
	const (
		roleName = "test-role"
		roleARN  = "arn:aws:iam::123456789012:role/" + roleName
	)

	tests := []struct {
		name          string
		setupMock     func(*awsapi.MockIAMAPI)
		expectARN     string
		expectError   bool
		errorContains string
	}{
		{
			name: "When role already exists it should return existing ARN without calling CreateRole",
			setupMock: func(m *awsapi.MockIAMAPI) {
				m.EXPECT().GetRole(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&iam.GetRoleOutput{Role: &iamtypes.Role{
						RoleName: aws.String(roleName),
						Arn:      aws.String(roleARN),
					}}, nil)
			},
			expectARN: roleARN,
		},
		{
			name: "When role does not exist it should create it and return new ARN",
			setupMock: func(m *awsapi.MockIAMAPI) {
				gomock.InOrder(
					m.EXPECT().GetRole(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(nil, noSuchEntity()),
					m.EXPECT().CreateRole(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(&iam.CreateRoleOutput{Role: &iamtypes.Role{
							RoleName: aws.String(roleName),
							Arn:      aws.String(roleARN),
						}}, nil),
				)
			},
			expectARN: roleARN,
		},
		{
			name: "When GetRole returns an API error it should return the error",
			setupMock: func(m *awsapi.MockIAMAPI) {
				m.EXPECT().GetRole(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(nil, errors.New("api error"))
			},
			expectError:   true,
			errorContains: "api error",
		},
		{
			name: "When CreateRole fails it should return the error",
			setupMock: func(m *awsapi.MockIAMAPI) {
				gomock.InOrder(
					m.EXPECT().GetRole(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(nil, noSuchEntity()),
					m.EXPECT().CreateRole(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(nil, errors.New("create failed")),
				)
			},
			expectError:   true,
			errorContains: "create failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			ctrl := gomock.NewController(t)
			mockIAM := awsapi.NewMockIAMAPI(ctrl)
			tt.setupMock(mockIAM)

			o := &CreateIAMRoleOptions{RoleName: roleName, TrustPolicy: "{}"}
			arn, err := o.CreateRole(context.Background(), mockIAM, logr.Discard())

			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
				if tt.errorContains != "" {
					g.Expect(err.Error()).To(ContainSubstring(tt.errorContains))
				}
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(arn).To(Equal(tt.expectARN))
			}
		})
	}
}

func TestCreateRoleWithInlinePolicy(t *testing.T) {
	const (
		roleName = "test-role"
		roleARN  = "arn:aws:iam::123456789012:role/" + roleName
	)

	tests := []struct {
		name          string
		allowAssume   bool
		setupMock     func(*awsapi.MockIAMAPI)
		expectARN     string
		expectError   bool
		errorContains string
	}{
		{
			name: "When role does not exist it should create it and put inline policy",
			setupMock: func(m *awsapi.MockIAMAPI) {
				gomock.InOrder(
					m.EXPECT().GetRole(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(nil, noSuchEntity()),
					m.EXPECT().CreateRole(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(&iam.CreateRoleOutput{Role: testRole(roleName)}, nil),
					m.EXPECT().PutRolePolicy(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(&iam.PutRolePolicyOutput{}, nil),
				)
			},
			expectARN: roleARN,
		},
		{
			name: "When role already exists it should skip creation and still put inline policy",
			setupMock: func(m *awsapi.MockIAMAPI) {
				gomock.InOrder(
					m.EXPECT().GetRole(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(&iam.GetRoleOutput{Role: testRole(roleName)}, nil),
					m.EXPECT().PutRolePolicy(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(&iam.PutRolePolicyOutput{}, nil),
				)
			},
			expectARN: roleARN,
		},
		{
			name:        "When AllowAssume is true it should also put the assume role inline policy",
			allowAssume: true,
			setupMock: func(m *awsapi.MockIAMAPI) {
				gomock.InOrder(
					m.EXPECT().GetRole(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(nil, noSuchEntity()),
					m.EXPECT().CreateRole(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(&iam.CreateRoleOutput{Role: testRole(roleName)}, nil),
					// inline permissions policy + assume policy
					m.EXPECT().PutRolePolicy(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(&iam.PutRolePolicyOutput{}, nil).Times(2),
				)
			},
			expectARN: roleARN,
		},
		{
			name: "When PutRolePolicy fails it should return the error",
			setupMock: func(m *awsapi.MockIAMAPI) {
				gomock.InOrder(
					m.EXPECT().GetRole(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(nil, noSuchEntity()),
					m.EXPECT().CreateRole(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(&iam.CreateRoleOutput{Role: testRole(roleName)}, nil),
					m.EXPECT().PutRolePolicy(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(nil, errors.New("put policy failed")),
				)
			},
			expectError:   true,
			errorContains: "put policy failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			ctrl := gomock.NewController(t)
			mockIAM := awsapi.NewMockIAMAPI(ctrl)
			tt.setupMock(mockIAM)

			o := &CreateIAMRoleOptions{
				RoleName:          roleName,
				TrustPolicy:       "{}",
				PermissionsPolicy: "{}",
				AllowAssume:       tt.allowAssume,
			}
			arn, err := o.CreateRoleWithInlinePolicy(context.Background(), mockIAM, logr.Discard())

			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
				if tt.errorContains != "" {
					g.Expect(err.Error()).To(ContainSubstring(tt.errorContains))
				}
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(arn).To(Equal(tt.expectARN))
			}
		})
	}
}

func TestCreateRoleWithManagedPolicy(t *testing.T) {
	const (
		roleName         = "test-role"
		managedPolicyARN = "arn:aws:iam::aws:policy/ManagedTestPolicy"
	)

	tests := []struct {
		name          string
		allowAssume   bool
		setupMock     func(*awsapi.MockIAMAPI)
		expectARN     string
		expectError   bool
		errorContains string
	}{
		{
			name: "When role does not exist it should create it and attach managed policy",
			setupMock: func(m *awsapi.MockIAMAPI) {
				gomock.InOrder(
					m.EXPECT().GetRole(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(nil, noSuchEntity()),
					m.EXPECT().CreateRole(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(&iam.CreateRoleOutput{Role: testRole(roleName)}, nil),
					m.EXPECT().AttachRolePolicy(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(&iam.AttachRolePolicyOutput{}, nil),
				)
			},
			expectARN: "arn:aws:iam::123456789012:role/" + roleName,
		},
		{
			name:        "When AllowAssume is true it should also put the assume role inline policy",
			allowAssume: true,
			setupMock: func(m *awsapi.MockIAMAPI) {
				gomock.InOrder(
					m.EXPECT().GetRole(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(nil, noSuchEntity()),
					m.EXPECT().CreateRole(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(&iam.CreateRoleOutput{Role: testRole(roleName)}, nil),
					m.EXPECT().AttachRolePolicy(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(&iam.AttachRolePolicyOutput{}, nil),
					m.EXPECT().PutRolePolicy(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(&iam.PutRolePolicyOutput{}, nil),
				)
			},
			expectARN: "arn:aws:iam::123456789012:role/" + roleName,
		},
		{
			name: "When role already exists it should skip creation and attach managed policy",
			setupMock: func(m *awsapi.MockIAMAPI) {
				gomock.InOrder(
					m.EXPECT().GetRole(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(&iam.GetRoleOutput{Role: testRole(roleName)}, nil),
					m.EXPECT().AttachRolePolicy(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(&iam.AttachRolePolicyOutput{}, nil),
				)
			},
			expectARN: "arn:aws:iam::123456789012:role/" + roleName,
		},
		{
			name: "When AttachRolePolicy fails it should return the error",
			setupMock: func(m *awsapi.MockIAMAPI) {
				gomock.InOrder(
					m.EXPECT().GetRole(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(nil, noSuchEntity()),
					m.EXPECT().CreateRole(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(&iam.CreateRoleOutput{Role: testRole(roleName)}, nil),
					m.EXPECT().AttachRolePolicy(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(nil, errors.New("attach failed")),
				)
			},
			expectError:   true,
			errorContains: "attach failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			ctrl := gomock.NewController(t)
			mockIAM := awsapi.NewMockIAMAPI(ctrl)
			tt.setupMock(mockIAM)

			o := &CreateIAMRoleOptions{
				RoleName:    roleName,
				TrustPolicy: "{}",
				AllowAssume: tt.allowAssume,
			}
			arn, err := o.CreateRoleWithManagedPolicy(context.Background(), mockIAM, managedPolicyARN, logr.Discard())

			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
				if tt.errorContains != "" {
					g.Expect(err.Error()).To(ContainSubstring(tt.errorContains))
				}
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(arn).To(Equal(tt.expectARN))
			}
		})
	}
}

func TestCreateOIDCProvider(t *testing.T) {
	// providerName is the URL without "https://" prefix — used for ARN matching.
	const (
		providerName = "s3.example.com/" + testInfraID
		// matchingARN contains providerName so the production code treats it as existing.
		matchingARN = "arn:aws:iam::123456789012:oidc-provider/" + providerName
		newARN      = "arn:aws:iam::123456789012:oidc-provider/" + providerName + "-new"
	)

	tests := []struct {
		name          string
		setupMock     func(*awsapi.MockIAMAPI)
		expectARN     string
		expectError   bool
		errorContains string
	}{
		{
			name: "When no existing provider matches it should create and return the new ARN",
			setupMock: func(m *awsapi.MockIAMAPI) {
				gomock.InOrder(
					m.EXPECT().ListOpenIDConnectProviders(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(&iam.ListOpenIDConnectProvidersOutput{
							OpenIDConnectProviderList: []iamtypes.OpenIDConnectProviderListEntry{},
						}, nil),
					m.EXPECT().CreateOpenIDConnectProvider(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(&iam.CreateOpenIDConnectProviderOutput{
							OpenIDConnectProviderArn: aws.String(newARN),
						}, nil),
				)
			},
			expectARN: newARN,
		},
		{
			name: "When an existing provider matches the issuer URL it should delete it then create a new one",
			setupMock: func(m *awsapi.MockIAMAPI) {
				gomock.InOrder(
					m.EXPECT().ListOpenIDConnectProviders(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(&iam.ListOpenIDConnectProvidersOutput{
							OpenIDConnectProviderList: []iamtypes.OpenIDConnectProviderListEntry{
								{Arn: aws.String(matchingARN)},
							},
						}, nil),
					m.EXPECT().DeleteOpenIDConnectProvider(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(&iam.DeleteOpenIDConnectProviderOutput{}, nil),
					m.EXPECT().CreateOpenIDConnectProvider(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(&iam.CreateOpenIDConnectProviderOutput{
							OpenIDConnectProviderArn: aws.String(newARN),
						}, nil),
				)
			},
			expectARN: newARN,
		},
		{
			name: "When ListOpenIDConnectProviders fails it should return the error",
			setupMock: func(m *awsapi.MockIAMAPI) {
				m.EXPECT().ListOpenIDConnectProviders(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(nil, errors.New("api error"))
			},
			expectError:   true,
			errorContains: "api error",
		},
		{
			name: "When DeleteOpenIDConnectProvider fails it should return the error",
			setupMock: func(m *awsapi.MockIAMAPI) {
				gomock.InOrder(
					m.EXPECT().ListOpenIDConnectProviders(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(&iam.ListOpenIDConnectProvidersOutput{
							OpenIDConnectProviderList: []iamtypes.OpenIDConnectProviderListEntry{
								{Arn: aws.String(matchingARN)},
							},
						}, nil),
					m.EXPECT().DeleteOpenIDConnectProvider(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(nil, errors.New("delete failed")),
				)
			},
			expectError:   true,
			errorContains: "delete failed",
		},
		{
			name: "When CreateOpenIDConnectProvider fails it should return the error",
			setupMock: func(m *awsapi.MockIAMAPI) {
				gomock.InOrder(
					m.EXPECT().ListOpenIDConnectProviders(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(&iam.ListOpenIDConnectProvidersOutput{}, nil),
					m.EXPECT().CreateOpenIDConnectProvider(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(nil, errors.New("create failed")),
				)
			},
			expectError:   true,
			errorContains: "create failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			ctrl := gomock.NewController(t)
			mockIAM := awsapi.NewMockIAMAPI(ctrl)
			tt.setupMock(mockIAM)

			o := createIAMOptions()
			arn, err := o.CreateOIDCProvider(context.Background(), mockIAM, logr.Discard())

			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
				if tt.errorContains != "" {
					g.Expect(err.Error()).To(ContainSubstring(tt.errorContains))
				}
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(arn).To(Equal(tt.expectARN))
			}
		})
	}
}

func TestCreateWorkerInstanceProfile(t *testing.T) {
	const profileName = testInfraID + "-worker"
	const standardRoleName = profileName + "-role"
	const policyName = profileName + "-policy"
	const rosaRoleName = profileName + "-" + ROSAWorkerRoleNameSuffix

	tests := []struct {
		name                   string
		useROSAManagedPolicies bool
		setupMock              func(*awsapi.MockIAMAPI)
		expectError            bool
		errorContains          string
	}{
		{
			name: "When role and profile do not exist it should create both and put inline policy",
			setupMock: func(m *awsapi.MockIAMAPI) {
				gomock.InOrder(
					// existingRole → not found → CreateRole
					m.EXPECT().GetRole(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(nil, noSuchEntity()),
					m.EXPECT().CreateRole(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(&iam.CreateRoleOutput{Role: testRole(standardRoleName)}, nil),
					// existingInstanceProfile → not found → CreateInstanceProfile
					m.EXPECT().GetInstanceProfile(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(nil, noSuchEntity()),
					m.EXPECT().CreateInstanceProfile(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(&iam.CreateInstanceProfileOutput{
							InstanceProfile: testInstanceProfile(profileName), // no roles yet
						}, nil),
					// role not yet in profile → AddRoleToInstanceProfile
					m.EXPECT().AddRoleToInstanceProfile(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(&iam.AddRoleToInstanceProfileOutput{}, nil),
					// existingRolePolicy → not found → PutRolePolicy
					m.EXPECT().GetRolePolicy(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(nil, noSuchEntity()),
					m.EXPECT().PutRolePolicy(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(&iam.PutRolePolicyOutput{}, nil),
				)
			},
		},
		{
			name: "When role and profile already exist and role is already in profile it should skip all creates",
			setupMock: func(m *awsapi.MockIAMAPI) {
				gomock.InOrder(
					// role exists
					m.EXPECT().GetRole(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(&iam.GetRoleOutput{Role: testRole(standardRoleName)}, nil),
					// profile exists and already has the role
					m.EXPECT().GetInstanceProfile(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(&iam.GetInstanceProfileOutput{
							InstanceProfile: testInstanceProfile(profileName,
								iamtypes.Role{RoleName: aws.String(standardRoleName)},
							),
						}, nil),
					// policy exists
					m.EXPECT().GetRolePolicy(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(&iam.GetRolePolicyOutput{
							PolicyName: aws.String(policyName),
							RoleName:   aws.String(standardRoleName),
						}, nil),
				)
			},
		},
		{
			name:                   "When UseROSAManagedPolicies is true it should use ROSA role name and attach managed policy",
			useROSAManagedPolicies: true,
			setupMock: func(m *awsapi.MockIAMAPI) {
				gomock.InOrder(
					// existingRole for ROSA role name → not found → CreateRole
					m.EXPECT().GetRole(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(nil, noSuchEntity()),
					m.EXPECT().CreateRole(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(&iam.CreateRoleOutput{Role: testRole(rosaRoleName)}, nil),
					// existingInstanceProfile → not found → CreateInstanceProfile
					m.EXPECT().GetInstanceProfile(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(nil, noSuchEntity()),
					m.EXPECT().CreateInstanceProfile(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(&iam.CreateInstanceProfileOutput{
							InstanceProfile: testInstanceProfile(profileName),
						}, nil),
					// role not in profile → AddRoleToInstanceProfile
					m.EXPECT().AddRoleToInstanceProfile(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(&iam.AddRoleToInstanceProfileOutput{}, nil),
					// ROSA path: AttachRolePolicy instead of PutRolePolicy
					m.EXPECT().AttachRolePolicy(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(&iam.AttachRolePolicyOutput{}, nil),
				)
			},
		},
		{
			name: "When CreateRole fails it should return the error",
			setupMock: func(m *awsapi.MockIAMAPI) {
				gomock.InOrder(
					m.EXPECT().GetRole(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(nil, noSuchEntity()),
					m.EXPECT().CreateRole(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(nil, errors.New("create role failed")),
				)
			},
			expectError:   true,
			errorContains: "cannot create worker role",
		},
		{
			name: "When CreateInstanceProfile fails it should return the error",
			setupMock: func(m *awsapi.MockIAMAPI) {
				gomock.InOrder(
					m.EXPECT().GetRole(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(nil, noSuchEntity()),
					m.EXPECT().CreateRole(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(&iam.CreateRoleOutput{Role: testRole(standardRoleName)}, nil),
					m.EXPECT().GetInstanceProfile(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(nil, noSuchEntity()),
					m.EXPECT().CreateInstanceProfile(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(nil, errors.New("create profile failed")),
				)
			},
			expectError:   true,
			errorContains: "cannot create instance profile",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			ctrl := gomock.NewController(t)
			mockIAM := awsapi.NewMockIAMAPI(ctrl)
			tt.setupMock(mockIAM)

			o := &CreateIAMOptions{
				InfraID:                testInfraID,
				UseROSAManagedPolicies: tt.useROSAManagedPolicies,
			}
			err := o.CreateWorkerInstanceProfile(context.Background(), mockIAM, profileName, logr.Discard())

			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
				if tt.errorContains != "" {
					g.Expect(err.Error()).To(ContainSubstring(tt.errorContains))
				}
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
		})
	}
}

func TestEnsureHostedZonePrefix(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		expectOut string
	}{
		{
			name:      "When hostedZone lacks prefix it should prepend hostedzone/",
			input:     "Z1234567890ABC",
			expectOut: "hostedzone/Z1234567890ABC",
		},
		{
			name:      "When hostedZone already has prefix it should return it unchanged",
			input:     "hostedzone/Z1234567890ABC",
			expectOut: "hostedzone/Z1234567890ABC",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			got := ensureHostedZonePrefix(tt.input)
			g.Expect(got).To(Equal(tt.expectOut))
		})
	}
}
