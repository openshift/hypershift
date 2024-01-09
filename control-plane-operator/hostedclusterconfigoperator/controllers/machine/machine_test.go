package machine

import (
	"context"
	"fmt"
	"math/rand"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/openshift/hypershift/client/clientset/clientset/scheme"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"
	kubevirtv1 "kubevirt.io/api/core/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	hyperv1 "github.com/openshift/hypershift/api/types/hypershift/v1beta1"
)

func TestReconcileDefaultIngressEndpoints(t *testing.T) {
	machineTypeMeta := metav1.TypeMeta{
		Kind:       "Machine",
		APIVersion: capiv1.GroupVersion.String(),
	}
	virtualmachineTypeMeta := metav1.TypeMeta{
		Kind:       "VirtualMachine",
		APIVersion: kubevirtv1.GroupVersion.String(),
	}
	newMachineMeta := func(namespace, name string) metav1.ObjectMeta {
		return metav1.ObjectMeta{
			Namespace:       namespace,
			Name:            name,
			UID:             types.UID(name + "-uid"),
			ResourceVersion: fmt.Sprintf("%d", rand.Intn(100)),
		}
	}

	worker1Meta := newMachineMeta("ns1-cluster1", "worker1")
	vm1Meta := newMachineMeta("ns1-cluster1", "workervm1")

	protocolTCP := corev1.ProtocolTCP

	hyperv1.AddToScheme(scheme.Scheme)
	kubevirtv1.AddToScheme(scheme.Scheme)
	discoveryv1.AddToScheme(scheme.Scheme)
	corev1.AddToScheme(scheme.Scheme)
	testCases := []struct {
		name                          string
		virtualmachine                *kubevirtv1.VirtualMachine
		machine                       *capiv1.Machine
		ingressSvc                    *corev1.Service
		ingressEndpointSlices         []discoveryv1.EndpointSlice
		hcp                           *hyperv1.HostedControlPlane
		expectedIngressEndpointSlices []discoveryv1.EndpointSlice
		error                         bool
	}{
		{
			name:       "Without service",
			ingressSvc: nil,
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns1-cluster1",
					Name:      "cluster1",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Kubevirt: &hyperv1.KubevirtPlatformSpec{
							GenerateID: "foobar",
						},
					},
				},
			},
			error: true,
		},
		{
			name: "With selector at ingress service",
			ingressSvc: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns1-cluster1",
					Name:      "default-ingress-passthrough-service-foobar",
				},
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{{
						TargetPort: intstr.FromInt(2222),
					}},
					Selector: map[string]string{
						"key1": "value1",
					},
				},
			},
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns1-cluster1",
					Name:      "cluster1",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Kubevirt: &hyperv1.KubevirtPlatformSpec{
							GenerateID: "foobar",
						},
					},
				},
			},
		},
		{
			name: "Without virtual machine",
			machine: &capiv1.Machine{
				TypeMeta:   machineTypeMeta,
				ObjectMeta: worker1Meta,
				Spec: capiv1.MachineSpec{
					InfrastructureRef: corev1.ObjectReference{
						Kind:       virtualmachineTypeMeta.Kind,
						APIVersion: virtualmachineTypeMeta.APIVersion,
						Namespace:  vm1Meta.Namespace,
						Name:       vm1Meta.Name,
					},
				},
			},
			ingressSvc: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns1-cluster1",
					Name:      "default-ingress-passthrough-service-foobar",
				},
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{{
						TargetPort: intstr.FromInt(2222),
					}},
				},
			},
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns1-cluster1",
					Name:      "cluster1",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Kubevirt: &hyperv1.KubevirtPlatformSpec{
							GenerateID: "foobar",
						},
					},
				},
			},
		},
		{
			name: "With Running machine with internal ipv4 and service with target port and no endpoints",
			virtualmachine: &kubevirtv1.VirtualMachine{
				ObjectMeta: vm1Meta,
			},
			machine: &capiv1.Machine{
				TypeMeta:   machineTypeMeta,
				ObjectMeta: worker1Meta,
				Spec: capiv1.MachineSpec{
					InfrastructureRef: corev1.ObjectReference{
						Kind:       virtualmachineTypeMeta.Kind,
						APIVersion: virtualmachineTypeMeta.APIVersion,
						Namespace:  vm1Meta.Namespace,
						Name:       vm1Meta.Name,
					},
				},
				Status: capiv1.MachineStatus{
					Phase: string(capiv1.MachinePhaseRunning),
					Addresses: []capiv1.MachineAddress{{
						Type:    capiv1.MachineInternalIP,
						Address: "192.168.1.3",
					}},
				},
			},
			ingressSvc: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns1-cluster1",
					Name:      "default-ingress-passthrough-service-foobar",
				},
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{{
						TargetPort: intstr.FromInt(2222),
						Protocol:   corev1.ProtocolTCP,
					}},
					IPFamilies: []corev1.IPFamily{corev1.IPv4Protocol},
				},
			},
			expectedIngressEndpointSlices: []discoveryv1.EndpointSlice{{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns1-cluster1",
					Name:      "default-ingress-passthrough-service-foobar-" + worker1Meta.Name + "-ipv4",
					Labels: map[string]string{
						"kubernetes.io/service-name":             "default-ingress-passthrough-service-foobar",
						"endpointslice.kubernetes.io/managed-by": "control-plane-operator.hypershift.openshift.io",
					},
					ResourceVersion: "1",
					OwnerReferences: []metav1.OwnerReference{{
						APIVersion:         virtualmachineTypeMeta.APIVersion,
						Kind:               virtualmachineTypeMeta.Kind,
						UID:                vm1Meta.UID,
						Name:               vm1Meta.Name,
						Controller:         pointer.Bool(true),
						BlockOwnerDeletion: pointer.Bool(true),
					}},
				},
				AddressType: discoveryv1.AddressTypeIPv4,
				Endpoints: []discoveryv1.Endpoint{{
					Addresses: []string{"192.168.1.3"},
				}},
				Ports: []discoveryv1.EndpointPort{{
					Port:     pointer.Int32(2222),
					Protocol: &protocolTCP,
				}},
			}},
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns1-cluster1",
					Name:      "cluster1",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Kubevirt: &hyperv1.KubevirtPlatformSpec{
							GenerateID: "foobar",
						},
					},
				},
			},
		},
		{
			name: "With Failing machine with internal ipv4 and service with target port and endpoints",
			virtualmachine: &kubevirtv1.VirtualMachine{
				ObjectMeta: vm1Meta,
			},
			machine: &capiv1.Machine{
				TypeMeta:   machineTypeMeta,
				ObjectMeta: worker1Meta,
				Spec: capiv1.MachineSpec{
					InfrastructureRef: corev1.ObjectReference{
						Kind:       virtualmachineTypeMeta.Kind,
						APIVersion: virtualmachineTypeMeta.APIVersion,
						Namespace:  vm1Meta.Namespace,
						Name:       vm1Meta.Name,
					},
				},
				Status: capiv1.MachineStatus{
					Phase: string(capiv1.MachinePhaseFailed),
					Addresses: []capiv1.MachineAddress{{
						Type:    capiv1.MachineInternalIP,
						Address: "192.168.1.3",
					}},
				},
			},
			ingressSvc: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns1-cluster1",
					Name:      "default-ingress-passthrough-service-foobar",
				},
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{{
						TargetPort: intstr.FromInt(2222),
						Protocol:   corev1.ProtocolTCP,
					}},
					IPFamilies: []corev1.IPFamily{corev1.IPv4Protocol},
				},
			},
			ingressEndpointSlices: []discoveryv1.EndpointSlice{{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns1-cluster1",
					Name:      "default-ingress-passthrough-service-foobar-" + worker1Meta.Name + "-ipv4",
					Labels: map[string]string{
						"kubernetes.io/service-name":             "default-ingress-passthrough-service-foobar",
						"endpointslice.kubernetes.io/managed-by": "control-plane-operator.hypershift.openshift.io",
					},
					ResourceVersion: "1",
					OwnerReferences: []metav1.OwnerReference{{
						APIVersion:         machineTypeMeta.APIVersion,
						Kind:               machineTypeMeta.Kind,
						UID:                worker1Meta.UID,
						Name:               worker1Meta.Name,
						Controller:         pointer.Bool(true),
						BlockOwnerDeletion: pointer.Bool(true),
					}},
				},
				AddressType: discoveryv1.AddressTypeIPv4,
				Endpoints: []discoveryv1.Endpoint{{
					Addresses: []string{"192.168.1.3"},
				}},
				Ports: []discoveryv1.EndpointPort{{
					Port:     pointer.Int32(2222),
					Protocol: &protocolTCP,
				}},
			}},
			expectedIngressEndpointSlices: []discoveryv1.EndpointSlice{{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns1-cluster1",
					Name:      "default-ingress-passthrough-service-foobar-" + worker1Meta.Name + "-ipv4",
					Labels: map[string]string{
						"kubernetes.io/service-name":             "default-ingress-passthrough-service-foobar",
						"endpointslice.kubernetes.io/managed-by": "control-plane-operator.hypershift.openshift.io",
					},
					ResourceVersion: "2",
					OwnerReferences: []metav1.OwnerReference{{
						APIVersion:         machineTypeMeta.APIVersion,
						Kind:               machineTypeMeta.Kind,
						UID:                worker1Meta.UID,
						Name:               worker1Meta.Name,
						Controller:         pointer.Bool(true),
						BlockOwnerDeletion: pointer.Bool(true),
					}},
				},
				AddressType: discoveryv1.AddressTypeIPv4,
				Endpoints:   []discoveryv1.Endpoint{},
				Ports:       []discoveryv1.EndpointPort{},
			}},
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns1-cluster1",
					Name:      "cluster1",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Kubevirt: &hyperv1.KubevirtPlatformSpec{
							GenerateID: "foobar",
						},
					},
				},
			},
		},

		{
			name: "With Running machine with internal dual stack address and service with target port and no endpoints",
			virtualmachine: &kubevirtv1.VirtualMachine{
				ObjectMeta: vm1Meta,
			},
			machine: &capiv1.Machine{
				TypeMeta:   machineTypeMeta,
				ObjectMeta: worker1Meta,
				Spec: capiv1.MachineSpec{
					InfrastructureRef: corev1.ObjectReference{
						Kind:       virtualmachineTypeMeta.Kind,
						APIVersion: virtualmachineTypeMeta.APIVersion,
						Namespace:  vm1Meta.Namespace,
						Name:       vm1Meta.Name,
					},
				},
				Status: capiv1.MachineStatus{
					Phase: string(capiv1.MachinePhaseRunning),
					Addresses: []capiv1.MachineAddress{
						{
							Type:    capiv1.MachineInternalIP,
							Address: "192.168.1.3",
						},
						{
							Type:    capiv1.MachineInternalIP,
							Address: "2001:db8:a0b:12f0::3",
						},
					},
				},
			},
			ingressSvc: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns1-cluster1",
					Name:      "default-ingress-passthrough-service-foobar",
				},
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{{
						TargetPort: intstr.FromInt(2222),
						Protocol:   corev1.ProtocolTCP,
					}},
					IPFamilies: []corev1.IPFamily{corev1.IPv4Protocol, corev1.IPv6Protocol},
				},
			},
			expectedIngressEndpointSlices: []discoveryv1.EndpointSlice{
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "ns1-cluster1",
						Name:      "default-ingress-passthrough-service-foobar-" + worker1Meta.Name + "-ipv4",
						Labels: map[string]string{
							"kubernetes.io/service-name":             "default-ingress-passthrough-service-foobar",
							"endpointslice.kubernetes.io/managed-by": "control-plane-operator.hypershift.openshift.io",
						},
						ResourceVersion: "1",
						OwnerReferences: []metav1.OwnerReference{{
							APIVersion:         virtualmachineTypeMeta.APIVersion,
							Kind:               virtualmachineTypeMeta.Kind,
							UID:                vm1Meta.UID,
							Name:               vm1Meta.Name,
							Controller:         pointer.Bool(true),
							BlockOwnerDeletion: pointer.Bool(true),
						}},
					},
					AddressType: discoveryv1.AddressTypeIPv4,
					Endpoints: []discoveryv1.Endpoint{{
						Addresses: []string{"192.168.1.3"},
					}},
					Ports: []discoveryv1.EndpointPort{{
						Port:     pointer.Int32(2222),
						Protocol: &protocolTCP,
					}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "ns1-cluster1",
						Name:      "default-ingress-passthrough-service-foobar-" + worker1Meta.Name + "-ipv6",
						Labels: map[string]string{
							"kubernetes.io/service-name":             "default-ingress-passthrough-service-foobar",
							"endpointslice.kubernetes.io/managed-by": "control-plane-operator.hypershift.openshift.io",
						},
						ResourceVersion: "1",
						OwnerReferences: []metav1.OwnerReference{{
							APIVersion:         virtualmachineTypeMeta.APIVersion,
							Kind:               virtualmachineTypeMeta.Kind,
							UID:                vm1Meta.UID,
							Name:               vm1Meta.Name,
							Controller:         pointer.Bool(true),
							BlockOwnerDeletion: pointer.Bool(true),
						}},
					},
					AddressType: discoveryv1.AddressTypeIPv6,
					Endpoints: []discoveryv1.Endpoint{{
						Addresses: []string{"2001:db8:a0b:12f0::3"},
					}},
					Ports: []discoveryv1.EndpointPort{{
						Port:     pointer.Int32(2222),
						Protocol: &protocolTCP,
					}},
				},
			},
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns1-cluster1",
					Name:      "cluster1",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Kubevirt: &hyperv1.KubevirtPlatformSpec{
							GenerateID: "foobar",
						},
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			objects := []client.Object{}
			if tc.ingressSvc != nil {
				objects = append(objects, tc.ingressSvc)
			}
			for _, ingressEndpointSlice := range tc.ingressEndpointSlices {
				objects = append(objects, &ingressEndpointSlice)
			}
			if tc.virtualmachine != nil {
				objects = append(objects, tc.virtualmachine)
			}
			g := NewWithT(t)
			r := &reconciler{
				client:                 fake.NewClientBuilder().Build(),
				kubevirtInfraClient:    fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(objects...).Build(),
				CreateOrUpdateProvider: &simpleCreateOrUpdater{},
			}
			err := r.reconcileKubevirtDefaultIngressEndpoints(context.Background(), tc.hcp, tc.machine)
			g.Expect(err != nil).To(Equal(tc.error), func() string {
				if !tc.error {
					return fmt.Sprintf("unexpected error: %v", err)
				} else {
					return "missing expected error"
				}
			})
			obtainedEndpointSliceList := discoveryv1.EndpointSliceList{}
			g.Expect(r.kubevirtInfraClient.List(context.Background(), &obtainedEndpointSliceList)).To(Succeed())
			g.Expect(obtainedEndpointSliceList.Items).To(ConsistOf(tc.expectedIngressEndpointSlices))
		})
	}
}

type simpleCreateOrUpdater struct{}

func (*simpleCreateOrUpdater) CreateOrUpdate(ctx context.Context, c client.Client, obj client.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {
	return controllerutil.CreateOrUpdate(ctx, c, obj, f)
}
