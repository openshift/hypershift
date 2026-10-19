package ignitionpayload

import (
	"context"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestSecretBackedStore(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	c := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()
	s := newSecretBackedStore(c, "hcp")
	owner := OwnerRef{Namespace: "hcp", Name: "np-1"}

	g.Expect(s.Put(ctx, owner, "tok-1", "id-1", []byte("A"))).To(Succeed())

	// Secret exists under the digest-derived name. The token lives in data (never in the
	// name or a label), and the payload is stored compressed.
	sec := &corev1.Secret{}
	g.Expect(c.Get(ctx, client.ObjectKey{Namespace: "hcp", Name: secretName("tok-1")}, sec)).To(Succeed())
	g.Expect(string(sec.Data["token"])).To(Equal("tok-1"))
	g.Expect(sec.Name).ToNot(ContainSubstring("tok-1"))

	// Get reads through from the API (no local cache) and decompresses.
	got, gotOwner, err := s.Get(ctx, "tok-1")
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(got).To(Equal([]byte("A")))
	g.Expect(gotOwner).To(Equal(owner))

	// FindByIdentity / ListByOwner.
	tok, err := s.FindByIdentity(ctx, owner, "id-1")
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(tok).To(Equal("tok-1"))
	toks, err := s.ListByOwner(ctx, owner)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(toks).To(ConsistOf("tok-1"))

	// In-place overwrite updates bytes + identity.
	g.Expect(s.Put(ctx, owner, "tok-1", "id-2", []byte("B"))).To(Succeed())
	got, _, err = s.Get(ctx, "tok-1")
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(got).To(Equal([]byte("B")))
	tok, err = s.FindByIdentity(ctx, owner, "id-2")
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(tok).To(Equal("tok-1"))

	// Delete removes the Secret; Get -> ErrNotFound; Delete idempotent.
	g.Expect(s.Delete(ctx, "tok-1")).To(Succeed())
	_, _, err = s.Get(ctx, "tok-1")
	g.Expect(err).To(MatchError(ErrNotFound))
	g.Expect(s.Delete(ctx, "tok-1")).To(Succeed())

	// Garbage/untrusted token input yields ErrNotFound (the token is hashed into a valid
	// name), not an internal "invalid resource name" error.
	_, _, err = s.Get(ctx, "../not-a-token")
	g.Expect(err).To(MatchError(ErrNotFound))
}

func TestSecretBackedStore_LongValues(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	c := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()
	s := newSecretBackedStore(c, "hcp")

	// 64-char identity hash (exceeds 63-char label limit).
	longIdentity := strings.Repeat("a", 64)
	// 253-char owner name (exceeds 63-char label limit).
	longOwnerName := strings.Repeat("b", 253)
	owner := OwnerRef{Namespace: "hcp", Name: longOwnerName}

	g.Expect(s.Put(ctx, owner, "tok-long", longIdentity, []byte("long-test"))).To(Succeed())

	// FindByIdentity must resolve exactly despite the label-value limit (the label holds
	// a fixed-length digest).
	tok, err := s.FindByIdentity(ctx, owner, longIdentity)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(tok).To(Equal("tok-long"))

	// Near-miss identity -> ErrNotFound.
	_, err = s.FindByIdentity(ctx, owner, longIdentity[:63])
	g.Expect(err).To(MatchError(ErrNotFound))

	// ListByOwner must resolve exactly despite the label-value limit.
	toks, err := s.ListByOwner(ctx, owner)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(toks).To(ConsistOf("tok-long"))

	// Get returns the correct owner.
	_, gotOwner, err := s.Get(ctx, "tok-long")
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(gotOwner).To(Equal(owner))
}
