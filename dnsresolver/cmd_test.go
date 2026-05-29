package dnsresolver

import (
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
)

func TestParseServiceName(t *testing.T) {
	tests := []struct {
		name        string
		dnsName     string
		expected    string
		expectError bool
	}{
		{
			name:     "When given a standard headless service DNS name it should extract the service name",
			dnsName:  "etcd-0.etcd-discovery.my-namespace.svc",
			expected: "etcd-discovery",
		},
		{
			name:     "When given a fully qualified DNS name it should extract the service name",
			dnsName:  "etcd-0.etcd-discovery.my-namespace.svc.cluster.local",
			expected: "etcd-discovery",
		},
		{
			name:     "When given a DNS name with a long namespace it should extract the service name",
			dnsName:  "etcd-2.etcd-discovery.ocm-arohcpci01-2q7h5rjtm2oud3pn6i3890qa6p37sts3-i2y6k1a2u2a0z1h.svc",
			expected: "etcd-discovery",
		},
		{
			name:        "When given a DNS name with too few components it should return an error",
			dnsName:     "etcd-0.etcd-discovery",
			expectError: true,
		},
		{
			name:        "When given a single component it should return an error",
			dnsName:     "etcd-0",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			result, err := parseServiceName(tt.dnsName)
			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(result).To(Equal(tt.expected))
			}
		})
	}
}

