package gmpparity

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

// TestGMPMonitoringParity is the repository-level GMP parity guard. It fails when
// an endpoint-bearing CoreOS monitor or rule group has no GMP counterpart and no
// documented waiver, so adding a scrape target under the CoreOS backend cannot
// silently leave the GMP backend incomplete.
func TestGMPMonitoringParity(t *testing.T) {
	inventory := buildInventory(t)

	coreOSTargets, err := inventory.coreOSTargets()
	if err != nil {
		t.Fatalf("failed to resolve the CoreOS scrape inventory: %v", err)
	}
	gmpTargets, err := inventory.gmpTargets()
	if err != nil {
		t.Fatalf("failed to resolve the GMP scrape inventory: %v", err)
	}

	requirements := auditedScopes(t, inventory.ruleRequirements)
	waivers := loadWaivers(t)

	assertScrapeTargetParity(t, coreOSTargets, gmpTargets, waivers.ScrapeTargets)
	assertRuleParity(t, requirements, inventory.gmpRuleGroups, waivers.Rules)
}

func endpointScheme(scheme string) *prometheusoperatorv1.Scheme {
	value := prometheusoperatorv1.Scheme(scheme)
	return &value
}

func endpointTargetPort(port string) *intstr.IntOrString {
	value := intstr.FromString(port)
	return &value
}

func testService(name string, labels, selector map[string]string, ports []corev1.ServicePort) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: hcpNamespace, Labels: labels},
		Spec: corev1.ServiceSpec{
			Selector: selector,
			Ports:    ports,
		},
	}
}

func testPod(component, namespace string, labels map[string]string, ports []corev1.ContainerPort) inventoryPod {
	return inventoryPod{
		Component: component,
		Namespace: namespace,
		Labels:    labels,
		Ports:     ports,
	}
}

