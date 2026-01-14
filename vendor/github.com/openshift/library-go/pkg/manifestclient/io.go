package manifestclient

import (
	"io"
	"sync/atomic"
	"time"
)

func newDelayedNothingReader(timeout time.Duration) *delayedNothingReaderCloser {
	return &delayedNothingReaderCloser{timeout: timeout}
}

type delayedNothingReaderCloser struct {
	timeout time.Duration
	closed  atomic.Bool
}

func (d *delayedNothingReaderCloser) Read(p []byte) (n int, err error) {
	if d.closed.Load() {
		return 0, io.EOF
	}
	select {
	case <-time.After(d.timeout):
		d.Close()
	}
	if d.closed.Load() {
		return 0, io.EOF
	}
	return 0, nil
}

func (d *delayedNothingReaderCloser) Close() error {
	d.closed.Store(true)
	return nil
}

var _ io.ReadCloser = &delayedNothingReaderCloser{}
