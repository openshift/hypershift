package cno

// Tests the rewrite-config init container script from the CNO deployment.
//
// The init container builds kubeconfig files using KUBERNETES_SERVICE_HOST and
// KUBERNETES_SERVICE_PORT to construct the management cluster's server URL.
// The URL must be correctly formatted per RFC 3986:
//
//   - IPv6 addresses require brackets: https://[fd00::1]:443
//   - IPv4 and hostnames must not have brackets: https://172.29.0.1:443
//
// This is required for CVE-2025-47912 compliance (Go 1.24.8+ rejects IPv4 in brackets).
//
// The test extracts the shell script from the actual deployment.yaml and runs it with a mocked kubectl that logs its
// arguments. The logged calls are compared against an expected template to verify the logic of IPV4 and IPV6 detection
// in the init container shell scripts.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"text/template"

	appsv1 "k8s.io/api/apps/v1"

	"sigs.k8s.io/yaml"

	"github.com/google/go-cmp/cmp"
)

// expectedKubectlLogTemplate is a template of the expected calls of kubectl. The {{.MgmtServer}} placeholder is the dynamic
// part calculated by the shell script. It will be filled by expectedKubectlLog() for each test case.
//
// We'll be mocking kubectl via a function that echos all the args its receives to a file and use this template to
// compare the expected output vs the actual output.
const expectedKubectlLogTemplate = `--kubeconfig /configs/management config set clusters.default.server {{.MgmtServer}}
--kubeconfig /configs/management config set clusters.default.certificate-authority /var/run/secrets/kubernetes.io/serviceaccount/ca.crt
--kubeconfig /configs/management config set users.admin.tokenFile /var/run/secrets/kubernetes.io/serviceaccount/token
--kubeconfig /configs/management config set contexts.default.cluster default
--kubeconfig /configs/management config set contexts.default.user admin
--kubeconfig /configs/management config set contexts.default.namespace NAMESPACE
--kubeconfig /configs/management config use-context default
--kubeconfig /configs/hosted config set clusters.default.server https://kube-apiserver:6443
--kubeconfig /configs/hosted config set clusters.default.certificate-authority /etc/certificate/ca/ca.crt
--kubeconfig /configs/hosted config set users.admin.tokenFile /var/run/secrets/kubernetes.io/hosted/token
--kubeconfig /configs/hosted config set contexts.default.cluster default
--kubeconfig /configs/hosted config set contexts.default.user admin
--kubeconfig /configs/hosted config set contexts.default.namespace openshift-network-operator
--kubeconfig /configs/hosted config use-context default
`

// expectedKubectlLog renders the template with the expected server URL.
func expectedKubectlLog(mgmtServer string) string {
	tmpl := template.Must(template.New("log").Parse(expectedKubectlLogTemplate))
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]string{
		"MgmtServer": mgmtServer,
	}); err != nil {
		panic("failed to execute kubectlLogTemplate: " + err.Error())
	}
	return buf.String()
}

// TestRewriteConfigInitContainer verifies that the rewrite-config init container generates calls kubectl with the
// correct args, including the properly formatted server URLs. This is basically verifying the logic of IPV4 and IPV6
// detection in the init container shell scripts
func TestRewriteConfigInitContainer(t *testing.T) {
	// Read and parse the deployment manifest
	deploymentYAML, err := os.ReadFile("../assets/cluster-network-operator/deployment.yaml")
	if err != nil {
		t.Fatalf("failed to read deployment.yaml: %v", err)
	}

	var deployment appsv1.Deployment
	if err := yaml.Unmarshal(deploymentYAML, &deployment); err != nil {
		t.Fatalf("failed to parse deployment YAML: %v", err)
	}

	script := extractRewriteConfigScript(t, &deployment)
	if script == "" {
		t.Fatal("could not find rewrite-config init container script")
	}

	// Test cases - only the varying parts
	tests := []struct {
		name           string
		host           string
		port           string
		expectedServer string
	}{
		{
			name:           "When KUBERNETES_SERVICE_HOST is IPv4 it should format URL without brackets",
			host:           "172.29.0.1",
			port:           "443",
			expectedServer: "https://172.29.0.1:443",
		},
		{
			name:           "When KUBERNETES_SERVICE_HOST is private IPv4 it should format URL without brackets",
			host:           "10.0.0.1",
			port:           "443",
			expectedServer: "https://10.0.0.1:443",
		},
		{
			name:           "When KUBERNETES_SERVICE_HOST is IPv6 loopback it should format URL with brackets",
			host:           "::1",
			port:           "443",
			expectedServer: "https://[::1]:443",
		},
		{
			name:           "When KUBERNETES_SERVICE_HOST is IPv6 ULA it should format URL with brackets",
			host:           "fd00::1",
			port:           "443",
			expectedServer: "https://[fd00::1]:443",
		},
		{
			name:           "When KUBERNETES_SERVICE_HOST is full IPv6 it should format URL with brackets",
			host:           "2001:db8::1",
			port:           "443",
			expectedServer: "https://[2001:db8::1]:443",
		},
		{
			name:           "When KUBERNETES_SERVICE_HOST is IPv6 mapped IPv4 it should format URL with brackets",
			host:           "::ffff:192.0.2.1",
			port:           "443",
			expectedServer: "https://[::ffff:192.0.2.1]:443",
		},
		{
			name:           "When KUBERNETES_SERVICE_HOST is hostname it should format URL without brackets",
			host:           "kubernetes.default.svc",
			port:           "443",
			expectedServer: "https://kubernetes.default.svc:443",
		},
		{
			name:           "When KUBERNETES_SERVICE_HOST is hostname with subdomain it should format URL without brackets",
			host:           "api.example.com",
			port:           "6443",
			expectedServer: "https://api.example.com:6443",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kubectlLog := filepath.Join(t.TempDir(), "kubectl.log")

			testScript := buildTestScript(script, kubectlLog)

			cmd := exec.Command("bash", "-c", testScript)
			cmd.Env = []string{
				"KUBERNETES_SERVICE_HOST=" + tt.host,
				"KUBERNETES_SERVICE_PORT=" + tt.port,
				"KUBE_APISERVER_SERVICE_PORT=6443",
			}

			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("script failed: %v\nOutput: %s", err, output)
			}

			actualLog, err := os.ReadFile(kubectlLog)
			if err != nil {
				t.Fatalf("failed to read kubectl log: %v", err)
			}

			expectedLog := expectedKubectlLog(tt.expectedServer)
			if diff := cmp.Diff(expectedLog, string(actualLog)); diff != "" {
				t.Errorf("kubectl log mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// extractRewriteConfigScript finds the rewrite-config init container and
// returns the shell script from its args.
func extractRewriteConfigScript(t *testing.T, deployment *appsv1.Deployment) string {
	t.Helper()
	for _, container := range deployment.Spec.Template.Spec.InitContainers {
		if container.Name == "rewrite-config" {
			for _, arg := range container.Args {
				if strings.Contains(arg, "KUBERNETES_SERVICE_HOST") {
					return arg
				}
			}
		}
	}
	return ""
}

// buildTestScript prepends mock functions for kubectl and cat to the script.
func buildTestScript(script, kubectlLog string) string {
	mocks := `
kubectl() { echo "$@" >> "` + kubectlLog + `"; }
cat() { echo "NAMESPACE"; }
`
	return mocks + script
}
