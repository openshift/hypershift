package sharedingressconfiggenerator

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	sendCommandCount  int
	lastCommand       string
}

func (m *mockHAProxyClient) sendCommand(socketPath, command string) (string, error) {
	m.sendCommandCalled = true
	m.sendCommandCount++
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
			now:           time.Now,
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
			now:        time.Now,
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
			now:        time.Now,
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
			now:           time.Now,
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

func TestReconcileCoalescing(t *testing.T) {
	g := NewWithT(t)
	tempDir, err := os.MkdirTemp("", "haproxy-coalesce-*")
	g.Expect(err).NotTo(HaveOccurred())
	defer os.RemoveAll(tempDir)

	configPath := filepath.Join(tempDir, "haproxy.cfg")

	t.Run("When reconciling for the first time, it should reload immediately without coalescing", func(t *testing.T) {
		g := NewWithT(t)
		mockClient := &mockHAProxyClient{
			MockResponse: "Success=1",
		}
		reconciler := &SharedIngressConfigReconciler{
			client:        fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
			configPath:    configPath,
			haProxyClient: mockClient,
			now:           time.Now,
		}

		result, err := reconciler.Reconcile(t.Context(), ctrl.Request{})

		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(result.RequeueAfter).To(BeZero(), "First reconcile should not defer reload")
		g.Expect(mockClient.sendCommandCount).To(Equal(1), "First reconcile should reload immediately")
	})

	t.Run("When a config change occurs within the coalesce window, it should defer the reload", func(t *testing.T) {
		g := NewWithT(t)
		mockClient := &mockHAProxyClient{
			MockResponse: "Success=1",
		}

		currentTime := time.Now()
		reconciler := &SharedIngressConfigReconciler{
			client:           fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
			configPath:       filepath.Join(tempDir, "haproxy-coalesce.cfg"),
			haProxyClient:    mockClient,
			now:              func() time.Time { return currentTime },
			lastReloadedHash: []byte("previous-hash"), // Simulate a previous successful reload
		}

		// First reconcile writes config and defers reload
		result, err := reconciler.Reconcile(t.Context(), ctrl.Request{})

		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(result.RequeueAfter).To(BeNumerically(">", 0), "Should defer reload with RequeueAfter")
		g.Expect(result.RequeueAfter).To(BeNumerically("<=", reloadCoalesceWindow))
		g.Expect(mockClient.sendCommandCount).To(Equal(0), "Should not reload during coalesce window")
	})

	t.Run("When the coalesce window expires, it should proceed with the reload", func(t *testing.T) {
		g := NewWithT(t)
		mockClient := &mockHAProxyClient{
			MockResponse: "Success=1",
		}

		currentTime := time.Now()
		reconciler := &SharedIngressConfigReconciler{
			client:           fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
			configPath:       filepath.Join(tempDir, "haproxy-expired.cfg"),
			haProxyClient:    mockClient,
			now:              func() time.Time { return currentTime },
			lastReloadedHash: []byte("previous-hash"),
		}

		// First reconcile: writes config, defers reload
		result, err := reconciler.Reconcile(t.Context(), ctrl.Request{})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		g.Expect(mockClient.sendCommandCount).To(Equal(0))

		// Advance time past the coalesce window
		currentTime = currentTime.Add(reloadCoalesceWindow + time.Second)

		// Second reconcile: coalesce window expired, reload should happen
		result, err = reconciler.Reconcile(t.Context(), ctrl.Request{})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(result.RequeueAfter).To(BeZero(), "Should not defer after coalesce window expired")
		g.Expect(mockClient.sendCommandCount).To(Equal(1), "Should reload after coalesce window expired")
	})

	t.Run("When rapid changes occur, it should reset the coalesce window", func(t *testing.T) {
		g := NewWithT(t)
		mockClient := &mockHAProxyClient{
			MockResponse: "Success=1",
		}

		currentTime := time.Now()
		configFile := filepath.Join(tempDir, "haproxy-rapid.cfg")
		reconciler := &SharedIngressConfigReconciler{
			client:           fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
			configPath:       configFile,
			haProxyClient:    mockClient,
			now:              func() time.Time { return currentTime },
			lastReloadedHash: []byte("previous-hash"),
		}

		// First reconcile: writes config, defers reload
		_, err := reconciler.Reconcile(t.Context(), ctrl.Request{})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(mockClient.sendCommandCount).To(Equal(0))

		// Advance 2s (still within 5s window) and trigger another reconcile.
		// Since config hasn't actually changed (same empty client), we need to
		// delete the config file to simulate a change.
		currentTime = currentTime.Add(2 * time.Second)
		g.Expect(os.Remove(configFile)).To(Succeed())

		_, err = reconciler.Reconcile(t.Context(), ctrl.Request{})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(mockClient.sendCommandCount).To(Equal(0), "Should still defer - coalesce window reset")

		// Advance another 2s (4s since second write, within window)
		currentTime = currentTime.Add(2 * time.Second)
		result, err := reconciler.Reconcile(t.Context(), ctrl.Request{})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(result.RequeueAfter).To(BeNumerically(">", 0), "Should still defer within coalesce window")
		g.Expect(mockClient.sendCommandCount).To(Equal(0))

		// Advance past the coalesce window from the last write
		currentTime = currentTime.Add(reloadCoalesceWindow)
		result, err = reconciler.Reconcile(t.Context(), ctrl.Request{})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(result.RequeueAfter).To(BeZero())
		g.Expect(mockClient.sendCommandCount).To(Equal(1), "Should reload after coalesce window from last write")
	})

	t.Run("When sustained changes exceed max coalesce delay, it should force a reload", func(t *testing.T) {
		g := NewWithT(t)
		mockClient := &mockHAProxyClient{
			MockResponse: "Success=1",
		}

		currentTime := time.Now()
		configFile := filepath.Join(tempDir, "haproxy-maxdelay.cfg")
		reconciler := &SharedIngressConfigReconciler{
			client:           fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
			configPath:       configFile,
			haProxyClient:    mockClient,
			now:              func() time.Time { return currentTime },
			lastReloadedHash: []byte("previous-hash"),
		}

		// First reconcile: writes config, defers reload
		_, err := reconciler.Reconcile(t.Context(), ctrl.Request{})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(mockClient.sendCommandCount).To(Equal(0))

		// Simulate sustained trickle of changes: advance 3s and write new config
		// repeatedly. Each write resets lastConfigWriteTime but firstPendingWriteTime
		// stays anchored to the first write.
		for range 9 {
			currentTime = currentTime.Add(3 * time.Second)
			g.Expect(os.Remove(configFile)).To(Succeed()) // force config change
			_, err = reconciler.Reconcile(t.Context(), ctrl.Request{})
			g.Expect(err).NotTo(HaveOccurred())
		}
		// After 9 iterations * 3s = 27s, still under maxCoalesceDelay (30s)
		g.Expect(mockClient.sendCommandCount).To(Equal(0), "Should still defer before max delay")

		// Advance 3s more: total 30s from first pending write, reaching maxCoalesceDelay
		currentTime = currentTime.Add(3 * time.Second)
		g.Expect(os.Remove(configFile)).To(Succeed())
		result, err := reconciler.Reconcile(t.Context(), ctrl.Request{})
		g.Expect(err).NotTo(HaveOccurred())

		// At 30s since first pending write, the max delay cap forces a reload
		// even though the last config write just happened (sinceLastWrite=0).
		g.Expect(result.RequeueAfter).To(BeZero(), "Max delay should force reload despite recent write")
		g.Expect(mockClient.sendCommandCount).To(Equal(1), "Should reload when max coalesce delay exceeded")
	})

	t.Run("When a reload succeeds, it should reset the max delay tracking", func(t *testing.T) {
		g := NewWithT(t)
		mockClient := &mockHAProxyClient{
			MockResponse: "Success=1",
		}

		currentTime := time.Now()
		configFile := filepath.Join(tempDir, "haproxy-maxreset.cfg")
		reconciler := &SharedIngressConfigReconciler{
			client:           fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
			configPath:       configFile,
			haProxyClient:    mockClient,
			now:              func() time.Time { return currentTime },
			lastReloadedHash: []byte("previous-hash"),
		}

		// First reconcile writes config, defers reload
		_, err := reconciler.Reconcile(t.Context(), ctrl.Request{})
		g.Expect(err).NotTo(HaveOccurred())

		// Advance past maxCoalesceDelay to force reload
		currentTime = currentTime.Add(maxCoalesceDelay + time.Second)
		_, err = reconciler.Reconcile(t.Context(), ctrl.Request{})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(mockClient.sendCommandCount).To(Equal(1), "Should have reloaded")

		// Verify firstPendingWriteTime was reset after successful reload
		g.Expect(reconciler.firstPendingWriteTime.IsZero()).To(BeTrue(),
			"firstPendingWriteTime should be reset after successful reload")

		// Simulate a new config change by setting lastReloadedHash to a different
		// value, as if the config has changed since the last reload.
		reconciler.lastReloadedHash = []byte("changed-again")
		currentTime = currentTime.Add(time.Second)
		g.Expect(os.Remove(configFile)).To(Succeed())
		result, err := reconciler.Reconcile(t.Context(), ctrl.Request{})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(result.RequeueAfter).To(BeNumerically(">", 0), "Should defer again after reset")
		g.Expect(mockClient.sendCommandCount).To(Equal(1), "Should not have reloaded yet")

		// Advance past coalesce window — should reload normally
		currentTime = currentTime.Add(reloadCoalesceWindow + time.Second)
		result, err = reconciler.Reconcile(t.Context(), ctrl.Request{})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(result.RequeueAfter).To(BeZero())
		g.Expect(mockClient.sendCommandCount).To(Equal(2), "Should reload after coalesce window")
	})
}
