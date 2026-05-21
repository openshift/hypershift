package k8sutil

import (
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

type fakeMessageCollector struct {
	messages []string
	err      error
}

func (f *fakeMessageCollector) ErrorMessages(_ client.Object) ([]string, error) {
	return f.messages, f.err
}

func TestCollectLBMessageIfNotProvisioned(t *testing.T) {
	t.Run("When load balancer is provisioned it should return empty string", func(t *testing.T) {
		g := NewWithT(t)
		svc := &corev1.Service{
			Status: corev1.ServiceStatus{
				LoadBalancer: corev1.LoadBalancerStatus{
					Ingress: []corev1.LoadBalancerIngress{{IP: "10.0.0.1"}},
				},
			},
		}

		msg, err := CollectLBMessageIfNotProvisioned(svc, &fakeMessageCollector{})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(msg).To(BeEmpty())
	})

	t.Run("When load balancer is not provisioned and no events it should return default message", func(t *testing.T) {
		g := NewWithT(t)
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "my-svc"},
		}

		msg, err := CollectLBMessageIfNotProvisioned(svc, &fakeMessageCollector{})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(msg).To(ContainSubstring("my-svc load balancer is not provisioned"))
	})

	t.Run("When load balancer is not provisioned and events exist it should include event messages", func(t *testing.T) {
		g := NewWithT(t)
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "my-svc"},
		}
		collector := &fakeMessageCollector{messages: []string{"quota exceeded"}}

		msg, err := CollectLBMessageIfNotProvisioned(svc, collector)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(msg).To(ContainSubstring("quota exceeded"))
	})

	t.Run("When message collector returns an error it should return the error with a default message", func(t *testing.T) {
		g := NewWithT(t)
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "my-svc"},
		}
		collector := &fakeMessageCollector{err: fmt.Errorf("event list failed")}

		msg, err := CollectLBMessageIfNotProvisioned(svc, collector)
		g.Expect(err).To(MatchError(ContainSubstring("event list failed")))
		g.Expect(msg).To(ContainSubstring("my-svc load balancer is not provisioned"))
	})
}
