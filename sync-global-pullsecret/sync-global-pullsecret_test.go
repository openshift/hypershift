package syncglobalpullsecret

import (
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
			name:               "file exists with different content - auths are merged",
			description:        "file exists with different registry, kubelet restart fails, rollback succeeds with merged content",
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
			name:               "merge auths from existing and desired - external auth preserved",
			description:        "file has external auth, desired has different auth, both should be in merged result",
			initialContent:     `{"auths":{"external.registry.com":{"auth":"ZXh0ZXJuYWw="}}}`,
			secretContent:      `{"auths":{"hypershift.registry.com":{"auth":"aHlwZXJzaGlmdA=="}}}`,
			rollbackShouldFail: false,
			expectedErrorContains: []string{
				"failed to restart kubelet after 3 attempts",
				"rolled back changes",
			},
			expectedFinalContent: `{"auths":{"external.registry.com":{"auth":"ZXh0ZXJuYWw="}}}`,
			expectError:          true,
		},
		{
			name:               "desired auth takes precedence over existing auth for same registry",
			description:        "both configs have auth for same registry, desired should win to allow HyperShift updates",
			initialContent:     `{"auths":{"shared.registry.com":{"auth":"ZXhpc3Rpbmc="}}}`,
			secretContent:      `{"auths":{"shared.registry.com":{"auth":"bmV3"}}}`,
			rollbackShouldFail: false,
			expectedErrorContains: []string{
				"failed to restart kubelet after 3 attempts",
				"rolled back changes",
			},
			expectedFinalContent: `{"auths":{"shared.registry.com":{"auth":"ZXhpc3Rpbmc="}}}`,
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
			name:               "rollback succeeds with merged content",
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
			name:               "merge reads multiple auths from disk and combines with desired",
			description:        "file has 2 external auths, desired has 2 HyperShift auths, all 4 should be in final merged result verifying disk read",
			initialContent:     `{"auths":{"external1.com":{"auth":"ZXh0MQ=="},"external2.com":{"auth":"ZXh0Mg=="}}}`,
			secretContent:      `{"auths":{"hypershift1.io":{"auth":"aHlwZXIx"},"hypershift2.io":{"auth":"aHlwZXIy"}}}`,
			rollbackShouldFail: true,
			expectedErrorContains: []string{
				"2 errors happened",
				"the kubelet restart failed after 3 attempts",
				"it failed to rollback the file",
			},
			expectedFinalContent: `{"auths":{"external1.com":{"auth":"ZXh0MQ=="},"external2.com":{"auth":"ZXh0Mg=="},"hypershift1.io":{"auth":"aHlwZXIx"},"hypershift2.io":{"auth":"aHlwZXIy"}}}`,
			expectError:          true,
		},
		{
			name:               "desired auth overwrites same registry from disk",
			description:        "file has auth for registry.io, desired has different auth for same registry, desired wins proving disk is read but not blindly preserved",
			initialContent:     `{"auths":{"registry.io":{"auth":"b2xkVmFsdWU="}}}`,
			secretContent:      `{"auths":{"registry.io":{"auth":"bmV3VmFsdWU="}}}`,
			rollbackShouldFail: true,
			expectedErrorContains: []string{
				"2 errors happened",
				"the kubelet restart failed after 3 attempts",
				"it failed to rollback the file",
			},
			expectedFinalContent: `{"auths":{"registry.io":{"auth":"bmV3VmFsdWU="}}}`,
			expectError:          true,
		},
		{
			name:               "preserve trailing newline when original file has one - with merge",
			description:        "file has trailing newline and different auth, merged content should preserve newline",
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
			name:               "preserve single newline when both have newlines - with merge",
			description:        "both original file and new content have trailing newlines, merged content should preserve single newline",
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
			name:               "no newline when original file has none - with merge",
			description:        "original file has no newline, merged content should have no newline",
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
			name:               "no newlines preserved - with merge",
			description:        "neither original file nor new content have newlines, merged content should have no newline",
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
		{
			name:               "multiple external auths preserved",
			description:        "file has multiple external auths, all should be preserved in merge",
			initialContent:     `{"auths":{"external1.com":{"auth":"ZXh0MQ=="},"external2.com":{"auth":"ZXh0Mg=="}}}`,
			secretContent:      `{"auths":{"hypershift.io":{"auth":"aHlwZXI="}}}`,
			rollbackShouldFail: false,
			expectedErrorContains: []string{
				"failed to restart kubelet after 3 attempts",
				"rolled back changes",
			},
			expectedFinalContent: `{"auths":{"external1.com":{"auth":"ZXh0MQ=="},"external2.com":{"auth":"ZXh0Mg=="}}}`,
			expectError:          true,
		},
		{
			name:               "corrupted config with potential external auths logs clear warning",
			description:        "existing file is corrupted (could have had external auths), logs warning about loss",
			initialContent:     `{"auths":{"external.registry.com":{"auth":"corrupt`,
			secretContent:      `{"auths":{"hypershift.io":{"auth":"aHlwZXJzaGlmdA=="}}}`,
			rollbackShouldFail: true,
			expectedErrorContains: []string{
				"2 errors happened",
				"the kubelet restart failed after 3 attempts",
				"it failed to rollback the file",
			},
			expectedFinalContent: `{"auths":{"hypershift.io":{"auth":"aHlwZXJzaGlmdA=="}}}`,
			expectError:          true,
			// Note: This scenario demonstrates the enhanced error logging:
			// When existing config is corrupted (malformed JSON), the code:
			// 1. Logs: "Existing kubelet config corrupted - external auths will be lost"
			//    with file path, error details, and action taken
			// 2. Falls back to cluster-provided config only (external auths are lost)
			// 3. Continues operation (doesn't fail the sync)
			// This warning is critical because external systems may have added auths
			// that are now being discarded due to file corruption.
		},
		{
			name:               "invalid existing config falls back to desired and logs warning",
			description:        "existing file has invalid JSON, should use desired config only and log that external auths will be lost",
			initialContent:     `{"auths":{"invalid json`,
			secretContent:      `{"auths":{"test.registry.com":{"auth":"dGVzdDp0ZXN0"}}}`,
			rollbackShouldFail: false,
			expectedErrorContains: []string{
				"failed to restart kubelet after 3 attempts",
				"rolled back changes",
			},
			expectedFinalContent: `{"auths":{"invalid json`,
			expectError:          true,
		},
		{
			name:               "complex merge with multiple overlapping and non-overlapping auths",
			description:        "both configs have multiple auths, some overlap, desired wins conflicts but external auth preserved",
			initialContent:     `{"auths":{"shared.io":{"auth":"b2xk"},"external.io":{"auth":"ZXh0"}}}`,
			secretContent:      `{"auths":{"shared.io":{"auth":"bmV3"},"hypershift.io":{"auth":"aHlwZXI="}}}`,
			rollbackShouldFail: false,
			expectedErrorContains: []string{
				"failed to restart kubelet after 3 attempts",
				"rolled back changes",
			},
			expectedFinalContent: `{"auths":{"shared.io":{"auth":"b2xk"},"external.io":{"auth":"ZXh0"}}}`,
			expectError:          true,
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

func TestParseDockerConfigJSON(t *testing.T) {
	tests := []struct {
		name        string
		input       []byte
		expectError bool
		expectAuths map[string]interface{}
		description string
	}{
		{
			name:        "valid config with single auth",
			input:       []byte(`{"auths":{"registry.io":{"auth":"dGVzdA=="}}}`),
			expectError: false,
			expectAuths: map[string]interface{}{
				"registry.io": map[string]interface{}{"auth": "dGVzdA=="},
			},
			description: "should parse valid config with one auth",
		},
		{
			name:        "valid config with multiple auths",
			input:       []byte(`{"auths":{"reg1.io":{"auth":"YXV0aDE="},"reg2.io":{"auth":"YXV0aDI="}}}`),
			expectError: false,
			expectAuths: map[string]interface{}{
				"reg1.io": map[string]interface{}{"auth": "YXV0aDE="},
				"reg2.io": map[string]interface{}{"auth": "YXV0aDI="},
			},
			description: "should parse valid config with multiple auths",
		},
		{
			name:        "valid config with empty auths",
			input:       []byte(`{"auths":{}}`),
			expectError: false,
			expectAuths: map[string]interface{}{},
			description: "should parse valid config with empty auths object",
		},
		{
			name:        "invalid JSON",
			input:       []byte(`{"auths":{"invalid`),
			expectError: true,
			description: "should fail on invalid JSON",
		},
		{
			name:        "missing auths key defaults to empty",
			input:       []byte(`{}`),
			expectError: false,
			expectAuths: map[string]interface{}{},
			description: "should create empty auths map when key is missing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			config, err := parseDockerConfigJSON(tt.input)

			if tt.expectError {
				g.Expect(err).To(HaveOccurred(), tt.description)
			} else {
				g.Expect(err).To(BeNil(), tt.description)
				g.Expect(config).ToNot(BeNil())
				g.Expect(len(config.Auths)).To(Equal(len(tt.expectAuths)))
			}
		})
	}
}

func TestMergeDockerConfigs(t *testing.T) {
	tests := []struct {
		name            string
		existing        *dockerConfigJSON
		desired         *dockerConfigJSON
		expectedAuthLen int
		checkRegistry   string
		expectedAuth    interface{}
		description     string
	}{
		{
			name: "merge non-overlapping auths",
			existing: &dockerConfigJSON{
				Auths: map[string]interface{}{
					"external.io": map[string]interface{}{"auth": "ZXh0ZXJuYWw="},
				},
			},
			desired: &dockerConfigJSON{
				Auths: map[string]interface{}{
					"hypershift.io": map[string]interface{}{"auth": "aHlwZXJzaGlmdA=="},
				},
			},
			expectedAuthLen: 2,
			checkRegistry:   "external.io",
			expectedAuth:    map[string]interface{}{"auth": "ZXh0ZXJuYWw="},
			description:     "should contain both auths when there's no overlap",
		},
		{
			name: "desired auth wins on conflict",
			existing: &dockerConfigJSON{
				Auths: map[string]interface{}{
					"shared.io": map[string]interface{}{"auth": "ZXhpc3Rpbmc="},
				},
			},
			desired: &dockerConfigJSON{
				Auths: map[string]interface{}{
					"shared.io": map[string]interface{}{"auth": "bmV3"},
				},
			},
			expectedAuthLen: 1,
			checkRegistry:   "shared.io",
			expectedAuth:    map[string]interface{}{"auth": "bmV3"},
			description:     "desired auth should win when both have same registry to allow HyperShift updates",
		},
		{
			name: "merge with empty existing",
			existing: &dockerConfigJSON{
				Auths: map[string]interface{}{},
			},
			desired: &dockerConfigJSON{
				Auths: map[string]interface{}{
					"hypershift.io": map[string]interface{}{"auth": "aHlwZXJzaGlmdA=="},
				},
			},
			expectedAuthLen: 1,
			checkRegistry:   "hypershift.io",
			expectedAuth:    map[string]interface{}{"auth": "aHlwZXJzaGlmdA=="},
			description:     "should use desired when existing is empty",
		},
		{
			name: "merge with empty desired",
			existing: &dockerConfigJSON{
				Auths: map[string]interface{}{
					"external.io": map[string]interface{}{"auth": "ZXh0ZXJuYWw="},
				},
			},
			desired: &dockerConfigJSON{
				Auths: map[string]interface{}{},
			},
			expectedAuthLen: 1,
			checkRegistry:   "external.io",
			expectedAuth:    map[string]interface{}{"auth": "ZXh0ZXJuYWw="},
			description:     "should preserve existing when desired is empty",
		},
		{
			name: "complex merge with overlaps",
			existing: &dockerConfigJSON{
				Auths: map[string]interface{}{
					"shared.io":   map[string]interface{}{"auth": "b2xk"},
					"external.io": map[string]interface{}{"auth": "ZXh0"},
				},
			},
			desired: &dockerConfigJSON{
				Auths: map[string]interface{}{
					"shared.io":     map[string]interface{}{"auth": "bmV3"},
					"hypershift.io": map[string]interface{}{"auth": "aHlwZXI="},
				},
			},
			expectedAuthLen: 3,
			checkRegistry:   "shared.io",
			expectedAuth:    map[string]interface{}{"auth": "bmV3"},
			description:     "should merge all auths with desired winning conflicts and external auth preserved",
		},
		{
			name: "hypershift can update its own auth even if file was modified",
			existing: &dockerConfigJSON{
				Auths: map[string]interface{}{
					"hypershift.io": map[string]interface{}{"auth": "b2xkVmFsdWU="},
				},
			},
			desired: &dockerConfigJSON{
				Auths: map[string]interface{}{
					"hypershift.io": map[string]interface{}{"auth": "bmV3VmFsdWU="},
				},
			},
			expectedAuthLen: 1,
			checkRegistry:   "hypershift.io",
			expectedAuth:    map[string]interface{}{"auth": "bmV3VmFsdWU="},
			description:     "desired auth should always win for known registries to allow updates",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			merged := mergeDockerConfigs(tt.existing, tt.desired, logr.Discard())

			g.Expect(merged).ToNot(BeNil())
			g.Expect(len(merged.Auths)).To(Equal(tt.expectedAuthLen), tt.description)

			if tt.checkRegistry != "" {
				auth, exists := merged.Auths[tt.checkRegistry]
				g.Expect(exists).To(BeTrue(), "Registry %s should exist in merged config", tt.checkRegistry)
				g.Expect(auth).To(Equal(tt.expectedAuth))
			}
		})
	}
}
