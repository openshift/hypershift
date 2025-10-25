package util

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestCreateTestNamespace tests the namespace creation helper
func TestCreateTestNamespace(t *testing.T) {
	tests := []struct {
		name          string
		namespaceName string
		expectError   bool
	}{
		{
			name:          "create new namespace",
			namespaceName: "test-ns-1",
			expectError:   false,
		},
		{
			name:          "create namespace with hyphenated name",
			namespaceName: "test-netpol-ingress-deny",
			expectError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			ctx := context.Background()

			// Create fake client
			scheme := runtime.NewScheme()
			_ = corev1.AddToScheme(scheme)
			client := fake.NewClientBuilder().WithScheme(scheme).Build()

			// Create namespace
			ns := createTestNamespace(ctx, client, tt.namespaceName, g)

			// Verify namespace was created
			g.Expect(ns).NotTo(BeNil())
			g.Expect(ns.Name).To(Equal(tt.namespaceName))
			g.Expect(ns.Labels).To(HaveKeyWithValue("hypershift-e2e-test", "cilium-network-policy"))

			// Verify namespace exists in fake client
			retrievedNs := &corev1.Namespace{}
			err := client.Get(ctx, crclient.ObjectKey{Name: tt.namespaceName}, retrievedNs)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(retrievedNs.Name).To(Equal(tt.namespaceName))
		})
	}
}

// TestCreateTestNamespaceIdempotent tests that creating an existing namespace doesn't fail
func TestCreateTestNamespaceIdempotent(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	// Pre-create the namespace
	existingNs := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "existing-ns",
		},
	}

	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existingNs).Build()

	// Attempt to create the same namespace - should not error
	ns := createTestNamespace(ctx, client, "existing-ns", g)
	g.Expect(ns).NotTo(BeNil())
	g.Expect(ns.Name).To(Equal("existing-ns"))
}

// TestDeployTestPod tests the pod deployment helper
func TestDeployTestPod(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		podName   string
		labels    map[string]string
		image     string
	}{
		{
			name:      "deploy nginx pod",
			namespace: "test-ns",
			podName:   "web-server",
			labels:    map[string]string{"app": "web"},
			image:     "nginxinc/nginx-unprivileged:stable-alpine",
		},
		{
			name:      "deploy curl pod",
			namespace: "test-ns",
			podName:   "client",
			labels:    map[string]string{"app": "client", "role": "test"},
			image:     "curlimages/curl:latest",
		},
		{
			name:      "deploy pod with multiple labels",
			namespace: "custom-ns",
			podName:   "multi-label-pod",
			labels:    map[string]string{"app": "test", "env": "dev", "tier": "backend"},
			image:     "busybox:stable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			ctx := context.Background()

			// Create fake client with namespace
			scheme := runtime.NewScheme()
			_ = corev1.AddToScheme(scheme)
			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: tt.namespace}}
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ns).Build()

			// Deploy pod
			pod := deployTestPod(ctx, client, tt.namespace, tt.podName, tt.labels, tt.image, g)

			// Verify pod was created with correct properties
			g.Expect(pod).NotTo(BeNil())
			g.Expect(pod.Name).To(Equal(tt.podName))
			g.Expect(pod.Namespace).To(Equal(tt.namespace))
			g.Expect(pod.Labels).To(Equal(tt.labels))
			g.Expect(pod.Spec.Containers).To(HaveLen(1))
			g.Expect(pod.Spec.Containers[0].Name).To(Equal(tt.podName))
			g.Expect(pod.Spec.Containers[0].Image).To(Equal(tt.image))
			g.Expect(pod.Spec.Containers[0].Command).To(Equal([]string{"sleep", "3600"}))
			g.Expect(pod.Spec.RestartPolicy).To(Equal(corev1.RestartPolicyAlways))

			// Verify pod exists in fake client
			retrievedPod := &corev1.Pod{}
			err := client.Get(ctx, crclient.ObjectKey{Namespace: tt.namespace, Name: tt.podName}, retrievedPod)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(retrievedPod.Spec.Containers[0].Image).To(Equal(tt.image))
		})
	}
}

