package syncglobalpullsecret

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/go-logr/logr"
	"go.uber.org/mock/gomock"
)

func TestCheckAndFixFile(t *testing.T) {
	tests := []struct {
		name                  string
		initialContent        string
		secretContent         string
		rollbackShouldFail    bool
		expectedErrorContains []string
		expectedFinalContent  string
		expectError           bool
		description           string
	}{
		{
			name:               "file does not exist",
			description:        "file does not exist, kubelet restart fails, rollback succeeds",
			initialContent:     "",
			secretContent:      `{"auths":{"test.registry.com":{"auth":"dGVzdDp0ZXN0"}}}`,
			rollbackShouldFail: false,
			expectedErrorContains: []string{
				"failed to restart kubelet after 3 attempts",
				"rolled back changes",
			},
			expectedFinalContent: "",
			expectError:          true,
		},
		{
			name:               "file exists with different content",
			description:        "file exists with different content, kubelet restart fails, rollback succeeds",
			initialContent:     `{"auths":{"old.registry.com":{"auth":"b2xkOnRlc3Q="}}}`,
			secretContent:      `{"auths":{"test.registry.com":{"auth":"dGVzdDp0ZXN0"}}}`,
			rollbackShouldFail: false,
			expectedErrorContains: []string{
				"failed to restart kubelet after 3 attempts",
				"rolled back changes",
			},
			expectedFinalContent: `{"auths":{"old.registry.com":{"auth":"b2xkOnRlc3Q="}}}`,
			expectError:          true,
		},
		{
			name:                  "file exists with same content",
			description:           "file exists with same content, no changes needed",
			initialContent:        `{"auths":{"test.registry.com":{"auth":"dGVzdDp0ZXN0"}}}`,
			secretContent:         `{"auths":{"test.registry.com":{"auth":"dGVzdDp0ZXN0"}}}`,
			rollbackShouldFail:    false,
			expectedErrorContains: []string{},
			expectedFinalContent:  `{"auths":{"test.registry.com":{"auth":"dGVzdDp0ZXN0"}}}`,
			expectError:           false,
		},
		{
			name:               "rollback succeeds",
			description:        "kubelet restart fails but rollback succeeds, file should be restored to original content",
			initialContent:     `{"auths":{"old.registry.com":{"auth":"b2xkOnRlc3Q="}}}`,
			secretContent:      `{"auths":{"test.registry.com":{"auth":"dGVzdDp0ZXN0"}}}`,
			rollbackShouldFail: false,
			expectedErrorContains: []string{
				"failed to restart kubelet after 3 attempts",
				"rolled back changes",
			},
			expectedFinalContent: `{"auths":{"old.registry.com":{"auth":"b2xkOnRlc3Q="}}}`,
			expectError:          true,
		},
		{
			name:               "rollback fails",
			description:        "both kubelet restart and rollback fail, file should remain with new content",
			initialContent:     `{"auths":{"old.registry.com":{"auth":"b2xkOnRlc3Q="}}}`,
			secretContent:      `{"auths":{"test.registry.com":{"auth":"dGVzdDp0ZXN0"}}}`,
			rollbackShouldFail: true,
			expectedErrorContains: []string{
				"2 errors happened",
				"the kubelet restart failed after 3 attempts",
				"it failed to rollback the file",
			},
			expectedFinalContent: `{"auths":{"test.registry.com":{"auth":"dGVzdDp0ZXN0"}}}`,
			expectError:          true,
		},
		{
			name:               "preserve trailing newline when original file has one",
			description:        "file has trailing newline, new content doesn't, should preserve newline",
			initialContent:     "{\"auths\":{\"old.registry.com\":{\"auth\":\"b2xkOnRlc3Q=\"}}}\n",
			secretContent:      `{"auths":{"test.registry.com":{"auth":"dGVzdDp0ZXN0"}}}`,
			rollbackShouldFail: false,
			expectedErrorContains: []string{
				"failed to restart kubelet after 3 attempts",
				"rolled back changes",
			},
			expectedFinalContent: "{\"auths\":{\"old.registry.com\":{\"auth\":\"b2xkOnRlc3Q=\"}}}\n",
			expectError:          true,
		},
		{
			name:               "preserve single newline when both have newlines",
			description:        "both original file and new content have trailing newlines, should preserve single newline",
			initialContent:     "{\"auths\":{\"old.registry.com\":{\"auth\":\"b2xkOnRlc3Q=\"}}}\n",
			secretContent:      "{\"auths\":{\"test.registry.com\":{\"auth\":\"dGVzdDp0ZXN0\"}}}\n",
			rollbackShouldFail: false,
			expectedErrorContains: []string{
				"failed to restart kubelet after 3 attempts",
				"rolled back changes",
			},
			expectedFinalContent: "{\"auths\":{\"old.registry.com\":{\"auth\":\"b2xkOnRlc3Q=\"}}}\n",
			expectError:          true,
		},
		{
			name:               "no newline when original file has none",
			description:        "original file has no newline, new content has newline, should preserve new content format",
			initialContent:     `{"auths":{"old.registry.com":{"auth":"b2xkOnRlc3Q="}}}`,
			secretContent:      "{\"auths\":{\"test.registry.com\":{\"auth\":\"dGVzdDp0ZXN0\"}}}\n",
			rollbackShouldFail: false,
			expectedErrorContains: []string{
				"failed to restart kubelet after 3 attempts",
				"rolled back changes",
			},
			expectedFinalContent: `{"auths":{"old.registry.com":{"auth":"b2xkOnRlc3Q="}}}`,
			expectError:          true,
		},
		{
			name:               "no newlines preserved",
			description:        "neither original file nor new content have newlines, should preserve format",
			initialContent:     `{"auths":{"old.registry.com":{"auth":"b2xkOnRlc3Q="}}}`,
			secretContent:      `{"auths":{"test.registry.com":{"auth":"dGVzdDp0ZXN0"}}}`,
			rollbackShouldFail: false,
			expectedErrorContains: []string{
				"failed to restart kubelet after 3 attempts",
				"rolled back changes",
			},
			expectedFinalContent: `{"auths":{"old.registry.com":{"auth":"b2xkOnRlc3Q="}}}`,
			expectError:          true,
		},
		{
			name:                  "same content with newline - no change needed",
			description:           "file content is identical including newline, no restart should be attempted",
			initialContent:        "{\"auths\":{\"test.registry.com\":{\"auth\":\"dGVzdDp0ZXN0\"}}}\n",
			secretContent:         `{"auths":{"test.registry.com":{"auth":"dGVzdDp0ZXN0"}}}`,
			rollbackShouldFail:    false,
			expectedErrorContains: []string{},
			expectedFinalContent:  "{\"auths\":{\"test.registry.com\":{\"auth\":\"dGVzdDp0ZXN0\"}}}\n",
			expectError:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			// Create a temporary directory for test files
			tempDir, err := os.MkdirTemp("", "sync-pullsecret-test-*")
			g.Expect(err).To(BeNil())
			defer os.RemoveAll(tempDir)

			// Create test file path
			testFilePath := filepath.Join(tempDir, "config.json")

			// Write initial content if provided
			if tt.initialContent != "" {
				err = os.WriteFile(testFilePath, []byte(tt.initialContent), 0600)
				g.Expect(err).To(BeNil())
			}

			// Verify initial content if file exists
			if tt.initialContent != "" {
				content, err := os.ReadFile(testFilePath)
				g.Expect(err).To(BeNil())
				g.Expect(string(content)).To(Equal(tt.initialContent))
			}

			// Create syncer for testing
			syncer := &GlobalPullSecretSyncer{
				kubeletConfigJsonPath: testFilePath,
				log:                   logr.Discard(),
			}

			// Save original write function and restore it after test
			originalWriteFileFunc := writeFileFunc
			defer func() { writeFileFunc = originalWriteFileFunc }()

			// Set up custom write function for rollback failure scenario
			if tt.rollbackShouldFail {
				// Create a custom write function that fails only during rollback
				writeCount := 0
				writeFileFunc = func(filename string, data []byte, perm os.FileMode) error {
					writeCount++
					// First write (new content) succeeds, second write (rollback) fails
					if writeCount == 1 {
						return os.WriteFile(filename, data, perm)
					}
					return fmt.Errorf("simulated rollback write failure")
				}
			}

			// Run checkAndFixFile
			err = syncer.checkAndFixFile([]byte(tt.secretContent))

			// Check error expectations
			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
				for _, expectedError := range tt.expectedErrorContains {
					g.Expect(err.Error()).To(ContainSubstring(expectedError))
				}
			} else {
				g.Expect(err).To(BeNil())
			}

			// Verify final file content
			if tt.expectedFinalContent != "" {
				content, err := os.ReadFile(testFilePath)
				g.Expect(err).To(BeNil())
				g.Expect(string(content)).To(Equal(tt.expectedFinalContent))
			}
		})
	}
}