func TestResolveServiceMonitorTargets(t *testing.T) {
	namedPortService := testService("etcd-client", map[string]string{"app": "etcd"},
		map[string]string{"app": "etcd"},
		[]corev1.ServicePort{
			{Name: "etcd-client", Port: 2379, TargetPort: intstr.FromString("client")},
			{Name: "metrics", Port: 2381, TargetPort: intstr.FromString("metrics")},
		})
	namedPortPod := testPod("etcd", hcpNamespace, map[string]string{"app": "etcd"},
		[]corev1.ContainerPort{
			{Name: "client", ContainerPort: 2379},
			{Name: "metrics", ContainerPort: 2381},
		})

	numericPortService := testService("kube-controller-manager", map[string]string{"app": "kube-controller-manager"},
		map[string]string{"app": "kube-controller-manager"},
		[]corev1.ServicePort{
			{Name: "client", Port: 10257, TargetPort: intstr.FromString("client")},
		})
	numericPortPod := testPod("kube-controller-manager", hcpNamespace, map[string]string{"app": "kube-controller-manager"},
		[]corev1.ContainerPort{{Name: "client", ContainerPort: 10257}})

	precedenceService := testService("precedence", map[string]string{"app": "precedence"},
		map[string]string{"app": "precedence"},
		[]corev1.ServicePort{
			{Name: "metrics", Port: 9090, TargetPort: intstr.FromString("service-port")},
		})
	precedencePod := testPod("precedence", hcpNamespace, map[string]string{"app": "precedence"},
		[]corev1.ContainerPort{
			{Name: "service-port", ContainerPort: 9091},
			{Name: "endpoint-target-port", ContainerPort: 9092},
		})

	testCases := []struct {
		name        string
		inventory   *clusterInventory
		monitor     *prometheusoperatorv1.ServiceMonitor
		expected    []scrapeTarget
		expectedErr string
	}{
		{
			name: "When the endpoint selects a named Service port, it should resolve the Service targetPort to a container port",
			inventory: &clusterInventory{
				services: []inventoryService{{Component: "etcd", Service: namedPortService}},
				pods:     []inventoryPod{namedPortPod},
			},
			monitor: &prometheusoperatorv1.ServiceMonitor{
				ObjectMeta: metav1.ObjectMeta{Name: "etcd", Namespace: hcpNamespace},
				Spec: prometheusoperatorv1.ServiceMonitorSpec{
					Selector: metav1.LabelSelector{MatchLabels: map[string]string{"app": "etcd"}},
					Endpoints: []prometheusoperatorv1.Endpoint{{
						Port:   "metrics",
						Scheme: endpointScheme("https"),
						Path:   "/metrics",
					}},
				},
			},
			expected: []scrapeTarget{{
				Component: "etcd",
				Namespace: hcpNamespace,
				Selector:  map[string]string{"app": "etcd"},
				Port:      2381,
				Path:      "/metrics",
				Scheme:    "https",
			}},
		},
		{
			name: "When the endpoint selects a named targetPort, it should resolve it directly against the pod container ports",
			inventory: &clusterInventory{
				services: []inventoryService{{Component: "kube-controller-manager", Service: numericPortService}},
				pods:     []inventoryPod{numericPortPod},
			},
			monitor: &prometheusoperatorv1.ServiceMonitor{
				ObjectMeta: metav1.ObjectMeta{Name: "kube-controller-manager", Namespace: hcpNamespace},
				Spec: prometheusoperatorv1.ServiceMonitorSpec{
					Selector: metav1.LabelSelector{MatchLabels: map[string]string{"app": "kube-controller-manager"}},
					Endpoints: []prometheusoperatorv1.Endpoint{{
						TargetPort: endpointTargetPort("client"),
						Scheme:     endpointScheme("https"),
					}},
				},
			},
			expected: []scrapeTarget{{
				Component: "kube-controller-manager",
				Namespace: hcpNamespace,
				Selector:  map[string]string{"app": "kube-controller-manager"},
				Port:      10257,
				Path:      "/metrics",
				Scheme:    "https",
			}},
		},
		{
			name: "When the endpoint sets both port and targetPort, it should give the named Service port precedence",
			inventory: &clusterInventory{
				services: []inventoryService{{Component: "precedence", Service: precedenceService}},
				pods:     []inventoryPod{precedencePod},
			},
			monitor: &prometheusoperatorv1.ServiceMonitor{
				ObjectMeta: metav1.ObjectMeta{Name: "precedence", Namespace: hcpNamespace},
				Spec: prometheusoperatorv1.ServiceMonitorSpec{
					Selector: metav1.LabelSelector{MatchLabels: map[string]string{"app": "precedence"}},
					Endpoints: []prometheusoperatorv1.Endpoint{{
						Port:       "metrics",
						TargetPort: endpointTargetPort("endpoint-target-port"),
					}},
				},
			},
			expected: []scrapeTarget{{
				Component: "precedence",
				Namespace: hcpNamespace,
				Selector:  map[string]string{"app": "precedence"},
				Port:      9091,
				Path:      "/metrics",
				Scheme:    "http",
			}},
		},
		{
			name: "When no Service matches the monitor selector, it should fail instead of dropping the target",
			inventory: &clusterInventory{
				services: []inventoryService{{Component: "etcd", Service: namedPortService}},
				pods:     []inventoryPod{namedPortPod},
			},
			monitor: &prometheusoperatorv1.ServiceMonitor{
				ObjectMeta: metav1.ObjectMeta{Name: "missing", Namespace: hcpNamespace},
				Spec: prometheusoperatorv1.ServiceMonitorSpec{
					Selector:  metav1.LabelSelector{MatchLabels: map[string]string{"app": "missing"}},
					Endpoints: []prometheusoperatorv1.Endpoint{{Port: "metrics"}},
				},
			},
			expectedErr: "selects no Service",
		},
		{
			name: "When the Service has no port with the requested name, it should fail",
			inventory: &clusterInventory{
				services: []inventoryService{{Component: "etcd", Service: namedPortService}},
				pods:     []inventoryPod{namedPortPod},
			},
			monitor: &prometheusoperatorv1.ServiceMonitor{
				ObjectMeta: metav1.ObjectMeta{Name: "etcd", Namespace: hcpNamespace},
				Spec: prometheusoperatorv1.ServiceMonitorSpec{
					Selector:  metav1.LabelSelector{MatchLabels: map[string]string{"app": "etcd"}},
					Endpoints: []prometheusoperatorv1.Endpoint{{Port: "absent"}},
				},
			},
			expectedErr: "no port named",
		},
		{
			name: "When the selected Service fronts no pod template, it should fail",
			inventory: &clusterInventory{
				services: []inventoryService{{Component: "etcd", Service: namedPortService}},
			},
			monitor: &prometheusoperatorv1.ServiceMonitor{
				ObjectMeta: metav1.ObjectMeta{Name: "etcd", Namespace: hcpNamespace},
				Spec: prometheusoperatorv1.ServiceMonitorSpec{
					Selector:  metav1.LabelSelector{MatchLabels: map[string]string{"app": "etcd"}},
					Endpoints: []prometheusoperatorv1.Endpoint{{Port: "metrics"}},
				},
			},
			expectedErr: "fronts no workload pod template",
		},
		{
			name: "When the backing pod does not expose the container port, it should fail",
			inventory: &clusterInventory{
				services: []inventoryService{{Component: "etcd", Service: namedPortService}},
				pods: []inventoryPod{testPod("etcd", hcpNamespace, map[string]string{"app": "etcd"},
					[]corev1.ContainerPort{{Name: "client", ContainerPort: 2379}})},
			},
			monitor: &prometheusoperatorv1.ServiceMonitor{
				ObjectMeta: metav1.ObjectMeta{Name: "etcd", Namespace: hcpNamespace},
				Spec: prometheusoperatorv1.ServiceMonitorSpec{
					Selector:  metav1.LabelSelector{MatchLabels: map[string]string{"app": "etcd"}},
					Endpoints: []prometheusoperatorv1.Endpoint{{Port: "metrics"}},
				},
			},
			expectedErr: `no container port named "metrics"`,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			targets, err := testCase.inventory.resolveServiceMonitorTargets("test", testCase.monitor)
			if testCase.expectedErr != "" {
				if err == nil {
					t.Fatalf("expected an error containing %q, got targets %v", testCase.expectedErr, targets)
				}
				if !strings.Contains(err.Error(), testCase.expectedErr) {
					t.Fatalf("expected an error containing %q, got %v", testCase.expectedErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertTargetsEqual(t, testCase.expected, targets)
		})
	}
}

func TestUncoveredTargets(t *testing.T) {
	base := scrapeTarget{
		Component: "etcd",
		Namespace: hcpNamespace,
		Selector:  map[string]string{"app": "etcd"},
		Port:      2381,
		Path:      "/metrics",
		Scheme:    "https",
	}

	withSelector := func(selector map[string]string) scrapeTarget {
		target := base
		target.Selector = selector
		return target
	}
	withPort := func(port int32) scrapeTarget {
		target := base
		target.Port = port
		return target
	}
	withPath := func(path string) scrapeTarget {
		target := base
		target.Path = path
		return target
	}
	withScheme := func(scheme string) scrapeTarget {
		target := base
		target.Scheme = scheme
		return target
	}

	testCases := []struct {
		name     string
		coreOS   []scrapeTarget
		gmp      []scrapeTarget
		expected []scrapeTarget
	}{
		{
			name:     "When the GMP endpoint matches the CoreOS target, it should report full coverage",
			coreOS:   []scrapeTarget{base},
			gmp:      []scrapeTarget{base},
			expected: nil,
		},
		{
			name:     "When the GMP selector is narrower than the CoreOS pod selector, it should report the target as uncovered",
			coreOS:   []scrapeTarget{base},
			gmp:      []scrapeTarget{withSelector(map[string]string{"app": "etcd", "tier": "control-plane"})},
			expected: []scrapeTarget{base},
		},
		{
			name:     "When the GMP selector is wider than the CoreOS pod selector, it should report the target as uncovered",
			coreOS:   []scrapeTarget{withSelector(map[string]string{"app": "etcd", "tier": "control-plane"})},
			gmp:      []scrapeTarget{base},
			expected: []scrapeTarget{withSelector(map[string]string{"app": "etcd", "tier": "control-plane"})},
		},
		{
			name:     "When the GMP endpoint uses a different port, it should report the target as uncovered",
			coreOS:   []scrapeTarget{base},
			gmp:      []scrapeTarget{withPort(2382)},
			expected: []scrapeTarget{base},
		},
		{
			name:     "When the GMP endpoint uses a different path, it should report the target as uncovered",
			coreOS:   []scrapeTarget{base},
			gmp:      []scrapeTarget{withPath("/metrics/resources")},
			expected: []scrapeTarget{base},
		},
		{
			name:     "When the GMP endpoint uses a different scheme, it should report the target as uncovered",
			coreOS:   []scrapeTarget{base},
			gmp:      []scrapeTarget{withScheme("http")},
			expected: []scrapeTarget{base},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assertTargetsEqual(t, testCase.expected, uncoveredTargets(testCase.coreOS, testCase.gmp))
		})
	}
}

func assertTargetsEqual(t *testing.T, expected, actual []scrapeTarget) {
	t.Helper()

	expectedKeys := make(map[string]struct{}, len(expected))
	for _, target := range expected {
		expectedKeys[target.coverageKey()] = struct{}{}
	}
	actualKeys := make(map[string]struct{}, len(actual))
	for _, target := range actual {
		actualKeys[target.coverageKey()] = struct{}{}
	}

	if len(expectedKeys) != len(actualKeys) {
		t.Fatalf("expected %d targets, got %d\nexpected: %v\nactual: %v", len(expectedKeys), len(actualKeys), expected, actual)
	}
	for key := range expectedKeys {
		if _, ok := actualKeys[key]; !ok {
			t.Fatalf("expected target %s, got %v", key, actual)
		}
	}
}
