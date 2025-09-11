package syncglobalpullsecret

// sync-global-pullsecret syncs the pull secret from the user provided pull secret in DataPlane and appends it to the HostedCluster PullSecret to be deployed in the nodes of the HostedCluster.

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/coreos/go-systemd/dbus"
	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// syncGlobalPullSecretOptions contains the configuration options for the sync-global-pullsecret command
type syncGlobalPullSecretOptions struct {
	kubeletConfigJsonPath string
	globalPSSecretName    string
}

//go:generate ../hack/tools/bin/mockgen -destination=sync-global-pullsecret_mock.go -package=syncglobalpullsecret . dbusConn
type dbusConn interface {
	RestartUnit(name string, mode string, ch chan<- string) (int, error)
	Close()
}

// GlobalPullSecretSyncer handles the synchronization of pull secrets
type GlobalPullSecretSyncer struct {
	kubeletConfigJsonPath string
	log                   logr.Logger
}

const (
	defaultKubeletConfigJsonPath = "/var/lib/kubelet/config.json"
	defaultGlobalPSSecretName    = "global-pull-secret"
	dbusRestartUnitMode          = "replace"
	kubeletServiceUnit           = "kubelet.service"

	// Mounted secret file paths
	originalPullSecretFilePath = "/etc/original-pull-secret/.dockerconfigjson"
	globalPullSecretFilePath   = "/etc/global-pull-secret/.dockerconfigjson"

	tickerPace = 30 * time.Second

	// systemd job completion state as documented in go-systemd/dbus
	systemdJobDone = "done" // Job completed successfully
)

var (
	// writeFileFunc is a variable that holds the function used to write files.
	// This allows tests to inject custom write functions for testing rollback scenarios.
	writeFileFunc = os.WriteFile

	// readFileFunc is a variable that holds the function used to read files.
	// This allows tests to inject custom read functions for testing.
	readFileFunc = os.ReadFile
)

// NewRunCommand creates a new cobra.Command for the sync-global-pullsecret command
func NewRunCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync-global-pullsecret",
		Short: "Syncs a mixture between the user provided pull secret in DataPlane and the HostedCluster PullSecret to be deployed in the nodes of the HostedCluster",
		Long:  `Syncs a mixture between the user provided pull secret in DataPlane and the HostedCluster PullSecret to be deployed in the nodes of the HostedCluster. The resulting pull secret is deployed in a DaemonSet in the DataPlane that updates the kubelet.config.json file with the new pull secret. If there are conflicting entries in the resulting global pull secret, the user provided pull secret will prevail.`,
	}

	opts := syncGlobalPullSecretOptions{
		kubeletConfigJsonPath: defaultKubeletConfigJsonPath,
	}
	cmd.Flags().StringVar(&opts.globalPSSecretName, "global-pull-secret-name", defaultGlobalPSSecretName, "The name of the global pullSecret secret in the DataPlane.")
	cmd.Run = func(cmd *cobra.Command, args []string) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Handle SIGINT and SIGTERM
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sigChan
			cancel()
		}()

		if err := opts.run(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}

	return cmd
}

// run executes the main logic of the sync-global-pullsecret command
func (o *syncGlobalPullSecretOptions) run(ctx context.Context) error {
	// Setup logger using zap with logr interface
	config := zap.NewProductionConfig()
	config.EncoderConfig.TimeKey = "timestamp"
	config.EncoderConfig.EncodeTime = zapcore.RFC3339TimeEncoder
	zapLogger, err := config.Build()
	if err != nil {
		return fmt.Errorf("failed to create logger: %w", err)
	}
	logger := zapr.NewLogger(zapLogger)

	// Create syncer
	syncer := &GlobalPullSecretSyncer{
		kubeletConfigJsonPath: o.kubeletConfigJsonPath,
		log:                   logger,
	}

	// Start the sync loop
	return syncer.runSyncLoop(ctx)
}

// runSyncLoop runs the main synchronization loop with backoff
func (s *GlobalPullSecretSyncer) runSyncLoop(ctx context.Context) error {
	s.log.Info("Starting global pull secret sync loop")

	// Initial sync
	if err := s.syncPullSecret(); err != nil {
		s.log.Error(err, "Initial sync failed")
	}

	// Sync loop with backoff
	ticker := time.NewTicker(tickerPace)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.log.Info("Context canceled, stopping sync loop")
			return ctx.Err()
		case <-ticker.C:
			if err := s.syncPullSecret(); err != nil {
				s.log.Error(err, "Sync failed")
				// Continue the loop even if sync fails
			}
		}
	}
}

