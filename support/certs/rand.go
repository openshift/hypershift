package certs

import (
	cryptorand "crypto/rand"
	"io"
	mathrand "math/rand"
	"sync"
)

var rng = struct {
	sync.Mutex
	reader io.Reader
}{
	reader: cryptorand.Reader,
}

// UnsafeSeed seeds the rng with the provided seed.
// This is not safe to do in production code and should only be used
// to make tests that interact with this package deterministic.
func UnsafeSeed(seed int64) {
	rng.Lock()
	defer rng.Unlock()

	rng.reader = mathrand.New(mathrand.NewSource(seed))
}

func Reader() io.Reader {
	rng.Lock()
	defer rng.Unlock()
	return rng.reader
}
