package openstack

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/cluster/openstack"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/test/integration/framework"

	utilrand "k8s.io/apimachinery/pkg/util/rand"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"gopkg.in/yaml.v3"
)

func TestNewCreateCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		test func(t *testing.T)
	}{
		{
			name: "When OpenStack create command is created, it should have 'openstack' as use",
			test: func(t *testing.T) {
				g := NewWithT(t)
				opts := core.DefaultOptions()
				cmd := NewCreateCommand(opts)
				g.Expect(cmd.Use).To(Equal("openstack"))
			},
		},
		{
			name: "When OpenStack create command is created, it should register credential flags",
			test: func(t *testing.T) {
				g := NewWithT(t)
				opts := core.DefaultOptions()
				cmd := NewCreateCommand(opts)

				g.Expect(cmd.Flag("openstack-credentials-file")).ToNot(BeNil())
				g.Expect(cmd.Flag("openstack-ca-cert-file")).ToNot(BeNil())
			},
		},
		{
			name: "When OpenStack create command is created, it should register cloud configuration flags",
			test: func(t *testing.T) {
				g := NewWithT(t)
				opts := core.DefaultOptions()
				cmd := NewCreateCommand(opts)

				g.Expect(cmd.Flag("openstack-cloud")).ToNot(BeNil())
				g.Expect(cmd.Flag("openstack-external-network-id")).ToNot(BeNil())
				g.Expect(cmd.Flag("openstack-ingress-floating-ip")).ToNot(BeNil())
				g.Expect(cmd.Flag("openstack-dns-nameservers")).ToNot(BeNil())
			},
		},
		{
			name: "When OpenStack create command is created, it should register nodepool flags",
			test: func(t *testing.T) {
				g := NewWithT(t)
				opts := core.DefaultOptions()
				cmd := NewCreateCommand(opts)

				g.Expect(cmd.Flag("openstack-node-flavor")).ToNot(BeNil())
				g.Expect(cmd.Flag("openstack-node-image-name")).ToNot(BeNil())
				g.Expect(cmd.Flag("openstack-node-availability-zone")).ToNot(BeNil())
			},
		},
		{
			name: "When OpenStack create command is added to a parent, pull-secret should be inherited",
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
			name: "When OpenStack create command is created, it should register exactly the expected flags",
			test: func(t *testing.T) {
				g := NewWithT(t)
				opts := core.DefaultOptions()
				cmd := NewCreateCommand(opts)

				expectedFlags := []string{
					"openstack-ca-cert-file",
					"openstack-cloud",
					"openstack-credentials-file",
					"openstack-dns-nameservers",
					"openstack-external-network-id",
					"openstack-ingress-floating-ip",
					"openstack-node-additional-port",
					"openstack-node-availability-zone",
					"openstack-node-flavor",
					"openstack-node-image-name",
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
	t.Setenv("OS_CLOUD", "")

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
			name: "When minimal flags are provided, it should render correctly",
			args: []string{
				"--openstack-credentials-file=" + credentialsFile,
				"--openstack-node-flavor=m1.xlarge",
				"--openstack-node-image-name=rhcos",
				"--pull-secret=" + pullSecretFile,
				"--render-sensitive",
				"--name=example",
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			flags := pflag.NewFlagSet(testCase.name, pflag.ContinueOnError)
			coreOpts := core.DefaultOptions()
			core.BindOptions(coreOpts, flags)
			openstackOpts := openstack.DefaultOptions()
			openstack.BindOptions(openstackOpts, flags)
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
