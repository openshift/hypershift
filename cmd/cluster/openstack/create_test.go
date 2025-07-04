package openstack

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/test/integration/framework"

	utilrand "k8s.io/apimachinery/pkg/util/rand"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"
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

	// Set up fake client objects for the test
	supportedVersionsCM := testutil.CreateSupportedVersionsConfigMap()
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
				"--openstack-credentials-file=" + credentialsFile,
				"--openstack-node-flavor=m1.xlarge",
				"--openstack-node-image-name=rhcos",
				"--pull-secret=" + pullSecretFile,
				"--render-sensitive",
				"--name=example",
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

func TestExtractCloud(t *testing.T) {
	dumpYAML := func(t *testing.T, path string, contents map[string]any) {
		data, err := yaml.Marshal(contents)
		if err != nil {
			t.Fatalf("failed to marshal YAML: %v", err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatalf("failed to write YAML to %s: %v", path, err)
		}
	}

	t.Run("invalid path", func(t *testing.T) {
		tempDir := t.TempDir()
		// we know a new temporary directory will be empty so this file will never exist
		cloudsYAMLPath := filepath.Join(tempDir, "clouds.yaml")

		cloudsYAML, caCert, err := extractCloud(cloudsYAMLPath, "", "openstack")

		assert.Nil(t, cloudsYAML)
		assert.Nil(t, caCert)
		assert.Error(t, err)
	})

	t.Run("empty clouds.yaml", func(t *testing.T) {
		tempDir := t.TempDir()
		cloudsYAMLPath := filepath.Join(tempDir, "clouds.yaml")
		junkData := []byte("{ this is not valid YAML }")
		if err := os.WriteFile(cloudsYAMLPath, junkData, 0600); err != nil {
			t.Fatalf("failed to write clouds.yaml: %v", err)
		}

		cloudsYAML, caCert, err := extractCloud(cloudsYAMLPath, "", "openstack")

		assert.Nil(t, cloudsYAML)
		assert.Nil(t, caCert)
		assert.Error(t, err)
	})

	t.Run("incomplete clouds.yaml", func(t *testing.T) {
		tempDir := t.TempDir()
		cloudsYAMLPath := filepath.Join(tempDir, "clouds.yaml")
		junkData := []byte("")
		if err := os.WriteFile(cloudsYAMLPath, junkData, 0600); err != nil {
			t.Fatalf("failed to write clouds.yaml: %v", err)
		}

		cloudsYAML, caCert, err := extractCloud(cloudsYAMLPath, "", "openstack")

		assert.Nil(t, cloudsYAML)
		assert.Nil(t, caCert)
		assert.Error(t, err)
	})

	t.Run("invalid cloud for clouds.yaml", func(t *testing.T) {
		tempDir := t.TempDir()
		cloudsYAMLPath := filepath.Join(tempDir, "clouds.yaml")
		clouds := map[string]any{
			"clouds": map[string]any{},
		}
		dumpYAML(t, cloudsYAMLPath, clouds)

		cloudsYAML, caCert, err := extractCloud(cloudsYAMLPath, "", "openstack")

		assert.Nil(t, cloudsYAML)
		assert.Nil(t, caCert)
		assert.Error(t, err)
	})

	t.Run("invalid cacert path in clouds.yaml", func(t *testing.T) {
		tempDir := t.TempDir()
		cloudsYAMLPath := filepath.Join(tempDir, "clouds.yaml")
		// we know a new temporary directory will be empty so this file will not exist
		caCertPath := filepath.Join(tempDir, "openstack-ca.crt")
		clouds := map[string]any{
			"clouds": map[string]any{
				"openstack": map[string]any{
					"auth": map[string]any{
						"auth_url": "fakeAuthURL",
					},
					"cacert": caCertPath,
				},
			},
		}
		dumpYAML(t, cloudsYAMLPath, clouds)

		cloudsYAML, caCert, err := extractCloud(cloudsYAMLPath, "", "openstack")

		assert.Nil(t, cloudsYAML)
		assert.Nil(t, caCert)
		assert.Error(t, err)
	})

	t.Run("drop any additional clouds specified in clouds.yaml", func(t *testing.T) {
		tempDir := t.TempDir()
		cloudsYAMLPath := filepath.Join(tempDir, "clouds.yaml")
		caCertPath := filepath.Join(tempDir, "valid-ca.crt")
		caCertData := []byte("this is not real CA cert data but that's okay")
		if err := os.WriteFile(caCertPath, caCertData, 0600); err != nil {
			t.Fatalf("failed to write %s: %v", caCertPath, err)
		}
		clouds := map[string]any{
			"clouds": map[string]any{
				"openstack": map[string]any{
					"auth": map[string]any{
						"auth_url": "fakeAuthURL",
					},
					"cacert": caCertPath,
				},
				"another-openstack": map[string]any{
					"auth": map[string]any{
						"auth_url": "fakeAuthURL",
					},
				},
			},
		}
		dumpYAML(t, cloudsYAMLPath, clouds)
		expectedCloudsYAML, err := yaml.Marshal(map[string]any{
			"clouds": map[string]any{
				"openstack": map[string]any{
					"auth": map[string]any{
						"auth_url": "fakeAuthURL",
					},
				},
			},
		})
		assert.Nil(t, err)

		cloudsYAML, caCert, err := extractCloud(cloudsYAMLPath, "", "openstack")

		assert.Equal(t, cloudsYAML, expectedCloudsYAML)
		assert.Equal(t, caCert, caCertData)
		assert.Nil(t, err)
	})

	t.Run("explicit cacert preferred to clouds.yaml", func(t *testing.T) {
		tempDir := t.TempDir()
		cloudsYAMLPath := filepath.Join(tempDir, "clouds.yaml")
		// we know a new temporary directory will be empty so this file will not exist
		invalidCACertPath := filepath.Join(tempDir, "invalid-ca.crt")
		validCACertPath := filepath.Join(tempDir, "valid-ca.crt")
		caCertData := []byte("this is not real CA cert data but that's okay")
		if err := os.WriteFile(validCACertPath, caCertData, 0600); err != nil {
			t.Fatalf("failed to write %s: %v", validCACertPath, err)
		}
		clouds := map[string]any{
			"clouds": map[string]any{
				"openstack": map[string]any{
					"auth": map[string]any{
						"auth_url": "fakeAuthURL",
					},
					// this should be ignored/not checked
					"cacert": invalidCACertPath,
				},
			},
		}
		dumpYAML(t, cloudsYAMLPath, clouds)
		expectedCloudsYAML, err := yaml.Marshal(map[string]any{
			"clouds": map[string]any{
				"openstack": map[string]any{
					"auth": map[string]any{
						"auth_url": "fakeAuthURL",
					},
				},
			},
		})
		assert.Nil(t, err)

		cloudsYAML, caCert, err := extractCloud(cloudsYAMLPath, validCACertPath, "openstack")
		assert.Equal(t, cloudsYAML, expectedCloudsYAML)
		assert.Equal(t, caCert, caCertData)
		assert.Nil(t, err)
	})

}