func TestRestartKubelet(t *testing.T) {
	tests := []struct {
		name          string
		setupMock     func(*MockdbusConn)
		expectedError string
		description   string
	}{
		{
			name: "Success",
			setupMock: func(mock *MockdbusConn) {
				mock.EXPECT().
					RestartUnit(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(name, mode string, ch chan<- string) (int, error) {
						go func() { ch <- "done" }()
						return 0, nil
					})
			},
			expectedError: "",
			description:   "systemd job completed successfully",
		},
		{
			name: "RestartUnit returns an error",
			setupMock: func(mock *MockdbusConn) {
				mock.EXPECT().
					RestartUnit(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(0, fmt.Errorf("dbus error"))
			},
			expectedError: "failed to restart kubelet: dbus error",
			description:   "dbus call itself failed",
		},
		{
			name: "Job failed",
			setupMock: func(mock *MockdbusConn) {
				mock.EXPECT().
					RestartUnit(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(name, mode string, ch chan<- string) (int, error) {
						go func() { ch <- "failed" }()
						return 0, nil
					})
			},
			expectedError: "failed to restart kubelet, result: failed",
			description:   "systemd job failed",
		},
		{
			name: "Job timeout",
			setupMock: func(mock *MockdbusConn) {
				mock.EXPECT().
					RestartUnit(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(name, mode string, ch chan<- string) (int, error) {
						go func() { ch <- "timeout" }()
						return 0, nil
					})
			},
			expectedError: "failed to restart kubelet, result: timeout",
			description:   "systemd job timed out",
		},
		{
			name: "Job canceled",
			setupMock: func(mock *MockdbusConn) {
				mock.EXPECT().
					RestartUnit(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(name, mode string, ch chan<- string) (int, error) {
						go func() { ch <- "canceled" }()
						return 0, nil
					})
			},
			expectedError: "failed to restart kubelet, result: canceled",
			description:   "systemd job was canceled",
		},
		{
			name: "Job dependency failed",
			setupMock: func(mock *MockdbusConn) {
				mock.EXPECT().
					RestartUnit(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(name, mode string, ch chan<- string) (int, error) {
						go func() { ch <- "dependency" }()
						return 0, nil
					})
			},
			expectedError: "failed to restart kubelet, result: dependency",
			description:   "systemd job dependency failed",
		},
		{
			name: "Job skipped",
			setupMock: func(mock *MockdbusConn) {
				mock.EXPECT().
					RestartUnit(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(name, mode string, ch chan<- string) (int, error) {
						go func() { ch <- "skipped" }()
						return 0, nil
					})
			},
			expectedError: "failed to restart kubelet, result: skipped",
			description:   "systemd job was skipped",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mock := NewMockdbusConn(ctrl)
			tt.setupMock(mock)

			err := restartKubelet(mock)
			if err != nil {
				if tt.expectedError == "" {
					t.Errorf("unexpected error: %v", err)
				} else if err.Error() != tt.expectedError {
					t.Errorf("expected error '%s', got '%s'", tt.expectedError, err.Error())
				}
			} else if tt.expectedError != "" {
				t.Errorf("expected error '%s', got nil", tt.expectedError)
			}
		})
	}
}

