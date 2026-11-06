package gmpparity

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	installassets "github.com/openshift/hypershift/cmd/install/assets"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	cpomanifests "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/oapi"
	kubemanifests "github.com/openshift/hypershift/pkg/manifests/cpo"
	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/yaml"
)

// auditedRuleScopes records the GMP rule scope required for each CoreOS rule
// group. Every rule group found in the inventory must have an entry here so that
// adding a rule group forces a deliberate scope decision. Namespaced Rules are
// the default because GMP Rules only process metric data from their own
// namespace; a group whose expressions aggregate across hosted clusters would
// instead need ClusterRules or GlobalRules.
var auditedRuleScopes = map[string]ruleScope{
	hcpNamespace + "/hypershift.rules":                 ruleScopeNamespace,
	managementNamespace + "/hypershift.rules":          ruleScopeNamespace,
	openshiftMonitoringNamespace + "/hypershift.rules": ruleScopeNamespace,
}

// buildInventory assembles the offline discovery inventory from the embedded
// control-plane assets, the Go-reconciled kube-apiserver Service and the
// management-level constructors used by `hypershift install`.
func buildInventory(t *testing.T) *clusterInventory {
	t.Helper()

	inv := &clusterInventory{}
	for _, component := range componentAssetsForTest(t) {
		inv.addAssetComponent(t, component)
	}

	// The kube-apiserver Service is reconciled in Go (infra.go), not from a
	// v2 asset. The ServiceMonitor resolves its selector through it, so omitting
	// it would fail discovery rather than prove coverage.
	kasService := kubemanifests.KubeAPIServerService(hcpNamespace)
	if err := kas.ReconcileServiceClusterIP(kasService, nil); err != nil {
		t.Fatalf("failed to construct the kube-apiserver Service: %v", err)
	}
	inv.services = append(inv.services, inventoryService{Component: "kube-apiserver", Service: kasService})

	// The openshift-apiserver Service is likewise reconciled in Go and has no
	// Service asset; its ServiceMonitor selects it directly.
	openshiftAPIServerService := cpomanifests.OpenshiftAPIServerService(hcpNamespace)
	if err := oapi.ReconcileOpenShiftAPIService(openshiftAPIServerService, config.OwnerRef{}); err != nil {
		t.Fatalf("failed to construct the openshift-apiserver Service: %v", err)
	}
	inv.services = append(inv.services, inventoryService{Component: "openshift-apiserver", Service: openshiftAPIServerService})

	addManagementResources(t, inv)
	return inv
}

// addManagementResources adds the endpoint-bearing management-level monitors and
// rule groups constructed by `hypershift install`.
func addManagementResources(t *testing.T, inv *clusterInventory) {
	t.Helper()

	operatorNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: managementNamespace}}
	operatorServiceAccount := installassets.HyperShiftOperatorServiceAccount{Namespace: operatorNamespace}.Build()

	operatorService := installassets.HyperShiftOperatorService{Namespace: operatorNamespace}.Build()
	inv.services = append(inv.services, inventoryService{Component: "operator", Service: operatorService})

	operatorDeployment := installassets.HyperShiftOperatorDeployment{
		Namespace:      operatorNamespace,
		ServiceAccount: operatorServiceAccount,
	}.Build()
	inv.pods = append(inv.pods, podFromTemplate("operator", managementNamespace, operatorDeployment.Spec.Template))

	externalDNSServiceAccount := installassets.ExternalDNSServiceAccount{Namespace: operatorNamespace}.Build()
	externalDNSDeployment := installassets.ExternalDNSDeployment{
		Namespace:      operatorNamespace,
		ServiceAccount: externalDNSServiceAccount,
	}.Build()
	inv.pods = append(inv.pods, podFromTemplate("external-dns", managementNamespace, externalDNSDeployment.Spec.Template))

	inv.serviceMonitors = append(inv.serviceMonitors, inventoryServiceMonitor{
		Component: "operator",
		Monitor:   installassets.HyperShiftServiceMonitor{Namespace: operatorNamespace}.Build(),
	})
	inv.podMonitors = append(inv.podMonitors, inventoryPodMonitor{
		Component: "external-dns",
		Monitor:   installassets.ExternalDNSPodMonitor{Namespace: operatorNamespace}.Build(),
	})

	inv.ruleRequirements = append(inv.ruleRequirements,
		prometheusRuleRequirements("operator", installassets.HypershiftRecordingRule{Namespace: operatorNamespace}.Build())...)

	alertingNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: openshiftMonitoringNamespace}}
	inv.ruleRequirements = append(inv.ruleRequirements,
		prometheusRuleRequirements("slo-alerts", installassets.HypershiftAlertingRule{Namespace: alertingNamespace}.Build())...)
}

