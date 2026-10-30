package ignitionpayloadserver

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	payloadstore "github.com/openshift/hypershift/support/ignitionpayload"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func reachedTestCR(name string) *hyperv1.IgnitionPayload {
	return &hyperv1.IgnitionPayload{
		ObjectMeta: metav1.ObjectMeta{Namespace: "hcp", Name: name},
		Status: hyperv1.IgnitionPayloadStatus{
			Current:  hyperv1.PayloadReference{ConfigHash: "h", RolloutHash: "r", UserDataSecret: hyperv1.SecretReference{Name: name + "-2"}, RolloutGeneration: 2},
			Previous: hyperv1.PayloadReference{ConfigHash: "h0", RolloutHash: "r0", UserDataSecret: hyperv1.SecretReference{Name: name + "-1"}, RolloutGeneration: 1},
		},
	}
}

func TestSetIgnitionReached(t *testing.T) {
	t.Run("the current generation flips IgnitionReached and leaves current/previous untouched", func(t *testing.T) {
		g := NewWithT(t)
		ctx := context.Background()
		cr := reachedTestCR("np-1")
		c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(cr).
			WithStatusSubresource(&hyperv1.IgnitionPayload{}).Build()

		g.Expect(SetIgnitionReached(ctx, c, payloadstore.OwnerRef{Namespace: "hcp", Name: "np-1"}, 2)).To(Succeed())

		got := &hyperv1.IgnitionPayload{}
		g.Expect(c.Get(ctx, client.ObjectKeyFromObject(cr), got)).To(Succeed())
		g.Expect(apimeta.IsStatusConditionTrue(got.Status.Conditions, hyperv1.IgnitionPayloadIgnitionReachedCondition)).To(BeTrue())
		g.Expect(got.Status.Current.RolloutGeneration).To(Equal(int64(2)))
		g.Expect(got.Status.Previous.RolloutGeneration).To(Equal(int64(1)))
	})

	t.Run("serving the previous (draining) generation does not flip IgnitionReached", func(t *testing.T) {
		g := NewWithT(t)
		ctx := context.Background()
		cr := reachedTestCR("np-2")
		c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(cr).
			WithStatusSubresource(&hyperv1.IgnitionPayload{}).Build()

		g.Expect(SetIgnitionReached(ctx, c, payloadstore.OwnerRef{Namespace: "hcp", Name: "np-2"}, 1)).To(Succeed())

		got := &hyperv1.IgnitionPayload{}
		g.Expect(c.Get(ctx, client.ObjectKeyFromObject(cr), got)).To(Succeed())
		g.Expect(apimeta.IsStatusConditionTrue(got.Status.Conditions, hyperv1.IgnitionPayloadIgnitionReachedCondition)).To(BeFalse())
	})
}
