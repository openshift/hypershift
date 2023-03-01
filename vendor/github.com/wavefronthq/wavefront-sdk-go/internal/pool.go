package internal

import (
	"bytes"
	"sync"
)

var buffers *sync.Pool

func init() {
	buffers = &sync.Pool{
		New: func() interface{} {
			return new(bytes.Buffer)
		},
	}
}

// GetBuffer fetches a buffers from the pool
func GetBuffer() *bytes.Buffer {
	return buffers.Get().(*bytes.Buffer)
}

// PutBuffer returns a buffers to the pool
func PutBuffer(buf *bytes.Buffer) {
	buf.Reset()
	buffers.Put(buf)
}
