package ignitionpayload

import (
	"context"
	"errors"
)

// OwnerRef identifies the IgnitionPayload CR that owns a store entry.
type OwnerRef struct {
	Namespace string
	Name      string
}

// ErrNotFound is returned when a (owner, rolloutGeneration) identity has no store entry.
var ErrNotFound = errors.New("ignition payload not found")

// PayloadStore stores rendered ignition payloads out-of-band from the CRD, keyed by the
// rollout identity (owner + rolloutGeneration). It never stores or keys on the bearer token:
// the token is minted by the generator, lives only in the userdata Secret, and is verified
// statelessly by the serving tier. Keying on the generation (rather than on rolloutHash) keeps
// serving identity stable across a rolloutHash formula rebaseline, which rewrites rolloutHash
// without advancing rolloutGeneration. Each entry records the payload-identity hash (configHash)
// of its content for idempotent generation and observability. Implementations are
// backend-independent.
type PayloadStore interface {
	// Put stores payload under (owner, rolloutGeneration), (re)setting the configHash it
	// records. If the entry already exists its bytes/configHash are overwritten in place
	// (last-write-wins; the Policy A refresh path).
	Put(ctx context.Context, owner OwnerRef, rolloutGeneration int64, configHash string, payload []byte) error
	// Get returns the payload bytes and the recorded configHash for (owner, rolloutGeneration),
	// or ErrNotFound. The serving tier uses payload; the generator compares configHash for
	// idempotent regeneration.
	Get(ctx context.Context, owner OwnerRef, rolloutGeneration int64) (payload []byte, configHash string, err error)
	// Delete removes the entry for (owner, rolloutGeneration). No-op (nil) if already absent.
	Delete(ctx context.Context, owner OwnerRef, rolloutGeneration int64) error
	// ListByOwner returns all rolloutGenerations owned by owner (empty slice if none), for the
	// inline per-CR sweep and the store-cleanup finalizer.
	ListByOwner(ctx context.Context, owner OwnerRef) (rolloutGenerations []int64, err error)
}
