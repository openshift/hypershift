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
