//go:build envtest

package envtest

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	cpomanifests "github.com/openshift/hypershift/pkg/manifests/cpo"
	"github.com/openshift/hypershift/support/upsert"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	deterministicLBAnnotation = "metallb.io/loadBalancerIPs"
	deterministicLBAddress    = "10.1.1.100"
	deterministicLBClass      = "metallb.io/metallb"
)

func deterministicLBStrategy() *hyperv1.ServicePublishingStrategy {
	return &hyperv1.ServicePublishingStrategy{
		Type: hyperv1.LoadBalancer,
		LoadBalancer: &hyperv1.LoadBalancerPublishingStrategy{
			ServiceAnnotations: map[string]string{deterministicLBAnnotation: deterministicLBAddress},
			LoadBalancerClass:  deterministicLBClass,
		},
	}
}

func deterministicLBHCP(namespace string, access hyperv1.AWSEndpointAccessType) *hyperv1.HostedControlPlane {
	return &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{Name: "hcp", Namespace: namespace},
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AWSPlatform,
				AWS:  &hyperv1.AWSPlatformSpec{EndpointAccess: access},
			},
			Services: []hyperv1.ServicePublishingStrategyMapping{
				{
					Service:                   hyperv1.APIServer,
					ServicePublishingStrategy: *deterministicLBStrategy(),
				},
			},
		},
	}
}

func createTestNamespace() *corev1.Namespace {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "deterministic-lb-"}}
	Expect(k8sClient.Create(ctx, ns)).To(Succeed(), "failed to create test namespace")
	DeferCleanup(func() {
		_ = k8sClient.Delete(ctx, ns)
	})
	return ns
}

func getService(namespace, name string) *corev1.Service {
	svc := &corev1.Service{}
	Expect(k8sClient.Get(ctx, crclient.ObjectKey{Namespace: namespace, Name: name}, svc)).To(Succeed(), fmt.Sprintf("failed to get Service %s/%s", namespace, name))
	return svc
}

