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

// addManagementResources adds the endpoint-bearing management-level monitors
// constructed by `hypershift install`.
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

	// The management-level monitors are generated like every other GMP manifest,
	// but they are not component assets, so they are loaded from their own tree.
	for _, dir := range []string{"operator", "external-dns"} {
		generated := loadGeneratedManagementPodMonitoring(t, dir)
		if generated == nil {
			t.Fatalf("management monitor %q has no staged GMP PodMonitoring", dir)
		}
		inv.podMonitorings = append(inv.podMonitorings, inventoryPodMonitoring{Component: dir, Monitor: generated})
	}
}

// scrapeTargetWaiver documents an intentional, tracked scrape-target exclusion.
type scrapeTargetWaiver struct {
	Component     string `json:"component"`
	Namespace     string `json:"namespace"`
	Selector      string `json:"selector"`
	Port          int32  `json:"port"`
	Path          string `json:"path"`
	Scheme        string `json:"scheme"`
	TLSServerName string `json:"tlsServerName"`
	Tracking      string `json:"tracking"`
	Reason        string `json:"reason"`
}

func (w scrapeTargetWaiver) coverageKey() string {
	return fmt.Sprintf("%s|%s|%d|%s|%s|%s", w.Namespace, w.Selector, w.Port, w.Path, w.Scheme, w.TLSServerName)
}

// parityWaivers is the checked-in waiver file format.
type parityWaivers struct {
	ScrapeTargets []scrapeTargetWaiver `json:"scrapeTargets"`
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

// documentationProblem reports a waiver that does not name both a tracking issue
// and a reason, or "" when it is documented.
func (w scrapeTargetWaiver) documentationProblem() string {
	if w.Tracking == "" || w.Reason == "" {
		return fmt.Sprintf("scrape target waiver %s must set both tracking and reason", w.coverageKey())
	}
	return ""
}

// scrapeTargetParityProblems reports every CoreOS scrape target that has neither
// a GMP endpoint nor a documented waiver, and every waiver that no longer matches
// an uncovered target.
func scrapeTargetParityProblems(coreOS, gmp []scrapeTarget, waivers []scrapeTargetWaiver) []string {
	var problems []string

	uncovered := uncoveredTargets(coreOS, gmp)
	uncoveredByKey := make(map[string][]scrapeTarget, len(uncovered))
	for _, target := range uncovered {
		uncoveredByKey[target.coverageKey()] = append(uncoveredByKey[target.coverageKey()], target)
	}

	waived := make(map[string]struct{}, len(waivers))
	for _, waiver := range waivers {
		if problem := waiver.documentationProblem(); problem != "" {
			problems = append(problems, problem)
		}

		key := waiver.coverageKey()
		if _, duplicate := waived[key]; duplicate {
			problems = append(problems, fmt.Sprintf("duplicate scrape target waiver %s", key))
			continue
		}
		waived[key] = struct{}{}

		targets, ok := uncoveredByKey[key]
		if !ok {
			problems = append(problems, fmt.Sprintf("stale scrape target waiver %s (%s): the target is now covered or no longer exists; remove the waiver",
				key, waiver.Component))
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
			problems = append(problems, fmt.Sprintf("scrape target waiver %s names component %q, but the uncovered target belongs to %v",
				key, waiver.Component, uncoveredComponents(targets)))
		}
	}

	for _, target := range uncovered {
		if _, ok := waived[target.coverageKey()]; ok {
			continue
		}
		problems = append(problems, fmt.Sprintf("CoreOS scrape target has no GMP coverage and no waiver: %s", target))
	}
	return problems
}

func uncoveredComponents(targets []scrapeTarget) []string {
	var components []string
	for _, target := range targets {
		components = append(components, target.Component)
	}
	sort.Strings(components)
	return components
}
