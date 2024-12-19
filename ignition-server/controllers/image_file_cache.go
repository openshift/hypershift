package controllers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/openshift/hypershift/support/releaseinfo/registryclient"

	ctrl "sigs.k8s.io/controller-runtime"
)

var log = ctrl.Log.WithName("image-cache")

func NewImageFileCache(workDir string) (*imageFileCache, error) {
	cacheDir, err := os.MkdirTemp(workDir, "cache-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	return &imageFileCache{
		cacheMap:  make(map[cacheKey]cacheValue),
		cacheDir:  cacheDir,
		regClient: registryclient.ExtractImageFile,
		mutex:     sync.RWMutex{},
	}, nil
}

// Caching wrapper around the registry client function "regClient".
// All files returned by the function are cached as files in a sub-directory of "cacheDir".
// The cached files never expire, but can be automatically re-downloaded if they become corrupted.
type imageFileCache struct {
	cacheMap  map[cacheKey]cacheValue
	cacheDir  string
	regClient regClient // wrapped registryclient function
	mutex     sync.RWMutex
}

type checksum []byte
type regClient func(ctx context.Context, imageRef string, pullSecret []byte, file string, out io.Writer) error

type cacheKey struct {
	imageRef  string
	imageFile string
}

type cacheValue struct {
	fileName string
	fileSha  checksum
}

func (c *imageFileCache) extractImageFile(ctx context.Context, imageRef string, pullSecret []byte, imageFile string, out io.Writer) error {
	key := cacheKey{imageRef: imageRef, imageFile: imageFile}

	c.mutex.RLock()
	file, ok := c.cacheMap[key]
	c.mutex.RUnlock()

	if ok && cacheFileIsValid(file) {
		log.Info("retrieved cached file", "imageRef", imageRef, "file", imageFile)
		return returnCacheFile(file.fileName, out)
	}

	// Image file does not seem to be in the cache, begin critical section to add it...
	c.mutex.Lock()
	defer c.mutex.Unlock()

	// Another thread could have added the file to cache while we were waiting for exclusive lock, check again...
	if file, ok := c.cacheMap[key]; ok && cacheFileIsValid(file) {
		log.Info("retrieved cached file", "imageRef", imageRef, "file", imageFile)
		return returnCacheFile(file.fileName, out)
	}

	// No, image file really is not in the cache or checksum test failed - download from the registry...
	newFileName, err := downloadImageFile(ctx, c.regClient, imageRef, pullSecret, imageFile, c.cacheDir)
	if err != nil {
		return err
	}

	newFileSha, err := calcFileChecksum(newFileName)
	if err != nil {
		return err
	}

	c.cacheMap[key] = cacheValue{newFileName, newFileSha}
	log.Info("downloaded and cached file", "imageRef", imageRef, "file", imageFile)

	return returnCacheFile(newFileName, out)
}

func cacheFileIsValid(file cacheValue) bool {
	checksum, err := calcFileChecksum(file.fileName)
	if err != nil {
		log.Error(err, "failed to calculate checksum of cached file", "file", file.fileName)
		return false
	}

	if !bytes.Equal(checksum, file.fileSha) {
		log.Error(err, "cache file is corrupted", "file", file.fileName)
		return false
	}
	return true
}

func calcFileChecksum(file string) (checksum, error) {
	f, err := os.Open(file)
	if err != nil {
		log.Error(err, "failed to open file", "file", file)
		return nil, err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		log.Error(err, "failed to calculate checksum of file", "file", file)
		return nil, err
	}

	return h.Sum(nil), nil
}

func returnCacheFile(fullCacheFileName string, out io.Writer) error {
	file, err := os.Open(fullCacheFileName)
	if err != nil {
		return fmt.Errorf("failed to open cache file: %w", err)
	}
	defer file.Close()
	if _, err := io.Copy(out, file); err != nil {
		return fmt.Errorf("failed to copy cache file: %w", err)
	}
	return nil
}

func downloadImageFile(ctx context.Context, regClient regClient, imageRef string, pullSecret []byte, imageFile string, cacheDir string) (_ string, err error) {
	newFile, err := os.CreateTemp(cacheDir, filepath.Base(imageFile)+"-*")
	if err != nil {
		return "", fmt.Errorf("failed to create cache file: %w", err)
	}

	defer func() {
		closeErr := newFile.Close()
		if closeErr != nil && err == nil {
			err = fmt.Errorf("failed to close cache file: %w", closeErr)
		}
	}()

	if err = regClient(ctx, imageRef, pullSecret, imageFile, newFile); err != nil {
		return "", fmt.Errorf("failed to extract image file: %w", err)
	}

	return newFile.Name(), nil
}