// syncPullSecret handles the synchronization logic for the GlobalPullSecret
func (s *GlobalPullSecretSyncer) syncPullSecret() error {
	s.log.Info("Syncing global pull secret")

	// Try to read the global pull secret from mounted file first
	globalPullSecretBytes, err := readPullSecretFromFile(globalPullSecretFilePath)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("failed to read global pull secret from file: %w", err)
		}
		// If global pull secret file doesn't exist, fall back to original pull secret
		s.log.Info("Global pull secret file not found, using original pull secret")
		originalPullSecretBytes, err := readPullSecretFromFile(originalPullSecretFilePath)
		if err != nil {
			return fmt.Errorf("failed to read original pull secret from file: %w", err)
		}
		globalPullSecretBytes = originalPullSecretBytes
	} else {
		s.log.Info("Global pull secret content found, using it")
	}

	if err := s.checkAndFixFile(globalPullSecretBytes); err != nil {
		return fmt.Errorf("failed to check and fix file: %w", err)
	}

	return nil
}

// checkAndFixFile reads the current file content and updates it if it differs from the desired content (global pull secret content).
func (s *GlobalPullSecretSyncer) checkAndFixFile(pullSecretBytes []byte) error {
	s.log.Info("Checking Kubelet's config.json file content")

	// Read existing content if file exists
	existingContent, err := os.ReadFile(s.kubeletConfigJsonPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read existing file: %w", err)
	}

	// If file content is different, write the desired content
	if string(existingContent) != string(pullSecretBytes) {
		s.log.Info("file content is different, updating it")
		// Save original content for potential rollback
		originalContent := existingContent

		// Write the new content
		if err := writeFileFunc(s.kubeletConfigJsonPath, pullSecretBytes, 0600); err != nil {
			return fmt.Errorf("failed to write file: %w", err)
		}
		s.log.Info("Pull secret updated", "file", s.kubeletConfigJsonPath)

		// Attempt to restart Kubelet with retries
		maxRetries := 3
		var lastErr error
		for attempt := 1; attempt <= maxRetries; attempt++ {
			if err := signalKubeletToRestartProcess(); err != nil {
				lastErr = err
				if attempt < maxRetries {
					s.log.Info(fmt.Sprintf("Attempt %d failed, retrying...: %v", attempt, err))
					time.Sleep(time.Duration(attempt) * time.Second)
					continue
				}
			} else {
				s.log.Info("Successfully restarted Kubelet", "attempt", attempt)
				return nil
			}
		}

		// If we reach this point, all retries failed - perform rollback
		s.log.Info("Failed to restart Kubelet after some attempts, executing rollback", "maxRetries", maxRetries, "error", lastErr)
		if err := writeFileFunc(s.kubeletConfigJsonPath, originalContent, 0600); err != nil {
			return fmt.Errorf("2 errors happened: the kubelet restart failed after %d attempts and it failed to rollback the file: %w", maxRetries, err)
		}
		return fmt.Errorf("failed to restart kubelet after %d attempts, rolled back changes: %w", maxRetries, lastErr)
	}

	return nil
}

// signalKubeletToRestartProcess signals Kubelet to reload the config by restarting the kubelet.service.
// This is done by sending a signal to systemd via dbus.
func signalKubeletToRestartProcess() error {
	conn, err := dbus.New()
	if err != nil {
		return fmt.Errorf("failed to connect to dbus: %w", err)
	}
	defer conn.Close()

	return restartKubelet(conn)
}

func restartKubelet(conn dbusConn) error {
	ch := make(chan string)
	if _, err := conn.RestartUnit(kubeletServiceUnit, dbusRestartUnitMode, ch); err != nil {
		return fmt.Errorf("failed to restart kubelet: %w", err)
	}

	// Wait for the result of the restart
	result := <-ch
	if result != systemdJobDone {
		return fmt.Errorf("failed to restart kubelet, result: %s", result)
	}

	return nil
}

// readPullSecretFromFile reads a pull secret from a mounted file path
func readPullSecretFromFile(filePath string) ([]byte, error) {
	content, err := readFileFunc(filePath)
	if err != nil {
		return nil, err
	}
	return content, nil
}
