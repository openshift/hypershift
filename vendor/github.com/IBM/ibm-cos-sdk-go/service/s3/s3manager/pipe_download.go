package s3manager

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/IBM/ibm-cos-sdk-go/aws"
	"github.com/IBM/ibm-cos-sdk-go/aws/awserr"
	"github.com/IBM/ibm-cos-sdk-go/service/s3"
	"golang.org/x/sys/unix"
)

// Error codes for pipe download operations
const (
	InvalidParameterError = "InvalidParameter"
	PipeOpenError         = "PipeOpenError"
)

// DownloadPipeInput provides the input parameters for downloading to a named pipe.
type DownloadPipeInput struct {
	// Required: Path to the named pipe (FIFO)
	PipePath *string

	// Required: Standard S3 GetObjectInput
	GetObjectInput *s3.GetObjectInput

	// Optional: Timeout for pipe write operations (default: 30s)
	PipeTimeout *int64 // in seconds

	// Optional: Maximum memory for buffering out-of-order parts
	//
	// If not provided, defaults to: Concurrency × PartSize × 2
	//
	// This controls how much RAM can be used to buffer downloaded parts that are
	// waiting to be written to the pipe in sequential order. The implementation
	// uses a worker pool pattern that limits active downloads to the Concurrency
	// setting, ensuring predictable memory usage.
	//
	// Parts download in parallel but must be written sequentially (1, 2, 3...).
	// When parts complete out of order (e.g., Part 5 before Part 1), they wait
	// in this buffer. Once Part 1 completes, all sequential parts are written
	// and their memory is freed.
	//
	// Formula: MaxBufferMemory = Concurrency × PartSize × 2
	// Example: 4 concurrent × 512MB parts × 2 = 4GB
	//
	// Guidelines for setting MaxBufferMemory:
	//   - Small files (<500MB):   1GB
	//   - Medium files (1-5GB):   2GB
	//   - Large files (5-20GB):   2-4GB
	//   - Very large (>20GB):     4-8GB
	//
	// The safety factor of 2 accounts for parts that have been downloaded but
	// not yet written to the pipe. Setting this too low causes "buffer memory
	// limit exceeded" errors.
	MaxBufferMemory *int64

	// Optional: Maximum number of retry attempts for failed part downloads (default: 3)
	// When a part download fails due to network errors or transient issues,
	// the SDK will automatically retry up to this many times with exponential backoff.
	// Set to 0 to disable retries (not recommended for production).
	MaxRetries *int

	// Optional: Progress callback
	ProgressFn func(bytesDownloaded, totalBytes int64)
}

var (
	ErrPipeWriteTimeout = errors.New("pipe write timeout")
)

// memoryManager manages memory reservations with backpressure
type memoryManager struct {
	maxMemory       int64
	availableMemory int64
	mu              sync.Mutex
	cond            *sync.Cond
}

func newMemoryManager(maxMemory int64) *memoryManager {
	mm := &memoryManager{
		maxMemory:       maxMemory,
		availableMemory: maxMemory,
	}
	mm.cond = sync.NewCond(&mm.mu)
	return mm
}

// Reserve blocks until the requested memory is available, then reserves it
func (mm *memoryManager) Reserve(size int64) {
	mm.mu.Lock()
	defer mm.mu.Unlock()

	// Wait until enough memory is available
	for mm.availableMemory < size {
		mm.cond.Wait()
	}

	mm.availableMemory -= size
}

// Release frees the reserved memory and signals waiting goroutines
func (mm *memoryManager) Release(size int64) {
	mm.mu.Lock()
	defer mm.mu.Unlock()

	mm.availableMemory += size
	// Broadcast to wake up all waiting goroutines
	mm.cond.Broadcast()
}

// orderedWriter ensures parts are written in sequential order
type orderedWriter struct {
	writer       io.Writer
	nextIndex    int64
	buffer       map[int64][]byte
	mu           sync.Mutex
	flushMu      sync.Mutex // serialises flushSequential across concurrent WritePart callers
	chunkSize    int
	writeTimeout time.Duration
	memoryMgr    *memoryManager
}

