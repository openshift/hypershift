package etcd

import (
	"context"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	"github.com/openshift/hypershift/support/upsert"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestReconcileCreatesUDNEndpointSliceAndOrphansDefault(t *testing.T) {
	ctx := context.Background()
	ns := "clusters-test"
	hcpName := "hcp"

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ns,
			Name:      hcpName,
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.KubevirtPlatform,
			},
		},
	}

	etcdPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ns,
			Name:      "etcd-0",
			UID:       types.UID("pod-uid"),
			Labels:    map[string]string{"app": "etcd"},
			Annotations: map[string]string{
				ovnPodNetworksAnnotationKey: `{"default":{"ip_address":"192.168.0.10/24","role":"secondary"},"` + ns + `/myudn":{"ip_address":"10.150.0.10/24","role":"primary"}}`,
			},
		},
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: corev1.ConditionTrue},
			},
		},
	}

	etcdClientSvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ns,
			Name:      etcdClientServiceName,
			UID:       types.UID("svc-client-uid"),
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: "None",
			Ports: []corev1.ServicePort{
				{Name: "etcd-client", Protocol: corev1.ProtocolTCP, Port: 2379},
				{Name: "metrics", Protocol: corev1.ProtocolTCP, Port: 2381},
			},
			Selector: map[string]string{"app": "etcd"},
		},
	}

	etcdDiscoverySvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ns,
			Name:      etcdDiscoveryServiceName,
			UID:       types.UID("svc-discovery-uid"),
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: "None",
			Ports: []corev1.ServicePort{
				{Name: "peer", Protocol: corev1.ProtocolTCP, Port: 2380},
				{Name: "etcd-client", Protocol: corev1.ProtocolTCP, Port: 2379},
			},
			Selector: map[string]string{"app": "etcd"},
		},
	}

	defaultSlice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ns,
			Name:      "etcd-client-default",
			Labels: map[string]string{
				discoveryv1.LabelServiceName: etcdClientServiceName,
				discoveryv1.LabelManagedBy:   defaultEndpointSliceController,
			},
		},
		AddressType: discoveryv1.AddressTypeIPv4,
		Endpoints: []discoveryv1.Endpoint{
			{Addresses: []string{"192.168.0.10"}},
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(api.Scheme).
		WithObjects(hcp, etcdPod, etcdClientSvc, etcdDiscoverySvc, defaultSlice).
		Build()

	r := &reconciler{
		client:                 c,
		hcpKey:                 types.NamespacedName{Namespace: ns, Name: hcpName},
		CreateOrUpdateProvider: upsert.New(false),
	}

	if _, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: r.hcpKey}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	udnSlice := &discoveryv1.EndpointSlice{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: "etcd-client-udn-ipv4"}, udnSlice); err != nil {
		t.Fatalf("expected UDN EndpointSlice to be created: %v", err)
	}
	if udnSlice.Labels[discoveryv1.LabelServiceName] != etcdClientServiceName {
		t.Fatalf("expected kubernetes.io/service-name label to be %q, got %q", etcdClientServiceName, udnSlice.Labels[discoveryv1.LabelServiceName])
	}
	if udnSlice.Labels[ovnUDNServiceNameLabelKey] != etcdClientServiceName {
		t.Fatalf("expected ovn service-name label to be %q, got %q", etcdClientServiceName, udnSlice.Labels[ovnUDNServiceNameLabelKey])
	}
	if udnSlice.Annotations[ovnUDNEndpointSliceNetworkAnnoKey] != ns+"_myudn" {
		t.Fatalf("expected endpointslice-network annotation to be %q, got %q", ns+"_myudn", udnSlice.Annotations[ovnUDNEndpointSliceNetworkAnnoKey])
	}
	if len(udnSlice.Endpoints) != 1 || len(udnSlice.Endpoints[0].Addresses) != 1 || udnSlice.Endpoints[0].Addresses[0] != "10.150.0.10" {
		t.Fatalf("expected UDN endpoint address to be 10.150.0.10, got %#v", udnSlice.Endpoints)
	}
	if udnSlice.Endpoints[0].Conditions.Ready == nil || *udnSlice.Endpoints[0].Conditions.Ready != true {
		t.Fatalf("expected endpoint to be ready, got %#v", udnSlice.Endpoints[0].Conditions.Ready)
	}
	if udnSlice.Endpoints[0].Conditions.Serving == nil || *udnSlice.Endpoints[0].Conditions.Serving != true {
		t.Fatalf("expected endpoint to be serving, got %#v", udnSlice.Endpoints[0].Conditions.Serving)
	}

	updatedDefault := &discoveryv1.EndpointSlice{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(defaultSlice), updatedDefault); err != nil {
		t.Fatalf("expected default EndpointSlice to still exist: %v", err)
	}
	if updatedDefault.Labels != nil {
		if _, ok := updatedDefault.Labels[discoveryv1.LabelServiceName]; ok {
			t.Fatalf("expected default slice to be orphaned (no service-name label), got labels=%v", updatedDefault.Labels)
		}
		if _, ok := updatedDefault.Labels[discoveryv1.LabelManagedBy]; ok {
			t.Fatalf("expected default slice to be orphaned (no managed-by label), got labels=%v", updatedDefault.Labels)
		}
	}
}
