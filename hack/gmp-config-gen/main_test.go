package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateGeneratedPodMonitoring(t *testing.T) {
	t.Parallel()
	doc := &podMonitoringDoc{
		APIVersion: gmpAPIVersion,
		Kind:       "PodMonitoring",
		Metadata:   metadataDoc{Name: "test-component"},
		Spec:       specDoc{Selector: labelSelectorDoc{MatchLabels: map[string]string{"app": "test-component"}}},
	}
	source := "v2/assets/test-component/podmonitor.yaml"
	raw, err := marshalDoc(doc, source)
	require.NoError(t, err)

	t.Run("When the generated document matches the pinned API, it should accept it", func(t *testing.T) {
		require.NoError(t, validateGeneratedPodMonitoring(raw, source))
	})
	t.Run("When a generated field is absent from the pinned API, it should reject it and identify the source", func(t *testing.T) {
		invalid := bytes.Replace(raw, []byte("spec:\n"), []byte("spec:\n  unsupportedField: true\n"), 1)
		require.NotEqual(t, raw, invalid)

		err := validateGeneratedPodMonitoring(invalid, source)

		require.ErrorContains(t, err, source)
		require.ErrorContains(t, err, "does not match the GMP API")
		require.ErrorContains(t, err, `unknown field "unsupportedField"`)
	})
}

func TestVerifyResult(t *testing.T) {
	testCases := []struct {
		name string
		// setup writes the committed tree under dir and returns the generator
		// result to verify against it.
		setup       func(t *testing.T, dir string) *generationResult
		expectError []string
	}{
		{
			name: "When every committed manifest matches the generator output, it should succeed",
			setup: func(t *testing.T, dir string) *generationResult {
				path := filepath.Join(dir, "etcd", generatedPodMonitoring)
				writeTestFile(t, path, "current")
				return &generationResult{generated: map[string][]byte{path: []byte("current")}}
			},
		},
		{
			name: "When a committed manifest differs from the generator output, it should report it",
			setup: func(t *testing.T, dir string) *generationResult {
				path := filepath.Join(dir, "etcd", generatedPodMonitoring)
				writeTestFile(t, path, "old")
				return &generationResult{generated: map[string][]byte{path: []byte("new")}}
			},
			expectError: []string{"out of date", filepath.Join("etcd", generatedPodMonitoring), "make generate-gmp-config"},
		},
		{
			name: "When a generated manifest has not been committed, it should report it",
			setup: func(t *testing.T, dir string) *generationResult {
				path := filepath.Join(dir, "etcd", generatedPodMonitoring)
				return &generationResult{generated: map[string][]byte{path: []byte("new")}}
			},
			expectError: []string{"out of date", filepath.Join("etcd", generatedPodMonitoring)},
		},
		{
			name: "When a committed manifest no longer has a source, it should report it as stale",
			setup: func(t *testing.T, dir string) *generationResult {
				path := filepath.Join(dir, "removed", generatedPodMonitoring)
				writeTestFile(t, path, generatedHeader)
				return &generationResult{generated: map[string][]byte{}, stale: []string{path}}
			},
			expectError: []string{"out of date", filepath.Join("removed", generatedPodMonitoring)},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.setup(t, t.TempDir())

			err := verifyResult(result)

			if len(tc.expectError) == 0 {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			for _, substring := range tc.expectError {
				assert.Contains(t, err.Error(), substring)
			}
		})
	}
}

func TestWriteResult(t *testing.T) {
	t.Run("When the result has new, changed and stale manifests, it should write the generated files and remove the stale ones", func(t *testing.T) {
		dir := t.TempDir()
		created := filepath.Join(dir, "new-component", generatedPodMonitoring)
		changed := filepath.Join(dir, "etcd", generatedPodMonitoring)
		stale := filepath.Join(dir, "removed", generatedPodMonitoring)
		writeTestFile(t, changed, "old")
		writeTestFile(t, stale, generatedHeader)

		err := writeResult(&generationResult{
			generated: map[string][]byte{
				created: []byte("created"),
				changed: []byte("changed"),
			},
			stale: []string{stale},
		})

		require.NoError(t, err)
		assertFileContent(t, created, "created")
		assertFileContent(t, changed, "changed")
		assert.NoFileExists(t, stale)
		assert.NoFileExists(t, created+".tmp")
		assert.NoFileExists(t, changed+".tmp")
		require.NoError(t, verifyResult(&generationResult{generated: map[string][]byte{
			created: []byte("created"),
			changed: []byte("changed"),
		}}), "a written result should verify cleanly")
	})
	t.Run("When a stale manifest is already gone, it should return an error", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "removed", generatedPodMonitoring)

		err := writeResult(&generationResult{generated: map[string][]byte{}, stale: []string{missing}})

		require.ErrorContains(t, err, "failed to remove stale")
	})
}

func TestWriteFileAtomic(t *testing.T) {
	t.Run("When the target exists, it should replace its content and leave no temporary file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), generatedPodMonitoring)
		writeTestFile(t, path, "old")

		require.NoError(t, writeFileAtomic(path, []byte("new")))

		assertFileContent(t, path, "new")
		assert.NoFileExists(t, path+".tmp")
	})
	t.Run("When the rename fails, it should return an error and remove the temporary file", func(t *testing.T) {
		// A non-empty directory at the target path makes the rename fail after
		// the temporary file has been written.
		path := filepath.Join(t.TempDir(), generatedPodMonitoring)
		writeTestFile(t, filepath.Join(path, "occupied"), "")

		err := writeFileAtomic(path, []byte("new"))

		require.ErrorContains(t, err, "failed to write")
		assert.NoFileExists(t, path+".tmp")
	})
	t.Run("When the output directory does not exist, it should return an error", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "missing", generatedPodMonitoring)

		err := writeFileAtomic(path, []byte("new"))

		require.ErrorContains(t, err, "failed to write")
		assert.NoFileExists(t, path)
	})
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func assertFileContent(t *testing.T, path, expected string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, expected, string(raw))
}