// TestDeployTestService tests the service deployment helper
func TestDeployTestService(t *testing.T) {
	tests := []struct {
		name        string
		namespace   string
		serviceName string
		selector    map[string]string
		port        int
	}{
		{
			name:        "create service on port 80",
			namespace:   "test-ns",
			serviceName: "web-server-svc",
			selector:    map[string]string{"app": "web"},
			port:        80,
		},
		{
			name:        "create service on port 8080",
			namespace:   "test-ns",
			serviceName: "backend-svc",
			selector:    map[string]string{"app": "backend", "tier": "api"},
			port:        8080,
		},
		{
			name:        "create service on port 443",
			namespace:   "custom-ns",
			serviceName: "secure-svc",
			selector:    map[string]string{"app": "secure"},
			port:        443,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			ctx := context.Background()

			// Create fake client with namespace
			scheme := runtime.NewScheme()
			_ = corev1.AddToScheme(scheme)
			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: tt.namespace}}
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ns).Build()

			// Deploy service
			svc := deployTestService(ctx, client, tt.namespace, tt.serviceName, tt.selector, tt.port, g)

			// Verify service was created with correct properties
			g.Expect(svc).NotTo(BeNil())
			g.Expect(svc.Name).To(Equal(tt.serviceName))
			g.Expect(svc.Namespace).To(Equal(tt.namespace))
			g.Expect(svc.Spec.Selector).To(Equal(tt.selector))
			g.Expect(svc.Spec.Ports).To(HaveLen(1))
			g.Expect(svc.Spec.Ports[0].Port).To(Equal(int32(tt.port)))
			g.Expect(svc.Spec.Ports[0].TargetPort).To(Equal(intstr.FromInt(tt.port)))
			g.Expect(svc.Spec.Ports[0].Protocol).To(Equal(corev1.ProtocolTCP))

			// Verify service exists in fake client
			retrievedSvc := &corev1.Service{}
			err := client.Get(ctx, crclient.ObjectKey{Namespace: tt.namespace, Name: tt.serviceName}, retrievedSvc)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(retrievedSvc.Spec.Selector).To(Equal(tt.selector))
		})
	}
}

// TestCreateCiliumNetworkPolicy tests the Cilium network policy creation helper
func TestCreateCiliumNetworkPolicy(t *testing.T) {
	tests := []struct {
		name             string
		namespace        string
		policyName       string
		endpointSelector map[string]string
		ingress          []interface{}
		egress           []interface{}
		expectIngress    bool
		expectEgress     bool
	}{
		{
			name:             "default deny all ingress",
			namespace:        "test-ns",
			policyName:       "deny-all-ingress",
			endpointSelector: map[string]string{},
			ingress:          []interface{}{},
			egress:           nil,
			expectIngress:    true,
			expectEgress:     false,
		},
		{
			name:             "default deny all egress",
			namespace:        "test-ns",
			policyName:       "deny-all-egress",
			endpointSelector: map[string]string{},
			ingress:          nil,
			egress:           []interface{}{},
			expectIngress:    false,
			expectEgress:     true,
		},
		{
			name:             "allow specific ingress",
			namespace:        "test-ns",
			policyName:       "allow-specific",
			endpointSelector: map[string]string{"app": "web"},
			ingress: []interface{}{
				map[string]interface{}{
					"fromEndpoints": []interface{}{
						map[string]interface{}{
							"matchLabels": map[string]string{"role": "allowed"},
						},
					},
				},
			},
			egress:        nil,
			expectIngress: true,
			expectEgress:  false,
		},
		{
			name:             "combined ingress and egress",
			namespace:        "test-ns",
			policyName:       "combined",
			endpointSelector: map[string]string{"app": "backend"},
			ingress: []interface{}{
				map[string]interface{}{
					"fromEndpoints": []interface{}{
						map[string]interface{}{
							"matchLabels": map[string]string{"role": "frontend"},
						},
					},
				},
			},
			egress: []interface{}{
				map[string]interface{}{
					"toEndpoints": []interface{}{
						map[string]interface{}{
							"matchLabels": map[string]string{"app": "database"},
						},
					},
				},
			},
			expectIngress: true,
			expectEgress:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			// Create policy
			policy := createCiliumNetworkPolicy(tt.namespace, tt.policyName, tt.endpointSelector, tt.ingress, tt.egress)

			// Verify basic properties
			g.Expect(policy).NotTo(BeNil())
			g.Expect(policy.GetAPIVersion()).To(Equal("cilium.io/v2"))
			g.Expect(policy.GetKind()).To(Equal("CiliumNetworkPolicy"))
			g.Expect(policy.GetName()).To(Equal(tt.policyName))
			g.Expect(policy.GetNamespace()).To(Equal(tt.namespace))

			// Verify spec - access directly without deep copy
			spec, ok := policy.Object["spec"].(map[string]interface{})
			g.Expect(ok).To(BeTrue(), "spec should be a map")
			g.Expect(spec).NotTo(BeNil())

			// Verify endpoint selector
			endpointSelector, ok := spec["endpointSelector"].(map[string]interface{})
			g.Expect(ok).To(BeTrue(), "endpointSelector should be a map")

			// matchLabels may be nil or not exist for empty selectors
			if len(tt.endpointSelector) > 0 {
				matchLabels, ok := endpointSelector["matchLabels"].(map[string]interface{})
				g.Expect(ok).To(BeTrue(), "matchLabels should be a map when selector is not empty")

				// Convert to map[string]string for comparison
				actualLabels := make(map[string]string)
				for k, v := range matchLabels {
					actualLabels[k] = v.(string)
				}
				g.Expect(actualLabels).To(Equal(tt.endpointSelector))
			} else {
				// Empty selector - matchLabels should be empty map
				matchLabels := endpointSelector["matchLabels"]
				g.Expect(matchLabels).NotTo(BeNil(), "matchLabels should exist even for empty selector")
			}

			// Verify ingress rules
			if tt.expectIngress {
				ingress, ok := spec["ingress"].([]interface{})
				g.Expect(ok).To(BeTrue(), "ingress should be a slice")
				g.Expect(ingress).To(Equal(tt.ingress))
			}

			// Verify egress rules
			if tt.expectEgress {
				egress, ok := spec["egress"].([]interface{})
				g.Expect(ok).To(BeTrue(), "egress should be a slice")
				g.Expect(egress).To(Equal(tt.egress))
			}
		})
	}
}

