package ignitionpayload

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/api"
	payloadstore "github.com/openshift/hypershift/support/ignitionpayload"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestNewReconciler(t *testing.T) {
	g := NewWithT(t)
	store := payloadstore.NewMemStore()
	c := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
	r := NewReconciler(ReconcilerConfig{
		Client:    c,
		Store:     store,
		Provider:  &fakeProvider{},
		Minter:    &fakeMinter{},
		Namespace: "hcp",
	})
	g.Expect(r).ToNot(BeNil())
	g.Expect(r.Store).To(Equal(store))
	g.Expect(r.Generator).ToNot(BeNil())
	g.Expect(r.Namespace).To(Equal("hcp"))
}
