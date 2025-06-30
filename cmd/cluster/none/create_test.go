package none

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/test/integration/framework"

	utilrand "k8s.io/apimachinery/pkg/util/rand"

	"github.com/spf13/pflag"
)

func TestCreateCluster(t *testing.T) {
	utilrand.Seed(1234567890)
	certs.UnsafeSeed(1234567890)
	ctx := framework.InterruptableContext(context.Background())
	tempDir := t.TempDir()
	t.Setenv("FAKE_CLIENT", "true")

	pullSecretFile := filepath.Join(tempDir, "pull-secret.json")

	if err := os.WriteFile(pullSecretFile, []byte(`fake`), 0600); err != nil {
		t.Fatalf("failed to write pullSecret: %v", err)
	}

	supportedVersionsCM := testutil.CreateSupportedVersionsConfigMap()

	// Set up fake client objects for the test
	util.SetFakeClientObjects(supportedVersionsCM)
	defer util.ClearFakeClientObjects()

	// Mock HTTP server that returns release tags
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := `{
					"name": "4-stable-multi",
					"tags": [
						{
							"name": "4.19.0",
							"pullSpec": "quay.io/openshift-release-dev/ocp-release:4.19.0-multi",
							"downloadURL": "https://example.com/4.19.0"
						},
						{
							"name": "4.18.5",
							"pullSpec": "quay.io/openshift-release-dev/ocp-release:4.18.5-multi",
							"downloadURL": "https://example.com/4.18.5"
						},
						{
							"name": "4.18.0",
							"pullSpec": "quay.io/openshift-release-dev/ocp-release:4.18.0-multi",
							"downloadURL": "https://example.com/4.18.0"
						}
					]
				}`
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(response))
		if err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer mockServer.Close()

	// Set the environment variable to override the release URL template with the mock server
	t.Setenv("HYPERSHIFT_RELEASE_URL_TEMPLATE", mockServer.URL+"/api/v1/releasestream/%s/tags")

	for _, testCase := range []struct {
		name string
		args []string
	}{
		{
			name: "minimal flags necessary to render",
			args: []string{
				"--external-api-server-address=fakeAddress", // if we don't set it, the machine's IP is looked up, which isn't portable
				"--render-sensitive",
				"--name=example",
				"--pull-secret=" + pullSecretFile,
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			flags := pflag.NewFlagSet(testCase.name, pflag.ContinueOnError)
			coreOpts := core.DefaultOptions()
			core.BindDeveloperOptions(coreOpts, flags)
			noneOpts := DefaultOptions()
			BindOptions(noneOpts, flags)
			if err := flags.Parse(testCase.args); err != nil {
				t.Fatalf("failed to parse flags: %v", err)
			}

			tempDir := t.TempDir()
			manifestsFile := filepath.Join(tempDir, "manifests.yaml")
			coreOpts.Render = true
			coreOpts.RenderInto = manifestsFile

			if err := core.CreateCluster(ctx, coreOpts, noneOpts); err != nil {
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
