package config

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-logr/logr"
	"sigs.k8s.io/yaml"
)

const (
	// maxConfigFileSize limits configuration file size to prevent memory exhaustion attacks.
	// 1MB should be more than sufficient for any reasonable configuration.
	maxConfigFileSize = 1 * 1024 * 1024 // 1MB
)

// Loader fetches and parses configuration from a mounted file.
type Loader interface {
	// Resolve returns the effective value for a given workload and container, and
	// whether the container is excluded and should not be injected.
	Resolve(ctx context.Context, workloadKind, workloadName, containerName string) (value string, excluded bool, ok bool)
}

type loader struct {
	configPath     string
	defaultValue   string
	logger         logr.Logger
	lastLoadedYAML atomic.Value // string
	lastLoaded     atomic.Value // *Config
	lastLoadTime   atomic.Pointer[time.Time]
	lastModTime    atomic.Pointer[time.Time]
}

func NewConfigLoader(configPath, defaultValue string, logger logr.Logger) Loader {
	l := &loader{configPath: configPath, defaultValue: defaultValue, logger: logger.WithName("config-loader")}
	return l
}

func (l *loader) Resolve(ctx context.Context, workloadKind, workloadName, containerName string) (string, bool, bool) {
	cfg := l.getConfig()
	if cfg == nil {
		l.logger.Error(nil, "Configuration loading failed, using fallback", "defaultValue", l.defaultValue, "workloadKind", workloadKind, "workloadName", workloadName, "containerName", containerName)
		if l.defaultValue == "" {
			return "", false, false
		}
		return l.defaultValue, false, true
	}

	// Check exclusions first
	for i := range cfg.Exclusions {
		ex := cfg.Exclusions[i]
		if strings.EqualFold(ex.WorkloadKind, workloadKind) && ex.WorkloadName == workloadName && (ex.ContainerName == containerName || ex.ContainerName == "*") {
			return "", true, true
		}
	}

	// Match overrides (prefer exact container match over wildcard)
	for i := range cfg.Overrides {
		ov := cfg.Overrides[i]
		if strings.EqualFold(ov.WorkloadKind, workloadKind) && ov.WorkloadName == workloadName && ov.ContainerName == containerName {
			return ov.Value, false, true
		}
	}
	for i := range cfg.Overrides {
		ov := cfg.Overrides[i]
		if strings.EqualFold(ov.WorkloadKind, workloadKind) && ov.WorkloadName == workloadName && ov.ContainerName == "*" {
			return ov.Value, false, true
		}
	}

	if cfg.Default == "" {
		if l.defaultValue == "" {
			return "", false, false
		}
		return l.defaultValue, false, true
	}
	return cfg.Default, false, true
}

func (l *loader) getConfig() *Config {
	// Check if file exists
	if l.configPath == "" {
		// No config file specified, return empty config to use defaults
		return &Config{}
	}

	now := time.Now()

	// Check throttling first - if we're within the throttle window, return cached result
	if t := l.lastLoadTime.Load(); t != nil && now.Sub(*t) < time.Second {
		if v := l.lastLoaded.Load(); v != nil {
			return v.(*Config)
		}
	}

	// Read the file atomically with size limit to prevent memory exhaustion attacks
	raw, err := readFileWithLimit(l.configPath, maxConfigFileSize)
	if err != nil {
		if os.IsNotExist(err) {
			l.logger.V(1).Info("Configuration file not found, using defaults", "configPath", l.configPath)
			return &Config{}
		}
		l.logger.Error(err, "Failed to read configuration file", "configPath", l.configPath)
		return nil
	}

	rawString := string(raw)

	// Check if content has changed since last load
	if prev, _ := l.lastLoadedYAML.Load().(string); prev == rawString {
		// Content hasn't changed, update load time and return cached config
		l.logger.V(2).Info("Configuration unchanged, using cached version", "configPath", l.configPath)
		l.lastLoadTime.Store(&now)
		if v := l.lastLoaded.Load(); v != nil {
			return v.(*Config)
		}
		// Cached config was somehow lost, fall through to reparse
		l.logger.V(1).Info("Cached configuration lost, re-parsing", "configPath", l.configPath)
	}

	// Parse the YAML
	cfg := &Config{}
	if rawString != "" {
		// Compute non-sensitive identifiers for logging on error
		contentSize := len(raw)
		rawSHA256Sum := sha256.Sum256(raw)
		contentHash := fmt.Sprintf("%x", rawSHA256Sum)[:24]
		if err := yaml.Unmarshal(raw, cfg); err != nil {
			l.logger.Error(err, "Failed to parse configuration YAML", "configPath", l.configPath, "contentSize", contentSize, "contentHash", contentHash)
			return nil
		}
	}

	// Basic normalization
	for i := range cfg.Overrides {
		if cfg.Overrides[i].Value == "" {
			// inherit default when specified as empty; resolve later
			cfg.Overrides[i].Value = cfg.Default
		}
	}

	// Cache the results
	l.lastLoaded.Store(cfg)
	l.lastLoadedYAML.Store(rawString)
	l.lastLoadTime.Store(&now)

	l.logger.V(1).Info("Configuration loaded successfully", "configPath", l.configPath, "default", cfg.Default, "overrides", len(cfg.Overrides), "exclusions", len(cfg.Exclusions))
	// Note: We no longer store/check file modification time since we use content-based change detection
	return cfg
}

// readFileWithLimit reads a file with a size limit to prevent memory exhaustion attacks.
// It uses io.LimitReader to prevent reading beyond the specified limit.
func readFileWithLimit(filename string, limit int64) ([]byte, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	// Use LimitReader to prevent reading beyond the limit
	limitedReader := io.LimitReader(file, limit+1) // +1 to detect if file exceeds limit
	data, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, err
	}

	// Check if we read exactly limit+1 bytes, which means the file is too large
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("config file %s is too large (exceeds %d bytes), maximum allowed is %d bytes", filename, limit, limit)
	}

	return data, nil
}
