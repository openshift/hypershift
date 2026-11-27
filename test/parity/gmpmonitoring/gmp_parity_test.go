package gmpparity

import (
	"strings"
	"testing"

	"github.com/openshift/hypershift/support/gmpmonitoring"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

// TestGMPMonitoringParity is the repository-level GMP parity guard. It fails when
// an endpoint-bearing CoreOS monitor has no GMP counterpart and no documented
// waiver, so adding a scrape target under the CoreOS backend cannot silently
// leave the GMP backend incomplete. Rules are not compared: the rules consumed on
// GCP live in gcp-hcp-infra.
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

	waivers := loadWaivers(t)

	// Discovery failures make every parity assertion below vacuous: an inventory
	// that silently loses its monitors or PodMonitorings would report full
	// coverage of nothing.
	assertInventoryPopulated(t, inventory, coreOSTargets, gmpTargets)

	problems := scrapeTargetParityProblems(coreOSTargets, gmpTargets, waivers.ScrapeTargets)
	for _, problem := range problems {
		t.Error(problem)
	}
	if len(problems) > 0 {
		t.Error(uncoveredTargetHint)
	}
}

// uncoveredTargetHint is reported once after the problems, so a team that changed
// a CoreOS monitor learns the usual fix without opening this test.
const uncoveredTargetHint = "If you changed a CoreOS ServiceMonitor or PodMonitor, run `make generate-gmp-config` and commit the result.\n" +
	"If this still fails after regenerating, the generator does not represent the monitor faithfully: waive the target with a\n" +
	"tracking issue (see hack/gmp-config-gen/README.md) or ask the GCP HCP team."

