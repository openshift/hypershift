package ignitionpayloadserver

import (
	"context"
	"time"

	payloadstore "github.com/openshift/hypershift/support/ignitionpayload"

	"k8s.io/client-go/util/workqueue"

	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	// reachedWriteTimeout bounds a single IgnitionReached write so a slow apiserver cannot stall the
	// worker indefinitely.
	reachedWriteTimeout = 10 * time.Second
	// reachedMaxRetries caps how often a failing write is retried before the key is dropped; a later
	// serve of the same generation enqueues it again.
	reachedMaxRetries = 10
)

// reachedKey identifies one payload generation whose IgnitionReached condition is pending a write.
type reachedKey struct {
	owner             payloadstore.OwnerRef
	rolloutGeneration int64
}

// ReachedRecorder decouples recording IgnitionReached from serving. The handler only calls Enqueue,
// which is instant and never touches the API; a single worker goroutine (Run) drains the queue and
// performs the status writes.
//
// The workqueue is the in-memory state: it holds each (payload, generation) that has been served but
// not yet recorded, de-duplicated, so a burst of N serves of the same generation results in one write,
// and at most one write is in flight per replica. Failed writes are retried with backoff. A generation
// that is no longer current is dropped on its first attempt, because the write function treats it as
// a successful no-op.
type ReachedRecorder struct {
	queue workqueue.TypedRateLimitingInterface[reachedKey]
	write func(ctx context.Context, owner payloadstore.OwnerRef, rolloutGeneration int64) error
}

// NewReachedRecorder returns a recorder that records IgnitionReached with write.
func NewReachedRecorder(write func(ctx context.Context, owner payloadstore.OwnerRef, rolloutGeneration int64) error) *ReachedRecorder {
	return newReachedRecorder(write, workqueue.DefaultTypedControllerRateLimiter[reachedKey]())
}

func newReachedRecorder(write func(ctx context.Context, owner payloadstore.OwnerRef, rolloutGeneration int64) error, rateLimiter workqueue.TypedRateLimiter[reachedKey]) *ReachedRecorder {
	return &ReachedRecorder{
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(rateLimiter, workqueue.TypedRateLimitingQueueConfig[reachedKey]{
			Name: "ignition-reached",
		}),
		write: write,
	}
}

// Enqueue records that rolloutGeneration of owner was served. It never blocks on the API.
func (r *ReachedRecorder) Enqueue(owner payloadstore.OwnerRef, rolloutGeneration int64) {
	r.queue.Add(reachedKey{owner: owner, rolloutGeneration: rolloutGeneration})
}

// Run processes the queue with a single worker until ctx is canceled. Pending keys are dropped on
// shutdown; recording IgnitionReached is best-effort and a later serve re-enqueues it.
func (r *ReachedRecorder) Run(ctx context.Context) {
	go func() {
		<-ctx.Done()
		r.queue.ShutDown()
	}()
	for r.processNext(ctx) {
	}
}

func (r *ReachedRecorder) processNext(ctx context.Context) bool {
	key, shutdown := r.queue.Get()
	if shutdown {
		return false
	}
	defer r.queue.Done(key)

	writeCtx, cancel := context.WithTimeout(ctx, reachedWriteTimeout)
	defer cancel()
	err := r.write(writeCtx, key.owner, key.rolloutGeneration)
	switch {
	case err == nil:
		r.queue.Forget(key)
	case r.queue.NumRequeues(key) < reachedMaxRetries:
		r.queue.AddRateLimited(key)
	default:
		ctrl.Log.WithName("ignition-reached").Error(err, "giving up recording IgnitionReached",
			"owner", key.owner.Namespace+"/"+key.owner.Name, "rolloutGeneration", key.rolloutGeneration)
		r.queue.Forget(key)
	}
	return true
}
