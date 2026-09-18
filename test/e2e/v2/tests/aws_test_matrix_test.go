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

func TestAWSTestMatrix(t *testing.T) {
	g := NewWithT(t)
	suiteConfig, _ := ginkgo.GinkgoConfiguration()
	if os.Getenv("E2E_HOSTED_CLUSTER_NAME") != "" || suiteConfig.LabelFilter != "" ||
		len(suiteConfig.FocusStrings) > 0 || len(suiteConfig.SkipStrings) > 0 {
		t.Skip("AWS test matrix validation requires an unfiltered test invocation")
	}

	report := ginkgo.PreviewSpecs("hypershift-e2e")
	g.Expect(report.SpecReports).NotTo(BeEmpty(), "unfiltered Ginkgo preview must contain registered specs")
	matrix := lifecycle.NewAWSPlatformConfig(lifecycle.AWSPlatformOptions{}, "").TestMatrix()
	validateTestMatrixFilters(g, "AWS", report.SpecReports, matrix)

	junitFiles := testMatrixJUnitFiles(matrix)
	g.Expect(junitFiles).To(HaveEach(Not(BeEmpty())),
		"every AWS test group must define a JUnit filename")
	g.Expect(uniqueStrings(junitFiles)).To(HaveLen(len(junitFiles)),
		"every AWS test group must use a unique JUnit filename")

	groupNames := testMatrixGroupNames(matrix)
	g.Expect(groupNames).To(HaveEach(Not(BeEmpty())),
		"every AWS test group must define a name for artifact namespacing")
	g.Expect(uniqueStrings(groupNames)).To(HaveLen(len(groupNames)),
		"every AWS test group name must be unique for supplemental JUnit reports")

	expectedVariantLanes := map[string][]string{
		"public":            {"sequential:public"},
		"autoscaling":       {"sequential:autoscaling"},
		"external-oidc":     {"sequential:external-oidc"},
		"upgrade":           {"sequential:upgrade-and-chaos"},
		"karpenter":         {"parallel:karpenter"},
		"karpenter-upgrade": {"parallel:karpenter-upgrade"},
	}
	g.Expect(testMatrixVariantLanes(matrix)).To(Equal(expectedVariantLanes),
		"every AWS variant must be assigned to its intended execution lane")

	// These are the AWS-specific and platform-agnostic capabilities, including
	// the coverage provided by the Azure matrix. Keep this list explicit so
	// removing a filter cannot silently reduce AWS coverage again.
	requiredLabels := []string{
		"hosted-cluster-aws",
		"hosted-cluster-compliance",
		"hosted-cluster-node-communication",
		"hosted-cluster-cpo",
		"nodepool-arm64",
		"secret-encryption",
		"control-plane-workloads",
		"hosted-cluster-security",
		"nodepool-osimagestream",
		"hosted-cluster-ingress",
		"hosted-cluster-dns",
		"hosted-cluster-health",
		"hosted-cluster-metrics",
		"hosted-cluster-image-registry",
		"nodepool-vm-size-rollout",
		"nodepool-replace-version-upgrade",
		"nodepool-inplace-version-upgrade",
		"nodepool-n1-release",
		"nodepool-n2-release",
		"nodepool-auto-repair",
		"nodepool-osimagestream-upgrade",
		"nodepool-machineconfig-rollout",
		"nodepool-autoscaling-balancing",
		"nodepool-nto-replace-rollout",
		"nodepool-nto-inplace-rollout",
		"nodepool-performance-profile",
		"nodepool-mirror-config",
		"external-oidc",
		"global-pull-secret",
		"nodepool-autoscaling-scale-up-down",
		"nodepool-trust-bundle",
		"control-plane-upgrade",
		"control-plane-pki-operator",
		"etcd-chaos",
		"karpenter",
		"karpenter-upgrade",
	}
	for _, label := range requiredLabels {
		g.Expect(matrixSelectsLabels(g, matrix, []string{label})).To(BeTrue(),
			"AWS test matrix must select required label %q", label)
	}
}

func matrixSelectsLabels(g Gomega, matrix lifecycle.TestMatrix, labels []string) bool {
	for _, group := range matrix.Parallel {
		filter, err := types.ParseLabelFilter(group.LabelFilter)
		g.Expect(err).NotTo(HaveOccurred())
		if filter(labels) {
			return true
		}
	}
	for _, sequential := range matrix.Sequential {
		for _, group := range sequential.Steps {
			filter, err := types.ParseLabelFilter(group.LabelFilter)
			g.Expect(err).NotTo(HaveOccurred())
			if filter(labels) {
				return true
			}
		}
	}
	return false
}
