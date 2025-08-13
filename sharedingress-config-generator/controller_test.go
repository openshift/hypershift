package sharedingressconfiggenerator

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/api"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// Ensure the mock fulfills the interface
var _ haProxyClient = &mockHAProxyClient{}

type mockHAProxyClient struct {
	MockResponse string
	MockError    error

	sendCommandCalled bool
	lastCommand       string
}

func (m *mockHAProxyClient) sendCommand(socketPath, command string) (string, error) {
	m.sendCommandCalled = true
	m.lastCommand = command
	return m.MockResponse, m.MockError
}

func TestReconcile(t *testing.T) {
	g := NewWithT(t)
	var err error
	tempDir, err := os.MkdirTemp("", "haproxy-config-*")
	g.Expect(err).NotTo(HaveOccurred())
	defer os.RemoveAll(tempDir)

	configPath := filepath.Join(tempDir, "haproxy.cfg")

	t.Run("When reconciliation succeeds", func(t *testing.T) {
		mockClient := &mockHAProxyClient{
			MockResponse: "Success=1",
			MockError:    nil,
		}
		reconciler := &SharedIngressConfigReconciler{
			client:        fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
			configPath:    configPath,
			haProxyClient: mockClient,
		}

		result, err := reconciler.Reconcile(t.Context(), ctrl.Request{})

		g.Expect(err).NotTo(HaveOccurred(), "Reconcile should not return an error on success")
		g.Expect(result).To(Equal(ctrl.Result{}), "Reconcile result should be empty on success")

		// Verify config file was created and has the expected content
		fileContent, err := os.ReadFile(configPath)
		g.Expect(err).NotTo(HaveOccurred(), "Config file should be readable")

		var writer bytes.Buffer
		err = generateRouterConfig(t.Context(), reconciler.client, &writer)
		g.Expect(err).NotTo(HaveOccurred())
		expectedConfig := writer.Bytes()

		g.Expect(fileContent).To(Equal(expectedConfig))

		// Verify that the HAProxy reload command was actually sent
		g.Expect(mockClient.sendCommandCalled).To(BeTrue(), "The sendCommand method should have been called")
		g.Expect(mockClient.lastCommand).To(Equal("reload"), "The command sent should be 'reload'")
	})

	t.Run("When HAProxy reload fails with an application error", func(t *testing.T) {
		reconciler := &SharedIngressConfigReconciler{
			client:     fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
			configPath: configPath,
			haProxyClient: &mockHAProxyClient{
				MockResponse: "Success=0\n--\n[ALERT] Invalid keyword 'blah' on line 10",
				MockError:    nil, // The connection itself was successful
			},
		}

		_, err := reconciler.Reconcile(t.Context(), ctrl.Request{})

		g.Expect(err).To(HaveOccurred(), "Reconcile should return an error when HAProxy fails to reload")
		g.Expect(err.Error()).To(ContainSubstring("Invalid keyword 'blah'"), "The error message should contain the reason for failure")
	})

	t.Run("When HAProxy reload fails with a transport error", func(t *testing.T) {
		reconciler := &SharedIngressConfigReconciler{
			client:     fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
			configPath: configPath,
			haProxyClient: &mockHAProxyClient{
				MockResponse: "",
				MockError:    fmt.Errorf("failed to connect to socket: no such file or directory"),
			},
		}

		_, err := reconciler.Reconcile(t.Context(), ctrl.Request{})

		g.Expect(err).To(HaveOccurred(), "Reconcile should return an error when the socket connection fails")
		g.Expect(err.Error()).To(ContainSubstring("no such file or directory"), "The error message should contain the transport failure reason")
	})

	// --- Test Case 4: Filesystem is Not Writable ---
	t.Run("When the config file path is not writable", func(t *testing.T) {
		reconciler := &SharedIngressConfigReconciler{
			client:        fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
			configPath:    configPath,
			haProxyClient: &mockHAProxyClient{},
		}

		// Make the temporary directory read-only to simulate a permissions error
		g.Expect(os.Chmod(tempDir, 0555)).To(Succeed()) // r-x r-x r-x
		// Ensure we restore permissions so the deferred os.RemoveAll can work
		defer func() {
			g.Expect(os.Chmod(tempDir, 0755)).To(Succeed())
		}()

		_, err := reconciler.Reconcile(t.Context(), ctrl.Request{})

		// Reconcile function should return a filesystem error
		g.Expect(err).To(HaveOccurred(), "Reconcile should return an error when the config file cannot be written")
		g.Expect(strings.ToLower(err.Error())).To(ContainSubstring("permission denied"), "The error message should indicate a permission issue")
	})
}
