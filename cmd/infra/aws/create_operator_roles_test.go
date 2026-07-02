package aws

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/awsapi"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"github.com/go-logr/logr"
	"go.uber.org/mock/gomock"
)

func TestCreateOperatorRolesValidate(t *testing.T) {
	tests := map[string]struct {
		opts        CreateOperatorRolesOptions
		expectError bool
	}{
		"When both oidc-issuer-url and instance-role-arn are provided it should error": {
			opts: CreateOperatorRolesOptions{
				OIDCIssuerURL:   "https://oidc.example.com",
				InstanceRoleARN: "arn:aws:iam::123456789012:role/instance-role",
			},
			expectError: true,
		},
		"When oidc-issuer-url is provided it should succeed": {
			opts: CreateOperatorRolesOptions{
				OIDCIssuerURL: "https://oidc.example.com",
			},
			expectError: false,
		},
		"When instance-role-arn is provided it should succeed": {
			opts: CreateOperatorRolesOptions{
				InstanceRoleARN: "arn:aws:iam::123456789012:role/instance-role",
			},
			expectError: false,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			err := test.opts.Validate(context.Background())
			if test.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}

func TestResolveOIDCProvider(t *testing.T) {
	tests := map[string]struct {
		issuerURL     string
		providers     []iamtypes.OpenIDConnectProviderListEntry
		listErr       error
		expectARN     string
		expectName    string
		expectError   bool
		errorContains string
	}{
		"When matching provider exists it should return its ARN": {
			issuerURL: "https://oidc.eks.us-east-1.amazonaws.com/id/EXAMPLE123",
			providers: []iamtypes.OpenIDConnectProviderListEntry{
				{Arn: aws.String("arn:aws:iam::123456789012:oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/EXAMPLE123")},
			},
			expectARN:  "arn:aws:iam::123456789012:oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/EXAMPLE123",
			expectName: "oidc.eks.us-east-1.amazonaws.com/id/EXAMPLE123",
		},
		"When no matching provider exists it should error": {
			issuerURL: "https://oidc.example.com",
			providers: []iamtypes.OpenIDConnectProviderListEntry{
				{Arn: aws.String("arn:aws:iam::123456789012:oidc-provider/other.example.com")},
			},
			expectError:   true,
			errorContains: "no OIDC provider found",
		},
		"When listing providers fails it should error": {
			issuerURL:     "https://oidc.example.com",
			listErr:       fmt.Errorf("access denied"),
			expectError:   true,
			errorContains: "failed to list OIDC providers",
		},
		"When provider list is empty it should error": {
			issuerURL:     "https://oidc.example.com",
			providers:     []iamtypes.OpenIDConnectProviderListEntry{},
			expectError:   true,
			errorContains: "no OIDC provider found",
		},
		"When issuer URL has https prefix it should strip it for matching": {
			issuerURL: "https://s3.us-east-1.amazonaws.com/mybucket",
			providers: []iamtypes.OpenIDConnectProviderListEntry{
				{Arn: aws.String("arn:aws:iam::123456789012:oidc-provider/s3.us-east-1.amazonaws.com/mybucket")},
			},
			expectARN:  "arn:aws:iam::123456789012:oidc-provider/s3.us-east-1.amazonaws.com/mybucket",
			expectName: "s3.us-east-1.amazonaws.com/mybucket",
		},
		"When substring match exists but not suffix match it should not match": {
			issuerURL: "https://example.com",
			providers: []iamtypes.OpenIDConnectProviderListEntry{
				{Arn: aws.String("arn:aws:iam::123456789012:oidc-provider/my-example.com")},
			},
			expectError:   true,
			errorContains: "no OIDC provider found",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			ctrl := gomock.NewController(t)
			mockIAM := awsapi.NewMockIAMAPI(ctrl)

			mockIAM.EXPECT().ListOpenIDConnectProviders(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(&iam.ListOpenIDConnectProvidersOutput{
					OpenIDConnectProviderList: test.providers,
				}, test.listErr)

			opts := &CreateOperatorRolesOptions{OIDCIssuerURL: test.issuerURL}
			arn, name, err := opts.resolveOIDCProvider(context.Background(), mockIAM, logr.Discard())

			if test.expectError {
				g.Expect(err).To(HaveOccurred())
				if test.errorContains != "" {
					g.Expect(err.Error()).To(ContainSubstring(test.errorContains))
				}
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(arn).To(Equal(test.expectARN))
				g.Expect(name).To(Equal(test.expectName))
			}
		})
	}
}

func TestBuildTrustPolicies(t *testing.T) {
	tests := map[string]struct {
		opts          CreateOperatorRolesOptions
		setupMock     func(*awsapi.MockIAMAPI)
		expectError   bool
		errorContains string
		validate      func(*GomegaWithT, *operatorTrustPolicies)
	}{
		"When instance role ARN is provided it should use instance role trust policy": {
			opts: CreateOperatorRolesOptions{
				InstanceRoleARN:   "arn:aws:iam::123456789012:role/instance-role",
				OperatorNamespace: "hypershift",
			},
			setupMock: func(m *awsapi.MockIAMAPI) {},
			validate: func(g *GomegaWithT, tp *operatorTrustPolicies) {
				g.Expect(tp.operatorTrust).To(ContainSubstring("arn:aws:iam::123456789012:role/instance-role"))
				g.Expect(tp.operatorTrust).To(ContainSubstring("sts:AssumeRole"))
				g.Expect(tp.operatorTrust).To(Equal(tp.externalDNSTrust))
			},
		},
		"When OIDC issuer URL is provided it should resolve provider and build trust policies": {
			opts: CreateOperatorRolesOptions{
				OIDCIssuerURL:     "https://oidc.example.com",
				OperatorNamespace: "hypershift",
			},
			setupMock: func(m *awsapi.MockIAMAPI) {
				m.EXPECT().ListOpenIDConnectProviders(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&iam.ListOpenIDConnectProvidersOutput{
						OpenIDConnectProviderList: []iamtypes.OpenIDConnectProviderListEntry{
							{Arn: aws.String("arn:aws:iam::123456789012:oidc-provider/oidc.example.com")},
						},
					}, nil)
			},
			validate: func(g *GomegaWithT, tp *operatorTrustPolicies) {
				g.Expect(tp.operatorTrust).To(ContainSubstring("system:serviceaccount:hypershift:operator"))
				g.Expect(tp.externalDNSTrust).To(ContainSubstring("system:serviceaccount:hypershift:external-dns"))
				g.Expect(tp.operatorTrust).ToNot(Equal(tp.externalDNSTrust))
			},
		},
		"When OIDC provider resolution fails it should return error": {
			opts: CreateOperatorRolesOptions{
				OIDCIssuerURL:     "https://unknown.example.com",
				OperatorNamespace: "hypershift",
			},
			setupMock: func(m *awsapi.MockIAMAPI) {
				m.EXPECT().ListOpenIDConnectProviders(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&iam.ListOpenIDConnectProvidersOutput{
						OpenIDConnectProviderList: []iamtypes.OpenIDConnectProviderListEntry{},
					}, nil)
			},
			expectError:   true,
			errorContains: "no OIDC provider found",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			ctrl := gomock.NewController(t)
			mockIAM := awsapi.NewMockIAMAPI(ctrl)
			test.setupMock(mockIAM)

			tp, err := test.opts.buildTrustPolicies(context.Background(), mockIAM, logr.Discard())
			if test.expectError {
				g.Expect(err).To(HaveOccurred())
				if test.errorContains != "" {
					g.Expect(err.Error()).To(ContainSubstring(test.errorContains))
				}
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				test.validate(g, tp)
			}
		})
	}
}

func TestCreateOrUpdateRole(t *testing.T) {
	const (
		roleName = "test-role"
		roleARN  = "arn:aws:iam::123456789012:role/" + roleName
	)

	tests := map[string]struct {
		setupMock     func(*awsapi.MockIAMAPI)
		expectARN     string
		expectError   bool
		errorContains string
	}{
		"When role creation and trust update succeed it should return the ARN": {
			setupMock: func(m *awsapi.MockIAMAPI) {
				m.EXPECT().GetRole(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&iam.GetRoleOutput{Role: &iamtypes.Role{
						RoleName: aws.String(roleName),
						Arn:      aws.String(roleARN),
					}}, nil)
				m.EXPECT().PutRolePolicy(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&iam.PutRolePolicyOutput{}, nil)
				m.EXPECT().UpdateAssumeRolePolicy(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&iam.UpdateAssumeRolePolicyOutput{}, nil)
			},
			expectARN: roleARN,
		},
		"When trust policy update fails it should return error": {
			setupMock: func(m *awsapi.MockIAMAPI) {
				m.EXPECT().GetRole(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&iam.GetRoleOutput{Role: &iamtypes.Role{
						RoleName: aws.String(roleName),
						Arn:      aws.String(roleARN),
					}}, nil)
				m.EXPECT().PutRolePolicy(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&iam.PutRolePolicyOutput{}, nil)
				m.EXPECT().UpdateAssumeRolePolicy(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(nil, fmt.Errorf("access denied"))
			},
			expectError:   true,
			errorContains: "failed to update trust policy",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			ctrl := gomock.NewController(t)
			mockIAM := awsapi.NewMockIAMAPI(ctrl)
			test.setupMock(mockIAM)

			opts := CreateIAMRoleOptions{
				RoleName:          roleName,
				TrustPolicy:       `{"Version":"2012-10-17"}`,
				PermissionsPolicy: `{"Version":"2012-10-17"}`,
			}

			arn, err := createOrUpdateRole(context.Background(), mockIAM, opts, logr.Discard())
			if test.expectError {
				g.Expect(err).To(HaveOccurred())
				if test.errorContains != "" {
					g.Expect(err.Error()).To(ContainSubstring(test.errorContains))
				}
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(arn).To(Equal(test.expectARN))
			}
		})
	}
}

func TestCreateOperatorRoles(t *testing.T) {
	const (
		ec2RoleARN  = "arn:aws:iam::123456789012:role/hypershift-operator-ec2"
		s3RoleARN   = "arn:aws:iam::123456789012:role/hypershift-operator-oidc-s3"
		dnsRoleARN  = "arn:aws:iam::123456789012:role/hypershift-external-dns"
		providerARN = "arn:aws:iam::123456789012:oidc-provider/oidc.example.com"
	)

	setupSuccessfulMock := func(m *awsapi.MockIAMAPI) {
		// OIDC provider resolution
		m.EXPECT().ListOpenIDConnectProviders(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(&iam.ListOpenIDConnectProvidersOutput{
				OpenIDConnectProviderList: []iamtypes.OpenIDConnectProviderListEntry{
					{Arn: aws.String(providerARN)},
				},
			}, nil)

		// Three role creations (GetRole + PutRolePolicy + UpdateAssumeRolePolicy each)
		for _, roleInfo := range []struct{ name, arn string }{
			{"hypershift-operator-ec2", ec2RoleARN},
			{"hypershift-operator-oidc-s3", s3RoleARN},
			{"hypershift-external-dns", dnsRoleARN},
		} {
			m.EXPECT().GetRole(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(&iam.GetRoleOutput{Role: &iamtypes.Role{
					RoleName: aws.String(roleInfo.name),
					Arn:      aws.String(roleInfo.arn),
				}}, nil)
			m.EXPECT().PutRolePolicy(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(&iam.PutRolePolicyOutput{}, nil)
			m.EXPECT().UpdateAssumeRolePolicy(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(&iam.UpdateAssumeRolePolicyOutput{}, nil)
		}
	}

	tests := map[string]struct {
		opts          CreateOperatorRolesOptions
		setupMock     func(*awsapi.MockIAMAPI)
		expectError   bool
		errorContains string
		validate      func(*GomegaWithT, *CreateOperatorRolesOutput)
	}{
		"When all roles are created successfully with OIDC it should return all ARNs": {
			opts: CreateOperatorRolesOptions{
				OIDCIssuerURL:                   "https://oidc.example.com",
				NamePrefix:                      "hypershift",
				OIDCStorageProviderS3BucketName: "test-bucket",
				OperatorNamespace:               "hypershift",
			},
			setupMock: setupSuccessfulMock,
			validate: func(g *GomegaWithT, output *CreateOperatorRolesOutput) {
				g.Expect(output.OperatorEC2RoleARN).To(Equal(ec2RoleARN))
				g.Expect(output.OperatorOIDCS3RoleARN).To(Equal(s3RoleARN))
				g.Expect(output.ExternalDNSRoleARN).To(Equal(dnsRoleARN))
			},
		},
		"When Route53 hosted zone ID is specified it should scope the policy": {
			opts: CreateOperatorRolesOptions{
				InstanceRoleARN:                 "arn:aws:iam::123456789012:role/instance-role",
				NamePrefix:                      "hypershift",
				OIDCStorageProviderS3BucketName: "test-bucket",
				Route53HostedZoneID:             "Z0123456789ABCDEF",
				OperatorNamespace:               "hypershift",
			},
			setupMock: func(m *awsapi.MockIAMAPI) {
				for _, roleInfo := range []struct{ name, arn string }{
					{"hypershift-operator-ec2", ec2RoleARN},
					{"hypershift-operator-oidc-s3", s3RoleARN},
					{"hypershift-external-dns", dnsRoleARN},
				} {
					m.EXPECT().GetRole(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(&iam.GetRoleOutput{Role: &iamtypes.Role{
							RoleName: aws.String(roleInfo.name),
							Arn:      aws.String(roleInfo.arn),
						}}, nil)
					m.EXPECT().PutRolePolicy(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(&iam.PutRolePolicyOutput{}, nil)
					m.EXPECT().UpdateAssumeRolePolicy(gomock.Any(), gomock.Any(), gomock.Any()).
						Return(&iam.UpdateAssumeRolePolicyOutput{}, nil)
				}
			},
			validate: func(g *GomegaWithT, output *CreateOperatorRolesOutput) {
				g.Expect(output.OperatorEC2RoleARN).To(Equal(ec2RoleARN))
				g.Expect(output.ExternalDNSRoleARN).To(Equal(dnsRoleARN))
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			ctrl := gomock.NewController(t)
			mockIAM := awsapi.NewMockIAMAPI(ctrl)
			test.setupMock(mockIAM)

			output, err := test.opts.CreateOperatorRoles(context.Background(), mockIAM, logr.Discard())
			if test.expectError {
				g.Expect(err).To(HaveOccurred())
				if test.errorContains != "" {
					g.Expect(err.Error()).To(ContainSubstring(test.errorContains))
				}
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				test.validate(g, output)
			}
		})
	}
}

func TestInstanceRoleTrustPolicy(t *testing.T) {
	g := NewGomegaWithT(t)
	roleARN := "arn:aws:iam::123456789012:role/my-instance-role"
	policy := instanceRoleTrustPolicy(roleARN)

	g.Expect(policy).To(ContainSubstring(roleARN))
	g.Expect(policy).To(ContainSubstring("sts:AssumeRole"))
	g.Expect(policy).To(ContainSubstring(`"Effect": "Allow"`))

	var parsed map[string]interface{}
	g.Expect(json.Unmarshal([]byte(policy), &parsed)).To(Succeed())
}

func TestParseAdditionalTags(t *testing.T) {
	tests := map[string]struct {
		tags        []string
		expectError bool
		expectCount int
	}{
		"When no tags provided it should succeed with empty list": {
			tags:        nil,
			expectCount: 0,
		},
		"When valid tags provided it should parse them": {
			tags:        []string{"env=prod", "team=platform"},
			expectCount: 2,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			opts := &CreateOperatorRolesOptions{AdditionalTags: test.tags}
			err := opts.parseAdditionalTags()
			if test.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(opts.additionalIAMTags).To(HaveLen(test.expectCount))
			}
		})
	}
}

func TestOutput(t *testing.T) {
	results := &CreateOperatorRolesOutput{
		OperatorEC2RoleARN:    "arn:aws:iam::123456789012:role/op-ec2",
		OperatorOIDCS3RoleARN: "arn:aws:iam::123456789012:role/op-s3",
		ExternalDNSRoleARN:    "arn:aws:iam::123456789012:role/ext-dns",
	}

	t.Run("When output file is specified it should write JSON to file", func(t *testing.T) {
		g := NewGomegaWithT(t)
		dir := t.TempDir()
		outFile := filepath.Join(dir, "roles.json")

		opts := &CreateOperatorRolesOptions{OutputFile: outFile}
		err := opts.output(results, logr.Discard())
		g.Expect(err).ToNot(HaveOccurred())

		data, err := os.ReadFile(outFile)
		g.Expect(err).ToNot(HaveOccurred())

		var parsed CreateOperatorRolesOutput
		g.Expect(json.Unmarshal(data, &parsed)).To(Succeed())
		g.Expect(parsed.OperatorEC2RoleARN).To(Equal(results.OperatorEC2RoleARN))
		g.Expect(parsed.OperatorOIDCS3RoleARN).To(Equal(results.OperatorOIDCS3RoleARN))
		g.Expect(parsed.ExternalDNSRoleARN).To(Equal(results.ExternalDNSRoleARN))
	})

	t.Run("When output file cannot be created it should return error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		opts := &CreateOperatorRolesOptions{OutputFile: "/nonexistent/path/roles.json"}
		err := opts.output(results, logr.Discard())
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("cannot create output file"))
	})

	t.Run("When no output file is specified it should write to stdout", func(t *testing.T) {
		g := NewGomegaWithT(t)
		// Redirect stdout
		oldStdout := os.Stdout
		r, w, err := os.Pipe()
		g.Expect(err).ToNot(HaveOccurred())
		os.Stdout = w

		opts := &CreateOperatorRolesOptions{}
		err = opts.output(results, logr.Discard())
		g.Expect(err).ToNot(HaveOccurred())

		w.Close()
		os.Stdout = oldStdout

		var buf bytes.Buffer
		_, err = buf.ReadFrom(r)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(buf.String()).To(ContainSubstring("op-ec2"))
	})

	t.Run("When instance role ARN is set it should log ec2-instance-metadata credential source", func(t *testing.T) {
		g := NewGomegaWithT(t)
		dir := t.TempDir()
		outFile := filepath.Join(dir, "roles.json")

		opts := &CreateOperatorRolesOptions{
			OutputFile:      outFile,
			InstanceRoleARN: "arn:aws:iam::123456789012:role/instance",
		}
		err := opts.output(results, logr.Discard())
		g.Expect(err).ToNot(HaveOccurred())
		_ = strings.Contains(CredentialSourceEC2InstanceMetadata, "ec2")
		g.Expect(CredentialSourceEC2InstanceMetadata).To(Equal("ec2-instance-metadata"))
	})
}
