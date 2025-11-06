package plugins

import (
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"
)

func TestGetConfigPath(t *testing.T) {
	g := NewWithT(t)

	path := getConfigPath()
	homeDir, err := os.UserHomeDir()
	g.Expect(err).NotTo(HaveOccurred())

	expected := filepath.Join(homeDir, ".config", "hcp", "config.yaml")
	g.Expect(path).To(Equal(expected))
}

func TestLoadConfig_DefaultConfig(t *testing.T) {
	g := NewWithT(t)

	// Setup: use temporary directory
	tempDir := t.TempDir()

	// Mock the getConfigPath function to use temporary directory
	originalGetConfigPath := getConfigPath
	defer func() {
		getConfigPath = func() string { return originalGetConfigPath() }
	}()

	getConfigPath = func() string {
		return filepath.Join(tempDir, "config.yaml")
	}

	// Reset global config for the test
	config = nil

	cfg, err := loadConfig()
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(cfg).NotTo(BeNil())
	g.Expect(cfg.Plugins).NotTo(BeNil())
	g.Expect(cfg.Plugins).To(BeEmpty())
}

func TestLoadConfig_ExistingConfig(t *testing.T) {
	g := NewWithT(t)

	// Setup: create temporary configuration file
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")

	configContent := `plugins:
  oadp: true
  test-plugin: false
`
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	g.Expect(err).NotTo(HaveOccurred())

	// Mock the getConfigPath function
	originalGetConfigPath := getConfigPath
	defer func() {
		getConfigPath = func() string { return originalGetConfigPath() }
	}()

	getConfigPath = func() string { return configPath }

	// Reset global config
	config = nil

	cfg, err := loadConfig()
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(cfg).NotTo(BeNil())
	g.Expect(cfg.Plugins["oadp"]).To(BeTrue())
	g.Expect(cfg.Plugins["test-plugin"]).To(BeFalse())
}

func TestSaveConfig(t *testing.T) {
	g := NewWithT(t)

	// Setup: temporary directory
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")

	// Mock the getConfigPath function
	originalGetConfigPath := getConfigPath
	defer func() {
		getConfigPath = func() string { return originalGetConfigPath() }
	}()

	getConfigPath = func() string { return configPath }

	// Create test configuration
	cfg := &Config{
		Plugins: map[string]bool{
			"oadp":        true,
			"test-plugin": false,
		},
	}

	err := saveConfig(cfg)
	g.Expect(err).NotTo(HaveOccurred())

	// Verify that the file was created
	g.Expect(configPath).To(BeAnExistingFile())

	// Verify content
	content, err := os.ReadFile(configPath)
	g.Expect(err).NotTo(HaveOccurred())

	expectedContent := "plugins:\n  oadp: true\n  test-plugin: false\n"
	g.Expect(string(content)).To(Equal(expectedContent))
}

func TestIsPluginEnabled(t *testing.T) {
	tests := []struct {
		name       string
		config     map[string]bool
		pluginName string
		expected   bool
	}{
		{
			name:       "plugin enabled",
			config:     map[string]bool{"oadp": true},
			pluginName: "oadp",
			expected:   true,
		},
		{
			name:       "plugin disabled",
			config:     map[string]bool{"oadp": false},
			pluginName: "oadp",
			expected:   false,
		},
		{
			name:       "plugin not configured",
			config:     map[string]bool{},
			pluginName: "nonexistent",
			expected:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			// Setup: temporary directory
			tempDir := t.TempDir()
			configPath := filepath.Join(tempDir, "config.yaml")

			// Mock the getConfigPath function
			originalGetConfigPath := getConfigPath
			defer func() {
				getConfigPath = func() string { return originalGetConfigPath() }
			}()

			getConfigPath = func() string { return configPath }

			// Reset global config
			config = nil

			// Create configuration if there are plugins
			if len(tt.config) > 0 {
				cfg := &Config{Plugins: tt.config}
				err := saveConfig(cfg)
				g.Expect(err).NotTo(HaveOccurred())
			}

			result := IsPluginEnabled(tt.pluginName)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestSetPluginEnabled(t *testing.T) {
	g := NewWithT(t)

	// Setup: temporary directory
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")

	// Mock the getConfigPath function
	originalGetConfigPath := getConfigPath
	defer func() {
		getConfigPath = func() string { return originalGetConfigPath() }
	}()

	getConfigPath = func() string { return configPath }

	// Reset global config
	config = nil

	// Test: enable plugin
	err := SetPluginEnabled("oadp", true)
	g.Expect(err).NotTo(HaveOccurred())

	// Verify that it was saved
	g.Expect(IsPluginEnabled("oadp")).To(BeTrue())

	// Test: disable plugin
	err = SetPluginEnabled("oadp", false)
	g.Expect(err).NotTo(HaveOccurred())

	// Verify that it was saved
	g.Expect(IsPluginEnabled("oadp")).To(BeFalse())
}
