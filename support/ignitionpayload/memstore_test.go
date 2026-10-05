package ignitionpayload

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
)

func TestMemStoreContract(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	s := NewMemStore()
	owner := OwnerRef{Namespace: "hcp", Name: "np-1"}

	// Put + Get round-trips bytes and owner.
	g.Expect(s.Put(ctx, owner, "tok-1", "id-1", []byte("payload-A"))).To(Succeed())
	got, gotOwner, err := s.Get(ctx, "tok-1")
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(got).To(Equal([]byte("payload-A")))
	g.Expect(gotOwner).To(Equal(owner))

	// Get of unknown token -> ErrNotFound.
	_, _, err = s.Get(ctx, "nope")
	g.Expect(err).To(MatchError(ErrNotFound))

	// FindByIdentity exact match; near-miss -> ErrNotFound.
	tok, err := s.FindByIdentity(ctx, owner, "id-1")
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(tok).To(Equal("tok-1"))
	_, err = s.FindByIdentity(ctx, owner, "id-other")
	g.Expect(err).To(MatchError(ErrNotFound))

	// Put in-place overwrite.
	g.Expect(s.Put(ctx, owner, "tok-1", "id-2", []byte("payload-B"))).To(Succeed())
	got, _, _ = s.Get(ctx, "tok-1")
	g.Expect(got).To(Equal([]byte("payload-B")))
	tok, _ = s.FindByIdentity(ctx, owner, "id-2")
	g.Expect(tok).To(Equal("tok-1"))

	// ListByOwner returns all owner tokens; Delete removes; Delete idempotent.
	g.Expect(s.Put(ctx, owner, "tok-2", "id-3", []byte("C"))).To(Succeed())
	toks, _ := s.ListByOwner(ctx, owner)
	g.Expect(toks).To(ConsistOf("tok-1", "tok-2"))
	g.Expect(s.Delete(ctx, "tok-1")).To(Succeed())
	_, _, err = s.Get(ctx, "tok-1")
	g.Expect(err).To(MatchError(ErrNotFound))
	g.Expect(s.Delete(ctx, "tok-1")).To(Succeed())
}
