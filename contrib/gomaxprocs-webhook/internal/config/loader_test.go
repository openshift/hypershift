package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/testr"
)

// testLoader wraps the loader for testing access to internal methods
type testLoader struct {
	*loader
}

func newTestLoader(configPath, defaultValue string, logger logr.Logger) *testLoader {
	l := NewConfigLoader(configPath, defaultValue, logger).(*loader)
	return &testLoader{loader: l}
}

func TestConfigLoader_FileSizeLimits(t *testing.T) {
	tests := []struct {
		name        string
		fileSize    int64
		expectError bool
	}{
		{
			name:        "small file within limit",
			fileSize:    1024, // 1KB
			expectError: false,
		},
		{
			name:        "large file exceeding limit",
			fileSize:    maxConfigFileSize + 1, // 1MB + 1 byte
			expectError: true,
		},
		{
			name:        "file at exact limit",
			fileSize:    maxConfigFileSize, // exactly 1MB
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary file with specified size
			tmpDir := t.TempDir()
			configPath := filepath.Join(tmpDir, "config.yaml")

			// Create file content of specified size (use valid YAML padding)
			baseYAML := "default: \"2\"\n"
			padding := strings.Repeat("# padding comment\n", int((tt.fileSize-int64(len(baseYAML)))/18)+1)
			content := baseYAML + padding
			// Trim to exact size
			if int64(len(content)) > tt.fileSize {
				content = content[:tt.fileSize]
			}

			if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
				t.Fatalf("failed to create test file: %v", err)
			}

			logger := testr.New(t)
			testLoader := newTestLoader(configPath, "default", logger)

			// Test file reading with size limit
			data, err := readFileWithLimit(configPath, maxConfigFileSize)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error for file size %d, but got nil", tt.fileSize)
				}
				if !strings.Contains(err.Error(), "too large") {
					t.Errorf("expected 'too large' error, got: %v", err)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error for file size %d: %v", tt.fileSize, err)
				}
				if int64(len(data)) != tt.fileSize {
					t.Errorf("expected data length %d, got %d", tt.fileSize, len(data))
				}
			}

			// Test config loading behavior
			cfg := testLoader.getConfig()
			if tt.expectError {
				if cfg != nil {
					t.Errorf("expected nil config for oversized file, got: %v", cfg)
				}
			} else {
				if cfg == nil {
					t.Errorf("expected valid config for file size %d, got nil", tt.fileSize)
				}
			}
		})
	}
}

func TestConfigLoader_DefaultFallbackCases(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T) string // returns config path
	}{
		{
			name: "When config file does not exist it should fall back to default",
			prepare: func(t *testing.T) string {
				return "/nonexistent/path/config.yaml"
			},
		},
		{
			name: "When config file is empty it should fall back to default",
			prepare: func(t *testing.T) string {
				tmpDir := t.TempDir()
				configPath := filepath.Join(tmpDir, "config.yaml")
				if err := os.WriteFile(configPath, []byte(""), 0644); err != nil {
					t.Fatalf("failed to create empty test file: %v", err)
				}
				return configPath
			},
		},
		{
			name: "When config file has invalid YAML it should fall back to default",
			prepare: func(t *testing.T) string {
				tmpDir := t.TempDir()
				configPath := filepath.Join(tmpDir, "config.yaml")
				invalidYAML := `
default: "2"
overrides:
  - workloadKind: "Deployment"
    workloadName: "test"
    containerName: "container"
    value: "4"
invalid_yaml_syntax: [unclosed bracket
`
				if err := os.WriteFile(configPath, []byte(invalidYAML), 0644); err != nil {
					t.Fatalf("failed to create test file: %v", err)
				}
				return configPath
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := testr.New(t)
			loader := NewConfigLoader(tt.prepare(t), "default-value", logger)

			value, excluded, ok := loader.Resolve(context.Background(), "Deployment", "test", "container")

			if excluded {
				t.Error("expected not excluded for default fallback case")
			}
			if !ok {
				t.Error("expected ok=true when falling back to default")
			}
			if value != "default-value" {
				t.Errorf("expected default value 'default-value', got '%s'", value)
			}
		})
	}
}

