//go:build e2ev2

package tests

import (
	"net"
	"os"
	"strconv"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/test/e2e/v2/lifecycle"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2/types"
)

func TestAzureTestMatrix(t *testing.T) {
	g := NewWithT(t)
	suiteConfig, _ := ginkgo.GinkgoConfiguration()
	if os.Getenv("E2E_HOSTED_CLUSTER_NAME") != "" || suiteConfig.LabelFilter != "" ||
		len(suiteConfig.FocusStrings) > 0 || len(suiteConfig.SkipStrings) > 0 {
		t.Skip("Azure test matrix validation requires an unfiltered test invocation")
	}

	report := ginkgo.PreviewSpecs("hypershift-e2e")
	g.Expect(report.SpecReports).NotTo(BeEmpty(), "unfiltered Ginkgo preview must contain registered specs")
	matrix := lifecycle.NewAzurePlatformConfig("").TestMatrix()

	previousFilters := []string{
		"self-managed-azure-public || nodepool-lifecycle || nodepool-arm64 || secret-encryption || control-plane-workloads || hosted-cluster-security || nodepool-osimagestream",
		"self-managed-azure-private || hosted-cluster-compliance",
		"self-managed-azure-oauth-lb || hosted-cluster-health || hosted-cluster-metrics || hosted-cluster-image-registry",
		"nodepool-autoscaling",
		"external-oidc || global-pull-secret",
		"control-plane-upgrade",
		"control-plane-pki-operator",
		"etcd-chaos",
	}

	previousSelection := selectedSpecs(g, report.SpecReports, previousFilters)
	currentSelection := selectedSpecs(g, report.SpecReports, testMatrixFilters(matrix))

	g.Expect(currentSelection).To(Equal(previousSelection),
		"the Azure matrix must select every previously selected spec exactly once")
	for spec, count := range previousSelection {
		g.Expect(count).To(Equal(1), "previous Azure matrix selected spec %q more than once", spec)
	}
	for spec, count := range currentSelection {
		g.Expect(count).To(Equal(1), "current Azure matrix selects spec %q more than once", spec)
	}

	junitFiles := testMatrixJUnitFiles(matrix)
	g.Expect(junitFiles).To(HaveEach(Not(BeEmpty())),
		"every Azure test group must define a JUnit filename")
	g.Expect(uniqueStrings(junitFiles)).To(HaveLen(len(junitFiles)),
		"every Azure test group must use a unique JUnit filename")

	groupNames := testMatrixGroupNames(matrix)
	g.Expect(groupNames).To(HaveEach(Not(BeEmpty())),
		"every Azure test group must define a name for artifact namespacing")
	g.Expect(uniqueStrings(groupNames)).To(HaveLen(len(groupNames)),
		"every Azure test group name must be unique for supplemental JUnit reports")

	variantLanes := testMatrixVariantLanes(matrix)
	expectedVariantLanes := map[string][]string{
		"private":       {"parallel:private"},
		"public":        {"sequential:public"},
		"autoscaling":   {"sequential:autoscaling"},
		"oauth-lb":      {"sequential:oauth-lb"},
		"external-oidc": {"sequential:external-oidc"},
		"upgrade":       {"sequential:upgrade-and-chaos"},
	}
	g.Expect(variantLanes).To(Equal(expectedVariantLanes),
		"every Azure variant must be assigned to its intended execution lane")
	for variant, lanes := range variantLanes {
		g.Expect(lanes).To(HaveLen(1),
			"hosted-cluster variant %q must appear in exactly one concurrent execution lane", variant)
	}
}

func selectedSpecs(g Gomega, specs types.SpecReports, filters []string) map[string]int {
	parsedFilters := make([]types.LabelFilter, 0, len(filters))
	for _, filter := range filters {
		parsed, err := types.ParseLabelFilter(filter)
		g.Expect(err).NotTo(HaveOccurred(), "label filter %q must parse", filter)
		parsedFilters = append(parsedFilters, parsed)
	}

	selected := map[string]int{}
	for _, spec := range specs {
		for _, filter := range parsedFilters {
			if filter(spec.Labels()) {
				id := net.JoinHostPort(spec.LeafNodeLocation.FileName, strconv.Itoa(spec.LeafNodeLocation.LineNumber)) + ": " + spec.FullText()
				selected[id]++
			}
		}
	}
	return selected
}

func testMatrixFilters(matrix lifecycle.TestMatrix) []string {
	var filters []string
	for _, group := range matrix.Parallel {
		filters = append(filters, group.LabelFilter)
	}
	for _, sequentialGroup := range matrix.Sequential {
		for _, step := range sequentialGroup.Steps {
			filters = append(filters, step.LabelFilter)
		}
	}
	return filters
}

func testMatrixJUnitFiles(matrix lifecycle.TestMatrix) []string {
	var files []string
	for _, group := range matrix.Parallel {
		files = append(files, group.JUnitFile())
	}
	for _, sequentialGroup := range matrix.Sequential {
		for _, step := range sequentialGroup.Steps {
			files = append(files, step.JUnitFile())
		}
	}
	return files
}

func testMatrixGroupNames(matrix lifecycle.TestMatrix) []string {
	var names []string
	for _, group := range matrix.Parallel {
		names = append(names, group.Name)
	}
	for _, sequentialGroup := range matrix.Sequential {
		for _, step := range sequentialGroup.Steps {
			names = append(names, step.Name)
		}
	}
	return names
}

func testMatrixVariantLanes(matrix lifecycle.TestMatrix) map[string][]string {
	lanes := map[string][]string{}
	for _, group := range matrix.Parallel {
		lanes[group.Variant] = append(lanes[group.Variant], "parallel:"+group.Name)
	}
	for _, sequentialGroup := range matrix.Sequential {
		lane := "sequential:" + sequentialGroup.Name
		for _, step := range sequentialGroup.Steps {
			if !contains(lanes[step.Variant], lane) {
				lanes[step.Variant] = append(lanes[step.Variant], lane)
			}
		}
	}
	return lanes
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
