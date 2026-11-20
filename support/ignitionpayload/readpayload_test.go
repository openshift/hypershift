package ignitionpayload

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	"k8s.io/client-go/kubernetes/scheme"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReadPayload(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	owner := OwnerRef{Namespace: "hcp", Name: "np-1"}
	c := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()

	// Seed via the store write path, then read via the bare reader (what the serving tier uses).
	s := newSecretBackedStore(c, "hcp")
	g.Expect(s.Put(ctx, owner, 2, "cfg-1", []byte("PAYLOAD"))).To(Succeed())

	payload, configHash, err := ReadPayload(ctx, c, "hcp", owner, 2)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(payload).To(Equal([]byte("PAYLOAD")))
	g.Expect(configHash).To(Equal("cfg-1"))

	// A generation with no entry returns ErrNotFound (the serving tier maps it to HTTP 511).
	_, _, err = ReadPayload(ctx, c, "hcp", owner, 99)
	g.Expect(err).To(MatchError(ErrNotFound))
}
