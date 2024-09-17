package filewatcher

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/fsnotify/fsnotify"
	ctrl "sigs.k8s.io/controller-runtime"
)

var (
	watchCertificateFileOnce sync.Once
	log                      = ctrl.Log.WithName("file-change-watcher")
)

// WatchFileForChanges watches the file, fileToWatch. If the file contents have changed, the pod this function is
// running on will be restarted.
func WatchFileForChanges(fileToWatch string) error {
	var err error

	// This starts only one occurrence of the file watcher, which watches the file, fileToWatch, for changes every interval.
	// In addition, it also captures an initial hash of the file contents to use to monitor the file for changes.
	watchCertificateFileOnce.Do(func() {
		log.Info("Starting the file change watcher...")

		// Update the file path to watch in case this is a symlink
		fileToWatch, err = filepath.EvalSymlinks(fileToWatch)
		if err != nil {
			return
		}
		log.Info("Watching file...", "file", fileToWatch)

		// Start the file watcher to monitor file changes
		go func() {
			err := checkForFileChanges(fileToWatch)
			if err != nil {
				log.Error(err, "Error checking for file changes")
			}
		}()
	})
	return err
}

// checkForFileChanges starts a new file watcher. If the file is changed, the pod running this function will exit.
func checkForFileChanges(path string) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if ok && (event.Has(fsnotify.Write) || event.Has(fsnotify.Chmod) || event.Has(fsnotify.Remove)) {
					log.Info("file was modified, exiting", "event name", event.Name, "event operation", event.Op)
					os.Exit(0)
				}
			case err, ok := <-watcher.Errors:
				if ok {
					log.Error(err, "file watcher error")
				}
			}
		}
	}()

	err = watcher.Add(path)
	if err != nil {
		return err
	}

	return nil
}
