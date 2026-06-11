package controllers

import (
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"
)

func TestWriteOSImageStreamCR(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		osStream string
	}{
		{
			name:     "When osStream is rhel-10 it should write correct OSImageStream CR",
			osStream: "rhel-10",
		},
		{
			name:     "When osStream is rhel-9 it should write correct OSImageStream CR",
			osStream: "rhel-9",
		},
	}

	// Test that empty osStream writes no file.
	t.Run("When osStream is empty it should not write any file", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		tmpDir, err := os.MkdirTemp("", "test-osimagestream-empty-*")
		g.Expect(err).NotTo(HaveOccurred())
		defer os.RemoveAll(tmpDir)

		err = writeOSImageStreamCR(tmpDir, "")
		g.Expect(err).NotTo(HaveOccurred())

		filePath := filepath.Join(tmpDir, "99_osimagestream.yaml")
		_, err = os.Stat(filePath)
		g.Expect(os.IsNotExist(err)).To(BeTrue(), "file should not exist when osStream is empty")
	})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			tmpDir, err := os.MkdirTemp("", "test-osimagestream-*")
			g.Expect(err).NotTo(HaveOccurred())
			defer os.RemoveAll(tmpDir)

			err = writeOSImageStreamCR(tmpDir, tt.osStream)
			g.Expect(err).NotTo(HaveOccurred())

			filePath := filepath.Join(tmpDir, "99_osimagestream.yaml")
			content, err := os.ReadFile(filePath)
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(string(content)).To(ContainSubstring("apiVersion: machineconfiguration.openshift.io/v1alpha1"))
			g.Expect(string(content)).To(ContainSubstring("kind: OSImageStream"))
			g.Expect(string(content)).To(ContainSubstring("name: cluster"))
			g.Expect(string(content)).To(ContainSubstring("defaultStream: " + tt.osStream))
		})
	}
}
