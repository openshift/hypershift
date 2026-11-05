package ignitionpayload

import (
	"context"
	"fmt"
	"sync"
)

// MemStore is an in-memory PayloadStore for testing.
type MemStore struct {
	mu      sync.RWMutex
	entries map[string]*memEntry
}

type memEntry struct {
	payload           []byte
	owner             OwnerRef
	rolloutGeneration int64
	configHash        string
}

// NewMemStore creates a new in-memory PayloadStore.
func NewMemStore() *MemStore {
	return &MemStore{
		entries: make(map[string]*memEntry),
	}
}

// memKey composes the store key (owner + rolloutGeneration) into a map key.
func memKey(owner OwnerRef, rolloutGeneration int64) string {
	return fmt.Sprintf("%s/%s/%d", owner.Namespace, owner.Name, rolloutGeneration)
}

func (s *MemStore) Put(ctx context.Context, owner OwnerRef, rolloutGeneration int64, configHash string, payload []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Make a copy to avoid aliasing.
	payloadCopy := make([]byte, len(payload))
	copy(payloadCopy, payload)

	s.entries[memKey(owner, rolloutGeneration)] = &memEntry{
		payload:           payloadCopy,
		owner:             owner,
		rolloutGeneration: rolloutGeneration,
		configHash:        configHash,
	}
	return nil
}

func (s *MemStore) Get(ctx context.Context, owner OwnerRef, rolloutGeneration int64) (payload []byte, configHash string, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, exists := s.entries[memKey(owner, rolloutGeneration)]
	if !exists {
		return nil, "", ErrNotFound
	}

	// Return a copy to avoid aliasing.
	payloadCopy := make([]byte, len(entry.payload))
	copy(payloadCopy, entry.payload)

	return payloadCopy, entry.configHash, nil
}

func (s *MemStore) Delete(ctx context.Context, owner OwnerRef, rolloutGeneration int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.entries, memKey(owner, rolloutGeneration))
	return nil
}

func (s *MemStore) ListByOwner(ctx context.Context, owner OwnerRef) (rolloutGenerations []int64, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := []int64{}
	for _, entry := range s.entries {
		if entry.owner == owner {
			result = append(result, entry.rolloutGeneration)
		}
	}
	return result, nil
}
