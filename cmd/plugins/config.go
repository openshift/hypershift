package plugins

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v2"
)

// Config represents the plugin configuration
type Config struct {
	Plugins map[string]bool `yaml:"plugins"`
}

var config *Config

// getConfigPath returns the path to the config file (internal implementation)
var getConfigPath = func() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(homeDir, ".config", "hcp", "config.yaml")
}

// GetConfigPath returns the path to the plugin configuration file
func GetConfigPath() string {
	return getConfigPath()
}

// loadConfig loads the configuration from file (internal implementation)
func loadConfig() (*Config, error) {
	configPath := getConfigPath()

	// If config file doesn't exist, return default config (all plugins disabled)
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return &Config{
			Plugins: make(map[string]bool),
		}, nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	if cfg.Plugins == nil {
		cfg.Plugins = make(map[string]bool)
	}

	return &cfg, nil
}

// saveConfig saves the configuration to file
func saveConfig(cfg *Config) error {
	configPath := getConfigPath()

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// GetConfig returns the current configuration, loading it if necessary
func GetConfig() (*Config, error) {
	if config == nil {
		var err error
		config, err = loadConfig()
		if err != nil {
			return nil, err
		}
	}
	return config, nil
}

// IsPluginEnabled checks if a plugin is enabled in the configuration
func IsPluginEnabled(pluginName string) bool {
	cfg, err := GetConfig()
	if err != nil {
		// If we can't load config, default to disabled
		return false
	}

	// Default to disabled if not explicitly set
	enabled, exists := cfg.Plugins[pluginName]
	return exists && enabled
}

// SetPluginEnabled enables or disables a plugin and saves the configuration
func SetPluginEnabled(pluginName string, enabled bool) error {
	cfg, err := GetConfig()
	if err != nil {
		return err
	}

	cfg.Plugins[pluginName] = enabled
	config = cfg // Update cached config

	return saveConfig(cfg)
}

// LoadConfig loads the plugin configuration from file with default fallback
func LoadConfig() (*Config, error) {
	return loadConfig()
}
