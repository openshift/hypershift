package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/test/integration/framework"
	"github.com/spf13/pflag"
)

func TestCreateCluster(t *testing.T) {
	ctx := framework.InterruptableContext(context.Background())

	for _, testCase := range []struct {
		name string
		args []string
	}{
		{
			name: "minimal flags necessary to render",
			args: []string{
				"--api-server-address=fakeAddress", // if we don't set it, the machine's IP is looked up, which isn't portable
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
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
