package ignitionpayload

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
)

//nolint:hypershiftlinter // testfuncstructure: store contract test exercising the full Put/Get/Delete/ListByOwner lifecycle, not one production function
func TestMemStoreContract(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	s := NewMemStore()
	owner := OwnerRef{Namespace: "hcp", Name: "np-1"}

	// Put + Get round-trips bytes and configHash.
	g.Expect(s.Put(ctx, owner, 1, "id-1", []byte("payload-A"))).To(Succeed())
	got, configHash, err := s.Get(ctx, owner, 1)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(got).To(Equal([]byte("payload-A")))
	g.Expect(configHash).To(Equal("id-1"))

	// Get of an unknown (owner, generation) -> ErrNotFound.
	_, _, err = s.Get(ctx, owner, 99)
	g.Expect(err).To(MatchError(ErrNotFound))

	// Put in-place overwrite of the same (owner, generation).
	g.Expect(s.Put(ctx, owner, 1, "id-2", []byte("payload-B"))).To(Succeed())
	got, configHash, _ = s.Get(ctx, owner, 1)
	g.Expect(got).To(Equal([]byte("payload-B")))
	g.Expect(configHash).To(Equal("id-2"))

	// ListByOwner returns all owner generations; Delete removes; Delete idempotent.
	g.Expect(s.Put(ctx, owner, 2, "id-3", []byte("C"))).To(Succeed())
	gens, _ := s.ListByOwner(ctx, owner)
	g.Expect(gens).To(ConsistOf(int64(1), int64(2)))
	g.Expect(s.Delete(ctx, owner, 1)).To(Succeed())
	_, _, err = s.Get(ctx, owner, 1)
	g.Expect(err).To(MatchError(ErrNotFound))
	g.Expect(s.Delete(ctx, owner, 1)).To(Succeed())
	gens, _ = s.ListByOwner(ctx, owner)
	g.Expect(gens).To(ConsistOf(int64(2)))
}