func newOrderedWriter(writer io.Writer, chunkSize int, writeTimeout time.Duration, memoryMgr *memoryManager) *orderedWriter {
	if chunkSize == 0 {
		chunkSize = 64 * 1024 // 64KB default
	}
	if writeTimeout == 0 {
		writeTimeout = 30 * time.Second
	}

	return &orderedWriter{
		writer:       writer,
		nextIndex:    1,
		buffer:       make(map[int64][]byte),
		chunkSize:    chunkSize,
		writeTimeout: writeTimeout,
		memoryMgr:    memoryMgr,
	}
}

func (ow *orderedWriter) WritePart(ctx context.Context, partNum int64, data []byte) error {
	ow.mu.Lock()
	ow.buffer[partNum] = data
	ow.mu.Unlock()

	return ow.flushSequential(ctx)
}

func (ow *orderedWriter) flushSequential(ctx context.Context) error {
	// flushMu serialises the entire flush loop — claim + write — so that
	// parts are written to the pipe in strict sequential order.
	//
	// Only one goroutine writes at a time: goroutine A holds flushMu while
	// writing part N; goroutine B waits at flushMu.Lock(). When A finishes
	// part N it checks for N+1 in the buffer. If N+1 is already there
	// (buffered by B while A was writing), A writes it immediately without
	// releasing flushMu — keeping the pipe writes strictly ordered.
	//
	// Memory release (memoryMgr.Release) happens OUTSIDE flushMu so that
	// workers blocked in memoryMgr.Reserve can unblock while the next part
	// is being written, preventing the backpressure deadlock.
	ow.flushMu.Lock()
	defer ow.flushMu.Unlock()

	for {
		ow.mu.Lock()
		data, exists := ow.buffer[ow.nextIndex]
		if !exists {
			ow.mu.Unlock()
			return nil
		}
		dataSize := int64(len(data))
		delete(ow.buffer, ow.nextIndex)
		ow.nextIndex++
		ow.mu.Unlock()

		// Write to pipe while holding flushMu (guarantees ordering) but
		// NOT holding ow.mu (allows other goroutines to buffer their parts).
		if err := ow.writeChunked(ctx, data); err != nil {
			return err
		}

		// Release memory outside flushMu so workers in Reserve can unblock
		// while we loop back to check for the next sequential part.
		ow.memoryMgr.Release(dataSize)
	}
}

