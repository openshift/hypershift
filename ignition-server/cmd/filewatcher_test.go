package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

func TestWatchSecretFiles(t *testing.T) {
	t.Run("When a watched file is modified it should cancel the context", func(t *testing.T) {
		dir := t.TempDir()
		secretFile := filepath.Join(dir, "tls.crt")
		if err := os.WriteFile(secretFile, []byte("initial"), 0600); err != nil {
			t.Fatalf("failed to write initial secret file: %v", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		if err := WatchSecretFiles(ctx, cancel, []string{secretFile}); err != nil {
			t.Fatalf("WatchSecretFiles returned error: %v", err)
		}

		// Give the watcher time to start.
		time.Sleep(100 * time.Millisecond)

		// Modify the file to trigger the watcher.
		if err := os.WriteFile(secretFile, []byte("updated"), 0600); err != nil {
			t.Fatalf("failed to write updated secret file: %v", err)
		}

		select {
		case <-ctx.Done():
			// Context was canceled as expected.
		case <-time.After(5 * time.Second):
			t.Fatal("expected context to be canceled after file change, but it was not")
		}
	})

	t.Run("When a new file is created in the watched directory it should cancel the context", func(t *testing.T) {
		dir := t.TempDir()
		secretFile := filepath.Join(dir, "tls.crt")
		if err := os.WriteFile(secretFile, []byte("initial"), 0600); err != nil {
			t.Fatalf("failed to write initial secret file: %v", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		if err := WatchSecretFiles(ctx, cancel, []string{secretFile}); err != nil {
			t.Fatalf("WatchSecretFiles returned error: %v", err)
		}

		// Give the watcher time to start.
		time.Sleep(100 * time.Millisecond)

		// Remove and recreate the file to simulate Kubernetes secret rotation.
		if err := os.Remove(secretFile); err != nil {
			t.Fatalf("failed to remove secret file: %v", err)
		}
		if err := os.WriteFile(secretFile, []byte("rotated"), 0600); err != nil {
			t.Fatalf("failed to recreate secret file: %v", err)
		}

		select {
		case <-ctx.Done():
			// Context was canceled as expected.
		case <-time.After(5 * time.Second):
			t.Fatal("expected context to be canceled after file recreation, but it was not")
		}
	})

	t.Run("When no file changes occur it should not cancel the context", func(t *testing.T) {
		dir := t.TempDir()
		secretFile := filepath.Join(dir, "tls.crt")
		if err := os.WriteFile(secretFile, []byte("initial"), 0600); err != nil {
			t.Fatalf("failed to write initial secret file: %v", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		if err := WatchSecretFiles(ctx, cancel, []string{secretFile}); err != nil {
			t.Fatalf("WatchSecretFiles returned error: %v", err)
		}

		// Wait a short period and verify the context is still active.
		time.Sleep(500 * time.Millisecond)

		select {
		case <-ctx.Done():
			t.Fatal("expected context to remain active when no file changes occur")
		default:
			// Context is still active as expected.
		}
	})

	t.Run("When the context is canceled externally it should clean up the watcher", func(t *testing.T) {
		dir := t.TempDir()
		secretFile := filepath.Join(dir, "tls.crt")
		if err := os.WriteFile(secretFile, []byte("initial"), 0600); err != nil {
			t.Fatalf("failed to write initial secret file: %v", err)
		}

		ctx, cancel := context.WithCancel(context.Background())

		if err := WatchSecretFiles(ctx, cancel, []string{secretFile}); err != nil {
			t.Fatalf("WatchSecretFiles returned error: %v", err)
		}

		// Cancel the context externally.
		cancel()

		// Give the watcher goroutine time to clean up.
		time.Sleep(200 * time.Millisecond)

		// If we reach here without hanging, the cleanup was successful.
	})

	t.Run("When multiple files are watched it should cancel on any change", func(t *testing.T) {
		dir := t.TempDir()
		certFile := filepath.Join(dir, "tls.crt")
		keyFile := filepath.Join(dir, "tls.key")
		if err := os.WriteFile(certFile, []byte("cert"), 0600); err != nil {
			t.Fatalf("failed to write cert file: %v", err)
		}
		if err := os.WriteFile(keyFile, []byte("key"), 0600); err != nil {
			t.Fatalf("failed to write key file: %v", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		if err := WatchSecretFiles(ctx, cancel, []string{certFile, keyFile}); err != nil {
			t.Fatalf("WatchSecretFiles returned error: %v", err)
		}

		// Give the watcher time to start.
		time.Sleep(100 * time.Millisecond)

		// Modify only the key file.
		if err := os.WriteFile(keyFile, []byte("new-key"), 0600); err != nil {
			t.Fatalf("failed to write updated key file: %v", err)
		}

		select {
		case <-ctx.Done():
			// Context was canceled as expected.
		case <-time.After(5 * time.Second):
			t.Fatal("expected context to be canceled after key file change, but it was not")
		}
	})

	t.Run("When a Kubernetes-style symlink swap occurs it should cancel the context", func(t *testing.T) {
		dir := t.TempDir()
		secretFile := filepath.Join(dir, "tls.crt")
		if err := os.WriteFile(secretFile, []byte("initial"), 0600); err != nil {
			t.Fatalf("failed to write initial secret file: %v", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		if err := WatchSecretFiles(ctx, cancel, []string{secretFile}); err != nil {
			t.Fatalf("WatchSecretFiles returned error: %v", err)
		}

		// Give the watcher time to start.
		time.Sleep(100 * time.Millisecond)

		// Simulate a Kubernetes-style ..data symlink swap.
		dataDir := filepath.Join(dir, "..data_new")
		if err := os.MkdirAll(dataDir, 0755); err != nil {
			t.Fatalf("failed to create data dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dataDir, "tls.crt"), []byte("rotated"), 0600); err != nil {
			t.Fatalf("failed to write to data dir: %v", err)
		}

		// Create a symlink that mimics the ..data swap.
		dataLink := filepath.Join(dir, "..data")
		if err := os.Symlink(dataDir, dataLink); err != nil {
			t.Fatalf("failed to create ..data symlink: %v", err)
		}

		select {
		case <-ctx.Done():
			// Context was canceled as expected.
		case <-time.After(5 * time.Second):
			t.Fatal("expected context to be canceled after ..data symlink swap, but it was not")
		}
	})
}

func TestIsRelevantEvent(t *testing.T) {
	watchedNames := map[string]struct{}{
		"tls.crt": {},
		"tls.key": {},
	}

	tests := []struct {
		name     string
		event    fsnotify.Event
		expected bool
	}{
		{
			name:     "When the event is for a watched file it should return true",
			event:    fsnotify.Event{Name: "/path/to/tls.crt", Op: fsnotify.Write},
			expected: true,
		},
		{
			name:     "When the event is for the other watched file it should return true",
			event:    fsnotify.Event{Name: "/path/to/tls.key", Op: fsnotify.Create},
			expected: true,
		},
		{
			name:     "When the event is for the Kubernetes ..data symlink it should return true",
			event:    fsnotify.Event{Name: "/path/to/..data", Op: fsnotify.Create},
			expected: true,
		},
		{
			name:     "When the event is for a Kubernetes timestamped data dir it should return true",
			event:    fsnotify.Event{Name: "/path/to/..2024_01_01_00_00_00.000000000", Op: fsnotify.Create},
			expected: true,
		},
		{
			name:     "When the event is for an unrelated file it should return false",
			event:    fsnotify.Event{Name: "/path/to/unrelated.txt", Op: fsnotify.Write},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isRelevantEvent(tt.event, watchedNames)
			if result != tt.expected {
				t.Errorf("isRelevantEvent(%v) = %v, want %v", tt.event, result, tt.expected)
			}
		})
	}
}
