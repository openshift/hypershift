package syncglobalpullsecret

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"go.uber.org/mock/gomock"
)

func TestCheckAndFixFile(t *testing.T) {
	g := NewWithT(t)

	// Create a temporary directory for test files
	tempDir, err := os.MkdirTemp("", "sync-pullsecret-test-*")
	g.Expect(err).To(BeNil())
	defer os.RemoveAll(tempDir)

	// Create test file path
	testFilePath := filepath.Join(tempDir, "config.json")

	// Create test secret
	testSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pull-secret",
			Namespace: "kube-system",
		},
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: []byte(`{"auths":{"test.registry.com":{"auth":"dGVzdDp0ZXN0"}}}`),
		},
	}

	// Create fake client
	fakeClient := fake.NewClientBuilder().WithObjects(testSecret).Build()

	tests := []struct {
		name           string
		initialContent string
		wantContent    string
		wantErr        bool
		expectRollback bool
	}{
		{
			name:           "file does not exist",
			initialContent: "",
			wantContent:    `{"auths":{"test.registry.com":{"auth":"dGVzdDp0ZXN0"}}}`,
			wantErr:        true,
			expectRollback: true,
		},
		{
			name:           "file exists with different content",
			initialContent: `{"auths":{"old.registry.com":{"auth":"b2xkOnRlc3Q="}}}`,
			wantContent:    `{"auths":{"test.registry.com":{"auth":"dGVzdDp0ZXN0"}}}`,
			wantErr:        true,
			expectRollback: true,
		},
		{
			name:           "file exists with same content",
			initialContent: `{"auths":{"test.registry.com":{"auth":"dGVzdDp0ZXN0"}}}`,
			wantContent:    `{"auths":{"test.registry.com":{"auth":"dGVzdDp0ZXN0"}}}`,
			wantErr:        false,
			expectRollback: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			// Setup test file if initial content is provided
			if tt.initialContent != "" {
				err := os.WriteFile(testFilePath, []byte(tt.initialContent), 0600)
				g.Expect(err).To(BeNil())
			}

			// Create options
			opts := &syncGlobalPullSecretOptions{
				kubeletConfigJsonPath: testFilePath,
				globalPSSecretName:    "test-pull-secret",
			}

			// Run checkAndFixFile
			err := opts.checkAndFixFile(context.Background(), fakeClient)

			// Check error
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("failed to restart kubelet after 3 attempts"))

				// If rollback is expected, verify the file was rolled back
				if tt.expectRollback {
					content, err := os.ReadFile(testFilePath)
					g.Expect(err).To(BeNil())
					g.Expect(string(content)).To(Equal(tt.initialContent))
				}
				return
			}
			g.Expect(err).To(BeNil())

			// Check file content (only for successful cases)
			content, err := os.ReadFile(testFilePath)
			g.Expect(err).To(BeNil())
			g.Expect(string(content)).To(Equal(tt.wantContent))

			// Cleanup
			os.Remove(testFilePath)
		})
	}
}

func TestCheckAndFixFileWithRollback(t *testing.T) {
	g := NewWithT(t)

	// Create a temporary directory for test files
	tempDir, err := os.MkdirTemp("", "sync-pullsecret-test-*")
	g.Expect(err).To(BeNil())
	defer os.RemoveAll(tempDir)

	// Create test file path
	testFilePath := filepath.Join(tempDir, "config.json")

	// Create test secret
	testSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pull-secret",
			Namespace: "kube-system",
		},
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: []byte(`{"auths":{"test.registry.com":{"auth":"dGVzdDp0ZXN0"}}}`),
		},
	}

	// Create fake client
	fakeClient := fake.NewClientBuilder().WithObjects(testSecret).Build()

	// Test case where Kubelet restart fails and rollback should occur
	initialContent := `{"auths":{"old.registry.com":{"auth":"b2xkOnRlc3Q="}}}`
	err = os.WriteFile(testFilePath, []byte(initialContent), 0600)
	g.Expect(err).To(BeNil())

	opts := &syncGlobalPullSecretOptions{
		kubeletConfigJsonPath: testFilePath,
		globalPSSecretName:    "test-pull-secret",
	}

	// Mock the signalKubeletToRestartProcess function to always fail
	// We'll need to patch this function for testing
	// For now, we'll just verify that the function handles errors appropriately
	err = opts.checkAndFixFile(context.Background(), fakeClient)

	// The function should return an error due to Kubelet restart failure
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("failed to restart kubelet after 3 attempts"))

	// Verify that the file was rolled back to original content
	content, err := os.ReadFile(testFilePath)
	g.Expect(err).To(BeNil())
	g.Expect(string(content)).To(Equal(initialContent))
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
