// Extracted from github.com/docker/docker/pkg/archive
package archive

import (
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os/exec"
)

type Compression int

const (
	// Uncompressed represents the uncompressed.
	Uncompressed Compression = iota
	// Bzip2 is bzip2 compression algorithm.
	Bzip2
	// Gzip is gzip compression algorithm.
	Gzip
	// Xz is xz compression algorithm.
	Xz
)

// Extension returns the extension of a file that uses the specified compression algorithm.
func (compression *Compression) Extension() string {
	switch *compression {
	case Uncompressed:
		return "tar"
	case Bzip2:
		return "tar.bz2"
	case Gzip:
		return "tar.gz"
	case Xz:
		return "tar.xz"
	}
	return ""
}

// DecompressStream decompresses the archive and returns a ReaderCloser with the decompressed archive.
func DecompressStream(archive io.Reader) (io.ReadCloser, error) {
	p := BufioReader32KPool
	buf := p.Get(archive)
	bs, err := buf.Peek(10)
	if err != nil && err != io.EOF {
		// Note: we'll ignore any io.EOF error because there are some odd
		// cases where the layer.tar file will be empty (zero bytes) and
		// that results in an io.EOF from the Peek() call. So, in those
		// cases we'll just treat it as a non-compressed stream and
		// that means just create an empty layer.
		// See Issue 18170
		return nil, err
	}

	compression := DetectCompression(bs)
	switch compression {
	case Uncompressed:
		readBufWrapper := p.NewReadCloserWrapper(buf, buf)
		return readBufWrapper, nil
	case Gzip:
		ctx, cancel := context.WithCancel(context.Background())

		gzReader, err := gzDecompress(ctx, buf)
		if err != nil {
			cancel()
			return nil, err
		}
		readBufWrapper := p.NewReadCloserWrapper(buf, gzReader)
		return wrapReadCloser(readBufWrapper, cancel), nil
	case Bzip2:
		bz2Reader := bzip2.NewReader(buf)
		readBufWrapper := p.NewReadCloserWrapper(buf, bz2Reader)
		return readBufWrapper, nil
	case Xz:
		ctx, cancel := context.WithCancel(context.Background())

		xzReader, err := xzDecompress(ctx, buf)
		if err != nil {
			cancel()
			return nil, err
		}
		readBufWrapper := p.NewReadCloserWrapper(buf, xzReader)
		return wrapReadCloser(readBufWrapper, cancel), nil
	default:
		return nil, fmt.Errorf("unsupported compression format %s", (&compression).Extension())
	}
}

// DetectCompression detects the compression algorithm of the source.
func DetectCompression(source []byte) Compression {
	for compression, m := range map[Compression][]byte{
		Bzip2: {0x42, 0x5A, 0x68},
		Gzip:  {0x1F, 0x8B, 0x08},
		Xz:    {0xFD, 0x37, 0x7A, 0x58, 0x5A, 0x00},
	} {
		if len(source) < len(m) {
			continue
		}
		if bytes.Equal(m, source[:len(m)]) {
			return compression
		}
	}
	return Uncompressed
}

func gzDecompress(ctx context.Context, buf io.Reader) (io.ReadCloser, error) {
	return gzip.NewReader(buf)
}

func wrapReadCloser(readBuf io.ReadCloser, cancel context.CancelFunc) io.ReadCloser {
	return NewReadCloserWrapper(readBuf, func() error {
		cancel()
		return readBuf.Close()
	})
}

func xzDecompress(ctx context.Context, archive io.Reader) (io.ReadCloser, error) {
	args := []string{"xz", "-d", "-c", "-q"}

	return cmdStream(exec.CommandContext(ctx, args[0], args[1:]...), archive)
}

// cmdStream executes a command, and returns its stdout as a stream.
// If the command fails to run or doesn't complete successfully, an error
// will be returned, including anything written on stderr.
func cmdStream(cmd *exec.Cmd, input io.Reader) (io.ReadCloser, error) {
	cmd.Stdin = input
	pipeR, pipeW := io.Pipe()
	cmd.Stdout = pipeW
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf

	// Run the command and return the pipe
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	// Copy stdout to the returned pipe
	go func() {
		if err := cmd.Wait(); err != nil {
			pipeW.CloseWithError(fmt.Errorf("%s: %s", err, errBuf.String()))
		} else {
			if err := pipeW.Close(); err != nil {
				// Log error but can't propagate it since this is in a goroutine
				fmt.Printf("failed to close pipe writer: %v\n", err)
			}
		}
	}()

	return pipeR, nil
}