func TestValidateDockerConfigJSON(t *testing.T) {
	tests := []struct {
		name        string
		input       []byte
		expectError bool
		description string
	}{
		{
			name:        "valid docker config with single auth",
			input:       []byte(`{"auths":{"test.registry.com":{"auth":"dGVzdDp0ZXN0"}}}`),
			expectError: false,
			description: "valid JSON with auths key containing single registry",
		},
		{
			name:        "valid docker config with multiple auths",
			input:       []byte(`{"auths":{"registry1.com":{"auth":"dGVzdDp0ZXN0"},"registry2.com":{"auth":"YW5vdGhlcjphdXRo"}}}`),
			expectError: false,
			description: "valid JSON with auths key containing multiple registries",
		},
		{
			name:        "valid docker config with empty auths",
			input:       []byte(`{"auths":{}}`),
			expectError: false,
			description: "valid JSON with empty auths object",
		},
		{
			name:        "valid docker config with additional fields",
			input:       []byte(`{"auths":{"test.registry.com":{"auth":"dGVzdDp0ZXN0"}},"credsStore":"desktop","credHelpers":{"registry.com":"registry-helper"}}`),
			expectError: false,
			description: "valid JSON with auths key and additional docker config fields",
		},
		{
			name:        "invalid JSON - malformed",
			input:       []byte(`{"auths":{"test.registry.com":{"auth":"dGVzdDp0ZXN0"}`),
			expectError: true,
			description: "malformed JSON missing closing brace",
		},
		{
			name:        "invalid JSON - trailing comma",
			input:       []byte(`{"auths":{"test.registry.com":{"auth":"dGVzdDp0ZXN0"}},}`),
			expectError: true,
			description: "malformed JSON with trailing comma",
		},
		{
			name:        "invalid JSON - unquoted key",
			input:       []byte(`{auths:{"test.registry.com":{"auth":"dGVzdDp0ZXN0"}}}`),
			expectError: true,
			description: "malformed JSON with unquoted key",
		},
		{
			name:        "missing auths key",
			input:       []byte(`{"registries":{"test.registry.com":{"auth":"dGVzdDp0ZXN0"}}}`),
			expectError: true,
			description: "valid JSON but missing required auths key",
		},
		{
			name:        "empty input",
			input:       []byte(``),
			expectError: true,
			description: "empty byte slice should fail JSON parsing",
		},
		{
			name:        "null input",
			input:       []byte(`null`),
			expectError: true,
			description: "null JSON value should fail validation",
		},
		{
			name:        "string input",
			input:       []byte(`"some string"`),
			expectError: true,
			description: "string JSON value should fail validation",
		},
		{
			name:        "array input",
			input:       []byte(`[{"auths":{"test.registry.com":{"auth":"dGVzdDp0ZXN0"}}}]`),
			expectError: true,
			description: "array JSON value should fail validation",
		},
		{
			name:        "number input",
			input:       []byte(`123`),
			expectError: true,
			description: "number JSON value should fail validation",
		},
		{
			name:        "boolean input",
			input:       []byte(`true`),
			expectError: true,
			description: "boolean JSON value should fail validation",
		},
		{
			name:        "auths key with null value",
			input:       []byte(`{"auths":null}`),
			expectError: false,
			description: "auths key with null value should be valid (auths key exists)",
		},
		{
			name:        "auths key with string value",
			input:       []byte(`{"auths":"not an object"}`),
			expectError: false,
			description: "auths key with non-object value should be valid (auths key exists)",
		},
		{
			name:        "auths key with array value",
			input:       []byte(`{"auths":[]}`),
			expectError: false,
			description: "auths key with array value should be valid (auths key exists)",
		},
		{
			name:        "whitespace only",
			input:       []byte(`   `),
			expectError: true,
			description: "whitespace only input should fail JSON parsing",
		},
		{
			name:        "empty object",
			input:       []byte(`{}`),
			expectError: true,
			description: "empty object should fail validation (missing auths key)",
		},
		{
			name:        "nested auths key",
			input:       []byte(`{"config":{"auths":{"test.registry.com":{"auth":"dGVzdDp0ZXN0"}}}}`),
			expectError: true,
			description: "auths key nested inside another object should fail validation",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			err := validateDockerConfigJSON(tt.input)

			if tt.expectError {
				g.Expect(err).To(HaveOccurred(), "Expected error for test case: %s", tt.description)
			} else {
				g.Expect(err).To(BeNil(), "Expected no error for test case: %s, but got: %v", tt.description, err)
			}
		})
	}
}

