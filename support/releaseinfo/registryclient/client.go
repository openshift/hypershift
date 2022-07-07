package registryclient

import (
	"bytes"
	"context"
	"fmt"
	"github.com/containers/common/pkg/config"
	"github.com/containers/image/v5/types"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"

	"github.com/containers/common/libimage"
	"github.com/containers/storage"
)

// ExtractImageFiles extracts a list of files from a registry image given the image reference, pull secret and the
// list of files to extract. It returns a map with file contents or an error.
func ExtractImageFiles(ctx context.Context, imageRef string, pullSecret []byte, files ...string) (map[string][]byte, error) {
	if len(files) == 0 {
		return nil, fmt.Errorf("empty list of files to extract")
	}
	fileContents := map[string][]byte{}

	mountedImage, err := mountImage(ctx, imageRef, pullSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to mount image %s %w", imageRef, err)
	}

	for _, file := range files {
		readFile, err := os.ReadFile(filepath.Join(mountedImage, file))
		if err != nil {
			return nil, err
		}
		fileContents[file] = readFile
	}

	return fileContents, nil
}

func ExtractImageFile(ctx context.Context, imageRef string, pullSecret []byte, file string, out io.Writer) error {
	mountedImage, err := mountImage(ctx, imageRef, pullSecret)
	if err != nil {
		return fmt.Errorf("failed to mount image %s %w", imageRef, err)
	}
	readFile, err := os.ReadFile(filepath.Join(mountedImage, file))
	if err != nil {
		return fmt.Errorf("failed to read file %s from the mounted image at %s %w", file, mountedImage, err)
	}
	if _, err = io.Copy(out, bytes.NewReader(readFile)); err != nil {
		return err
	}
	return nil
}

// mountImage mounts an image locally and return the mounted directory, otherwise returns an error.
func mountImage(ctx context.Context, imageRef string, pullSecret []byte) (string, error) {
	// write the pull secret to a temp file for now (better to volume mount it under the conventional place)
	pullSecretFile, err := os.CreateTemp("", "pull-secret")
	if err != nil {
		return "", err
	}
	_, err = pullSecretFile.Write(pullSecret)
	if err != nil {
		return "", err
	}
	storeOptions, err := storage.DefaultStoreOptionsAutoDetectUID()
	options := libimage.RuntimeOptions{SystemContext: &types.SystemContext{
		AuthFilePath: pullSecretFile.Name(),
	}}
	if err != nil {
		return "", fmt.Errorf("getting store options %w", err)
	}

	runtime, err := libimage.RuntimeFromStoreOptions(&options, &storeOptions)
	if err != nil {
		return "", fmt.Errorf("creating runtime %w", err)
	}

	pulledImage, err := runtime.Pull(context.Background(), imageRef, config.PullPolicyAlways, nil)
	if err != nil {
		return "", fmt.Errorf("pulling image %w", err)
	}
	for _, i := range pulledImage {
		var mountOpt []string
		mount, err := i.Mount(context.Background(), mountOpt, "")
		if err != nil {
			fmt.Printf("failed to mount %v", err)
			return "", err
		}
		return mount, nil
	}
	return "", fmt.Errorf("there are no pulled images for image %s", imageRef)
}

func ExtractImageFilesToDir(ctx context.Context, imageRef string, pullSecret []byte, pattern string, outputDir string) error {
	mountedImage, err := mountImage(ctx, imageRef, pullSecret)
	if err != nil {
		return fmt.Errorf("failed to mount image %s %w", imageRef, err)
	}

	compile, _ := regexp.Compile(filepath.Join(mountedImage, pattern))
	walk := filepath.WalkDir(mountedImage, func(path string, d fs.DirEntry, err1 error) error {
		if compile.Match([]byte(path)) {
			rel, err := filepath.Rel(mountedImage, path)
			if err != nil {
				return err
			}
			if d.IsDir() {
				os.MkdirAll(filepath.Join(outputDir, rel), 0755)
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			newFilePath := filepath.Join(outputDir, rel)
			err = os.WriteFile(newFilePath, data, 0755)
			if err != nil {
				return err
			}
		}
		return nil
	})
	return walk
}
