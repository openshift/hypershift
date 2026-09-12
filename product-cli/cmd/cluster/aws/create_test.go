package aws

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hypershiftaws "github.com/openshift/hypershift/cmd/cluster/aws"
	"github.com/openshift/hypershift/cmd/cluster/core"
	awsinfra "github.com/openshift/hypershift/cmd/infra/aws"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/test/integration/framework"

	utilrand "k8s.io/apimachinery/pkg/util/rand"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func TestNewCreateCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		test func(t *testing.T)
	}{
		{
			name: "When AWS create command is created, it should have 'aws' as use",
			test: func(t *testing.T) {
				g := NewWithT(t)
				opts := core.DefaultOptions()
				cmd := NewCreateCommand(opts)
				g.Expect(cmd.Use).To(Equal("aws"))
			},
		},
		{
			name: "When AWS create command is created, it should set release stream to default",
			test: func(t *testing.T) {
				g := NewWithT(t)
				opts := core.DefaultOptions()
				_ = NewCreateCommand(opts)
				g.Expect(opts.ReleaseStream).To(Equal(config.DefaultReleaseStream))
			},
		},
		{
			name: "When AWS create command is created, it should default region to us-east-1",
			test: func(t *testing.T) {
				g := NewWithT(t)
				opts := core.DefaultOptions()
				cmd := NewCreateCommand(opts)
				g.Expect(cmd.Flag("region").DefValue).To(Equal("us-east-1"))
			},
		},
		{
			name: "When AWS create command is created, it should default root-volume-type to gp3",
			test: func(t *testing.T) {
				g := NewWithT(t)
				opts := core.DefaultOptions()
				cmd := NewCreateCommand(opts)
				g.Expect(cmd.Flag("root-volume-type").DefValue).To(Equal("gp3"))
			},
		},
		{
			name: "When AWS create command is created, it should default root-volume-size to 120",
			test: func(t *testing.T) {
				g := NewWithT(t)
				opts := core.DefaultOptions()
				cmd := NewCreateCommand(opts)
				g.Expect(cmd.Flag("root-volume-size").DefValue).To(Equal("120"))
			},
		},
		{
			name: "When AWS create command is created, it should default endpoint-access to Public",
			test: func(t *testing.T) {
				g := NewWithT(t)
				opts := core.DefaultOptions()
				cmd := NewCreateCommand(opts)
				g.Expect(cmd.Flag("endpoint-access").DefValue).To(Equal("Public"))
			},
		},
		{
			name: "When AWS create command is created, it should register product credential flags",
			test: func(t *testing.T) {
				g := NewWithT(t)
				opts := core.DefaultOptions()
				cmd := NewCreateCommand(opts)

				g.Expect(cmd.Flag("role-arn")).ToNot(BeNil(), "role-arn flag should be registered")
				g.Expect(cmd.Flag("sts-creds")).ToNot(BeNil(), "sts-creds flag should be registered")
			},
		},
		{
			name: "When AWS create command is created, it should not register developer-only flags",
			test: func(t *testing.T) {
				g := NewWithT(t)
				opts := core.DefaultOptions()
				cmd := NewCreateCommand(opts)

				g.Expect(cmd.Flag("iam-json")).To(BeNil(), "iam-json is a developer-only flag")
				g.Expect(cmd.Flag("single-nat-gateway")).To(BeNil(), "single-nat-gateway is a developer-only flag")
			},
		},
		{
			name: "When AWS create command is added to a parent, pull-secret should be inherited",
			test: func(t *testing.T) {
				g := NewWithT(t)
				opts := core.DefaultOptions()

				parent := &cobra.Command{Use: "cluster"}
				core.BindOptions(opts, parent.PersistentFlags())

				cmd := NewCreateCommand(opts)
				parent.AddCommand(cmd)

				pullSecretFlag := cmd.InheritedFlags().Lookup("pull-secret")
				g.Expect(pullSecretFlag).ToNot(BeNil(), "pull-secret should be inherited from parent command")
			},
		},
		{
			name: "When AWS create command is created, it should register exactly the expected flags",
			test: func(t *testing.T) {
				g := NewWithT(t)
				opts := core.DefaultOptions()
				cmd := NewCreateCommand(opts)

				expectedFlags := []string{
					"additional-tags",
					"auto-node",
					"enable-proxy",
					"enable-secure-proxy",
					"endpoint-access",
					"instance-type",
					"kms-key-arn",
					"multi-arch",
					"oidc-issuer-url",
					"private-zones-in-cluster-account",
					"proxy-vpc-endpoint-service-name",
					"public-only",
					"region",
					"role-arn",
					"root-volume-iops",
					"root-volume-kms-key",
					"root-volume-size",
					"root-volume-type",
					"sa-token-issuer-private-key-path",
					"secret-creds",
					"shared-role",
					"sts-creds",
					"use-rosa-managed-policies",
					"vpc-cidr",
					"zones",
				}
				var actualFlags []string
				cmd.Flags().VisitAll(func(f *pflag.Flag) {
					actualFlags = append(actualFlags, f.Name)
				})
				sort.Strings(actualFlags)
				g.Expect(actualFlags).To(Equal(expectedFlags))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.test(t)
		})
	}
}

