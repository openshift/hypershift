package aws

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	awsinfra "github.com/openshift/hypershift/cmd/infra/aws"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/test/integration/framework"

	utilrand "k8s.io/apimachinery/pkg/util/rand"

	"github.com/spf13/pflag"
)

func TestIsRequiredOption(t *testing.T) {
	tests := map[string]struct {
		value         string
		expectedError bool
	}{
		"when value is empty": {
			value:         "",
			expectedError: true,
		},
		"when value is not empty": {
			value:         "",
			expectedError: true,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			err := util.ValidateRequiredOption("", test.value)
			if test.expectedError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).To(BeNil())
			}
		})
	}
}

func TestValidateCreateCredentialInfo(t *testing.T) {
	tests := map[string]struct {
		credentials          awsutil.AWSCredentialsOptions
		credentialSecretName string
		pullSecretFile       string
		expectError          bool
	}{
		"when CredentialSecretName is blank and aws-creds is also blank": {
			expectError: true,
		},
		"when CredentialSecretName is blank, aws-creds is not blank, and pull-secret is blank": {
			pullSecretFile:       "",
			credentialSecretName: "",
			credentials:          awsutil.AWSCredentialsOptions{AWSCredentialsFile: "asdf"},
			expectError:          true,
		},
		"when CredentialSecretName is blank, aws-creds is not blank, and pull-secret is not blank": {
			pullSecretFile:       "asdf",
			credentialSecretName: "",
			credentials:          awsutil.AWSCredentialsOptions{AWSCredentialsFile: "asdf"},
			expectError:          false,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			err := ValidateCreateCredentialInfo(test.credentials, test.credentialSecretName, "", test.pullSecretFile)
			if test.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).To(BeNil())
			}
		})
	}
}

func TestCreateCluster(t *testing.T) {
	utilrand.Seed(1234567890)
	certs.UnsafeSeed(1234567890)
	ctx := framework.InterruptableContext(context.Background())
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
			name: "minimal flags necessary to render",
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
		{
			name: "default creation flags for cesar",
			args: []string{
				"--pull-secret=" + pullSecretFile,
				"--name=example",
				"--sts-creds=" + credentialsFile,
				"--infra-json=" + infraFile,
				"--iam-json=" + iamFile,
				"--role-arn=fakeRoleARN",
				"--pull-secret=" + pullSecretFile,
				"--instance-type=m5.large",
				"--region=us-east-2",
				"--auto-repair",
				"--name=cesar",
				"--endpoint-access=Public",
				"--node-pool-replicas=2",
				"--base-domain=cewong.hypershift.devcluster.openshift.com",
				"--control-plane-operator-image=fakeCPOImage",
				"--release-image=fakeReleaseImage",
				"--annotations=hypershift.openshift.io/cleanup-cloud-resources=true",
				"--render-sensitive",
			},
		},
		{
			name: "minimal with KubeAPIServerDNSName",
			args: []string{
				"--name=example",
				"--sts-creds=" + credentialsFile,
				"--infra-json=" + infraFile,
				"--iam-json=" + iamFile,
				"--role-arn=fakeRoleARN",
				"--pull-secret=" + pullSecretFile,
				"--kas-dns-name=test-dns-name.example.com",
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			flags := pflag.NewFlagSet(testCase.name, pflag.ContinueOnError)
			coreOpts := core.DefaultOptions()
			core.BindDeveloperOptions(coreOpts, flags)
			awsOpts := DefaultOptions()
			BindDeveloperOptions(awsOpts, flags)
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
