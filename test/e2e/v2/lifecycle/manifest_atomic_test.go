//go:build e2ev2

package lifecycle

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestWriteManifest(t *testing.T) {
	t.Run("When replacing an existing manifest, it should atomically preserve all new cleanup identities with private permissions", func(t *testing.T) {
		dir := t.TempDir()
		original := &ClusterManifest{Clusters: []ClusterEntry{{Name: "original", InfraID: "original-infra", Namespace: "clusters"}}}
		if err := WriteManifest(dir, original); err != nil {
			t.Fatal(err)
		}
		replacement := &ClusterManifest{Clusters: []ClusterEntry{
			{Name: "original", InfraID: "original-infra", Namespace: "clusters", ExpectedNodePools: map[string]int32{"original": 0}},
			{Name: "second", InfraID: "second-infra", Namespace: "clusters", DestroyArgs: []string{"--region=us-east-1"}},
		}}
		if err := WriteManifest(dir, replacement); err != nil {
			t.Fatal(err)
		}
		actual, err := ReadManifest(dir)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(actual, replacement) {
			t.Fatalf("cleanup identities changed: got %#v, want %#v", actual, replacement)
		}
		info, err := os.Stat(filepath.Join(dir, ManifestFileName))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("manifest permissions = %v, want 0600", info.Mode().Perm())
		}
		assertNoManifestTemporaryFiles(t, dir)
	})
	t.Run("When replacement validation fails, it should retain the prior readable manifest", func(t *testing.T) {
		dir := t.TempDir()
		original := &ClusterManifest{Clusters: []ClusterEntry{{Name: "original", InfraID: "original-infra", Namespace: "clusters"}}}
		if err := WriteManifest(dir, original); err != nil {
			t.Fatal(err)
		}
		invalid := &ClusterManifest{Clusters: []ClusterEntry{{ExpectedNodePools: map[string]int32{"pool": -1}}}}
		if err := WriteManifest(dir, invalid); err == nil {
			t.Fatal("expected invalid replacement to fail")
		}
		actual, err := ReadManifest(dir)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(actual, original) {
			t.Fatalf("prior cleanup identities changed: got %#v", actual)
		}
		assertNoManifestTemporaryFiles(t, dir)
	})
}

func TestWriteManifestAtomically(t *testing.T) {
	injected := errors.New("injected manifest failure")
	for _, test := range []struct {
		name  string
		stage string
		want  error
	}{
		{name: "When temporary creation fails, it should retain the prior manifest", stage: "create", want: injected},
		{name: "When a partial write fails, it should retain the prior manifest and remove the temporary file", stage: "write", want: injected},
		{name: "When a write is short without an error, it should reject replacement and remove the temporary file", stage: "short", want: io.ErrShortWrite},
		{name: "When sync fails, it should retain the prior manifest and remove the temporary file", stage: "sync", want: injected},
		{name: "When close fails, it should retain the prior manifest and remove the temporary file", stage: "close", want: injected},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			original := &ClusterManifest{Clusters: []ClusterEntry{{Name: "original", InfraID: "cleanup-identity", Namespace: "clusters"}}}
			if err := WriteManifest(dir, original); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, ManifestFileName)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			createTemp := func(dir, pattern string) (manifestTempFile, error) {
				if test.stage == "create" {
					return nil, injected
				}
				file, err := os.CreateTemp(dir, pattern)
				if err != nil {
					return nil, err
				}
				return &failingManifestFile{File: file, stage: test.stage, failure: injected}, nil
			}
			if err := writeManifestAtomically(path, []byte(`{"clusters":[]}`), createTemp); !errors.Is(err, test.want) {
				t.Fatalf("replacement error = %v, want %v", err, test.want)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("failed replacement changed the sole cleanup manifest")
			}
			actual, err := ReadManifest(dir)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actual, original) {
				t.Fatal("failed replacement changed cleanup identities")
			}
			assertNoManifestTemporaryFiles(t, dir)
		})
	}
	t.Run("When rename fails, it should retain the destination and remove the temporary file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, ManifestFileName)
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		if err := writeManifestAtomically(path, []byte("replacement"), newManifestTempFile); err == nil {
			t.Fatal("expected rename over a directory to fail")
		}
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			t.Fatalf("failed replacement changed destination: %v", err)
		}
		assertNoManifestTemporaryFiles(t, dir)
	})
}

func assertNoManifestTemporaryFiles(t *testing.T, dir string) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "."+ManifestFileName+"-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("temporary manifests remain: %v", files)
	}
}

type failingManifestFile struct {
	*os.File
	stage   string
	failure error
}

func (file *failingManifestFile) Write(data []byte) (int, error) {
	if file.stage == "write" || file.stage == "short" {
		written, err := file.File.Write(data[:len(data)/2])
		if err != nil {
			return written, err
		}
		if file.stage == "write" {
			return written, file.failure
		}
		return written, nil
	}
	return file.File.Write(data)
}

func (file *failingManifestFile) Sync() error {
	if file.stage == "sync" {
		return file.failure
	}
	return file.File.Sync()
}

func (file *failingManifestFile) Close() error {
	err := file.File.Close()
	if file.stage == "close" {
		return errors.Join(err, file.failure)
	}
	return err
}
