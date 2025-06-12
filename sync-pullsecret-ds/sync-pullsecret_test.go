package syncpullsecret

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
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
	}{
		{
			name:           "file does not exist",
			initialContent: "",
			wantContent:    `{"auths":{"test.registry.com":{"auth":"dGVzdDp0ZXN0"}}}`,
			wantErr:        false,
		},
		{
			name:           "file exists with different content",
			initialContent: `{"auths":{"old.registry.com":{"auth":"b2xkOnRlc3Q="}}}`,
			wantContent:    `{"auths":{"test.registry.com":{"auth":"dGVzdDp0ZXN0"}}}`,
			wantErr:        false,
		},
		{
			name:           "file exists with same content",
			initialContent: `{"auths":{"test.registry.com":{"auth":"dGVzdDp0ZXN0"}}}`,
			wantContent:    `{"auths":{"test.registry.com":{"auth":"dGVzdDp0ZXN0"}}}`,
			wantErr:        false,
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
				return
			}
			g.Expect(err).To(BeNil())

			// Check file content
			content, err := os.ReadFile(testFilePath)
			g.Expect(err).To(BeNil())
			g.Expect(string(content)).To(Equal(tt.wantContent))

			// Cleanup
			os.Remove(testFilePath)
		})
	}
}
