//go:build e2ev2

package tests

import (
	"os"
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
	validateAzureMatrixFilters(g, report.SpecReports, matrix)

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
		"private":          {"parallel:private"},
		"oauth-lb-private": {"parallel:oauth-lb-private"},
		"public":           {"sequential:public"},
		"oauth-lb":         {"sequential:oauth-lb"},
		"external-oidc":    {"sequential:external-oidc"},
		"upgrade":          {"sequential:upgrade-and-chaos"},
	}
	g.Expect(variantLanes).To(Equal(expectedVariantLanes),
		"every Azure variant must be assigned to its intended execution lane")
	for variant, lanes := range variantLanes {
		g.Expect(lanes).To(HaveLen(1),
			"hosted-cluster variant %q must appear in exactly one concurrent execution lane", variant)
	}
}

func validateAzureMatrixFilters(g Gomega, specs types.SpecReports, matrix lifecycle.TestMatrix) {
	validateGroup := func(group lifecycle.TestGroup) {
		filter, err := types.ParseLabelFilter(group.LabelFilter)
		g.Expect(err).NotTo(HaveOccurred(), "Azure test group %q must have a valid label filter", group.Name)

		matched := false
		for _, spec := range specs {
			if spec.LeafNodeType.Is(types.NodeTypeIt) && filter(spec.Labels()) {
				matched = true
				break
			}
		}
		g.Expect(matched).To(BeTrue(), "Azure test group %q must select at least one registered test", group.Name)
	}

	for _, group := range matrix.Parallel {
		validateGroup(group)
	}
	for _, sequential := range matrix.Sequential {
		for _, group := range sequential.Steps {
			validateGroup(group)
		}
	}
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