func TestCreateCluster(t *testing.T) {
	utilrand.Seed(1234567890)
	certs.UnsafeSeed(1234567890)
	ctx := framework.InterruptableContext(t.Context())
	tempDir := t.TempDir()
	t.Setenv("FAKE_CLIENT", "true")

	rawCreds, err := json.Marshal(&awsutil.STSCreds{
		Credentials: awsutil.Credentials{
			AccessKeyId:     "fakeAccessKeyId",
			SecretAccessKey: "fakeSecretAccessKey",
			SessionToken:    "fakeSessionToken",
			Expiration:      "fakeExpiration",
		},
	})
	if err != nil {
		t.Fatalf("failed to marshal creds: %v", err)
	}
	credentialsFile := filepath.Join(tempDir, "credentials.yaml")
	if err := os.WriteFile(credentialsFile, rawCreds, 0600); err != nil {
		t.Fatalf("failed to write creds: %v", err)
	}

	rawIAM, err := json.Marshal(&awsinfra.CreateIAMOutput{
		Region:      "fakeRegion",
		ProfileName: "fakeProfileName",
		InfraID:     "fakeInfraID",
		IssuerURL:   "fakeIssuerURL",
		Roles: hyperv1.AWSRolesRef{
			IngressARN:              "fakeIngressARN",
			ImageRegistryARN:        "fakeImageRegistryARN",
			StorageARN:              "fakeStorageARN",
			NetworkARN:              "fakeNetworkARN",
			KubeCloudControllerARN:  "fakeKubeCloudControllerARN",
			NodePoolManagementARN:   "fakeNodePoolManagementARN",
			ControlPlaneOperatorARN: "fakeControlPlaneOperatorARN",
		},
		KMSKeyARN:          "fakeKMSKeyARN",
		KMSProviderRoleARN: "fakeKMSProviderRoleARN",
	})
	if err != nil {
		t.Fatalf("failed to marshal iam: %v", err)
	}
	iamFile := filepath.Join(tempDir, "iam.json")
	if err := os.WriteFile(iamFile, rawIAM, 0600); err != nil {
		t.Fatalf("failed to write iam: %v", err)
	}

	rawInfra, err := json.Marshal(&awsinfra.CreateInfraOutput{
		Region:      "fakeRegion",
		Zone:        "fakeZone",
		InfraID:     "fakeInfraID",
		MachineCIDR: "192.0.2.0/24",
		VPCID:       "fakeVPCID",
		Zones: []*awsinfra.CreateInfraOutputZone{
			{
				Name:     "fakeName",
				SubnetID: "fakeSubnetID",
			},
		},
		Name:             "fakeName",
		BaseDomain:       "fakeBaseDomain",
		BaseDomainPrefix: "fakeBaseDomainPrefix",
		PublicZoneID:     "fakePublicZoneID",
		PrivateZoneID:    "fakePrivateZoneID",
		LocalZoneID:      "fakeLocalZoneID",
		ProxyAddr:        "fakeProxyAddr",
	})
	if err != nil {
		t.Fatalf("failed to marshal infra: %v", err)
	}
	infraFile := filepath.Join(tempDir, "infra.json")
	if err := os.WriteFile(infraFile, rawInfra, 0600); err != nil {
		t.Fatalf("failed to write infra: %v", err)
	}

	pullSecretFile := filepath.Join(tempDir, "pull-secret.json")
	if err := os.WriteFile(pullSecretFile, []byte(`fake`), 0600); err != nil {
		t.Fatalf("failed to write pullSecret: %v", err)
	}

	for _, testCase := range []struct {
		name string
		args []string
	}{
		{
			name: "When minimal flags are provided, it should render correctly",
			args: []string{
				"--sts-creds=" + credentialsFile,
				"--infra-json=" + infraFile,
				"--iam-json=" + iamFile,
				"--role-arn=fakeRoleARN",
				"--pull-secret=" + pullSecretFile,
				"--render-sensitive",
				"--name=example",
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			flags := pflag.NewFlagSet(testCase.name, pflag.ContinueOnError)
			coreOpts := core.DefaultOptions()
			core.BindDeveloperOptions(coreOpts, flags)
			awsOpts := hypershiftaws.DefaultOptions()
			hypershiftaws.BindDeveloperOptions(awsOpts, flags)
			if err := flags.Parse(testCase.args); err != nil {
				t.Fatalf("failed to parse flags: %v", err)
			}

			tempDir := t.TempDir()
			manifestsFile := filepath.Join(tempDir, "manifests.yaml")
			coreOpts.Render = true
			coreOpts.RenderInto = manifestsFile

			if err := core.CreateCluster(ctx, coreOpts, awsOpts); err != nil {
				t.Fatalf("failed to create cluster: %v", err)
			}

			manifests, err := os.ReadFile(manifestsFile)
			if err != nil {
				t.Fatalf("failed to read manifests file: %v", err)
			}
			testutil.CompareWithFixture(t, manifests)
		})
	}
}
