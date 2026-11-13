package ignitionpayload

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	payloadstore "github.com/openshift/hypershift/support/ignitionpayload"
)

type fakeProvider struct {
	calls               int
	payload             []byte
	lastImg             string
	lastPullSecretHash  string
	lastTrustBundleHash string
	lastHCConfigHash    string
	lastCloudConfigHash string
}

func (f *fakeProvider) GetPayload(ctx context.Context, img, cfg, pullSecretHash, trustBundleHash, hcConfigHash, osImageStream, cloudConfigHash string) ([]byte, error) {
	f.calls++
	f.lastImg = img
	f.lastPullSecretHash = pullSecretHash
	f.lastTrustBundleHash = trustBundleHash
	f.lastHCConfigHash = hcConfigHash
	f.lastCloudConfigHash = cloudConfigHash
	return f.payload, nil
}

func TestPayloadGenerator_EnsurePayload(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	fp := &fakeProvider{payload: []byte("PAYLOAD")}
	gen := &payloadGenerator{store: payloadstore.NewMemStore(), provider: fp}
	owner := payloadstore.OwnerRef{Namespace: "hcp", Name: "np-1"}

	// First generation for (owner, generation 1, configHash id-1): renders and stores.
	generated, err := gen.ensurePayload(ctx, owner, 1, "id-1", genInputs{})
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(generated).To(BeTrue())
	g.Expect(fp.calls).To(Equal(1))

	// (RF#6) same generation + same configHash reuses — no extra pull.
	generated2, err := gen.ensurePayload(ctx, owner, 1, "id-1", genInputs{})
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(generated2).To(BeFalse())
	g.Expect(fp.calls).To(Equal(1))

	// Policy A refresh: same generation, NEW configHash → overwrite bytes in place, re-render.
	fp.payload = []byte("PAYLOAD2")
	generated3, err := gen.ensurePayload(ctx, owner, 1, "id-2", genInputs{})
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(generated3).To(BeTrue())
	got, hash, err := gen.store.Get(ctx, owner, 1)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(got).To(Equal([]byte("PAYLOAD2")))
	g.Expect(hash).To(Equal("id-2"))
	g.Expect(fp.calls).To(Equal(2))
}