func TestEnsureEndpointSlice(t *testing.T) {
	const (
		namespace = "ocm-test-namespace"
		hostname  = "etcd-0"
		podIP     = "10.128.64.186"
		dnsName   = "etcd-0.etcd-discovery.ocm-test-namespace.svc"
		podUID    = "test-pod-uid-1234"
		nodeName  = "aks-userswft1-12345-vmss000000"
	)

	newPod := func() *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      hostname,
				Namespace: namespace,
				UID:       types.UID(podUID),
			},
			Spec: corev1.PodSpec{
				NodeName: nodeName,
			},
		}
	}

	t.Run("When no EndpointSlice exists it should create one with correct fields", func(t *testing.T) {
		g := NewGomegaWithT(t)
		client := fake.NewClientset(newPod())

		err := ensureEndpointSlice(t.Context(), client, dnsName, hostname, namespace, podIP)
		g.Expect(err).NotTo(HaveOccurred())

		slice, err := client.DiscoveryV1().EndpointSlices(namespace).Get(t.Context(), "etcd-discovery-self-etcd-0", metav1.GetOptions{})
		g.Expect(err).NotTo(HaveOccurred())

		g.Expect(slice.Labels[discoveryv1.LabelServiceName]).To(Equal("etcd-discovery"))
		g.Expect(slice.Labels[discoveryv1.LabelManagedBy]).To(Equal("etcd-self-register.hypershift.openshift.io"))
		g.Expect(slice.AddressType).To(Equal(discoveryv1.AddressTypeIPv4))
		g.Expect(slice.Endpoints).To(HaveLen(1))
		g.Expect(slice.Endpoints[0].Addresses).To(Equal([]string{podIP}))
		g.Expect(*slice.Endpoints[0].Hostname).To(Equal(hostname))
		g.Expect(*slice.Endpoints[0].NodeName).To(Equal(nodeName))
		g.Expect(*slice.Endpoints[0].Conditions.Ready).To(BeTrue())
		g.Expect(slice.Endpoints[0].TargetRef.Kind).To(Equal("Pod"))
		g.Expect(slice.Endpoints[0].TargetRef.Name).To(Equal(hostname))
		g.Expect(slice.Endpoints[0].TargetRef.UID).To(Equal(types.UID(podUID)))
		g.Expect(slice.Ports).To(HaveLen(2))
		g.Expect(*slice.Ports[0].Name).To(Equal("peer"))
		g.Expect(*slice.Ports[0].Port).To(Equal(int32(2380)))
		g.Expect(*slice.Ports[1].Name).To(Equal("etcd-client"))
		g.Expect(*slice.Ports[1].Port).To(Equal(int32(2379)))
		g.Expect(slice.OwnerReferences).To(HaveLen(1))
		g.Expect(slice.OwnerReferences[0].Kind).To(Equal("Pod"))
		g.Expect(slice.OwnerReferences[0].Name).To(Equal(hostname))
		g.Expect(slice.OwnerReferences[0].UID).To(Equal(types.UID(podUID)))
	})

	t.Run("When an EndpointSlice already exists it should update it", func(t *testing.T) {
		g := NewGomegaWithT(t)
		existingSlice := &discoveryv1.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "etcd-discovery-self-etcd-0",
				Namespace: namespace,
				Labels: map[string]string{
					discoveryv1.LabelServiceName: "etcd-discovery",
					discoveryv1.LabelManagedBy:   "etcd-self-register.hypershift.openshift.io",
				},
			},
			AddressType: discoveryv1.AddressTypeIPv4,
			Endpoints: []discoveryv1.Endpoint{
				{Addresses: []string{"10.0.0.99"}},
			},
		}
		client := fake.NewClientset(newPod(), existingSlice)

		err := ensureEndpointSlice(t.Context(), client, dnsName, hostname, namespace, podIP)
		g.Expect(err).NotTo(HaveOccurred())

		slice, err := client.DiscoveryV1().EndpointSlices(namespace).Get(t.Context(), "etcd-discovery-self-etcd-0", metav1.GetOptions{})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(slice.Endpoints[0].Addresses).To(Equal([]string{podIP}))
	})

	t.Run("When given an IPv6 address it should set AddressTypeIPv6", func(t *testing.T) {
		g := NewGomegaWithT(t)
		client := fake.NewClientset(newPod())
		ipv6 := "fd00::1"

		err := ensureEndpointSlice(t.Context(), client, dnsName, hostname, namespace, ipv6)
		g.Expect(err).NotTo(HaveOccurred())

		slice, err := client.DiscoveryV1().EndpointSlices(namespace).Get(t.Context(), "etcd-discovery-self-etcd-0", metav1.GetOptions{})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(slice.AddressType).To(Equal(discoveryv1.AddressTypeIPv6))
		g.Expect(slice.Endpoints[0].Addresses).To(Equal([]string{ipv6}))
	})

	t.Run("When the pod does not exist it should return an error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		client := fake.NewClientset()

		err := ensureEndpointSlice(t.Context(), client, dnsName, hostname, namespace, podIP)
		g.Expect(err).To(MatchError(ContainSubstring("failed to get pod")))
	})

	t.Run("When given an invalid DNS name it should return an error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		client := fake.NewClientset(newPod())

		err := ensureEndpointSlice(t.Context(), client, "invalid", hostname, namespace, podIP)
		g.Expect(err).To(MatchError(ContainSubstring("failed to parse service name")))
	})

	t.Run("When called for etcd-1 it should use the correct slice name and hostname", func(t *testing.T) {
		g := NewGomegaWithT(t)
		pod1 := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "etcd-1",
				Namespace: namespace,
				UID:       "uid-etcd-1",
			},
			Spec: corev1.PodSpec{NodeName: nodeName},
		}
		client := fake.NewClientset(pod1)

		err := ensureEndpointSlice(t.Context(), client, "etcd-1.etcd-discovery.ocm-test-namespace.svc", "etcd-1", namespace, "10.128.64.187")
		g.Expect(err).NotTo(HaveOccurred())

		slice, err := client.DiscoveryV1().EndpointSlices(namespace).Get(t.Context(), "etcd-discovery-self-etcd-1", metav1.GetOptions{})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(*slice.Endpoints[0].Hostname).To(Equal("etcd-1"))
		g.Expect(slice.Endpoints[0].Addresses).To(Equal([]string{"10.128.64.187"}))
	})

	t.Run("When multiple etcd pods self-register it should create separate EndpointSlices", func(t *testing.T) {
		g := NewGomegaWithT(t)
		pods := []runtime.Object{
			&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "etcd-0", Namespace: namespace, UID: "uid-0"}, Spec: corev1.PodSpec{NodeName: nodeName}},
			&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "etcd-1", Namespace: namespace, UID: "uid-1"}, Spec: corev1.PodSpec{NodeName: nodeName}},
			&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "etcd-2", Namespace: namespace, UID: "uid-2"}, Spec: corev1.PodSpec{NodeName: nodeName}},
		}
		client := fake.NewClientset(pods...)

		for i := range 3 {
			h := metav1.ObjectMeta{Name: pods[i].(*corev1.Pod).Name}.Name
			dns := h + ".etcd-discovery." + namespace + ".svc"
			ip := fmt.Sprintf("10.128.64.%d", 186+i)
			err := ensureEndpointSlice(t.Context(), client, dns, h, namespace, ip)
			g.Expect(err).NotTo(HaveOccurred())
		}

		slices, err := client.DiscoveryV1().EndpointSlices(namespace).List(t.Context(), metav1.ListOptions{})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(slices.Items).To(HaveLen(3))
	})
}