func TestReadPreserveRegistriesList(t *testing.T) {
	tests := []struct {
		name           string
		fileContent    string
		fileExists     bool
		expectedResult []string
		expectError    bool
		description    string
	}{
		{
			name:           "file does not exist",
			fileExists:     false,
			expectedResult: nil,
			expectError:    false,
			description:    "should return nil when file doesn't exist",
		},
		{
			name:           "empty file",
			fileContent:    "",
			fileExists:     true,
			expectedResult: nil,
			expectError:    false,
			description:    "empty file should return empty list",
		},
		{
			name:           "single registry",
			fileContent:    "my-registry.example.com",
			fileExists:     true,
			expectedResult: []string{"my-registry.example.com"},
			expectError:    false,
			description:    "single registry on one line",
		},
		{
			name:           "multiple registries",
			fileContent:    "registry1.example.com\nregistry2.io\nregistry3.net/v2",
			fileExists:     true,
			expectedResult: []string{"registry1.example.com", "registry2.io", "registry3.net/v2"},
			expectError:    false,
			description:    "multiple registries on separate lines",
		},
		{
			name:           "with empty lines and whitespace",
			fileContent:    "  registry1.example.com  \n\n  \nregistry2.io\n",
			fileExists:     true,
			expectedResult: []string{"registry1.example.com", "registry2.io"},
			expectError:    false,
			description:    "should trim whitespace and skip empty lines",
		},
		{
			name:           "with comments",
			fileContent:    "# This is a comment\nregistry1.example.com\n# Another comment\nregistry2.io",
			fileExists:     true,
			expectedResult: []string{"registry1.example.com", "registry2.io"},
			expectError:    false,
			description:    "should skip lines starting with #",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			// Save original readFileFunc and restore after test
			originalReadFileFunc := readFileFunc
			defer func() { readFileFunc = originalReadFileFunc }()

			// Mock the readFileFunc
			readFileFunc = func(path string) ([]byte, error) {
				if !tt.fileExists {
					return nil, os.ErrNotExist
				}
				return []byte(tt.fileContent), nil
			}

			result, err := readPreserveRegistriesList()

			if tt.expectError {
				g.Expect(err).To(HaveOccurred(), tt.description)
			} else {
				g.Expect(err).NotTo(HaveOccurred(), tt.description)
				g.Expect(result).To(Equal(tt.expectedResult), tt.description)
			}
		})
	}
}

