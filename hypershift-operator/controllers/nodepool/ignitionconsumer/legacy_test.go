package ignitionconsumer

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	payloadstore "github.com/openshift/hypershift/support/ignitionpayload"
	supportutil "github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcileLegacyInPlaceSecret(t *testing.T) {
	ctx := context.Background()
	current := hyperv1.PayloadReference{ConfigHash: "cfg1", RolloutHash: "roll1", UserDataSecret: hyperv1.SecretReference{Name: "np-1-2"}, RolloutGeneration: 2}
	crOwner := &hyperv1.IgnitionPayload{ObjectMeta: metav1.ObjectMeta{Name: "np-1", Namespace: "hcp", UID: "uid-1"}}
	owner := payloadstore.OwnerRef{Namespace: "hcp", Name: "np-1"}

	t.Run("InPlace writes a byte-compatible token Secret keyed by configHash", func(t *testing.T) {
		g := NewWithT(t)
		c := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
		store := payloadstore.NewMemStore()
		g.Expect(store.Put(ctx, owner, current.RolloutGeneration, current.ConfigHash, []byte("PAYLOAD"))).To(Succeed())

		err := reconcileLegacyInPlaceSecret(ctx, c, store, "ms-1", hyperv1.UpgradeTypeInPlace, "4.23.0", current, crOwner)
		g.Expect(err).ToNot(HaveOccurred())

		secret := &corev1.Secret{}
		g.Expect(c.Get(ctx, client.ObjectKey{Namespace: "hcp", Name: "token-ms-1-cfg1"}, secret)).To(Succeed())

		wantPayload, err := supportutil.CompressAndEncode([]byte("PAYLOAD"))
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(secret.Data["payload"]).To(Equal(wantPayload.Bytes()))
		// The HCCO InPlaceUpgrader errors without a non-empty release-version key.
		g.Expect(string(secret.Data["release-version"])).To(Equal("4.23.0"))
		// CR-owned so it cascade-deletes with the CR.
		g.Expect(secret.OwnerReferences).To(HaveLen(1))
		g.Expect(secret.OwnerReferences[0].Name).To(Equal("np-1"))
		g.Expect(secret.OwnerReferences[0].Controller).ToNot(BeNil())
		g.Expect(*secret.OwnerReferences[0].Controller).To(BeTrue())
	})

	// A same-generation refresh writes the store before it commits the new configHash to status, so
	// the store can be ahead of the status read. The Secret named for the status hash must not get the
	// newer bytes.
	t.Run("InPlace writes nothing when the stored payload is for another configHash", func(t *testing.T) {
		g := NewWithT(t)
		c := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
		store := payloadstore.NewMemStore()
		g.Expect(store.Put(ctx, owner, current.RolloutGeneration, "cfg2", []byte("PAYLOAD-2"))).To(Succeed())

		err := reconcileLegacyInPlaceSecret(ctx, c, store, "ms-1", hyperv1.UpgradeTypeInPlace, "4.23.0", current, crOwner)
		g.Expect(err).To(HaveOccurred())

		err = c.Get(ctx, client.ObjectKey{Namespace: "hcp", Name: "token-ms-1-cfg1"}, &corev1.Secret{})
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	t.Run("Replace writes nothing", func(t *testing.T) {
		g := NewWithT(t)
		c := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
		store := payloadstore.NewMemStore()
		g.Expect(store.Put(ctx, owner, current.RolloutGeneration, current.ConfigHash, []byte("PAYLOAD"))).To(Succeed())

		err := reconcileLegacyInPlaceSecret(ctx, c, store, "ms-1", hyperv1.UpgradeTypeReplace, "4.23.0", current, crOwner)
		g.Expect(err).ToNot(HaveOccurred())

		secret := &corev1.Secret{}
		err = c.Get(ctx, client.ObjectKey{Namespace: "hcp", Name: "token-ms-1-cfg1"}, secret)
		g.Expect(err).To(HaveOccurred())
	})
}
