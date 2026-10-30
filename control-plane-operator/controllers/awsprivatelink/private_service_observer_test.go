package awsprivatelink

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/upsert"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kubefake "k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/go-logr/logr"
)

func TestPrivateServiceObserverReconcile(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		access        hyperv1.AWSEndpointAccessType
		missing       bool
		deleting      bool
		unprovisioned bool
		failGet       bool
		wantEndpoint  bool
	}{
		{name: "When a private load balancer is provisioned, it should create its endpoint service", access: hyperv1.Private, wantEndpoint: true},
		{name: "When public and private access is configured, it should retain PrivateLink reconciliation", access: hyperv1.PublicAndPrivate, wantEndpoint: true},
		{name: "When the cluster becomes public, it should not recreate the endpoint service during cleanup", access: hyperv1.Public},
		{name: "When the Service is being deleted, it should not recreate its endpoint service", access: hyperv1.Private, deleting: true},
		{name: "When the Service is missing, it should not create an endpoint service", access: hyperv1.Private, missing: true},
		{name: "When the LB is pending, it should wait without creating an endpoint service", access: hyperv1.Private, unprovisioned: true},
		{name: "When the Service cannot be read, it should propagate the error without creating an endpoint service", access: hyperv1.Private, failGet: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "hc", Namespace: "clusters-hc"},
				Spec:       hyperv1.HostedControlPlaneSpec{Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform, AWS: &hyperv1.AWSPlatformSpec{EndpointAccess: tt.access}}},
			}
			svc := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "kube-apiserver-private", Namespace: hcp.Namespace},
				Status:     corev1.ServiceStatus{LoadBalancer: corev1.LoadBalancerStatus{Ingress: []corev1.LoadBalancerIngress{{Hostname: "private-abc.elb.us-east-1.amazonaws.com"}}}},
			}
			if tt.deleting {
				now := metav1.Now()
				svc.DeletionTimestamp = &now
				svc.Finalizers = []string{"service.kubernetes.io/load-balancer-cleanup"}
			}
			if tt.unprovisioned {
				svc.Status.LoadBalancer.Ingress = nil
			}
			kubeClient := kubefake.NewClientset()
			if !tt.missing {
				_, err := kubeClient.CoreV1().Services(svc.Namespace).Create(t.Context(), svc, metav1.CreateOptions{})
				g.Expect(err).NotTo(HaveOccurred())
			}
			if tt.failGet {
				kubeClient.PrependReactor("get", "services", func(clienttesting.Action) (bool, runtime.Object, error) {
					return true, nil, apierrors.NewServiceUnavailable("API unavailable")
				})
			}
			c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(hcp).Build()
			r := &PrivateServiceObserver{Client: c, clientset: kubeClient, log: logr.Discard(), HCPNamespace: hcp.Namespace, ServiceName: svc.Name, CreateOrUpdateProvider: upsert.New(false)}
			_, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: svc.Namespace, Name: svc.Name}})
			if tt.failGet {
				g.Expect(err).To(MatchError(ContainSubstring("API unavailable")))
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
			endpoints := &hyperv1.AWSEndpointServiceList{}
			g.Expect(c.List(t.Context(), endpoints, client.InNamespace(hcp.Namespace))).To(Succeed())
			if tt.wantEndpoint {
				g.Expect(endpoints.Items).To(HaveLen(1))
				g.Expect(endpoints.Items[0].Spec.NetworkLoadBalancerName).To(Equal("private"))
			} else {
				g.Expect(endpoints.Items).To(BeEmpty())
			}
		})
	}
}