// TestCreateCiliumNetworkPolicyWithComplexRules tests complex policy scenarios
func TestCreateCiliumNetworkPolicyWithComplexRules(t *testing.T) {
	g := NewWithT(t)

	// Test DNS and Kube API egress policy
	egress := []interface{}{
		map[string]interface{}{
			"toPorts": []interface{}{
				map[string]interface{}{
					"ports": []interface{}{
						map[string]interface{}{
							"port":     "53",
							"protocol": "UDP",
						},
					},
				},
			},
		},
		map[string]interface{}{
			"toPorts": []interface{}{
				map[string]interface{}{
					"ports": []interface{}{
						map[string]interface{}{
							"port":     "443",
							"protocol": "TCP",
						},
					},
				},
			},
		},
		map[string]interface{}{
			"toEntities": []string{"kube-apiserver"},
		},
	}

	policy := createCiliumNetworkPolicy("test-ns", "allow-dns-and-api", map[string]string{"app": "client"}, nil, egress)

	// Verify structure
	g.Expect(policy).NotTo(BeNil())
	spec := policy.Object["spec"].(map[string]interface{})
	egressRules, ok := spec["egress"].([]interface{})
	g.Expect(ok).To(BeTrue(), "egress should be a slice")
	g.Expect(egressRules).To(HaveLen(3))

	// Verify first rule (DNS port 53)
	firstRule := egressRules[0].(map[string]interface{})
	toPorts := firstRule["toPorts"].([]interface{})
	g.Expect(toPorts).To(HaveLen(1))
	portSpec := toPorts[0].(map[string]interface{})
	ports := portSpec["ports"].([]interface{})
	g.Expect(ports).To(HaveLen(1))
	portDetails := ports[0].(map[string]interface{})
	g.Expect(portDetails["port"]).To(Equal("53"))
	g.Expect(portDetails["protocol"]).To(Equal("UDP"))

	// Verify third rule (kube-apiserver entity)
	thirdRule := egressRules[2].(map[string]interface{})
	// toEntities can be either []string or []interface{} depending on how it was created
	toEntitiesRaw := thirdRule["toEntities"]
	switch v := toEntitiesRaw.(type) {
	case []string:
		g.Expect(v).To(ContainElement("kube-apiserver"))
	case []interface{}:
		g.Expect(v).To(ContainElement("kube-apiserver"))
	default:
		g.Fail("toEntities should be a slice")
	}
}

