package cmd

import (
	"context"
	"log"
	"path/filepath"
	"strings"

	"github.com/fsnotify/fsnotify"
)

// WatchSecretFiles watches the parent directories of the given file paths for changes
// and cancels the provided context when a relevant change is detected. This is used
// to trigger a restart of the ignition-server when mounted secret data (e.g. TLS
// certificates) is updated by Kubernetes.
//
// Kubernetes secret mounts use atomic symlink swaps via a "..data" directory,
// so watching parent directories is necessary to detect these updates.
func WatchSecretFiles(ctx context.Context, cancel context.CancelFunc, filePaths []string) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	// Deduplicate parent directories to avoid watching the same directory multiple times.
	dirs := make(map[string]struct{})
	for _, p := range filePaths {
		dirs[filepath.Dir(p)] = struct{}{}
	}

	// Build a set of base file names for filtering events.
	watchedNames := make(map[string]struct{})
	for _, p := range filePaths {
		watchedNames[filepath.Base(p)] = struct{}{}
	}

	for dir := range dirs {
		if err := watcher.Add(dir); err != nil {
			// Close watcher before returning to avoid leaking resources.
			_ = watcher.Close()
			return err
		}
	}

	go func() {
		defer func() {
			if err := watcher.Close(); err != nil {
				log.Printf("error closing fsnotify watcher: %s", err)
			}
		}()

		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if isRelevantEvent(event, watchedNames) {
					log.Printf("Secret data changed (%s), canceling context to trigger restart", event)
					cancel()
					return
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Printf("fsnotify watcher error: %s", err)
			case <-ctx.Done():
				return
			}
		}
	}()

	return nil
}

// isRelevantEvent returns true if the fsnotify event corresponds to one of
// the watched file names or to a Kubernetes secret mount symlink swap
// (indicated by the "..data" directory being updated).
func isRelevantEvent(event fsnotify.Event, watchedNames map[string]struct{}) bool {
	baseName := filepath.Base(event.Name)

	// Kubernetes secret mounts update through atomic symlink swaps of
	// the "..data" directory. Detect this as a relevant change.
	if strings.HasPrefix(baseName, "..") {
		return true
	}

	_, relevant := watchedNames[baseName]
	return relevant
}
