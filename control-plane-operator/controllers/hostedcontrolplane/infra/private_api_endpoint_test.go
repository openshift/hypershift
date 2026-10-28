package infra

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	cpomanifests "github.com/openshift/hypershift/pkg/manifests/cpo"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestDeletePrivateAPIServerService(t *testing.T) {
	t.Parallel()
	const cpoFinalizer = "hypershift.openshift.io/control-plane-operator-finalizer"
	const hoFinalizer = "hypershift.openshift.io/hypershift-operator-finalizer"
	tests := []struct {
		name             string
		authorized       bool
		publicReady      bool
		endpointService  bool
		missingService   bool
		pendingFinalizer string
		failDelete       bool
		wantPrivate      bool
	}{
		{name: "When worker cutover is incomplete, it should preserve PrivateLink and the private Service", publicReady: true, endpointService: true, wantPrivate: true},
		{name: "When the public LB is pending, it should preserve PrivateLink and the private Service", authorized: true, endpointService: true, wantPrivate: true},
		{name: "When cutover is complete, it should wait for PrivateLink finalizers before deleting the private Service", authorized: true, publicReady: true, endpointService: true, wantPrivate: true},
		{name: "When only the CPO finalizer remains, it should retain the private Service until AWS cleanup completes", authorized: true, publicReady: true, endpointService: true, pendingFinalizer: cpoFinalizer, wantPrivate: true},
		{name: "When PrivateLink cleanup has completed, it should delete the private Service", authorized: true, publicReady: true},
		{name: "When PrivateLink deletion fails, it should report the error and retain the private Service", authorized: true, publicReady: true, endpointService: true, failDelete: true, wantPrivate: true},
		{name: "When the Service is already absent, it should finish the remaining PrivateLink cleanup", authorized: true, publicReady: true, endpointService: true, missingService: true},
		{name: "When cleanup is already complete, it should reconcile repeatedly without recreating private resources", authorized: true, publicReady: true, missingService: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "hc", Namespace: "clusters-hc"},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform, AWS: &hyperv1.AWSPlatformSpec{EndpointAccess: hyperv1.Public}},
					Services: []hyperv1.ServicePublishingStrategyMapping{{Service: hyperv1.APIServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.LoadBalancer}}},
				},
			}
			if tt.authorized {
				hcp.Annotations = map[string]string{config.PrivateAPIServerCleanupAnnotation: "public-endpoint"}
			}
			publicService := cpomanifests.KubeAPIServerService(hcp.Namespace)
			if tt.publicReady {
				publicService.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{Hostname: "public.elb.us-east-1.amazonaws.com"}}
			}
			privateService := manifests.KubeAPIServerPrivateService(hcp.Namespace)
			privateService.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{Hostname: "private.elb.us-east-1.amazonaws.com"}}
			objects := []client.Object{publicService}
			if !tt.missingService {
				objects = append(objects, privateService)
			}
			endpointService := &hyperv1.AWSEndpointService{ObjectMeta: metav1.ObjectMeta{
				Name: privateService.Name, Namespace: hcp.Namespace,
				Finalizers: []string{cpoFinalizer, hoFinalizer},
			}}
			if tt.endpointService {
				objects = append(objects, endpointService)
			}
			c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objects...).WithInterceptorFuncs(interceptor.Funcs{
				Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					if _, ok := obj.(*hyperv1.AWSEndpointService); ok && tt.failDelete {
						return apierrors.NewServiceUnavailable("API unavailable")
					}
					return c.Delete(ctx, obj, opts...)
				},
			}).Build()
			checkPrivateService := func(wantExists bool) {
				t.Helper()
				err := c.Get(t.Context(), client.ObjectKeyFromObject(privateService), privateService)
				if wantExists {
					g.Expect(err).NotTo(HaveOccurred())
				} else {
					g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
				}
			}
			r := NewReconciler(c, "")
			err := r.deletePrivateAPIServerService(t.Context(), hcp, publicService)
			if tt.failDelete {
				g.Expect(err).To(MatchError(ContainSubstring("failed to delete API server endpoint service")))
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
			checkPrivateService(tt.wantPrivate)
			if tt.endpointService {
				g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(endpointService), endpointService)).To(Succeed())
				wantDeleting := tt.authorized && tt.publicReady && !tt.failDelete
				g.Expect(!endpointService.DeletionTimestamp.IsZero()).To(Equal(wantDeleting))
				if wantDeleting {
					// Repeated reconciles must not delete the Service while either AWS finalizer remains.
					g.Expect(r.deletePrivateAPIServerService(t.Context(), hcp, publicService)).To(Succeed())
					checkPrivateService(!tt.missingService)
					pendingFinalizer := tt.pendingFinalizer
					if pendingFinalizer == "" {
						pendingFinalizer = hoFinalizer
					}
					endpointService.Finalizers = []string{pendingFinalizer}
					g.Expect(c.Update(t.Context(), endpointService)).To(Succeed())
					g.Expect(r.deletePrivateAPIServerService(t.Context(), hcp, publicService)).To(Succeed())
					checkPrivateService(!tt.missingService)
					endpointService.Finalizers = nil
					g.Expect(c.Update(t.Context(), endpointService)).To(Succeed())
					g.Expect(r.deletePrivateAPIServerService(t.Context(), hcp, publicService)).To(Succeed())
					g.Expect(apierrors.IsNotFound(c.Get(t.Context(), client.ObjectKeyFromObject(privateService), privateService))).To(BeTrue())
				}
			}
			if tt.authorized && tt.publicReady && !tt.failDelete {
				for range 2 {
					g.Expect(r.deletePrivateAPIServerService(t.Context(), hcp, publicService)).To(Succeed())
					checkPrivateService(false)
					g.Expect(apierrors.IsNotFound(c.Get(t.Context(), client.ObjectKeyFromObject(endpointService), endpointService))).To(BeTrue())
				}
			}
			g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(publicService), publicService)).To(Succeed())
		})
	}
}
