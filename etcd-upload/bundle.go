package etcdupload

import (
	"archive/tar"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type secretData struct {
	Data map[string]string `json:"data"`
}

func bundleArchive(snapshotPath, secretsDir string) (string, error) {
	tmpFile, err := os.CreateTemp("", "etcd-backup-*.tar.gz")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	defer tmpFile.Close()

	gw := gzip.NewWriter(tmpFile)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()

	if err := addFileToArchive(tw, snapshotPath, "snapshot.db"); err != nil {
		os.Remove(tmpFile.Name())
		return "", err
	}

	entries, err := os.ReadDir(secretsDir)
	if err != nil {
		os.Remove(tmpFile.Name())
		return "", fmt.Errorf("failed to read secrets directory: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		jsonBytes, err := serializeSecretDir(filepath.Join(secretsDir, entry.Name()))
		if err != nil {
			os.Remove(tmpFile.Name())
			return "", fmt.Errorf("failed to serialize secret %s: %w", entry.Name(), err)
		}
		archiveName := "secrets/" + entry.Name() + ".json"
		hdr := &tar.Header{
			Name: archiveName,
			Mode: 0644,
			Size: int64(len(jsonBytes)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			os.Remove(tmpFile.Name())
			return "", fmt.Errorf("failed to write tar header for %s: %w", archiveName, err)
		}
		if _, err := tw.Write(jsonBytes); err != nil {
			os.Remove(tmpFile.Name())
			return "", fmt.Errorf("failed to write tar data for %s: %w", archiveName, err)
		}
	}

	return tmpFile.Name(), nil
}

func addFileToArchive(tw *tar.Writer, filePath, archiveName string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open %s: %w", filePath, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat %s: %w", filePath, err)
	}

	hdr := &tar.Header{
		Name: archiveName,
		Mode: 0644,
		Size: info.Size(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("failed to write tar header for %s: %w", archiveName, err)
	}
	if _, err := io.Copy(tw, f); err != nil {
		return fmt.Errorf("failed to write tar data for %s: %w", archiveName, err)
	}
	return nil
}

func serializeSecretDir(dirPath string) ([]byte, error) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory %s: %w", dirPath, err)
	}

	data := make(map[string]string)
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), "..") {
			continue
		}
		content, err := os.ReadFile(filepath.Join(dirPath, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("failed to read %s: %w", entry.Name(), err)
		}
		data[entry.Name()] = base64.StdEncoding.EncodeToString(content)
	}

	return json.Marshal(secretData{Data: data})
}
