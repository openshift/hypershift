package syncglobalpullsecret

// sync-global-pullsecret syncs the pull secret from the user provided pull secret in DataPlane and appends it to the HostedCluster PullSecret to be deployed in the nodes of the HostedCluster.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
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
	ecrRegistries         string
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

	// ECR-specific fields
	ecrClient     ecrClient
	ecrRegistries []string
	ecrCredCache  *ecrCredentialCache
}

const (
	defaultKubeletConfigJsonPath = "/var/lib/kubelet/config.json"
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
		kubeletConfigJsonPath: defaultKubeletConfigJsonPath,
		ecrRegistries:         os.Getenv("ECR_REGISTRIES"),
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
		kubeletConfigJsonPath: o.kubeletConfigJsonPath,
		log:                   logger,
	}

	// Parse ECR registries from environment variable (comma-separated list)
	if o.ecrRegistries != "" {
		syncer.ecrRegistries = parseECRRegistries(o.ecrRegistries)
	}

	// Start the sync loop
	return syncer.runSyncLoop(ctx)
}

// runSyncLoop runs the main synchronization loop with backoff
func (s *GlobalPullSecretSyncer) runSyncLoop(ctx context.Context) error {
	s.log.Info("Starting global pull secret sync loop")

	// Initialize ECR if registries are configured
	if len(s.ecrRegistries) > 0 {
		if err := s.initializeECR(ctx); err != nil {
			s.log.Error(err, "Failed to initialize ECR, continuing without ECR credentials")
			s.ecrRegistries = nil
		}
	}

	// Initial sync
	if err := s.syncPullSecret(); err != nil {
		s.log.Error(err, "Initial sync failed")
	}

	// Sync loop with backoff
	ticker := time.NewTicker(tickerPace)
	defer ticker.Stop()

	// Add ECR refresh ticker
	var ecrTicker *time.Ticker
	if len(s.ecrRegistries) > 0 {
		ecrTicker = time.NewTicker(ecrRefreshInterval)
		defer ecrTicker.Stop()
	}

	for {
		select {
		case <-ctx.Done():
			s.log.Info("Context canceled, stopping sync loop")
			return nil
		case <-ticker.C:
			// Check if ECR credentials need refresh (based on expiration)
			if len(s.ecrRegistries) > 0 {
				if err := s.refreshECRCredentialsIfNeeded(ctx); err != nil {
					s.log.Error(err, "ECR credential refresh check failed")
				}
			}

			if err := s.syncPullSecret(); err != nil {
				s.log.Error(err, "Sync failed")
				// Continue the loop even if sync fails
			}
		case <-ecrTicker.C:
			// Periodic ECR refresh (every 6 hours)
			s.log.Info("Periodic ECR credential refresh")
			if _, err := s.fetchECRCredentials(ctx); err != nil {
				s.log.Error(err, "Periodic ECR credential refresh failed")
			}
		}
	}
}

// initializeECR sets up ECR client and fetches initial credentials
func (s *GlobalPullSecretSyncer) initializeECR(ctx context.Context) error {
	s.log.Info("Initializing ECR credential injection", "registryCount", len(s.ecrRegistries))

	// Initialize ECR client
	client, err := newECRClient(ctx, s.log)
	if err != nil {
		return fmt.Errorf("failed to create ECR client: %w", err)
	}
	s.ecrClient = client

	// Initialize credential cache
	s.ecrCredCache = &ecrCredentialCache{
		credentials: make(map[string]*cachedCredential),
	}

	// Fetch initial credentials
	_, err = s.fetchECRCredentials(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch initial ECR credentials: %w", err)
	}

	s.log.Info("ECR credential injection initialized successfully")
	return nil
}

// refreshECRCredentialsIfNeeded checks cache and refreshes if needed
func (s *GlobalPullSecretSyncer) refreshECRCredentialsIfNeeded(ctx context.Context) error {
	if len(s.ecrRegistries) == 0 || s.ecrCredCache == nil {
		return nil
	}

	needsRefresh := false

	// Check if any cached credentials are expiring soon
	s.ecrCredCache.mu.RLock()
	for registry, cred := range s.ecrCredCache.credentials {
		if !cred.isValid() {
			s.log.Info("ECR credential needs refresh", "registry", registry)
			needsRefresh = true
			break
		}
	}
	s.ecrCredCache.mu.RUnlock()

	// Also refresh if cache is empty
	if len(s.ecrCredCache.credentials) == 0 {
		needsRefresh = true
	}

	if needsRefresh {
		_, err := s.fetchECRCredentials(ctx)
		if err != nil {
			// Log error but don't fail - use cached credentials if available
			s.log.Error(err, "Failed to refresh ECR credentials, using cached credentials if available")
			return err
		}
	}

	return nil
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

	// Merge ECR credentials if enabled
	finalPullSecretBytes := globalPullSecretBytes
	if len(s.ecrRegistries) > 0 {
		finalPullSecretBytes, err = s.buildDockerConfigWithECR(context.Background(), globalPullSecretBytes)
		if err != nil {
			// Log error but continue with original pull secret
			s.log.Error(err, "Failed to merge ECR credentials, using pull secret without ECR")
			finalPullSecretBytes = globalPullSecretBytes
		}
	}

	if err := s.checkAndFixFile(finalPullSecretBytes); err != nil {
		return fmt.Errorf("failed to check and fix file: %w", err)
	}

	return nil
}

// checkAndFixFile reads the current file content and updates it if it differs from the desired content (global pull secret content).
func (s *GlobalPullSecretSyncer) checkAndFixFile(pullSecretBytes []byte) error {
	s.log.Info("Checking Kubelet's config.json file content")

	// Basic sanity check
	if err := validateDockerConfigJSON(pullSecretBytes); err != nil {
		return fmt.Errorf("invalid docker config.json content: %w", err)
	}

	// Read existing content if file exists
	existingContent, err := readFileFunc(s.kubeletConfigJsonPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read existing file: %w", err)
	}

	// Preserve trailing newline if it exists in the original file
	contentToWrite := pullSecretBytes
	if len(existingContent) > 0 && existingContent[len(existingContent)-1] == '\n' {
		if len(pullSecretBytes) == 0 || pullSecretBytes[len(pullSecretBytes)-1] != '\n' {
			contentToWrite = append(pullSecretBytes, '\n')
		}
	}

	// If file content is different, write the desired content
	if string(existingContent) != string(contentToWrite) {
		s.log.Info("file content is different, updating it")
		// Save original content for potential rollback
		originalContent := existingContent

		// Write the new content
		if err := writeFileFunc(s.kubeletConfigJsonPath, contentToWrite, 0600); err != nil {
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

// parseECRRegistries parses a comma-separated list of ECR registries
func parseECRRegistries(registries string) []string {
	if registries == "" {
		return nil
	}

	// Split by comma and trim whitespace
	var result []string
	for _, registry := range strings.Split(registries, ",") {
		registry = strings.TrimSpace(registry)
		if registry != "" {
			result = append(result, registry)
		}
	}

	return result
}
