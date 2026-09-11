package s3manager

import (
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/IBM/ibm-cos-sdk-go/aws"
	"github.com/IBM/ibm-cos-sdk-go/aws/awserr"
)

// UploadPipeInput provides the input parameters for uploading from a named pipe.
// It embeds UploadInput to support all standard S3 upload parameters.
type UploadPipeInput struct {
	// Required: Path to the named pipe (FIFO)
	PipePath *string

	// Optional: Embed standard UploadInput for all S3 parameters
	// This allows using all existing features like Metadata, Tagging, etc.
	*UploadInput

	// Optional: Timeout for pipe operations (default: 30s)
	PipeTimeout *int64 // in seconds

	// Optional: Progress callback
	ProgressFn func(bytesUploaded int64)
}

// pipeReader wraps a pipe and tracks bytes read
type pipeReader struct {
	r         io.Reader
	bytesRead int64
	mu        sync.Mutex
	callback  func(int64)
}

func (pr *pipeReader) Read(p []byte) (n int, err error) {
	n, err = pr.r.Read(p)

	pr.mu.Lock()
	pr.bytesRead += int64(n)
	bytesRead := pr.bytesRead
	pr.mu.Unlock()

	if pr.callback != nil && n > 0 {
		pr.callback(bytesRead)
	}

	return n, err
}

func (pr *pipeReader) BytesRead() int64 {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	return pr.bytesRead
}

// uploadWithPipe implements the pipe upload logic
func uploadWithPipe(ctx aws.Context, u *Uploader, input *UploadPipeInput, options ...func(*Uploader)) (*UploadOutput, error) {
	if input.PipePath == nil {
		return nil, awserr.New("InvalidParameter", "PipePath is required", nil)
	}

	if input.UploadInput == nil {
		return nil, awserr.New("InvalidParameter", "UploadInput is required", nil)
	}

	logMessage(u.S3, aws.LogDebug, fmt.Sprintf(
		"pipe upload starting, bucket %s, key %s, pipe %s",
		aws.StringValue(input.UploadInput.Bucket),
		aws.StringValue(input.UploadInput.Key),
		aws.StringValue(input.PipePath)))

	// Open pipe for reading
	pipe, err := os.OpenFile(*input.PipePath, os.O_RDONLY, os.ModeNamedPipe)
	if err != nil {
		logMessage(u.S3, aws.LogDebug, fmt.Sprintf("failed to open pipe %s, %v",
			aws.StringValue(input.PipePath), err))
		return nil, awserr.New("PipeOpenError", fmt.Sprintf("failed to open pipe: %v", err), err)
	}
	defer pipe.Close()

	logMessage(u.S3, aws.LogDebug, fmt.Sprintf("pipe opened for reading, %s",
		aws.StringValue(input.PipePath)))

	// Wrap pipe with a metrics reader
	reader := &pipeReader{
		r:        pipe,
		callback: input.ProgressFn,
	}

	// Create a copy of UploadInput with the pipe reader
	uploadInput := *input.UploadInput
	uploadInput.Body = reader

	// Reuse the standard upload path so multipart, concurrency and buffering
	// behave exactly as they do for any other Upload call.
	result, err := u.UploadWithContext(ctx, &uploadInput, options...)
	if err != nil {
		logMessage(u.S3, aws.LogDebug, fmt.Sprintf(
			"pipe upload failed after %d bytes read from %s, %v",
			reader.BytesRead(), aws.StringValue(input.PipePath), err))
		return nil, err
	}

	logMessage(u.S3, aws.LogDebug, fmt.Sprintf(
		"pipe upload complete, %d bytes read from %s",
		reader.BytesRead(), aws.StringValue(input.PipePath)))

	return result, nil
}
