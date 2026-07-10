package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/test/integration/framework"

	utilrand "k8s.io/apimachinery/pkg/util/rand"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/pflag"
)

func TestRawCreateOptions_Validate(t *testing.T) {
	for _, test := range []struct {
		name          string
		input         RawCreateOptions
		expectedError string
	}{
		{
			name: "When service-publishing-strategy is unsupported, it should return an error",
			input: RawCreateOptions{
				ServicePublishingStrategy: "whatever",
			},
			expectedError: "service publishing strategy whatever is not supported, supported options: NodePort, LoadBalancer",
		},
		{
			name: "When api-server-address is set with LoadBalancer strategy, it should return an error",
			input: RawCreateOptions{
				ServicePublishingStrategy: LoadBalancerServicePublishingStrategy,
				APIServerAddress:          "1.2.3.4",
			},
			expectedError: "--api-server-address is supported only for NodePort service publishing strategy, service publishing strategy LoadBalancer is used",
		},
		{
			name: "When service-publishing-strategy is NodePort, it should pass validation",
			input: RawCreateOptions{
				ServicePublishingStrategy: NodePortServicePublishingStrategy,
				APIServerAddress:          "1.2.3.4",
			},
		},
		{
			name: "When service-publishing-strategy is LoadBalancer, it should pass validation",
			input: RawCreateOptions{
				ServicePublishingStrategy: LoadBalancerServicePublishingStrategy,
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var errString string
			if _, err := test.input.Validate(t.Context(), nil); err != nil {
				errString = err.Error()
			}
			if diff := cmp.Diff(test.expectedError, errString); diff != "" {
				t.Errorf("got incorrect error: %v", diff)
			}
		})
	}
}

func TestCreateCluster(t *testing.T) {
	ctx := framework.InterruptableContext(t.Context())

	tempDir := t.TempDir()

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
				"--api-server-address=fakeAddress", // if we don't set it, the machine's IP is looked up, which isn't portable
				"--render-sensitive",
				"--name=example",
				"--pull-secret=" + pullSecretFile,
			},
		},
		{
			name: "When service-publishing-strategy is LoadBalancer, it should render LoadBalancer services",
			args: []string{
				"--service-publishing-strategy=LoadBalancer",
				"--render-sensitive",
				"--name=example",
				"--pull-secret=" + pullSecretFile,
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			utilrand.Seed(1234567890)
			certs.UnsafeSeed(1234567890)

			flags := pflag.NewFlagSet(testCase.name, pflag.ContinueOnError)
			coreOpts := core.DefaultOptions()
			core.BindDeveloperOptions(coreOpts, flags)
			agentOpts := DefaultOptions()
			BindOptions(agentOpts, flags)
			if err := flags.Parse(testCase.args); err != nil {
				t.Fatalf("failed to parse flags: %v", err)
			}

			tempDir := t.TempDir()
			manifestsFile := filepath.Join(tempDir, "manifests.yaml")
			coreOpts.Render = true
			coreOpts.RenderInto = manifestsFile

			if err := core.CreateCluster(ctx, coreOpts, agentOpts); err != nil {
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
