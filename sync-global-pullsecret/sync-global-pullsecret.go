package syncglobalpullsecret

// sync-global-pullsecret syncs the pull secret from the user provided pull secret in DataPlane and appends it to the HostedCluster PullSecret to be deployed in the nodes of the HostedCluster.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// syncGlobalPullSecretOptions contains the configuration options for the sync-global-pullsecret command
type syncGlobalPullSecretOptions struct {
	authDDropInPath string
}

// GlobalPullSecretSyncer handles the synchronization of pull secrets
type GlobalPullSecretSyncer struct {
	authDDropInPath string
	log             logr.Logger
}

const (
	defaultAuthDDropInPath = "/var/lib/kubelet/auth.d/global-pull-secret.json"

	// Mounted secret file paths
	originalPullSecretFilePath = "/etc/original-pull-secret/.dockerconfigjson"
	globalPullSecretFilePath   = "/etc/global-pull-secret/.dockerconfigjson"

	tickerPace = 30 * time.Second
)

var (
	// writeFileFunc is a variable that holds the function used to write files.
	// This allows tests to inject custom write functions for testing rollback scenarios.
	writeFileFunc = writeAtomic

	// readFileFunc is a variable that holds the function used to read files.
	// This allows tests to inject custom read functions for testing.
	readFileFunc = os.ReadFile
)

// NewRunCommand creates a new cobra.Command for the sync-global-pullsecret command
func NewRunCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync-global-pullsecret",
		Short: "Syncs a mixture between the user original pull secret in DataPlane and the HostedCluster PullSecret to be deployed in the nodes of the HostedCluster",
		Long:  `Syncs a mixture between the user original pull secret in DataPlane and the HostedCluster PullSecret to be deployed in the nodes of the HostedCluster. The resulting pull secret is deployed in a DaemonSet in the DataPlane that updates the kubelet.config.json file with the new pull secret. If there are conflicting entries in the resulting global pull secret, the original pull secret entries will prevail to ensure the well functioning of the nodes.`,
	}

	opts := syncGlobalPullSecretOptions{
		authDDropInPath: defaultAuthDDropInPath,
	}
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
		authDDropInPath: o.authDDropInPath,
		log:             logger,
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
			return nil
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
		if len(globalPullSecretBytes) == 0 {
			s.log.Info("Global pull secret file is empty, using original pull secret")
			originalPullSecretBytes, err := readPullSecretFromFile(originalPullSecretFilePath)
			if err != nil {
				return fmt.Errorf("failed to read original pull secret from file: %w", err)
			}
			globalPullSecretBytes = originalPullSecretBytes
		} else {
			s.log.Info("Global pull secret content found, using it")
		}
	}

	if err := s.checkAndFixFile(globalPullSecretBytes); err != nil {
		return fmt.Errorf("failed to check and fix file: %w", err)
	}

	return nil
}

// checkAndFixFile reads the current file content and updates it if it differs from the desired content (global pull secret content).
func (s *GlobalPullSecretSyncer) checkAndFixFile(pullSecretBytes []byte) error {
	s.log.Info("Checking auth.d drop-in file content")

	// Basic sanity check
	if err := validateDockerConfigJSON(pullSecretBytes); err != nil {
		return fmt.Errorf("invalid docker config.json content: %w", err)
	}

	// Read existing content if file exists
	existingContent, err := readFileFunc(s.authDDropInPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read existing file: %w", err)
	}

	contentToWrite := pullSecretBytes

	// Compare content ignoring trailing newlines
	existingTrimmed := bytes.TrimRight(existingContent, "\n")
	newTrimmed := bytes.TrimRight(contentToWrite, "\n")

	// If actual content differs (ignoring trailing newlines), update the file
	if !bytes.Equal(existingTrimmed, newTrimmed) {
		s.log.Info("file content is different, updating it")

		// Write the new content atomically
		if err := writeFileFunc(s.authDDropInPath, contentToWrite, 0600); err != nil {
			return fmt.Errorf("failed to write file: %w", err)
		}
		s.log.Info("Pull secret drop-in updated", "file", s.authDDropInPath)
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

func validateDockerConfigJSON(b []byte) error {
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	if _, ok := m["auths"]; !ok {
		return fmt.Errorf("missing 'auths' key")
	}
	return nil
}

func writeAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".config.json.tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(perm); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
