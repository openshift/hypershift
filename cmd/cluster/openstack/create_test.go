package openstack

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/test/integration/framework"
	"github.com/spf13/pflag"
	"gopkg.in/yaml.v3"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
)

func TestCreateOptions_Validate(t *testing.T) {
	for _, test := range []struct {
		name          string
		input         RawCreateOptions
		expectedError string
	}{
		{
			name: "missing OpenStack credentials file",
			input: RawCreateOptions{
				OpenStackCredentialsFile: "thisisajunkfilename.yaml",
			},
			expectedError: "OpenStack credentials file does not exist",
		},
	} {
		var errString string
		if _, err := test.input.Validate(context.Background(), nil); err != nil {
			errString = err.Error()
		}
		if !strings.Contains(errString, test.expectedError) {
			t.Errorf("got incorrect error: expected: %v actual: %v", test.expectedError, errString)
		}
	}
}

func TestCreateCluster(t *testing.T) {
	utilrand.Seed(1234567890)
	certs.UnsafeSeed(1234567890)
	ctx := framework.InterruptableContext(context.Background())
	tempDir := t.TempDir()
	t.Setenv("FAKE_CLIENT", "true")

	cloudsYAML := map[string]interface{}{
		"clouds": map[string]interface{}{
			"openstack": map[string]interface{}{
				"auth": map[string]interface{}{
					"auth_url": "fakeAuthURL",
				},
			},
		},
	}
	cloudsData, err := yaml.Marshal(cloudsYAML)
	if err != nil {
		t.Fatalf("failed to marshal clouds.yaml: %v", err)
	}
	credentialsFile := filepath.Join(tempDir, "clouds.yaml")
	if err := os.WriteFile(credentialsFile, cloudsData, 0600); err != nil {
		t.Fatalf("failed to write creds: %v", err)
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
				"--openstack-credentials-file=" + credentialsFile,
				"--openstack-node-flavor=m1.xlarge",
				"--openstack-node-image-name=rhcos",
				"--pull-secret=" + pullSecretFile,
				"--render-sensitive",
			},
		},
		{
			name: "default creation flags",
			args: []string{
				"--openstack-credentials-file=" + credentialsFile,
				"--openstack-external-network-id=5387f86a-a10e-47fe-91c6-41ac131f9f30",
				"--openstack-node-image-name=rhcos",
				"--openstack-node-flavor=fakeFlavor",
				"--pull-secret=" + pullSecretFile,
				"--auto-repair",
				"--name=test",
				"--node-pool-replicas=2",
				"--base-domain=test.hypershift.devcluster.openshift.com",
				"--control-plane-operator-image=fakeCPOImage",
				"--release-image=fakeReleaseImage",
				"--annotations=hypershift.openshift.io/cleanup-cloud-resources=true",
				"--render-sensitive",
				"--machine-cidr=192.168.25.0/24",
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			flags := pflag.NewFlagSet(testCase.name, pflag.ContinueOnError)
			coreOpts := core.DefaultOptions()
			core.BindDeveloperOptions(coreOpts, flags)
			openstackOpts := DefaultOptions()
			BindOptions(openstackOpts, flags)
			if err := flags.Parse(testCase.args); err != nil {
				t.Fatalf("failed to parse flags: %v", err)
			}

			tempDir := t.TempDir()
			manifestsFile := filepath.Join(tempDir, "manifests.yaml")
			coreOpts.Render = true
			coreOpts.RenderInto = manifestsFile

			if err := core.CreateCluster(ctx, coreOpts, openstackOpts); err != nil {
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
