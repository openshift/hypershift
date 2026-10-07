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
	s := NewSecretBackedStore(c, "hcp")
	owner := OwnerRef{Namespace: "hcp", Name: "np-1"}

	g.Expect(s.Put(ctx, owner, "tok-1", "id-1", []byte("A"))).To(Succeed())

	// Secret exists with the derived name and the data key.
	sec := &corev1.Secret{}
	g.Expect(c.Get(ctx, client.ObjectKey{Namespace: "hcp", Name: "ignition-payload-tok-1"}, sec)).To(Succeed())
	g.Expect(sec.Data["payload"]).To(Equal([]byte("A")))

	// Get read-through directly from the API (no local cache in this struct).
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
	got, _, _ = s.Get(ctx, "tok-1")
	g.Expect(got).To(Equal([]byte("B")))
	tok, err = s.FindByIdentity(ctx, owner, "id-2")
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(tok).To(Equal("tok-1"))

	// Delete removes the Secret; Get -> ErrNotFound; Delete idempotent.
	g.Expect(s.Delete(ctx, "tok-1")).To(Succeed())
	_, _, err = s.Get(ctx, "tok-1")
	g.Expect(err).To(MatchError(ErrNotFound))
	g.Expect(s.Delete(ctx, "tok-1")).To(Succeed())
}

func TestSecretBackedStore_LongValues(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	c := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()
	s := NewSecretBackedStore(c, "hcp")

	// 64-char identity hash (exceeds 63-char label limit).
	longIdentity := strings.Repeat("a", 64)
	// 253-char owner name (exceeds 63-char label limit).
	longOwnerName := strings.Repeat("b", 253)
	owner := OwnerRef{Namespace: "hcp", Name: longOwnerName}

	g.Expect(s.Put(ctx, owner, "tok-long", longIdentity, []byte("long-test"))).To(Succeed())

	// FindByIdentity must resolve exactly despite the label-value limit.
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