// assertInventoryPopulated fails when discovery returned nothing to compare, so
// the parity guard cannot pass by inspecting an empty inventory.
func assertInventoryPopulated(t *testing.T, inventory *clusterInventory, coreOS, gmp []scrapeTarget) {
	t.Helper()

	if len(inventory.serviceMonitors) == 0 {
		t.Error("discovery found no CoreOS ServiceMonitor assets")
	}
	if len(inventory.podMonitors) == 0 {
		t.Error("discovery found no CoreOS PodMonitor assets")
	}
	if len(coreOS) == 0 {
		t.Error("the resolved CoreOS scrape inventory is empty")
	}
	if len(gmp) == 0 {
		t.Error("the resolved GMP scrape inventory is empty")
	}
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

func TestPodEndpointPort(t *testing.T) {
	portName := "metrics"
	portNumber := int32(8085)
	targetPort := intstr.FromString("legacy")

	testCases := []struct {
		name        string
		port        *string
		portNumber  *int32
		targetPort  *intstr.IntOrString
		expected    intstr.IntOrString
		expectedErr string
	}{
		{
			name:     "When the endpoint names a port, it should prefer the port name",
			port:     &portName,
			expected: intstr.FromString("metrics"),
		},
		{
			name:       "When the endpoint sets portNumber, it should use the numeric port",
			portNumber: &portNumber,
			expected:   intstr.FromInt32(8085),
		},
		{
			name:       "When the endpoint sets the legacy targetPort, it should fall back to it",
			targetPort: &targetPort,
			expected:   intstr.FromString("legacy"),
		},
		{
			name:       "When the endpoint sets both port and portNumber, it should prefer the port name",
			port:       &portName,
			portNumber: &portNumber,
			expected:   intstr.FromString("metrics"),
		},
		{
			name:        "When the endpoint sets no port at all, it should fail",
			expectedErr: "neither port, portNumber nor targetPort",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			port, err := podEndpointPort(testCase.port, testCase.portNumber, testCase.targetPort)
			if testCase.expectedErr != "" {
				if err == nil {
					t.Fatalf("expected an error containing %q, got port %v", testCase.expectedErr, port)
				}
				if !strings.Contains(err.Error(), testCase.expectedErr) {
					t.Fatalf("expected an error containing %q, got %v", testCase.expectedErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if port != testCase.expected {
				t.Fatalf("expected port %v, got %v", testCase.expected, port)
			}
		})
	}
}

func TestSelectorExpressionsKey(t *testing.T) {
	t.Run("When requirements and values are reordered, it should render the same key", func(t *testing.T) {
		first := selectorExpressionsKey([]metav1.LabelSelectorRequirement{
			{Key: "tier", Operator: metav1.LabelSelectorOpIn, Values: []string{"b", "a"}},
			{Key: "app", Operator: metav1.LabelSelectorOpExists},
		})
		second := selectorExpressionsKey([]metav1.LabelSelectorRequirement{
			{Key: "app", Operator: metav1.LabelSelectorOpExists},
			{Key: "tier", Operator: metav1.LabelSelectorOpIn, Values: []string{"a", "b"}},
		})
		if first != second {
			t.Fatalf("expected equal keys, got %q and %q", first, second)
		}
	})
	t.Run("When the operator differs, it should render a different key", func(t *testing.T) {
		in := selectorExpressionsKey([]metav1.LabelSelectorRequirement{{Key: "tier", Operator: metav1.LabelSelectorOpIn, Values: []string{"a"}}})
		notIn := selectorExpressionsKey([]metav1.LabelSelectorRequirement{{Key: "tier", Operator: metav1.LabelSelectorOpNotIn, Values: []string{"a"}}})
		if in == notIn {
			t.Fatalf("expected different keys, both were %q", in)
		}
	})
	t.Run("When there are no requirements, it should render an empty key", func(t *testing.T) {
		if key := selectorExpressionsKey(nil); key != "" {
			t.Fatalf("expected an empty key, got %q", key)
		}
	})
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
	withSelectorExpressions := func(expressions string) scrapeTarget {
		target := base
		target.SelectorExpressions = expressions
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
	withServerName := func(serverName string) scrapeTarget {
		target := base
		target.TLSServerName = serverName
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
			name:     "When the GMP selector drops the CoreOS matchExpressions, it should report the target as uncovered",
			coreOS:   []scrapeTarget{withSelectorExpressions("tier In (control-plane)")},
			gmp:      []scrapeTarget{base},
			expected: []scrapeTarget{withSelectorExpressions("tier In (control-plane)")},
		},
		{
			name:     "When the GMP selector adds matchExpressions the CoreOS selector lacks, it should report the target as uncovered",
			coreOS:   []scrapeTarget{base},
			gmp:      []scrapeTarget{withSelectorExpressions("tier In (control-plane)")},
			expected: []scrapeTarget{base},
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
		{
			name:     "When the GMP endpoint verifies a different TLS server name, it should report the target as uncovered",
			coreOS:   []scrapeTarget{withServerName("node-tuning-operator.hcp-namespace.svc")},
			gmp:      []scrapeTarget{withServerName("node-tuning-operator.stale-cluster.example.svc")},
			expected: []scrapeTarget{withServerName("node-tuning-operator.hcp-namespace.svc")},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assertTargetsEqual(t, testCase.expected, uncoveredTargets(testCase.coreOS, testCase.gmp))
		})
	}
}

func TestScrapeTargetParityProblems(t *testing.T) {
	base := scrapeTarget{
		Component: "etcd",
		Namespace: hcpNamespace,
		Selector:  map[string]string{"app": "etcd"},
		Port:      2381,
		Path:      "/metrics",
		Scheme:    "https",
	}
	// The waiver key excludes the component, so changing only the component name
	// keeps the key identical and exercises the component-mismatch branch.
	waiver := func(component string) scrapeTargetWaiver {
		return scrapeTargetWaiver{
			Component: component,
			Namespace: base.Namespace,
			Selector:  "app=etcd",
			Port:      base.Port,
			Path:      base.Path,
			Scheme:    base.Scheme,
			Tracking:  "GCP-1293",
			Reason:    "not ported yet",
		}
	}
	undocumented := waiver("etcd")
	undocumented.Tracking = ""

	testCases := []struct {
		name     string
		coreOS   []scrapeTarget
		gmp      []scrapeTarget
		waivers  []scrapeTargetWaiver
		expected []string
	}{
		{
			name:   "When every CoreOS target is covered, it should report no problems",
			coreOS: []scrapeTarget{base},
			gmp:    []scrapeTarget{base},
		},
		{
			name:   "When a covered target also has a waiver, it should report the waiver as stale",
			coreOS: []scrapeTarget{base},
			gmp:    []scrapeTarget{base},
			waivers: []scrapeTargetWaiver{
				waiver("etcd"),
			},
			expected: []string{"stale scrape target waiver"},
		},
		{
			name:     "When an uncovered target has a matching waiver, it should report no problems",
			coreOS:   []scrapeTarget{base},
			waivers:  []scrapeTargetWaiver{waiver("etcd")},
			expected: nil,
		},
		{
			name:     "When an uncovered target has no waiver, it should report the missing coverage",
			coreOS:   []scrapeTarget{base},
			expected: []string{"CoreOS scrape target has no GMP coverage and no waiver"},
		},
		{
			name:    "When two waivers share a coverage key, it should report the duplicate",
			coreOS:  []scrapeTarget{base},
			waivers: []scrapeTargetWaiver{waiver("etcd"), waiver("etcd")},
			expected: []string{
				"duplicate scrape target waiver",
			},
		},
		{
			name:     "When a waiver names a different component than the uncovered target, it should report the mismatch",
			coreOS:   []scrapeTarget{base},
			waivers:  []scrapeTargetWaiver{waiver("kube-apiserver")},
			expected: []string{`names component "kube-apiserver"`},
		},
		{
			name:     "When a waiver omits its tracking issue, it should report the missing documentation",
			coreOS:   []scrapeTarget{base},
			waivers:  []scrapeTargetWaiver{undocumented},
			expected: []string{"must set both tracking and reason"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assertProblemSubstrings(t, testCase.expected, scrapeTargetParityProblems(testCase.coreOS, testCase.gmp, testCase.waivers))
		})
	}
}

// assertProblemSubstrings checks that the problems contain one entry per
// expected substring and nothing else.
func assertProblemSubstrings(t *testing.T, expected, problems []string) {
	t.Helper()

	if len(problems) != len(expected) {
		t.Fatalf("expected %d problems, got %d:\nexpected: %v\nactual: %v", len(expected), len(problems), expected, problems)
	}
	for i, substring := range expected {
		if !strings.Contains(problems[i], substring) {
			t.Fatalf("expected problem %d to contain %q, got %q", i, substring, problems[i])
		}
	}
}

// gmpRelabelingRules translates prometheus-operator relabel configs into the
// GMP form independently of the generator, so a conversion bug in the generator
// cannot hide behind a shared helper. GMP stores the action in lower case and
// leaves unset pointer fields empty.
func gmpRelabelingRules(source []prometheusoperatorv1.RelabelConfig) []gmpmonitoring.RelabelingRule {
	rules := make([]gmpmonitoring.RelabelingRule, 0, len(source))
	for _, config := range source {
		rule := gmpmonitoring.RelabelingRule{
			TargetLabel: config.TargetLabel,
			Regex:       config.Regex,
			Modulus:     config.Modulus,
			Action:      strings.ToLower(config.Action),
		}
		for _, label := range config.SourceLabels {
			rule.SourceLabels = append(rule.SourceLabels, string(label))
		}
		if config.Separator != nil {
			rule.Separator = *config.Separator
		}
		if config.Replacement != nil {
			rule.Replacement = *config.Replacement
		}
		rules = append(rules, rule)
	}
	return rules
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