func (ow *orderedWriter) writeChunked(ctx context.Context, data []byte) error {
	for i := 0; i < len(data); i += ow.chunkSize {
		end := min(i+ow.chunkSize, len(data))

		writeCtx, cancel := context.WithTimeout(ctx, ow.writeTimeout)

		errChan := make(chan error, 1)
		chunk := data[i:end] // Capture chunk for goroutine
		go func() {
			_, err := ow.writer.Write(chunk)
			errChan <- err
		}()

		select {
		case <-writeCtx.Done():
			cancel()
			// Note: The write goroutine may still be blocked on pipe write.
			// This is unavoidable with blocking I/O, but we return timeout error.
			// The goroutine will eventually complete when pipe is read or closed.
			return ErrPipeWriteTimeout
		case err := <-errChan:
			cancel()
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (ow *orderedWriter) Flush(ctx context.Context) error {
	ow.mu.Lock()
	defer ow.mu.Unlock()

	if len(ow.buffer) > 0 {
		return fmt.Errorf("parts remain unwritten in buffer")
	}
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// getPipeBufferSize detects the pipe buffer size
func getPipeBufferSize(pipe *os.File) int {
	const defaultSize = 64 * 1024

	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return defaultSize
	}

	const F_GETPIPE_SZ = 1032
	size, err := unix.FcntlInt(pipe.Fd(), F_GETPIPE_SZ, 0)
	if err != nil {
		return defaultSize
	}

	return size
}

// ObjectMetadata contains metadata about the S3 object to be downloaded
type ObjectMetadata struct {
	ContentLength int64
	PartsCount    int64
	PartSize      int64
}

// GetObjectMetadata retrieves metadata about the S3 object
func (d *Downloader) GetObjectMetadata(ctx aws.Context, input *s3.GetObjectInput) (*ObjectMetadata, error) {
	headInput := &s3.HeadObjectInput{
		Bucket:    input.Bucket,
		Key:       input.Key,
		VersionId: input.VersionId,
	}

	headOutput, err := d.S3.HeadObjectWithContext(ctx, headInput, d.RequestOptions...)
	if err != nil {
		return nil, err
	}

	metadata := &ObjectMetadata{
		ContentLength: aws.Int64Value(headOutput.ContentLength),
		PartsCount:    aws.Int64Value(headOutput.PartsCount),
	}

	if metadata.PartsCount == 0 {
		metadata.PartsCount = 1
	}

	return metadata, nil
}

// computePartSize calculates the part size based on content length and parts count
func (m *ObjectMetadata) computePartSize() {
	if m.ContentLength == 0 || m.PartsCount == 0 {
		m.PartSize = 0
		return
	}

	m.PartSize = m.ContentLength / m.PartsCount
	if m.ContentLength%m.PartsCount != 0 {
		m.PartSize++
	}
}

// getMaxBufferMemory calculates the default max buffer memory
// Formula: Concurrency × PartSize × 2
func getMaxBufferMemory(d *Downloader, metadata *ObjectMetadata) int64 {
	concurrency := int64(d.Concurrency)
	if concurrency == 0 {
		concurrency = DefaultDownloadConcurrency
	}

	partSize := metadata.PartSize
	if partSize == 0 {
		partSize = d.PartSize
		if partSize == 0 {
			partSize = DefaultDownloadPartSize
		}
	}

	return concurrency * partSize * 2
}

// validateDownloadPipeInput validates the DownloadPipeInput parameters
func validateDownloadPipeInput(input *DownloadPipeInput) error {
	if input.PipePath == nil {
		return awserr.New(InvalidParameterError, "PipePath is required", nil)
	}

	if input.GetObjectInput == nil {
		return awserr.New(InvalidParameterError, "GetObjectInput is required", nil)
	}

	// MaxBufferMemory is now optional - will be calculated if not provided
	if input.MaxBufferMemory != nil && *input.MaxBufferMemory <= 0 {
		return awserr.New(InvalidParameterError, "MaxBufferMemory must be greater than 0", nil)
	}

	return nil
}

// downloadWithPipe implements the pipe download logic
func downloadWithPipe(ctx aws.Context, d *Downloader, input *DownloadPipeInput, options ...func(*Downloader)) (n int64, err error) {
	// Validate input parameters
	if err := validateDownloadPipeInput(input); err != nil {
		return 0, err
	}

	// Create a copy of downloader with options
	dl := *d
	for _, option := range options {
		option(&dl)
	}

	// Open pipe for writing FIRST — before any network calls.
	// Named pipe open(O_WRONLY) blocks until the reader (e.g. HANA) opens its
	// end. The reader starts its timeout from the moment it signals the pipe
	// path to us, so we must open the pipe before doing any network I/O.
	// Calling GetObjectMetadata first risks exceeding that timeout: the reader
	// closes its end before we open the write end, and the download hangs with
	// no error (observed as restore progress freezing at ~95%).
	logMessage(dl.S3, aws.LogDebug, fmt.Sprintf(
		"pipe download starting, bucket %s, key %s, pipe %s",
		aws.StringValue(input.GetObjectInput.Bucket),
		aws.StringValue(input.GetObjectInput.Key),
		aws.StringValue(input.PipePath)))

	pipe, err := os.OpenFile(*input.PipePath, os.O_WRONLY, os.ModeNamedPipe)
	if err != nil {
		logMessage(dl.S3, aws.LogDebug, fmt.Sprintf("failed to open pipe %s, %v",
			aws.StringValue(input.PipePath), err))
		return 0, awserr.New(PipeOpenError, fmt.Sprintf("failed to open pipe: %v", err), err)
	}
	defer pipe.Close()

	logMessage(dl.S3, aws.LogDebug, fmt.Sprintf("pipe opened for writing, %s",
		aws.StringValue(input.PipePath)))

	// Get object metadata — safe now that the pipe is open.
	metadata, err := dl.GetObjectMetadata(ctx, input.GetObjectInput)
	if err != nil {
		logMessage(dl.S3, aws.LogDebug, fmt.Sprintf("failed to get object metadata, %v", err))
		return 0, err
	}

	// Compute part size
	metadata.computePartSize()

	// Calculate or use provided MaxBufferMemory
	maxBufferMemory := getMaxBufferMemory(&dl, metadata)
	if input.MaxBufferMemory != nil {
		maxBufferMemory = *input.MaxBufferMemory
	}

	// Get pipe buffer size
	bufferSize := getPipeBufferSize(pipe)

	// Create memory manager for backpressure
	memoryMgr := newMemoryManager(maxBufferMemory)

	// Create an ordered writer
	timeout := time.Duration(30) * time.Second
	if input.PipeTimeout != nil {
		timeout = time.Duration(*input.PipeTimeout) * time.Second
	}

	orderedWriter := newOrderedWriter(pipe, bufferSize, timeout, memoryMgr)

	// Get values from metadata
	totalSize := metadata.ContentLength
	partsCount := metadata.PartsCount
	chunkSize := metadata.PartSize

	// Create a cancellable context that will be cancelled on first error
	// This ensures all workers stop immediately when any worker fails
	workerCtx, cancelWorkers := context.WithCancel(ctx)
	defer cancelWorkers() // Ensure context is cancelled on function exit

	// Download parts in parallel using worker pool pattern with backpressure
	// Workers reserve memory before downloading, ensuring total memory usage
	// never exceeds MaxBufferMemory
	var wg sync.WaitGroup
	// Buffer only dl.Concurrency slots — the producer blocks on select anyway,
	// so a larger buffer provides no throughput benefit but wastes memory on
	// objects with thousands of parts.
	partsChan := make(chan int64, dl.Concurrency)
	errChan := make(chan error, dl.Concurrency)

	var downloadedBytes int64
	var mu sync.Mutex

	// Determine max retries (default: 3)
	maxRetries := 3
	if input.MaxRetries != nil {
		maxRetries = *input.MaxRetries
	}

	logMessage(dl.S3, aws.LogDebug, fmt.Sprintf(
		"pipe download plan, contentLength %d, parts %d, partSize %d, concurrency %d, "+
			"maxBufferMemory %d, pipeBufferSize %d, writeTimeout %s, maxRetries %d",
		totalSize, partsCount, chunkSize, dl.Concurrency,
		maxBufferMemory, bufferSize, timeout, maxRetries))

	// Worker function that processes parts from the channel
	worker := func() {
		defer wg.Done()

		for pn := range partsChan {
			// Check context cancellation before processing
			select {
			case <-workerCtx.Done():
				errChan <- workerCtx.Err()
				return
			default:
			}

			// Process each part in a closure to ensure defer cleanup per iteration
			if err := func() error {
				// Calculate byte range and expected size
				start := (pn - 1) * chunkSize
				end := start + chunkSize - 1
				if end >= totalSize {
					end = totalSize - 1
				}
				expectedSize := end - start + 1

				byteRange := fmt.Sprintf("bytes=%d-%d", start, end)

				// Reserve memory before downloading (blocks if not enough memory available)
				// This implements backpressure - workers wait here if buffer is full
				memoryMgr.Reserve(expectedSize)

				// Ensure memory is released on any failure path (download, read, write errors, panics, etc.)
				// If write succeeds, memory ownership transfers to orderedWriter
				memoryTransferred := false
				defer func() {
					if !memoryTransferred {
						memoryMgr.Release(expectedSize)
					}
				}()

				// Retry logic for downloading part
				var getOutput *s3.GetObjectOutput
				var buf *bytes.Buffer
				var lastErr error

				for attempt := 1; attempt <= maxRetries; attempt++ {
					// Check context before retry
					select {
					case <-workerCtx.Done():
						return workerCtx.Err()
					default:
					}

					// Download part
					getInput := &s3.GetObjectInput{
						Bucket:     input.GetObjectInput.Bucket,
						Key:        input.GetObjectInput.Key,
						VersionId:  input.GetObjectInput.VersionId,
						PartNumber: aws.Int64(pn),
						Range:      aws.String(byteRange),
					}

					getOutput, lastErr = dl.S3.GetObjectWithContext(workerCtx, getInput, dl.RequestOptions...)
					if lastErr != nil {
						if attempt < maxRetries {
							logMessage(dl.S3, aws.LogDebug, fmt.Sprintf(
								"failed to download part %d on attempt %d of %d, retrying, %v",
								pn, attempt, maxRetries, lastErr))
							// Linear backoff: 1s, 2s, 3s...
							time.Sleep(time.Duration(attempt) * time.Second)
							continue
						}
						logMessage(dl.S3, aws.LogDebug, fmt.Sprintf(
							"failed to download part %d after %d attempts, %v", pn, maxRetries, lastErr))
						return fmt.Errorf("failed to download part %d after %d attempts: %w", pn, maxRetries, lastErr)
					}

					// Read part data with retry
					buf = new(bytes.Buffer)
					_, lastErr = io.Copy(buf, getOutput.Body)
					getOutput.Body.Close()

					if lastErr != nil {
						if attempt < maxRetries {
							logMessage(dl.S3, aws.LogDebug, fmt.Sprintf(
								"failed to read part %d on attempt %d of %d, retrying, %v",
								pn, attempt, maxRetries, lastErr))
							// Linear backoff: 1s, 2s, 3s...
							time.Sleep(time.Duration(attempt) * time.Second)
							continue
						}
						logMessage(dl.S3, aws.LogDebug, fmt.Sprintf(
							"failed to read part %d after %d attempts, %v", pn, maxRetries, lastErr))
						return fmt.Errorf("failed to read part %d after %d attempts: %w", pn, maxRetries, lastErr)
					}

					// Success - break out of retry loop
					break
				}

				// Adjust reservation if actual size differs from expected.
				// Release first, then reserve the new size, then update expectedSize
				// so the defer uses the correct value on any subsequent failure.
				actualSize := int64(buf.Len())
				if actualSize != expectedSize {
					memoryMgr.Release(expectedSize)
					memoryMgr.Reserve(actualSize)
					expectedSize = actualSize
				}

				// Write to ordered writer (use worker context for proper cancellation).
				// If WritePart fails the defer will release expectedSize (= actualSize),
				// which is the correct live reservation at this point.
				if err := orderedWriter.WritePart(workerCtx, pn, buf.Bytes()); err != nil {
					logMessage(dl.S3, aws.LogDebug, fmt.Sprintf(
						"failed to write part %d to pipe, %v", pn, err))
					return fmt.Errorf("failed to write part %d: %w", pn, err)
				}

				// Memory ownership transferred to orderedWriter — defer must not release.
				memoryTransferred = true

				// Update progress
				mu.Lock()
				downloadedBytes += actualSize
				progress := downloadedBytes
				if input.ProgressFn != nil {
					input.ProgressFn(downloadedBytes, totalSize)
				}
				mu.Unlock()

				logMessage(dl.S3, aws.LogDebug, fmt.Sprintf(
					"wrote part %d of %d to pipe, %d bytes, %d of %d total",
					pn, partsCount, actualSize, progress, totalSize))

				return nil
			}(); err != nil {
				errChan <- err
				cancelWorkers() // Cancel all other workers on first error
				return
			}
		}
	}

	// Start worker goroutines (limited by Concurrency)
	for i := 0; i < dl.Concurrency; i++ {
		wg.Add(1)
		go worker()
	}

	// Producer goroutine that feeds parts to workers
	// Stops on context cancellation or when all parts are sent
	producerDone := make(chan struct{})
	go func() {
		defer close(producerDone)
		defer close(partsChan)

		for partNum := int64(1); partNum <= partsCount; partNum++ {
			select {
			case <-workerCtx.Done():
				// Context cancelled (by user or by worker error), stop producing
				return
			case partsChan <- partNum:
				// Part sent successfully
			}
		}
	}()

	// Wait for all workers to complete
	go func() {
		wg.Wait()
		close(errChan)
	}()

	// Check for errors - return on first error
	for err := range errChan {
		if err != nil {
			logMessage(dl.S3, aws.LogDebug, fmt.Sprintf("pipe download failed, %v", err))
			// Wait for producer to stop
			<-producerDone
			return 0, err
		}
	}

	// Flush any remaining data
	if err := orderedWriter.Flush(context.Background()); err != nil {
		logMessage(dl.S3, aws.LogDebug, fmt.Sprintf("pipe download flush failed, %v", err))
		return 0, err
	}

	logMessage(dl.S3, aws.LogDebug, fmt.Sprintf(
		"pipe download complete, %d bytes written to %s",
		downloadedBytes, aws.StringValue(input.PipePath)))

	return downloadedBytes, nil
}
