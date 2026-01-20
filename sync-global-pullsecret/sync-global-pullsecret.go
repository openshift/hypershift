package syncglobalpullsecret

// sync-global-pullsecret syncs the pull secret from the user provided pull secret in DataPlane and appends it to the HostedCluster PullSecret to be deployed in the nodes of the HostedCluster.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/coreos/go-systemd/dbus"
	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"github.com/gofrs/flock"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// syncGlobalPullSecretOptions contains the configuration options for the sync-global-pullsecret command
type syncGlobalPullSecretOptions struct {
	kubeletConfigJsonPath string
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
	dbusRestartUnitMode          = "replace"
	kubeletServiceUnit           = "kubelet.service"

	// Mounted secret file paths
	originalPullSecretFilePath = "/etc/original-pull-secret/.dockerconfigjson"
	globalPullSecretFilePath   = "/etc/global-pull-secret/.dockerconfigjson"

	// Cache file path for tracking last-synced pull secret state
	// This allows us to distinguish between external auths (preserve) and removed HyperShift auths (delete)
	pullSecretCachePath = "/var/lib/kubelet/.hypershift-pullsecret-cache.json"

	tickerPace = 30 * time.Second

	// systemd job completion state as documented in go-systemd/dbus
	systemdJobDone = "done" // Job completed successfully

	// Values used for handling file locking
	lockTimeout   = 30 * time.Second
	retryInterval = 100 * time.Millisecond
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

// acquireFileLock acquires an exclusive lock on the file at the given path.
// It returns a *flock.Flock instance that should be used to release the lock when done.
func acquireFileLock(path string) (*flock.Flock, error) {
	fileLock := flock.New(path)

	// Try to acquire exclusive lock with timeout and retry
	deadline := time.Now().Add(lockTimeout)

	for time.Now().Before(deadline) {
		locked, err := fileLock.TryLock()
		if err != nil {
			return nil, fmt.Errorf("failed to acquire lock: %w", err)
		}
		if locked {
			return fileLock, nil
		}
		time.Sleep(retryInterval)
	}

	return nil, fmt.Errorf("timeout acquiring lock on %s after %v", path, lockTimeout)
}

// checkAndFixFile reads the current file content and updates it if it differs from the desired content (global pull secret content).
// This function merges auths from the existing on-disk file with the desired pull secret to preserve any external modifications.
// It uses file locking to prevent race conditions with external processes modifying the file.
func (s *GlobalPullSecretSyncer) checkAndFixFile(pullSecretBytes []byte) error {
	s.log.Info("Checking Kubelet's config.json file content")

	// Basic sanity check
	if err := validateDockerConfigJSON(pullSecretBytes); err != nil {
		return fmt.Errorf("invalid docker config.json content: %w", err)
	}

	// Parse the desired pull secret
	desiredConfig, err := parseDockerConfigJSON(pullSecretBytes)
	if err != nil {
		return fmt.Errorf("failed to parse desired pull secret: %w", err)
	}

	// Acquire exclusive lock on a stable lock file to prevent race conditions
	// We use a separate .lock file because writeAtomic replaces the target file via os.Rename,
	// which would invalidate a lock on the target path itself
	lockPath := s.kubeletConfigJsonPath + ".lock"
	fileLock, err := acquireFileLock(lockPath)
	if err != nil {
		return fmt.Errorf("failed to acquire file lock: %w", err)
	}
	defer func() {
		if err := fileLock.Unlock(); err != nil {
			s.log.Error(err, "Failed to release file lock")
		}
	}()

	// Read existing content while holding the lock
	existingContent, err := readFileFunc(s.kubeletConfigJsonPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read existing file: %w", err)
	}

	// Determine if we need to preserve trailing newline
	preserveNewline := len(existingContent) > 0 && existingContent[len(existingContent)-1] == '\n'

	// Read cached state to detect removed auths
	cachedConfig, err := readCachedPullSecret()
	if err != nil {
		s.log.Info("Failed to read cache, treating as first run", "error", err)
		cachedConfig = nil
	}

	// Merge with existing config if it exists
	var mergedConfig *dockerConfigJSON
	if len(existingContent) > 0 {
		existingConfig, err := parseDockerConfigJSON(existingContent)
		if err != nil {
			s.log.Info("Existing kubelet config corrupted - external auths will be lost",
				"file", s.kubeletConfigJsonPath,
				"error", err,
				"action", "using cluster-provided config only")
			mergedConfig = desiredConfig
		} else {
			// Merge configs, using cache to distinguish external auths from removed HyperShift auths
			mergedConfig = mergeDockerConfigs(existingConfig, desiredConfig, cachedConfig, s.log)
		}
	} else {
		// No existing file, use desired config
		mergedConfig = desiredConfig
	}

	// Marshal the merged config
	mergedBytes, err := json.Marshal(mergedConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal merged config: %w", err)
	}

	// Preserve trailing newline if it existed in the original file
	contentToWrite := mergedBytes
	if preserveNewline && (len(mergedBytes) == 0 || mergedBytes[len(mergedBytes)-1] != '\n') {
		contentToWrite = append(mergedBytes, '\n')
	}

	// If file content is different, write the merged content while still holding the lock
	if string(existingContent) != string(contentToWrite) {
		s.log.Info("file content is different, updating it")
		// Save original content for potential rollback
		originalContent := existingContent

		// Write the new content while holding the lock
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

				// Update cache with the merged config we just wrote
				// This allows us to detect removed auths on the next sync
				if err := writeCachedPullSecret(mergedConfig); err != nil {
					s.log.Info("Failed to update pull secret cache - auth removal detection may not work on next sync", "error", err)
					// Don't fail the sync for a cache write error
				}

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

// dockerConfigJSON represents the structure of a Docker config.json file
type dockerConfigJSON struct {
	Auths map[string]interface{} `json:"auths"`
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

// parseDockerConfigJSON parses a Docker config.json byte slice
func parseDockerConfigJSON(b []byte) (*dockerConfigJSON, error) {
	var config dockerConfigJSON
	if err := json.Unmarshal(b, &config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal docker config: %w", err)
	}
	if config.Auths == nil {
		config.Auths = make(map[string]interface{})
	}
	return &config, nil
}

// mergeDockerConfigs merges Docker configs with the following precedence rules:
// - Desired config auths always take precedence (HyperShift manages these registries)
// - Existing auths are preserved ONLY if they're truly external (not previously synced by HyperShift)
// - Auths that were previously synced by HyperShift but removed are NOT preserved (prevents auth leakage)
//
// The cached parameter contains what HyperShift last synced to the node. This allows us to:
// - Detect when HyperShift removes an auth (it was in cached, but not in desired)
// - Preserve truly external auths (not in cached, not in desired, but on disk)
func mergeDockerConfigs(existing, desired, cached *dockerConfigJSON, log logr.Logger) *dockerConfigJSON {
	merged := &dockerConfigJSON{
		Auths: make(map[string]interface{}),
	}

	// First, add all auths from the desired config (these always win)
	for registry, auth := range desired.Auths {
		merged.Auths[registry] = auth
	}

	// Then, add auths from existing config ONLY if they're truly external
	externalRegistries := make([]string, 0)
	removedRegistries := make([]string, 0)

	for registry, auth := range existing.Auths {
		if _, inDesired := desired.Auths[registry]; inDesired {
			// Already in desired, skip
			continue
		}

		// Check if this auth was previously synced by HyperShift
		if cached != nil {
			if _, wasCached := cached.Auths[registry]; wasCached {
				// This auth was from a previous HyperShift sync but is now removed
				// DO NOT preserve it - this prevents auth leakage when user removes from additional-pull-secret
				log.Info("Removing previously-synced auth that was deleted from cluster config", "registry", registry)
				removedRegistries = append(removedRegistries, registry)
				continue
			}
		}

		// This registry is truly external (never synced by HyperShift), preserve it
		log.Info("Preserving external auth from on-disk file", "registry", registry)
		merged.Auths[registry] = auth
		externalRegistries = append(externalRegistries, registry)
	}

	if len(externalRegistries) > 0 {
		log.Info("Preserved external auths from on-disk file", "registryCount", len(externalRegistries))
	}
	if len(removedRegistries) > 0 {
		log.Info("Removed previously-synced auths that were deleted from cluster config", "registryCount", len(removedRegistries))
	}

	return merged
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

// readCachedPullSecret reads the cached pull secret state from disk.
// This cache contains the last-synced pull secret content that HyperShift wrote to the node.
// Returns nil if the cache doesn't exist (first run or cache was deleted).
func readCachedPullSecret() (*dockerConfigJSON, error) {
	content, err := readFileFunc(pullSecretCachePath)
	if err != nil {
		if os.IsNotExist(err) {
			// Cache doesn't exist yet - this is normal on first run
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read cache file: %w", err)
	}

	if len(content) == 0 {
		return nil, nil
	}

	cached, err := parseDockerConfigJSON(content)
	if err != nil {
		// Cache is corrupted - treat as if it doesn't exist
		return nil, nil
	}

	return cached, nil
}

// writeCachedPullSecret writes the pull secret state to the cache file.
// This cache is used to track what HyperShift last synced to the node, allowing
// us to distinguish between external auths and removed HyperShift auths.
func writeCachedPullSecret(config *dockerConfigJSON) error {
	data, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal cache data: %w", err)
	}

	if err := writeFileFunc(pullSecretCachePath, data, 0600); err != nil {
		return fmt.Errorf("failed to write cache file: %w", err)
	}

	return nil
}
