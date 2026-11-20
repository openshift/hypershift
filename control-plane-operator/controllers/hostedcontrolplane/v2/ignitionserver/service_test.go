package ignitionserver

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/upsert"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func TestIgnitionServiceReconcile(t *testing.T) {
	testCases := []struct {
		name         string
		platform     hyperv1.PlatformType
		services     []hyperv1.ServicePublishingStrategyMapping
		svc_existing *corev1.Service
		svc_out      corev1.Service
		err          error
	}{
		{
			name:     "IBM Cloud, NodePort strategy, NodePort service, expected to fill port number from strategy",
			platform: hyperv1.IBMCloudPlatform,
			services: []hyperv1.ServicePublishingStrategyMapping{
				{
					Service: hyperv1.Ignition,
					ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
						Type:     hyperv1.NodePort,
						NodePort: &hyperv1.NodePortPublishingStrategy{Port: 1125},
					},
				},
			},
			svc_existing: &corev1.Service{
				Spec: corev1.ServiceSpec{
					Type: corev1.ServiceTypeNodePort,
					Ports: []corev1.ServicePort{
						{},
					},
				},
			},
			svc_out: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeNodePort,
				Ports: []corev1.ServicePort{
					{
						Name:       "https",
						Protocol:   corev1.ProtocolTCP,
						Port:       443,
						TargetPort: intstr.IntOrString{IntVal: 9090},
						NodePort:   1125,
					},
				},
			}},
			err: nil,
		},
		{
			name:     "IBM Cloud, Route strategy, NodePort service with existing port number, expected not to change",
			platform: hyperv1.IBMCloudPlatform,
			services: []hyperv1.ServicePublishingStrategyMapping{
				{
					Service:                   hyperv1.Ignition,
					ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.Route},
				}},
			svc_existing: &corev1.Service{
				Spec: corev1.ServiceSpec{
					Type: corev1.ServiceTypeNodePort,
					Ports: []corev1.ServicePort{
						{
							NodePort: 1125,
						},
					},
				},
			},
			svc_out: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeNodePort,
				Ports: []corev1.ServicePort{
					{
						Name:       "https",
						Protocol:   corev1.ProtocolTCP,
						Port:       443,
						TargetPort: intstr.IntOrString{IntVal: 9090},
						NodePort:   1125,
					},
				},
			}},
			err: nil,
		},
		{
			name:     "Non-IBM Cloud, Route strategy, ClusterIP service, expected to not change",
			platform: hyperv1.AWSPlatform,
			services: []hyperv1.ServicePublishingStrategyMapping{
				{
					Service:                   hyperv1.Ignition,
					ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.Route},
				}},
			svc_out: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeClusterIP,
				Ports: []corev1.ServicePort{
					{
						Name:       "https",
						Protocol:   corev1.ProtocolTCP,
						Port:       443,
						TargetPort: intstr.IntOrString{IntVal: 9090},
					},
				},
			}},
			err: nil,
		},
		{
			name:     "IBM Cloud, Invalid strategy",
			platform: hyperv1.IBMCloudPlatform,
			services: []hyperv1.ServicePublishingStrategyMapping{
				{
					Service:                   hyperv1.Ignition,
					ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.LoadBalancer},
				}},
			err: fmt.Errorf("invalid publishing strategy for Ignition service: LoadBalancer"),
		},
		{
			name:     "IBM Cloud, strategy missing",
			platform: hyperv1.IBMCloudPlatform,
			services: []hyperv1.ServicePublishingStrategyMapping{},
			err:      fmt.Errorf("ignition service strategy not specified"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			clientBuilder := fake.NewClientBuilder().WithScheme(api.Scheme)
			if tc.svc_existing != nil {
				clientBuilder = clientBuilder.WithObjects(tc.svc_existing)
			}
			client := clientBuilder.Build()

			ctx := component.WorkloadContext{
				HCP: &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						Platform: hyperv1.PlatformSpec{Type: tc.platform},
						Services: tc.services,
					},
				},
				Client: client,
			}
			svc_out := corev1.Service{
				Spec: corev1.ServiceSpec{
					Type: corev1.ServiceTypeClusterIP,
					Ports: []corev1.ServicePort{
						{
							Name:       "https",
							Protocol:   corev1.ProtocolTCP,
							Port:       443,
							TargetPort: intstr.IntOrString{IntVal: 9090},
						},
					},
					Selector: map[string]string{"app": "ignition-server"},
				}}

			err := adaptService(ctx, &svc_out)

			g := NewWithT(t)
			if tc.err == nil {
				g.Expect(err).To(BeNil())
				g.Expect(svc_out.Spec.Type).To(Equal(tc.svc_out.Spec.Type))
				g.Expect(svc_out.Spec.Ports).To(Equal(tc.svc_out.Spec.Ports))
			} else {
				g.Expect(tc.err.Error()).To(Equal(err.Error()))
			}
		})
	}

	const namespace = "clusters-example"
	ignitionStrategy := func(strategy hyperv1.ServicePublishingStrategy) []hyperv1.ServicePublishingStrategyMapping {
		return []hyperv1.ServicePublishingStrategyMapping{{Service: hyperv1.Ignition, ServicePublishingStrategy: strategy}}
	}
	// renderService loads the service manifest and adapts it the same way the
	// component framework does before applying it.
	renderService := func(t *testing.T, cpContext component.WorkloadContext) *corev1.Service {
		t.Helper()
		g := NewWithT(t)
		obj, _, err := assets.LoadManifest(ComponentName, "service.yaml")
		g.Expect(err).ToNot(HaveOccurred())
		svc := obj.(*corev1.Service)
		svc.SetNamespace(namespace)
		g.Expect(adaptService(cpContext, svc)).To(Succeed())
		return svc
	}
	existingService := func(spec corev1.ServiceSpec) *corev1.Service {
		return &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: ComponentName, Namespace: namespace},
			Spec:       spec,
		}
	}

	ipFamilyPolicyTestCases := []struct {
		name             string
		platform         hyperv1.PlatformType
		services         []hyperv1.ServicePublishingStrategyMapping
		existing         *corev1.Service
		expectedType     corev1.ServiceType
		expectedNodePort int32
	}{
		{
			name:         "When the platform is AWS with a Route strategy, it should prefer dual-stack on a ClusterIP service",
			platform:     hyperv1.AWSPlatform,
			services:     ignitionStrategy(hyperv1.ServicePublishingStrategy{Type: hyperv1.Route}),
			expectedType: corev1.ServiceTypeClusterIP,
		},
		{
			name:         "When the platform is Agent with a NodePort strategy, it should prefer dual-stack on a ClusterIP service",
			platform:     hyperv1.AgentPlatform,
			services:     ignitionStrategy(hyperv1.ServicePublishingStrategy{Type: hyperv1.NodePort}),
			expectedType: corev1.ServiceTypeClusterIP,
		},
		{
			name:         "When the platform is None with a Route strategy, it should prefer dual-stack on a ClusterIP service",
			platform:     hyperv1.NonePlatform,
			services:     ignitionStrategy(hyperv1.ServicePublishingStrategy{Type: hyperv1.Route}),
			expectedType: corev1.ServiceTypeClusterIP,
		},
		{
			name:             "When the platform is IBM Cloud with a NodePort strategy, it should prefer dual-stack and use the strategy port",
			platform:         hyperv1.IBMCloudPlatform,
			services:         ignitionStrategy(hyperv1.ServicePublishingStrategy{Type: hyperv1.NodePort, NodePort: &hyperv1.NodePortPublishingStrategy{Port: 31125}}),
			expectedType:     corev1.ServiceTypeNodePort,
			expectedNodePort: 31125,
		},
		{
			name:     "When the platform is IBM Cloud with a Route strategy and an existing NodePort service, it should prefer dual-stack and keep the port",
			platform: hyperv1.IBMCloudPlatform,
			services: ignitionStrategy(hyperv1.ServicePublishingStrategy{Type: hyperv1.Route}),
			existing: existingService(corev1.ServiceSpec{
				Type:  corev1.ServiceTypeNodePort,
				Ports: []corev1.ServicePort{{Name: "https", Port: 443, NodePort: 30125}},
			}),
			expectedType:     corev1.ServiceTypeNodePort,
			expectedNodePort: 30125,
		},
	}
	for _, tc := range ipFamilyPolicyTestCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			clientBuilder := fake.NewClientBuilder().WithScheme(api.Scheme)
			if tc.existing != nil {
				clientBuilder = clientBuilder.WithObjects(tc.existing)
			}
			cpContext := component.WorkloadContext{
				Context: t.Context(),
				HCP: &hyperv1.HostedControlPlane{
					ObjectMeta: metav1.ObjectMeta{Namespace: namespace},
					Spec: hyperv1.HostedControlPlaneSpec{
						Platform: hyperv1.PlatformSpec{Type: tc.platform},
						Services: tc.services,
					},
				},
				Client: clientBuilder.Build(),
			}

			svc := renderService(t, cpContext)

			g.Expect(svc.Spec.IPFamilyPolicy).To(Equal(ptr.To(corev1.IPFamilyPolicyPreferDualStack)))
			g.Expect(svc.Spec.Type).To(Equal(tc.expectedType))
			g.Expect(svc.Spec.Ports).To(HaveLen(1))
			g.Expect(svc.Spec.Ports[0].NodePort).To(Equal(tc.expectedNodePort))
			// Address allocation is left to the API server.
			g.Expect(svc.Spec.ClusterIP).To(BeEmpty())
			g.Expect(svc.Spec.ClusterIPs).To(BeNil())
			g.Expect(svc.Spec.IPFamilies).To(BeNil())
		})
	}

	// Existing services are updated by the apply provider with the rendered service.
	// The fake client does not allocate addresses or ports like the API server does,
	// so these cases check the update request and that a later reconcile of the
	// API server's dual-stack result is a no-op.
	existingServiceTestCases := []struct {
		name                    string
		platform                hyperv1.PlatformType
		services                []hyperv1.ServicePublishingStrategyMapping
		existing                corev1.ServiceSpec
		expectedRequestNodePort int32
	}{
		{
			name:     "When an existing single-stack service is updated on AWS, it should request dual-stack without allocated addresses and then stay unchanged",
			platform: hyperv1.AWSPlatform,
			services: ignitionStrategy(hyperv1.ServicePublishingStrategy{Type: hyperv1.Route}),
			existing: corev1.ServiceSpec{
				Type:           corev1.ServiceTypeClusterIP,
				ClusterIP:      "172.30.12.34",
				ClusterIPs:     []string{"172.30.12.34"},
				IPFamilies:     []corev1.IPFamily{corev1.IPv4Protocol},
				IPFamilyPolicy: ptr.To(corev1.IPFamilyPolicySingleStack),
				Ports:          []corev1.ServicePort{{Name: "https", Port: 443, Protocol: corev1.ProtocolTCP, TargetPort: intstr.FromInt(9090)}},
				Selector:       map[string]string{"app": ComponentName},
			},
		},
		{
			name:     "When an existing single-stack NodePort service is updated on IBM Cloud with Route, it should request dual-stack with the existing port and then stay unchanged",
			platform: hyperv1.IBMCloudPlatform,
			services: ignitionStrategy(hyperv1.ServicePublishingStrategy{Type: hyperv1.Route}),
			existing: corev1.ServiceSpec{
				Type:           corev1.ServiceTypeNodePort,
				ClusterIP:      "172.30.12.35",
				ClusterIPs:     []string{"172.30.12.35"},
				IPFamilies:     []corev1.IPFamily{corev1.IPv4Protocol},
				IPFamilyPolicy: ptr.To(corev1.IPFamilyPolicySingleStack),
				Ports:          []corev1.ServicePort{{Name: "https", Port: 443, Protocol: corev1.ProtocolTCP, TargetPort: intstr.FromInt(9090), NodePort: 30125}},
				Selector:       map[string]string{"app": ComponentName},
			},
			expectedRequestNodePort: 30125,
		},
	}
	for _, tc := range existingServiceTestCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			existing := existingService(*tc.existing.DeepCopy())
			existing.Labels = map[string]string{"app": ComponentName}
			existing.Annotations = map[string]string{upsert.DesiredStateHashAnnotation: "hash-of-previous-desired-state"}

			var updateRequests []*corev1.Service
			c := fake.NewClientBuilder().WithScheme(api.Scheme).
				WithObjects(existing).
				WithInterceptorFuncs(interceptor.Funcs{
					// The control-plane-operator reads through the manager cache, which sets
					// apiVersion and kind on returned objects. The fake client clears them,
					// which would make every apply compare as drift.
					Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						if err := c.Get(ctx, key, obj, opts...); err != nil {
							return err
						}
						obj.GetObjectKind().SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("Service"))
						return nil
					},
					Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
						if svc, ok := obj.(*corev1.Service); ok {
							updateRequests = append(updateRequests, svc.DeepCopy())
						}
						return c.Update(ctx, obj, opts...)
					},
				}).
				Build()
			cpContext := component.WorkloadContext{
				Context: t.Context(),
				HCP: &hyperv1.HostedControlPlane{
					ObjectMeta: metav1.ObjectMeta{Namespace: namespace},
					Spec: hyperv1.HostedControlPlaneSpec{
						Platform: hyperv1.PlatformSpec{Type: tc.platform},
						Services: tc.services,
					},
				},
				Client: c,
			}
			provider := upsert.NewApplyProvider(false)

			result, err := provider.ApplyManifest(t.Context(), c, renderService(t, cpContext))
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(controllerutil.OperationResultUpdated))
			g.Expect(updateRequests).To(HaveLen(1))
			request := updateRequests[0]
			g.Expect(request.Spec.IPFamilyPolicy).To(Equal(ptr.To(corev1.IPFamilyPolicyPreferDualStack)))
			g.Expect(request.Spec.Type).To(Equal(tc.existing.Type))
			g.Expect(request.Spec.Selector).To(Equal(tc.existing.Selector))
			g.Expect(request.Spec.Ports).To(HaveLen(1))
			g.Expect(request.Spec.Ports[0].Port).To(Equal(int32(443)))
			g.Expect(request.Spec.Ports[0].NodePort).To(Equal(tc.expectedRequestNodePort))
			// The update leaves allocated addresses out; the API server keeps the
			// existing ones and adds the second family on dual-stack clusters.
			g.Expect(request.Spec.ClusterIP).To(BeEmpty())
			g.Expect(request.Spec.ClusterIPs).To(BeNil())
			g.Expect(request.Spec.IPFamilies).To(BeNil())

			// Emulate the API server's dual-stack result: keep the primary ClusterIP and
			// NodePort, and append an IPv6 ClusterIP.
			stored := existingService(corev1.ServiceSpec{})
			g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(stored), stored)).To(Succeed())
			stored.Spec.ClusterIP = tc.existing.ClusterIP
			stored.Spec.ClusterIPs = []string{tc.existing.ClusterIP, "fd02::12:34"}
			stored.Spec.IPFamilies = []corev1.IPFamily{corev1.IPv4Protocol, corev1.IPv6Protocol}
			stored.Spec.Ports[0].NodePort = tc.existing.Ports[0].NodePort
			g.Expect(c.Update(t.Context(), stored)).To(Succeed())
			updateRequests = nil

			result, err = provider.ApplyManifest(t.Context(), c, renderService(t, cpContext))
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(controllerutil.OperationResultNone))
			g.Expect(updateRequests).To(BeEmpty())
		})
	}
}