func TestExtractPreservedAuths(t *testing.T) {
	tests := []struct {
		name          string
		existingJSON  map[string]any
		registries    []string
		expectedAuths map[string]any
		expectError   bool
		description   string
	}{
		{
			name:          "empty existing content",
			existingJSON:  nil,
			registries:    []string{"registry1.com"},
			expectedAuths: nil,
			expectError:   false,
			description:   "should return nil when existing content is empty",
		},
		{
			name:          "empty registries list",
			existingJSON:  map[string]any{"auths": map[string]any{"registry1.com": map[string]any{"auth": "dGVzdA=="}}},
			registries:    []string{},
			expectedAuths: nil,
			expectError:   false,
			description:   "should return nil when registries list is empty",
		},
		{
			name:          "registry found in existing config",
			existingJSON:  map[string]any{"auths": map[string]any{"registry1.com": map[string]any{"auth": "dGVzdA=="}}},
			registries:    []string{"registry1.com"},
			expectedAuths: map[string]any{"registry1.com": map[string]any{"auth": "dGVzdA=="}},
			expectError:   false,
			description:   "should extract auth for matching registry",
		},
		{
			name: "multiple registries, some found",
			existingJSON: map[string]any{"auths": map[string]any{
				"registry1.com": map[string]any{"auth": "auth1"},
				"registry2.com": map[string]any{"auth": "auth2"},
			}},
			registries:    []string{"registry1.com", "registry3.com"},
			expectedAuths: map[string]any{"registry1.com": map[string]any{"auth": "auth1"}},
			expectError:   false,
			description:   "should only extract auths for registries in the list",
		},
		{
			name:          "registry not found in existing config",
			existingJSON:  map[string]any{"auths": map[string]any{"other-registry.com": map[string]any{"auth": "dGVzdA=="}}},
			registries:    []string{"registry1.com"},
			expectedAuths: map[string]any{},
			expectError:   false,
			description:   "should return empty map when no registries match",
		},
		{
			name:          "existing config without auths key",
			existingJSON:  map[string]any{"other": "data"},
			registries:    []string{"registry1.com"},
			expectedAuths: nil,
			expectError:   false,
			description:   "should return nil when auths key is missing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			var existingContent []byte
			if tt.existingJSON != nil {
				var err error
				existingContent, err = json.Marshal(tt.existingJSON)
				g.Expect(err).NotTo(HaveOccurred())
			}

			result, err := extractPreservedAuths(existingContent, tt.registries)

			if tt.expectError {
				g.Expect(err).To(HaveOccurred(), tt.description)
			} else {
				g.Expect(err).NotTo(HaveOccurred(), tt.description)
				g.Expect(result).To(Equal(tt.expectedAuths), tt.description)
			}
		})
	}
}