// TestCreateCiliumNetworkPolicyNamespaceSelector tests cross-namespace policies
func TestCreateCiliumNetworkPolicyNamespaceSelector(t *testing.T) {
	g := NewWithT(t)

	// Test policy with namespace selector (deny from namespace-b)
	ingress := []interface{}{
		map[string]interface{}{
			"fromEndpoints": []interface{}{
				map[string]interface{}{
					"matchExpressions": []interface{}{
						map[string]interface{}{
							"key":      "io.kubernetes.pod.namespace",
							"operator": "NotIn",
							"values":   []string{"namespace-b"},
						},
					},
				},
			},
		},
	}

	policy := createCiliumNetworkPolicy("namespace-a", "deny-namespace-b", map[string]string{"app": "web"}, ingress, nil)

	// Verify structure
	g.Expect(policy).NotTo(BeNil())
	spec := policy.Object["spec"].(map[string]interface{})
	ingressRules, ok := spec["ingress"].([]interface{})
	g.Expect(ok).To(BeTrue(), "ingress should be a slice")
	g.Expect(ingressRules).To(HaveLen(1))

	// Verify fromEndpoints with matchExpressions
	firstRule := ingressRules[0].(map[string]interface{})
	fromEndpoints := firstRule["fromEndpoints"].([]interface{})
	g.Expect(fromEndpoints).To(HaveLen(1))

	endpoint := fromEndpoints[0].(map[string]interface{})
	matchExpressions := endpoint["matchExpressions"].([]interface{})
	g.Expect(matchExpressions).To(HaveLen(1))

	expression := matchExpressions[0].(map[string]interface{})
	g.Expect(expression["key"]).To(Equal("io.kubernetes.pod.namespace"))
	g.Expect(expression["operator"]).To(Equal("NotIn"))

	values := expression["values"].([]interface{})
	g.Expect(values).To(ContainElement("namespace-b"))
}

// TestApplyCiliumNetworkPolicy tests applying a policy to the cluster
func TestApplyCiliumNetworkPolicy(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()

	// Create fake client with CRD scheme support
	scheme := runtime.NewScheme()
	client := fake.NewClientBuilder().WithScheme(scheme).Build()

	// Create a simple policy
	policy := createCiliumNetworkPolicy("test-ns", "test-policy", map[string]string{"app": "test"}, []interface{}{}, nil)

	// Apply policy
	applyCiliumNetworkPolicy(ctx, client, policy, g)

	// Verify policy was created
	retrievedPolicy := &unstructured.Unstructured{}
	retrievedPolicy.SetGroupVersionKind(policy.GroupVersionKind())
	err := client.Get(ctx, crclient.ObjectKey{Namespace: "test-ns", Name: "test-policy"}, retrievedPolicy)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(retrievedPolicy.GetName()).To(Equal("test-policy"))
	g.Expect(retrievedPolicy.GetNamespace()).To(Equal("test-ns"))
}

// TestDeletePod tests pod deletion helper
func TestDeletePod(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()

	// Create fake client with a pod
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "test-ns",
		},
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pod).Build()

	// Verify pod exists
	retrievedPod := &corev1.Pod{}
	err := client.Get(ctx, crclient.ObjectKey{Namespace: "test-ns", Name: "test-pod"}, retrievedPod)
	g.Expect(err).NotTo(HaveOccurred())

	// Delete pod
	deletePod(ctx, client, "test-ns", "test-pod")

	// Verify pod was deleted
	err = client.Get(ctx, crclient.ObjectKey{Namespace: "test-ns", Name: "test-pod"}, retrievedPod)
	g.Expect(err).To(HaveOccurred())
}

// TestDeleteService tests service deletion helper
func TestDeleteService(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()

	// Create fake client with a service
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-svc",
			Namespace: "test-ns",
		},
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(svc).Build()

	// Verify service exists
	retrievedSvc := &corev1.Service{}
	err := client.Get(ctx, crclient.ObjectKey{Namespace: "test-ns", Name: "test-svc"}, retrievedSvc)
	g.Expect(err).NotTo(HaveOccurred())

	// Delete service
	deleteService(ctx, client, "test-ns", "test-svc")

	// Verify service was deleted
	err = client.Get(ctx, crclient.ObjectKey{Namespace: "test-ns", Name: "test-svc"}, retrievedSvc)
	g.Expect(err).To(HaveOccurred())
}

