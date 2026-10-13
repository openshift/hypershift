package controllers

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	. "github.com/onsi/gomega"
)

func TestImageFileCache(t *testing.T) {
	g := NewGomegaWithT(t)

	var content string // image file content registryClientMock returns
	var called int     // number of times registryClientMock called
	var fail bool      // registryClientMock responds with failure

	regClientFuncMock := func(ctx context.Context, imageRef string, pullSecret []byte, imageFile string, out io.Writer) error {
		_, err := out.Write([]byte(content))
		if err != nil {
			return err
		}
		called++
		if fail {
			return errors.New("mocked failure")
		}
		return nil
	}

	cacheDir := t.TempDir()

	sut := &imageFileCache{
		cacheMap:  make(map[cacheKey]cacheValue),
		cacheDir:  cacheDir,
		regClient: regClientFuncMock,
		mutex:     sync.RWMutex{},
	}

	// first file should miss the cache
	content = "test1"
	response1, err1 := getImageFile(t, sut, "ref1", "dir/file1")
	g.Expect(err1).Should(Succeed())
	g.Expect(called).To(Equal(1)) // incremented
	g.Expect(response1).To(Equal(content))

	// next call should hit the cache
	response2, err2 := getImageFile(t, sut, "ref1", "dir/file1")
	g.Expect(err2).Should(Succeed())
	g.Expect(called).To(Equal(1)) // not incremented
	g.Expect(response2).To(Equal(content))

	// corrupted files should be re-downloaded
	simulateFileCorruption(t, cacheDir)
	response2bis, err2bis := getImageFile(t, sut, "ref1", "dir/file1")
	g.Expect(err2bis).Should(Succeed())
	g.Expect(called).To(Equal(2)) // incremented
	g.Expect(response2bis).To(Equal(content))

	// call with different imageRef should miss the cache
	content = "test2"
	response3, err3 := getImageFile(t, sut, "ref2", "dir/file1")
	g.Expect(err3).Should(Succeed())
	g.Expect(called).To(Equal(3)) // incremented
	g.Expect(response3).To(Equal(content))

	// next call to get the same image file should hit the cache
	response4, err4 := getImageFile(t, sut, "ref2", "dir/file1")
	g.Expect(err4).Should(Succeed())
	g.Expect(called).To(Equal(3)) // not incremented
	g.Expect(response4).To(Equal(content))

	// registry client failure should be propagated back to the caller
	fail = true
	_, err5 := getImageFile(t, sut, "ref2", "dir/file2")
	g.Expect(called).To(Equal(4)) // incremented
	g.Expect(err5).Should(HaveOccurred())
	g.Expect(err5.Error()).Should(ContainSubstring("mocked failure"))
	t.Log("failure message returned:", err5)
}

func TestDownloadImageErrorHashStripping(t *testing.T) {
	tests := []struct {
		name        string
		regErr      string
		wantContain string
		wantExclude string
	}{
		{
			name:        "When error contains a sha256 hash it should remove only the hash",
			regErr:      "unable to access the source layer sha256:a3ed95caeb02ffe68cdd9fd84406680ae93d633cb16422d00e8a7c22955b46d4: connection refused",
			wantContain: "unable to access the source layer: connection refused",
			wantExclude: "sha256:",
		},
		{
			name:        "When error contains multiple sha256 hashes it should remove all of them",
			regErr:      "layer sha256:a3ed95caeb02ffe68cdd9fd84406680ae93d633cb16422d00e8a7c22955b46d4 and sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb failed",
			wantContain: "layer and failed",
			wantExclude: "sha256:",
		},
		{
			name:        "When error contains no sha256 hash it should preserve the message unchanged",
			regErr:      "connection refused",
			wantContain: "connection refused",
		},
		{
			name:        "When error contains a short hex string after sha256 prefix it should not strip it",
			regErr:      "unexpected sha256:abcdef value",
			wantContain: "sha256:abcdef",
		},
		{
			name:        "When error contains sha256 with uppercase hex it should still strip it",
			regErr:      "layer sha256:A3ED95CAEB02FFE68CDD9FD84406680AE93D633CB16422D00E8A7C22955B46D4 gone",
			wantContain: "layer gone",
			wantExclude: "sha256:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			regClientFuncMock := func(ctx context.Context, imageRef string, pullSecret []byte, file string, out io.Writer) error {
				return errors.New(tt.regErr)
			}

			cacheDir := t.TempDir()
			_, err := downloadImageFile(t.Context(), regClientFuncMock, "quay.io/test/image:latest", []byte("pull-secret"), "usr/bin/test", cacheDir)

			g.Expect(err).To(HaveOccurred())
			g.Expect(err.Error()).To(ContainSubstring("failed to extract image file"))
			g.Expect(err.Error()).To(ContainSubstring(tt.wantContain))
			if tt.wantExclude != "" {
				g.Expect(err.Error()).NotTo(ContainSubstring(tt.wantExclude))
			}
		})
	}
}

func getImageFile(t *testing.T, sut *imageFileCache, imageRef string, imageFile string) (string, error) {
	var buff bytes.Buffer
	sutErr := sut.extractImageFile(t.Context(), imageRef, []byte("pull-secret"), imageFile, &buff)
	return buff.String(), sutErr
}

func simulateFileCorruption(t *testing.T, cacheDir string) {
	files, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatal("failed to open cache directory", err)
	}
	fileInfo := files[0] // only one file is supposed to be in the directory
	cachedFileName := filepath.Join(cacheDir, fileInfo.Name())
	t.Log("adding some garbage into file:", cachedFileName)
	cachedFile, err := os.OpenFile(cachedFileName, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal("failed to open cache file", err)
	}
	defer cachedFile.Close()
	if _, err := cachedFile.WriteString("世界"); err != nil {
		t.Fatal("failed to add data to a cache file", err)
	}
}
