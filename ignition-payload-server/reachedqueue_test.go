package ignitionpayloadserver

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	payloadstore "github.com/openshift/hypershift/support/ignitionpayload"

	"k8s.io/client-go/util/workqueue"
)

// fakeWriter records IgnitionReached writes per key and the peak number of concurrent writes.
type fakeWriter struct {
	mu       sync.Mutex
	calls    map[reachedKey]int
	inFlight int
	peak     int
	// fail, when set, decides whether the n-th (1-based) call for a key fails.
	fail func(key reachedKey, n int) bool
	// block, when set, is waited on inside every write.
	block chan struct{}
	// sawDeadline records whether every write context carried a deadline.
	sawDeadline bool
}

func newFakeWriter() *fakeWriter {
	return &fakeWriter{calls: map[reachedKey]int{}, sawDeadline: true}
}

func (f *fakeWriter) write(ctx context.Context, owner payloadstore.OwnerRef, gen int64) error {
	key := reachedKey{owner: owner, rolloutGeneration: gen}
	f.mu.Lock()
	f.calls[key]++
	n := f.calls[key]
	f.inFlight++
	f.peak = max(f.peak, f.inFlight)
	if _, ok := ctx.Deadline(); !ok {
		f.sawDeadline = false
	}
	f.mu.Unlock()

	if f.block != nil {
		<-f.block
	}

	f.mu.Lock()
	f.inFlight--
	f.mu.Unlock()
	if f.fail != nil && f.fail(key, n) {
		return errors.New("apiserver unavailable")
	}
	return nil
}

func (f *fakeWriter) callsFor(key reachedKey) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[key]
}

func (f *fakeWriter) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	total := 0
	for _, n := range f.calls {
		total += n
	}
	return total
}

func fastRecorder(f *fakeWriter) *ReachedRecorder {
	return newReachedRecorder(f.write, workqueue.NewTypedItemExponentialFailureRateLimiter[reachedKey](time.Millisecond, 10*time.Millisecond))
}

func runRecorder(t *testing.T, r *ReachedRecorder) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.Run(ctx)
	}()
	stop = func() {
		cancel()
		NewWithT(t).Eventually(done).Should(BeClosed(), "Run must return once its context is canceled")
	}
	t.Cleanup(stop)
	return stop
}

var (
	ownerA = payloadstore.OwnerRef{Namespace: "hcp", Name: "np-1"}
	ownerB = payloadstore.OwnerRef{Namespace: "hcp", Name: "np-2"}
	keyA   = reachedKey{owner: ownerA, rolloutGeneration: 2}
	keyB   = reachedKey{owner: ownerB, rolloutGeneration: 5}
)

func TestReachedRecorder_Run(t *testing.T) {
	// A burst of serves of the same generation results in a single
	// write, while a different generation still gets its own.
	t.Run("When a burst of serves hits the same generation, it should write once while another generation still gets its own write", func(t *testing.T) {
		g := NewWithT(t)
		f := newFakeWriter()
		r := fastRecorder(f)

		var wg sync.WaitGroup
		for range 50 {
			wg.Go(func() { r.Enqueue(ownerA, 2) })
		}
		wg.Wait()
		r.Enqueue(ownerB, 5)

		runRecorder(t, r)
		g.Eventually(func() int { return f.callsFor(keyB) }).Should(Equal(1))
		g.Eventually(func() int { return f.callsFor(keyA) }).Should(Equal(1))
		g.Consistently(func() int { return f.callsFor(keyA) }, 100*time.Millisecond).Should(Equal(1))
	})

	// Writes are performed one at a time, never concurrently, and serves
	// that arrive while a write for the same key is in flight coalesce into at most one follow-up write.
	t.Run("When serves arrive while a write is in flight, it should never write concurrently and coalesce them into one follow-up write", func(t *testing.T) {
		g := NewWithT(t)
		f := newFakeWriter()
		f.block = make(chan struct{})
		r := fastRecorder(f)
		runRecorder(t, r)

		r.Enqueue(ownerA, 2)
		g.Eventually(func() int { return f.callsFor(keyA) }).Should(Equal(1), "first write in flight")

		// While A's write is blocked: more serves of A and a serve of B.
		for range 20 {
			r.Enqueue(ownerA, 2)
		}
		r.Enqueue(ownerB, 5)
		g.Consistently(f.total, 50*time.Millisecond).Should(Equal(1), "no second write starts while one is in flight")

		close(f.block)
		g.Eventually(func() int { return f.callsFor(keyB) }).Should(Equal(1))
		g.Eventually(func() int { return f.callsFor(keyA) }).Should(Equal(2), "serves during the write coalesce into one follow-up")
		g.Consistently(func() int { return f.callsFor(keyA) }, 100*time.Millisecond).Should(Equal(2))

		f.mu.Lock()
		defer f.mu.Unlock()
		g.Expect(f.peak).To(Equal(1), "at most one write in flight")
		g.Expect(f.sawDeadline).To(BeTrue(), "every write is bounded by a timeout")
	})

	// A failing write is retried with backoff until it succeeds.
	t.Run("When a write fails, it should retry with backoff until it succeeds", func(t *testing.T) {
		g := NewWithT(t)
		f := newFakeWriter()
		f.fail = func(_ reachedKey, n int) bool { return n <= 2 }
		r := fastRecorder(f)
		runRecorder(t, r)

		r.Enqueue(ownerA, 2)
		g.Eventually(func() int { return f.callsFor(keyA) }).Should(Equal(3), "two failures, then success")
		g.Consistently(func() int { return f.callsFor(keyA) }, 100*time.Millisecond).Should(Equal(3), "no writes after success")
	})

	// A write that keeps failing is dropped after the retry cap, and a later
	// serve of the same generation enqueues it again.
	t.Run("When a write keeps failing, it should give up after the retry cap and accept a later serve again", func(t *testing.T) {
		g := NewWithT(t)
		f := newFakeWriter()
		f.fail = func(reachedKey, int) bool { return true }
		r := fastRecorder(f)
		runRecorder(t, r)

		r.Enqueue(ownerA, 2)
		g.Eventually(func() int { return f.callsFor(keyA) }, 5*time.Second).Should(Equal(reachedMaxRetries + 1))
		g.Consistently(func() int { return f.callsFor(keyA) }, 100*time.Millisecond).Should(Equal(reachedMaxRetries+1), "dropped after the retry cap")

		r.Enqueue(ownerA, 2)
		g.Eventually(func() int { return f.callsFor(keyA) }).Should(BeNumerically(">", reachedMaxRetries+1), "a later serve re-enqueues it")
	})

	// Run returns once its context is canceled, and nothing enqueued
	// afterwards is written.
	t.Run("When the context is canceled, it should stop and write nothing enqueued afterwards", func(t *testing.T) {
		g := NewWithT(t)
		f := newFakeWriter()
		r := fastRecorder(f)
		stop := runRecorder(t, r)

		r.Enqueue(ownerA, 2)
		g.Eventually(func() int { return f.callsFor(keyA) }).Should(Equal(1))

		stop()
		r.Enqueue(ownerB, 5)
		g.Consistently(func() int { return f.callsFor(keyB) }, 100*time.Millisecond).Should(Equal(0))
	})
}