// scrapeTargetWaiver documents an intentional, tracked scrape-target exclusion.
type scrapeTargetWaiver struct {
	Component string `json:"component"`
	Namespace string `json:"namespace"`
	Selector  string `json:"selector"`
	Port      int32  `json:"port"`
	Path      string `json:"path"`
	Scheme    string `json:"scheme"`
	Tracking  string `json:"tracking"`
	Reason    string `json:"reason"`
}

func (w scrapeTargetWaiver) coverageKey() string {
	return fmt.Sprintf("%s|%s|%d|%s|%s", w.Namespace, w.Selector, w.Port, w.Path, w.Scheme)
}

// ruleWaiver documents an intentional, tracked rule-group exclusion.
type ruleWaiver struct {
	Namespace string    `json:"namespace"`
	Group     string    `json:"group"`
	Scope     ruleScope `json:"scope"`
	Tracking  string    `json:"tracking"`
	Reason    string    `json:"reason"`
}

func (w ruleWaiver) coverageKey() string {
	return ruleCoverageKey(w.Namespace, w.Group, w.Scope)
}

func ruleCoverageKey(namespace, group string, scope ruleScope) string {
	return fmt.Sprintf("%s|%s|%s", scope, namespace, group)
}

// parityWaivers is the checked-in waiver file format.
type parityWaivers struct {
	ScrapeTargets []scrapeTargetWaiver `json:"scrapeTargets"`
	Rules         []ruleWaiver         `json:"rules"`
}

func loadWaivers(t *testing.T) parityWaivers {
	t.Helper()

	path := filepath.Join(repoRoot(t), "test", "parity", "gmpmonitoring", "waivers.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read %s: %v", path, err)
	}

	var waivers parityWaivers
	if err := yaml.UnmarshalStrict(raw, &waivers); err != nil {
		t.Fatalf("failed to parse %s: %v", path, err)
	}
	return waivers
}

// requireDocumented asserts that a waiver names a tracking issue and a reason.
func (w scrapeTargetWaiver) requireDocumented(t *testing.T) {
	t.Helper()
	if w.Tracking == "" || w.Reason == "" {
		t.Errorf("scrape target waiver %s must set both tracking and reason", w.coverageKey())
	}
}

func (w ruleWaiver) requireDocumented(t *testing.T) {
	t.Helper()
	if w.Tracking == "" || w.Reason == "" {
		t.Errorf("rule waiver %s must set both tracking and reason", w.coverageKey())
	}
}

// auditedScopes applies the checked-in rule-scope audit to the discovered rule
// requirements and rejects groups that have not been audited.
func auditedScopes(t *testing.T, requirements []ruleRequirement) []ruleRequirement {
	t.Helper()

	audited := make([]ruleRequirement, 0, len(requirements))
	for _, requirement := range requirements {
		// The audit key deliberately ignores the discovered scope: the
		// requirement records what the control plane emits, and the audit
		// records what GMP must reproduce.
		scope, ok := auditedRuleScopes[requirement.Namespace+"/"+requirement.Group]
		if !ok {
			t.Errorf("rule group %q in namespace %q has no audited GMP scope; add it to auditedRuleScopes (key %q)",
				requirement.Group, requirement.Namespace, requirement.Namespace+"/"+requirement.Group)
			continue
		}
		if scope != ruleScopeNamespace {
			t.Errorf("rule group %q requires %q scope, which is not representable by the vendored GMP API; extend support/gmpmonitoring and the reconcilers first",
				requirement.Group, scope)
			continue
		}
		requirement.Scope = scope
		audited = append(audited, requirement)
	}
	return audited
}

