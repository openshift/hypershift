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

// ErrNotFound is returned when a token or identity has no store entry.
var ErrNotFound = errors.New("ignition payload not found")

// PayloadStore stores rendered ignition payloads out-of-band from the CRD,
// keyed by an opaque token. Every entry is labeled with its owning CR and the
// payload-identity hash of its content. Implementations are backend-independent.
type PayloadStore interface {
	// Put stores payload under token, (re)setting the owner and identityHash
	// labels. If token already exists its bytes/labels are overwritten in place
	// (last-write-wins; the Policy A refresh path).
	Put(ctx context.Context, owner OwnerRef, token, identityHash string, payload []byte) error
	// Get returns payload bytes and the owning CR for token, or ErrNotFound.
	Get(ctx context.Context, token string) (payload []byte, owner OwnerRef, err error)
	// Delete removes the entry for token. No-op (nil) if already absent.
	Delete(ctx context.Context, token string) error
	// FindByIdentity returns the token of owner's entry whose identity equals
	// identityHash exactly, or ErrNotFound.
	FindByIdentity(ctx context.Context, owner OwnerRef, identityHash string) (token string, err error)
	// ListByOwner returns all tokens owned by owner (empty slice if none).
	ListByOwner(ctx context.Context, owner OwnerRef) (tokens []string, err error)
}
