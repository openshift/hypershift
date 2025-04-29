//go:build e2e_batch
// +build e2e_batch

package e2e_exp

import (
	"context"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"k8s.io/klog/v2"
)

var (
	batchTotal  = flag.Int("e2e.batch-total", 0, "Total number of batches to split tests into (optional)")
	batchNumber = flag.Int("e2e.batch-number", 0, "Which batch number to run (1-based index, optional)")
	failureProb = flag.Float64("e2e.failure-probability", 0.0, "Probability of test failure (0.0 to 1.0, default 0 means all tests pass)")
)

// FailureProb returns the configured failure probability
func FailureProb() float64 {
	return *failureProb
}

func init() {
	rand.Seed(time.Now().UnixNano())
}

func TestMain(m *testing.M) {
	flag.Parse()
	os.Exit(main(m))
}

func main(m *testing.M) int {
	// Handle batch processing if enabled
	if *batchTotal > 0 {
		var (
			files   []string
			err     error
			pattern string
		)

		// Get current working directory
		wd, err := os.Getwd()
		if err != nil {
			klog.Error("failed to get working directory:", err)
			return -1
		}

		// Look for test files in e2e_exp directory
		pattern = filepath.Join(wd, "*_test.go")
		files, err = filepath.Glob(pattern)
		if err != nil {
			klog.Error("failed to list test files:", err)
			return -1
		}

		// Filter out e2e_test.go since it contains test setup
		var testFiles []string
		for _, f := range files {
			if filepath.Base(f) != "e2e_test.go" {
				testFiles = append(testFiles, f)
			}
		}

		if len(testFiles) == 0 {
			klog.Error("no test files found")
			return -1
		}

		if *batchNumber < 1 || *batchNumber > *batchTotal {
			klog.Error("batch number must be between 1 and total batches",
				"batchNumber", *batchNumber,
				"totalBatches", *batchTotal)
			return -1
		}

		// Sort files for consistent batching
		sort.Strings(testFiles)

		// Calculate which files belong to this batch
		filesPerBatch := (len(testFiles) + *batchTotal - 1) / *batchTotal // Round up division
		startIdx := (*batchNumber - 1) * filesPerBatch
		endIdx := startIdx + filesPerBatch
		if endIdx > len(testFiles) {
			endIdx = len(testFiles)
		}

		if startIdx >= len(testFiles) {
			klog.Error("batch number exceeds available test files",
				"batchNumber", *batchNumber,
				"totalBatches", *batchTotal)
			return -1
		}

		// Build test patterns for this batch
		var testPatterns []string
		for _, file := range testFiles[startIdx:endIdx] {
			base := filepath.Base(file)
			base = strings.TrimSuffix(base, "_test.go")
			testPatterns = append(testPatterns, fmt.Sprintf("Test%s", strings.Title(base)))
		}

		pattern = strings.Join(testPatterns, "|")
		flag.Set("test.run", pattern)

		klog.Info("Running test batch",
			"batchNumber", *batchNumber,
			"totalBatches", *batchTotal,
			"pattern", pattern,
			"files", testFiles[startIdx:endIdx])
	}

	// Handle cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-sigs:
			klog.Info("tests received shutdown signal and will be cancelled")
			cancel()
		case <-ctx.Done():
			return
		}
	}()

	return m.Run()
}