func TestConfigLoader_ValidConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	// Create file with valid YAML (avoid leading tabs which break YAML parsing)
	validYAML := strings.Join([]string{
		`default: "2"`,
		`overrides:`,
		`  - workloadKind: "Deployment"`,
		`    workloadName: "test-deployment"`,
		`    containerName: "app"`,
		`    value: "4"`,
		`  - workloadKind: "Deployment"`,
		`    workloadName: "wildcard-deployment"`,
		`    containerName: "*"`,
		`    value: "5"`,
		`  - workloadKind: "Deployment"`,
		`    workloadName: "wildcard-deployment"`,
		`    containerName: "specific"`,
		`    value: "6"`,
		`exclusions:`,
		`  - workloadKind: "StatefulSet"`,
		`    workloadName: "test-statefulset"`,
		`    containerName: "sidecar"`,
		`  - workloadKind: "StatefulSet"`,
		`    workloadName: "wildcard-statefulset"`,
		`    containerName: "*"`,
	}, "\n")
	if err := os.WriteFile(configPath, []byte(validYAML), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	logger := testr.New(t)
	loader := NewConfigLoader(configPath, "default-value", logger)

	tests := []struct {
		name          string
		workloadKind  string
		workloadName  string
		containerName string
		expectedValue string
		expectedExcl  bool
		expectedOk    bool
	}{
		{
			name:          "override match",
			workloadKind:  "Deployment",
			workloadName:  "test-deployment",
			containerName: "app",
			expectedValue: "4",
			expectedExcl:  false,
			expectedOk:    true,
		},
		{
			name:          "exclusion match",
			workloadKind:  "StatefulSet",
			workloadName:  "test-statefulset",
			containerName: "sidecar",
			expectedValue: "",
			expectedExcl:  true,
			expectedOk:    true,
		},
		{
			name:          "default fallback",
			workloadKind:  "DaemonSet",
			workloadName:  "other",
			containerName: "container",
			expectedValue: "2",
			expectedExcl:  false,
			expectedOk:    true,
		},
		{
			name:          "case insensitive workload kind",
			workloadKind:  "deployment", // lowercase
			workloadName:  "test-deployment",
			containerName: "app",
			expectedValue: "4",
			expectedExcl:  false,
			expectedOk:    true,
		},
		{
			name:          "wildcard override applies",
			workloadKind:  "Deployment",
			workloadName:  "wildcard-deployment",
			containerName: "anything",
			expectedValue: "5",
			expectedExcl:  false,
			expectedOk:    true,
		},
		{
			name:          "specific override beats wildcard",
			workloadKind:  "Deployment",
			workloadName:  "wildcard-deployment",
			containerName: "specific",
			expectedValue: "6",
			expectedExcl:  false,
			expectedOk:    true,
		},
		{
			name:          "wildcard exclusion applies",
			workloadKind:  "StatefulSet",
			workloadName:  "wildcard-statefulset",
			containerName: "whatever",
			expectedValue: "",
			expectedExcl:  true,
			expectedOk:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value, excluded, ok := loader.Resolve(context.Background(), tt.workloadKind, tt.workloadName, tt.containerName)

			if excluded != tt.expectedExcl {
				t.Errorf("expected excluded=%v, got %v", tt.expectedExcl, excluded)
			}
			if ok != tt.expectedOk {
				t.Errorf("expected ok=%v, got %v", tt.expectedOk, ok)
			}
			if value != tt.expectedValue {
				t.Errorf("expected value='%s', got '%s'", tt.expectedValue, value)
			}
		})
	}
}

func TestConfigLoader_ConfigCaching(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	// Create initial config
	initialYAML := `default: "2"`
	if err := os.WriteFile(configPath, []byte(initialYAML), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	logger := testr.New(t)
	testLoader := newTestLoader(configPath, "default-value", logger)

	// First load
	value1, _, ok1 := testLoader.Resolve(context.Background(), "Deployment", "test", "container")
	if !ok1 || value1 != "2" {
		t.Fatalf("first load failed: value=%s, ok=%v", value1, ok1)
	}

	// Should be cached - verify by checking load time
	firstLoadTime := testLoader.lastLoadTime.Load()
	if firstLoadTime == nil {
		t.Fatal("expected load time to be set")
	}

	// Second load immediately (should use cache due to throttling)
	value2, _, ok2 := testLoader.Resolve(context.Background(), "Deployment", "test", "container")
	if !ok2 || value2 != "2" {
		t.Fatalf("second load failed: value=%s, ok=%v", value2, ok2)
	}

	// Wait for throttle window to pass
	time.Sleep(1100 * time.Millisecond)

	// Update config file
	updatedYAML := `default: "4"`
	if err := os.WriteFile(configPath, []byte(updatedYAML), 0644); err != nil {
		t.Fatalf("failed to update test file: %v", err)
	}

	// Third load (should detect change and reload)
	value3, _, ok3 := testLoader.Resolve(context.Background(), "Deployment", "test", "container")
	if !ok3 || value3 != "4" {
		t.Fatalf("third load failed: value=%s, ok=%v", value3, ok3)
	}
}

func TestConfigLoader_ContentBasedCaching(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	// Create config
	configYAML := `default: "2"`
	if err := os.WriteFile(configPath, []byte(configYAML), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	logger := testr.New(t)
	testLoader := newTestLoader(configPath, "default-value", logger)

	// Load config
	testLoader.Resolve(context.Background(), "Deployment", "test", "container")

	// Wait for throttle window
	time.Sleep(1100 * time.Millisecond)

	// Write same content again (should not trigger re-parse)
	if err := os.WriteFile(configPath, []byte(configYAML), 0644); err != nil {
		t.Fatalf("failed to rewrite test file: %v", err)
	}

	// Get cached config before reload
	cachedConfig := testLoader.lastLoaded.Load()

	// Load again
	testLoader.Resolve(context.Background(), "Deployment", "test", "container")

	// Should be same config object (not re-parsed)
	newCachedConfig := testLoader.lastLoaded.Load()
	if cachedConfig != newCachedConfig {
		t.Error("expected same config object when content unchanged")
	}
}