// assertScrapeTargetParity fails for every CoreOS scrape target that has neither
// a GMP endpoint nor a documented waiver, and for waivers that no longer match an
// uncovered target.
func assertScrapeTargetParity(t *testing.T, coreOS, gmp []scrapeTarget, waivers []scrapeTargetWaiver) {
	t.Helper()

	uncovered := uncoveredTargets(coreOS, gmp)
	uncoveredByKey := make(map[string][]scrapeTarget, len(uncovered))
	for _, target := range uncovered {
		uncoveredByKey[target.coverageKey()] = append(uncoveredByKey[target.coverageKey()], target)
	}

	waived := make(map[string]struct{}, len(waivers))
	for _, waiver := range waivers {
		waiver.requireDocumented(t)

		key := waiver.coverageKey()
		if _, duplicate := waived[key]; duplicate {
			t.Errorf("duplicate scrape target waiver %s", key)
			continue
		}
		waived[key] = struct{}{}

		targets, ok := uncoveredByKey[key]
		if !ok {
			t.Errorf("stale scrape target waiver %s (%s): the target is now covered or no longer exists; remove the waiver",
				key, waiver.Component)
			continue
		}
		componentMatched := false
		for _, target := range targets {
			if target.Component == waiver.Component {
				componentMatched = true
				break
			}
		}
		if !componentMatched {
			t.Errorf("scrape target waiver %s names component %q, but the uncovered target belongs to %v",
				key, waiver.Component, uncoveredComponents(targets))
		}
	}

	for _, target := range uncovered {
		if _, ok := waived[target.coverageKey()]; ok {
			continue
		}
		t.Errorf("CoreOS scrape target has no GMP coverage and no waiver: %s", target)
	}
}

func uncoveredComponents(targets []scrapeTarget) []string {
	var components []string
	for _, target := range targets {
		components = append(components, target.Component)
	}
	sort.Strings(components)
	return components
}

// assertRuleParity fails for every CoreOS rule group that has neither a GMP rule
// group at the audited scope nor a documented waiver.
func assertRuleParity(t *testing.T, requirements []ruleRequirement, gmpGroups []gmpRuleGroup, waivers []ruleWaiver) {
	t.Helper()

	covered := make(map[string]struct{}, len(gmpGroups))
	for _, group := range gmpGroups {
		covered[ruleCoverageKey(group.Namespace, group.Group, group.Scope)] = struct{}{}
	}

	var uncovered []ruleRequirement
	for _, requirement := range requirements {
		key := ruleCoverageKey(requirement.Namespace, requirement.Group, requirement.Scope)
		if _, ok := covered[key]; ok {
			continue
		}
		uncovered = append(uncovered, requirement)
	}

	uncoveredByKey := make(map[string]ruleRequirement, len(uncovered))
	for _, requirement := range uncovered {
		uncoveredByKey[ruleCoverageKey(requirement.Namespace, requirement.Group, requirement.Scope)] = requirement
	}

	waived := make(map[string]struct{}, len(waivers))
	for _, waiver := range waivers {
		waiver.requireDocumented(t)

		key := waiver.coverageKey()
		if _, duplicate := waived[key]; duplicate {
			t.Errorf("duplicate rule waiver %s", key)
			continue
		}
		waived[key] = struct{}{}

		if _, ok := uncoveredByKey[key]; !ok {
			t.Errorf("stale rule waiver %s: the rule group is now covered or no longer exists; remove the waiver", key)
		}
	}

	for _, requirement := range uncovered {
		key := ruleCoverageKey(requirement.Namespace, requirement.Group, requirement.Scope)
		if _, ok := waived[key]; ok {
			continue
		}
		t.Errorf("CoreOS rule group has no GMP counterpart at the audited %s scope and no waiver: group=%q namespace=%q source=%s",
			requirement.Scope, requirement.Group, requirement.Namespace, requirement.Source)
	}
}
