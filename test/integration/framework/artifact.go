package framework

import (
	"fmt"
	"os"
	"path/filepath"
)

// Artifact opens relPath under the artifact dir, ensuring that owning directories exist.
// Closing the file is the responsibility of the caller.
func Artifact(opts *Options, relPath string) (*os.File, error) {
	filePath := filepath.Join(opts.ArtifactDir, relPath)
	base := filepath.Dir(filePath)
	if err := os.MkdirAll(base, 0777); err != nil {
		return nil, fmt.Errorf("couldn't ensure artifact directory: %w", err)
	}

	return os.Create(filePath)
}