// TestDeleteCiliumNetworkPolicy tests policy deletion helper
func TestDeleteCiliumNetworkPolicy(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()

	// Create fake client
	scheme := runtime.NewScheme()
	client := fake.NewClientBuilder().WithScheme(scheme).Build()

	// Create and apply a policy
	policy := createCiliumNetworkPolicy("test-ns", "test-policy", map[string]string{}, []interface{}{}, nil)
	applyCiliumNetworkPolicy(ctx, client, policy, g)

	// Verify policy exists
	retrievedPolicy := &unstructured.Unstructured{}
	retrievedPolicy.SetGroupVersionKind(policy.GroupVersionKind())
	err := client.Get(ctx, crclient.ObjectKey{Namespace: "test-ns", Name: "test-policy"}, retrievedPolicy)
	g.Expect(err).NotTo(HaveOccurred())

	// Delete policy
	deleteCiliumNetworkPolicy(ctx, client, "test-ns", "test-policy")

	// Verify policy was deleted
	err = client.Get(ctx, crclient.ObjectKey{Namespace: "test-ns", Name: "test-policy"}, retrievedPolicy)
	g.Expect(err).To(HaveOccurred())
}

// TestPolicyStructureValidation validates that generated policies have correct Cilium structure
func TestPolicyStructureValidation(t *testing.T) {
	tests := []struct {
		name             string
		policyName       string
		endpointSelector map[string]string
		ingress          []interface{}
		egress           []interface{}
		validateFunc     func(*testing.T, *unstructured.Unstructured)
	}{
		{
			name:             "empty endpoint selector selects all pods",
			policyName:       "select-all",
			endpointSelector: map[string]string{},
			ingress:          []interface{}{},
			egress:           nil,
			validateFunc: func(t *testing.T, policy *unstructured.Unstructured) {
				g := NewWithT(t)
				spec := policy.Object["spec"].(map[string]interface{})
				endpointSelector := spec["endpointSelector"].(map[string]interface{})
				matchLabels := endpointSelector["matchLabels"].(map[string]interface{})
				g.Expect(matchLabels).To(BeEmpty(), "empty selector should match all pods")
			},
		},
		{
			name:             "specific selector targets specific pods",
			policyName:       "select-specific",
			endpointSelector: map[string]string{"app": "web", "tier": "frontend"},
			ingress:          nil,
			egress:           nil,
			validateFunc: func(t *testing.T, policy *unstructured.Unstructured) {
				g := NewWithT(t)
				spec := policy.Object["spec"].(map[string]interface{})
				endpointSelector := spec["endpointSelector"].(map[string]interface{})
				matchLabels := endpointSelector["matchLabels"].(map[string]interface{})
				g.Expect(matchLabels).To(HaveLen(2))
				g.Expect(matchLabels).To(HaveKeyWithValue("app", "web"))
				g.Expect(matchLabels).To(HaveKeyWithValue("tier", "frontend"))
			},
		},
		{
			name:             "empty ingress array denies all ingress",
			policyName:       "deny-ingress",
			endpointSelector: map[string]string{},
			ingress:          []interface{}{},
			egress:           nil,
			validateFunc: func(t *testing.T, policy *unstructured.Unstructured) {
				g := NewWithT(t)
				spec := policy.Object["spec"].(map[string]interface{})
				ingress, ok := spec["ingress"].([]interface{})
				g.Expect(ok).To(BeTrue(), "ingress field should be present")
				g.Expect(ingress).To(BeEmpty(), "empty ingress should deny all")
			},
		},
		{
			name:             "nil ingress allows all ingress",
			policyName:       "allow-all-ingress",
			endpointSelector: map[string]string{},
			ingress:          nil,
			egress:           []interface{}{},
			validateFunc: func(t *testing.T, policy *unstructured.Unstructured) {
				g := NewWithT(t)
				spec := policy.Object["spec"].(map[string]interface{})
				_, ok := spec["ingress"]
				g.Expect(ok).To(BeFalse(), "nil ingress should not be present in spec")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := createCiliumNetworkPolicy("test-ns", tt.policyName, tt.endpointSelector, tt.ingress, tt.egress)
			tt.validateFunc(t, policy)
		})
	}
}

// BenchmarkCreateCiliumNetworkPolicy benchmarks policy creation
func BenchmarkCreateCiliumNetworkPolicy(b *testing.B) {
	endpointSelector := map[string]string{"app": "web"}
	ingress := []interface{}{
		map[string]interface{}{
			"fromEndpoints": []interface{}{
				map[string]interface{}{
					"matchLabels": map[string]string{"role": "allowed"},
				},
			},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = createCiliumNetworkPolicy("test-ns", "test-policy", endpointSelector, ingress, nil)
	}
}

// BenchmarkDeployTestPod benchmarks pod creation
func BenchmarkDeployTestPod(b *testing.B) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "test-ns"}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ns).Build()
		g := NewWithT(b)
		_ = deployTestPod(ctx, client, "test-ns", "test-pod", map[string]string{"app": "test"}, "nginx", g)
	}
}
