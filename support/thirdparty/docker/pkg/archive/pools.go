// Extracted from github.com/docker/docker/pkg/pools
package archive

import (
	"bufio"
	"fmt"
	"io"
	"sync"
)

const buffer32K = 32 * 1024

var (
	// BufioReader32KPool is a pool which returns bufio.Reader with a 32K buffer.
	BufioReader32KPool = newBufioReaderPoolWithSize(buffer32K)
)

// BufioReaderPool is a bufio reader that uses sync.Pool.
type BufioReaderPool struct {
	pool sync.Pool
}

// newBufioReaderPoolWithSize is unexported because new pools should be
// added here to be shared where required.
func newBufioReaderPoolWithSize(size int) *BufioReaderPool {
	return &BufioReaderPool{
		pool: sync.Pool{
			New: func() interface{} { return bufio.NewReaderSize(nil, size) },
		},
	}
}

// Get returns a bufio.Reader which reads from r. The buffer size is that of the pool.
func (bufPool *BufioReaderPool) Get(r io.Reader) *bufio.Reader {
	buf := bufPool.pool.Get().(*bufio.Reader)
	buf.Reset(r)
	return buf
}

// Put puts the bufio.Reader back into the pool.
func (bufPool *BufioReaderPool) Put(b *bufio.Reader) {
	b.Reset(nil)
	bufPool.pool.Put(b)
}

// NewReadCloserWrapper returns a wrapper which puts the bufio.Reader back
// into the pool and closes the reader if it's an io.ReadCloser.
func (bufPool *BufioReaderPool) NewReadCloserWrapper(buf *bufio.Reader, r io.Reader) io.ReadCloser {
	return NewReadCloserWrapper(r, func() error {
		if readCloser, ok := r.(io.ReadCloser); ok {
			if err := readCloser.Close(); err != nil {
				// Log error but don't fail the cleanup operation
				fmt.Printf("failed to close reader: %v\n", err)
			}
		}
		bufPool.Put(buf)
		return nil
	})
}

// ReadCloserWrapper wraps an io.Reader, and implements an io.ReadCloser
// It calls the given callback function when closed. It should be constructed
// with NewReadCloserWrapper
type ReadCloserWrapper struct {
	io.Reader
	closer func() error
}

// Close calls back the passed closer function
func (r *ReadCloserWrapper) Close() error {
	return r.closer()
}

// NewReadCloserWrapper returns a new io.ReadCloser.
func NewReadCloserWrapper(r io.Reader, closer func() error) io.ReadCloser {
	return &ReadCloserWrapper{
		Reader: r,
		closer: closer,
	}
}
