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

//nolint:hypershiftlinter // testfuncstructure: store contract test exercising the full Put/Get/Delete/ListByOwner lifecycle, not one production function
func TestSecretBackedStore(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	c := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()
	s := newSecretBackedStore(c, "hcp")
	owner := OwnerRef{Namespace: "hcp", Name: "np-1"}

	g.Expect(s.Put(ctx, owner, 1, "id-1", []byte("A"))).To(Succeed())

	// Secret exists under the digest-derived name. No bearer token is stored anywhere, and
	// the payload is stored compressed.
	sec := &corev1.Secret{}
	g.Expect(c.Get(ctx, client.ObjectKey{Namespace: "hcp", Name: secretName(owner, 1)}, sec)).To(Succeed())
	_, hasToken := sec.Data["token"]
	g.Expect(hasToken).To(BeFalse())
	g.Expect(sec.Annotations[AnnotationConfigHash]).To(Equal("id-1"))
	g.Expect(sec.Annotations[AnnotationRolloutGeneration]).To(Equal("1"))

	// Get reads through from the API (no local cache), decompresses, and returns configHash.
	got, configHash, err := s.Get(ctx, owner, 1)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(got).To(Equal([]byte("A")))
	g.Expect(configHash).To(Equal("id-1"))

	// ListByOwner returns the owned generations.
	gens, err := s.ListByOwner(ctx, owner)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(gens).To(ConsistOf(int64(1)))

	// In-place overwrite of the same (owner, generation) updates bytes + configHash.
	g.Expect(s.Put(ctx, owner, 1, "id-2", []byte("B"))).To(Succeed())
	got, configHash, err = s.Get(ctx, owner, 1)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(got).To(Equal([]byte("B")))
	g.Expect(configHash).To(Equal("id-2"))

	// A second generation is a distinct entry; ListByOwner returns both.
	g.Expect(s.Put(ctx, owner, 2, "id-3", []byte("C"))).To(Succeed())
	gens, err = s.ListByOwner(ctx, owner)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(gens).To(ConsistOf(int64(1), int64(2)))

	// Delete removes one entry; Get -> ErrNotFound; Delete idempotent.
	g.Expect(s.Delete(ctx, owner, 1)).To(Succeed())
	_, _, err = s.Get(ctx, owner, 1)
	g.Expect(err).To(MatchError(ErrNotFound))
	g.Expect(s.Delete(ctx, owner, 1)).To(Succeed())
	gens, err = s.ListByOwner(ctx, owner)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(gens).To(ConsistOf(int64(2)))

	// Get of a generation that was never written -> ErrNotFound.
	_, _, err = s.Get(ctx, owner, 99)
	g.Expect(err).To(MatchError(ErrNotFound))
}

//nolint:hypershiftlinter // testfuncstructure: store behavior test for owner values exceeding the 63-char label limit, spanning Put/Get/ListByOwner
func TestSecretBackedStore_LongValues(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	c := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()
	s := newSecretBackedStore(c, "hcp")

	// 253-char owner name (exceeds 63-char label limit).
	longOwnerName := strings.Repeat("b", 253)
	owner := OwnerRef{Namespace: "hcp", Name: longOwnerName}
	other := OwnerRef{Namespace: "hcp", Name: strings.Repeat("c", 253)}

	g.Expect(s.Put(ctx, owner, 7, "id-long", []byte("long-test"))).To(Succeed())
	g.Expect(s.Put(ctx, other, 7, "id-other", []byte("other"))).To(Succeed())

	// Get resolves exactly despite the label-value limit (labels hold fixed-length digests;
	// annotations hold the exact values used for post-list confirmation).
	got, configHash, err := s.Get(ctx, owner, 7)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(got).To(Equal([]byte("long-test")))
	g.Expect(configHash).To(Equal("id-long"))

	// ListByOwner must resolve exactly and not bleed across owners with digest-equal labels.
	gens, err := s.ListByOwner(ctx, owner)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(gens).To(ConsistOf(int64(7)))
}