func TestMergeWithPreservedAuths(t *testing.T) {
	tests := []struct {
		name               string
		pullSecret         map[string]any
		preservedAuths     map[string]any
		originalPullSecret map[string]any
		expectedAuths      map[string]any
		expectError        bool
		description        string
	}{
		{
			name:           "no preserved auths",
			pullSecret:     map[string]any{"auths": map[string]any{"registry1.com": map[string]any{"auth": "ps1"}}},
			preservedAuths: nil,
			expectedAuths:  map[string]any{"registry1.com": map[string]any{"auth": "ps1"}},
			expectError:    false,
			description:    "should return original pull secret when no preserved auths",
		},
		{
			name:           "empty preserved auths",
			pullSecret:     map[string]any{"auths": map[string]any{"registry1.com": map[string]any{"auth": "ps1"}}},
			preservedAuths: map[string]any{},
			expectedAuths:  map[string]any{"registry1.com": map[string]any{"auth": "ps1"}},
			expectError:    false,
			description:    "should return original pull secret when preserved auths is empty",
		},
		{
			name:       "preserved auth added (not in pull secret)",
			pullSecret: map[string]any{"auths": map[string]any{"registry1.com": map[string]any{"auth": "ps1"}}},
			preservedAuths: map[string]any{
				"preserved-registry.com": map[string]any{"auth": "preserved"},
			},
			expectedAuths: map[string]any{
				"registry1.com":          map[string]any{"auth": "ps1"},
				"preserved-registry.com": map[string]any{"auth": "preserved"},
			},
			expectError: false,
			description: "should add preserved auth when not in pull secret",
		},
		{
			name:       "preserved auth overrides additional pull secret",
			pullSecret: map[string]any{"auths": map[string]any{"shared-registry.com": map[string]any{"auth": "additional"}}},
			preservedAuths: map[string]any{
				"shared-registry.com": map[string]any{"auth": "preserved"},
			},
			originalPullSecret: map[string]any{"auths": map[string]any{}},
			expectedAuths: map[string]any{
				"shared-registry.com": map[string]any{"auth": "preserved"},
			},
			expectError: false,
			description: "preserved auth should override additional pull secret",
		},
		{
			name:       "original HCP wins over preserved",
			pullSecret: map[string]any{"auths": map[string]any{"hcp-registry.com": map[string]any{"auth": "hcp"}}},
			preservedAuths: map[string]any{
				"hcp-registry.com": map[string]any{"auth": "preserved"},
			},
			originalPullSecret: map[string]any{"auths": map[string]any{"hcp-registry.com": map[string]any{"auth": "hcp"}}},
			expectedAuths: map[string]any{
				"hcp-registry.com": map[string]any{"auth": "hcp"},
			},
			expectError: false,
			description: "original HCP pull secret should win over preserved auth",
		},
		{
			name: "complex merge scenario",
			pullSecret: map[string]any{"auths": map[string]any{
				"hcp-registry.com":        map[string]any{"auth": "hcp"},
				"additional-registry.com": map[string]any{"auth": "additional"},
			}},
			preservedAuths: map[string]any{
				"hcp-registry.com":        map[string]any{"auth": "preserved-hcp"},
				"additional-registry.com": map[string]any{"auth": "preserved-additional"},
				"legacy-registry.com":     map[string]any{"auth": "legacy"},
			},
			originalPullSecret: map[string]any{"auths": map[string]any{"hcp-registry.com": map[string]any{"auth": "hcp"}}},
			expectedAuths: map[string]any{
				"hcp-registry.com":        map[string]any{"auth": "hcp"},                  // HCP wins
				"additional-registry.com": map[string]any{"auth": "preserved-additional"}, // preserved wins over additional
				"legacy-registry.com":     map[string]any{"auth": "legacy"},               // preserved added
			},
			expectError: false,
			description: "complex merge: HCP > preserved > additional",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			pullSecretBytes, err := json.Marshal(tt.pullSecret)
			g.Expect(err).NotTo(HaveOccurred())

			var originalBytes []byte
			if tt.originalPullSecret != nil {
				originalBytes, err = json.Marshal(tt.originalPullSecret)
				g.Expect(err).NotTo(HaveOccurred())
			}

			result, err := mergeWithPreservedAuths(pullSecretBytes, tt.preservedAuths, originalBytes)

			if tt.expectError {
				g.Expect(err).To(HaveOccurred(), tt.description)
			} else {
				g.Expect(err).NotTo(HaveOccurred(), tt.description)

				var resultJSON map[string]any
				err = json.Unmarshal(result, &resultJSON)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(resultJSON["auths"]).To(Equal(tt.expectedAuths), tt.description)
			}
		})
	}
}
