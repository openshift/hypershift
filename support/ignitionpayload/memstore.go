package ignitionpayload

import (
	"context"
	"sync"
)

// MemStore is an in-memory PayloadStore for testing.
type MemStore struct {
	mu      sync.RWMutex
	entries map[string]*memEntry
}

type memEntry struct {
	payload  []byte
	owner    OwnerRef
	identity string
}

// NewMemStore creates a new in-memory PayloadStore.
func NewMemStore() *MemStore {
	return &MemStore{
		entries: make(map[string]*memEntry),
	}
}

func (s *MemStore) Put(ctx context.Context, owner OwnerRef, token, identityHash string, payload []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Make a copy to avoid aliasing.
	payloadCopy := make([]byte, len(payload))
	copy(payloadCopy, payload)

	s.entries[token] = &memEntry{
		payload:  payloadCopy,
		owner:    owner,
		identity: identityHash,
	}
	return nil
}

func (s *MemStore) Get(ctx context.Context, token string) (payload []byte, owner OwnerRef, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, exists := s.entries[token]
	if !exists {
		return nil, OwnerRef{}, ErrNotFound
	}

	// Return a copy to avoid aliasing.
	payloadCopy := make([]byte, len(entry.payload))
	copy(payloadCopy, entry.payload)

	return payloadCopy, entry.owner, nil
}

func (s *MemStore) Delete(ctx context.Context, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.entries, token)
	return nil
}

func (s *MemStore) FindByIdentity(ctx context.Context, owner OwnerRef, identityHash string) (token string, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for tok, entry := range s.entries {
		if entry.owner == owner && entry.identity == identityHash {
			return tok, nil
		}
	}
	return "", ErrNotFound
}

func (s *MemStore) ListByOwner(ctx context.Context, owner OwnerRef) (tokens []string, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := []string{}
	for tok, entry := range s.entries {
		if entry.owner == owner {
			result = append(result, tok)
		}
	}
	return result, nil
}
