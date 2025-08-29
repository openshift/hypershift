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
	cachedFile, err := os.OpenFile(cachedFileName, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal("failed to open cache file", err)
	}
	defer func() {
		if closeErr := cachedFile.Close(); closeErr != nil {
			t.Logf("Failed to close cached file: %v", closeErr)
		}
	}()
	if _, err := cachedFile.WriteString("世界"); err != nil {
		t.Fatal("failed to add data to a cache file", err)
	}
}