var _ = Describe("Deterministic LB IP KAS Service reconciliation", Label("deterministic-lb-ip"), func() {
	createOrUpdate := upsert.New(false).CreateOrUpdate

	It("When a PublicAndPrivate HostedCluster is created, it should put the address annotations only on the public KAS Service", func() {
		ns := createTestNamespace()
		strategy := deterministicLBStrategy()
		hcp := deterministicLBHCP(ns.Name, hyperv1.PublicAndPrivate)

		publicSvc := cpomanifests.KubeAPIServerService(ns.Name)
		_, err := createOrUpdate(ctx, k8sClient, publicSvc, func() error {
			return kas.ReconcileService(publicSvc, strategy, nil, 6443, nil, hcp)
		})
		Expect(err).NotTo(HaveOccurred(), "failed to reconcile public KAS Service")

		privateSvc := manifests.KubeAPIServerPrivateService(ns.Name)
		_, err = createOrUpdate(ctx, k8sClient, privateSvc, func() error {
			return kas.ReconcilePrivateService(privateSvc, hcp, nil)
		})
		Expect(err).NotTo(HaveOccurred(), "failed to reconcile private KAS Service")

		public := getService(ns.Name, publicSvc.Name)
		Expect(public.Spec.Type).To(Equal(corev1.ServiceTypeLoadBalancer))
		Expect(public.Spec.LoadBalancerClass).To(HaveValue(Equal(deterministicLBClass)))
		Expect(public.Annotations).To(HaveKeyWithValue(deterministicLBAnnotation, deterministicLBAddress))

		private := getService(ns.Name, privateSvc.Name)
		Expect(private.Spec.Type).To(Equal(corev1.ServiceTypeLoadBalancer))
		Expect(private.Spec.LoadBalancerClass).To(HaveValue(Equal(deterministicLBClass)))
		Expect(private.Annotations).NotTo(HaveKey(deterministicLBAnnotation))
	})

	It("When a PublicAndPrivate HostedCluster transitions to Private, it should make the public KAS Service ClusterIP and move the annotations to the private Service", func() {
		ns := createTestNamespace()
		strategy := deterministicLBStrategy()
		publicPrivateHCP := deterministicLBHCP(ns.Name, hyperv1.PublicAndPrivate)
		privateHCP := deterministicLBHCP(ns.Name, hyperv1.Private)

		publicSvc := cpomanifests.KubeAPIServerService(ns.Name)
		_, err := createOrUpdate(ctx, k8sClient, publicSvc, func() error {
			return kas.ReconcileService(publicSvc, strategy, nil, 6443, nil, publicPrivateHCP)
		})
		Expect(err).NotTo(HaveOccurred(), "failed to reconcile public KAS Service before transition")

		privateSvc := manifests.KubeAPIServerPrivateService(ns.Name)
		_, err = createOrUpdate(ctx, k8sClient, privateSvc, func() error {
			return kas.ReconcilePrivateService(privateSvc, publicPrivateHCP, nil)
		})
		Expect(err).NotTo(HaveOccurred(), "failed to reconcile private KAS Service before transition")

		// Transition to Private. The public Service must stop being a LoadBalancer
		// and release the address annotations; the private Service becomes primary.
		publicSvc = cpomanifests.KubeAPIServerService(ns.Name)
		_, err = createOrUpdate(ctx, k8sClient, publicSvc, func() error {
			return kas.ReconcileService(publicSvc, strategy, nil, 6443, nil, privateHCP)
		})
		Expect(err).NotTo(HaveOccurred(), "the public KAS Service update must be accepted during the transition")

		privateSvc = manifests.KubeAPIServerPrivateService(ns.Name)
		_, err = createOrUpdate(ctx, k8sClient, privateSvc, func() error {
			return kas.ReconcilePrivateService(privateSvc, privateHCP, nil)
		})
		Expect(err).NotTo(HaveOccurred(), "failed to reconcile private KAS Service after transition")

		public := getService(ns.Name, publicSvc.Name)
		Expect(public.Spec.Type).To(Equal(corev1.ServiceTypeClusterIP), "public KAS Service must no longer be exposed through a LoadBalancer")
		// Kubernetes itself drops loadBalancerClass when the Service type is no
		// longer LoadBalancer, even if the update object still carries it.
		Expect(public.Spec.LoadBalancerClass).To(BeNil(), "loadBalancerClass must be cleared once the Service is not a LoadBalancer")

		private := getService(ns.Name, privateSvc.Name)
		Expect(private.Spec.Type).To(Equal(corev1.ServiceTypeLoadBalancer))
		Expect(private.Spec.LoadBalancerClass).To(HaveValue(Equal(deterministicLBClass)))
		Expect(private.Annotations).To(HaveKeyWithValue(deterministicLBAnnotation, deterministicLBAddress))
	})

	It("When a HostedCluster is created as Private, it should configure only the private KAS Service", func() {
		ns := createTestNamespace()
		strategy := deterministicLBStrategy()
		hcp := deterministicLBHCP(ns.Name, hyperv1.Private)

		publicSvc := cpomanifests.KubeAPIServerService(ns.Name)
		_, err := createOrUpdate(ctx, k8sClient, publicSvc, func() error {
			return kas.ReconcileService(publicSvc, strategy, nil, 6443, nil, hcp)
		})
		Expect(err).NotTo(HaveOccurred(), "failed to reconcile public KAS Service")

		privateSvc := manifests.KubeAPIServerPrivateService(ns.Name)
		_, err = createOrUpdate(ctx, k8sClient, privateSvc, func() error {
			return kas.ReconcilePrivateService(privateSvc, hcp, nil)
		})
		Expect(err).NotTo(HaveOccurred(), "failed to reconcile private KAS Service")

		public := getService(ns.Name, publicSvc.Name)
		Expect(public.Spec.Type).To(Equal(corev1.ServiceTypeClusterIP))
		Expect(public.Spec.LoadBalancerClass).To(BeNil())
		Expect(public.Annotations).NotTo(HaveKey(deterministicLBAnnotation))

		private := getService(ns.Name, privateSvc.Name)
		Expect(private.Spec.Type).To(Equal(corev1.ServiceTypeLoadBalancer))
		Expect(private.Spec.LoadBalancerClass).To(HaveValue(Equal(deterministicLBClass)))
		Expect(private.Annotations).To(HaveKeyWithValue(deterministicLBAnnotation, deterministicLBAddress))
	})

	It("When a Service changes from LoadBalancer to ClusterIP, the API server should clear loadBalancerClass", func() {
		ns := createTestNamespace()

		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "api-server", Namespace: ns.Name},
			Spec: corev1.ServiceSpec{
				Type:              corev1.ServiceTypeLoadBalancer,
				LoadBalancerClass: ptr.To(deterministicLBClass),
				Ports:             []corev1.ServicePort{{Port: 6443}},
			},
		}
		Expect(k8sClient.Create(ctx, svc)).To(Succeed())

		current := getService(ns.Name, svc.Name)
		current.Spec.Type = corev1.ServiceTypeClusterIP
		// Keep loadBalancerClass set on purpose. The API server's Service update
		// strategy drops type-dependent fields when the Service is no longer a
		// LoadBalancer, so the update succeeds and the field is cleared.
		Expect(k8sClient.Update(ctx, current)).To(Succeed())

		updated := getService(ns.Name, svc.Name)
		Expect(updated.Spec.Type).To(Equal(corev1.ServiceTypeClusterIP))
		Expect(updated.Spec.LoadBalancerClass).To(BeNil())
	})
})
