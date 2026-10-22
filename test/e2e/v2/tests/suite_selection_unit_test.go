//go:build e2ev2

package tests

import (
	"slices"
	"testing"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2/types"
)

func TestDefaultFixtureLabelFilter(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		requested string
		labels    []string
		selected  bool
	}{
		{name: "When no filter is requested, it should retain ordinary health coverage", labels: []string{"hosted-cluster-health"}, selected: true},
		{name: "When no filter is requested, it should retain optional CPO coverage", labels: []string{"hosted-cluster-cpo"}, selected: true},
		{name: "When no filter is requested, it should not require initial fixture metadata", labels: []string{"hosted-cluster-health-configured-workers"}},
		{name: "When no filter is requested, it should not require a mapped override fixture", labels: []string{"hosted-cluster-cpo-required"}},
		{name: "When no filter is requested, it should not require an HA zero-worker fixture", labels: []string{"hosted-cluster-break-glass-ha"}},
		{name: "When no filter is requested, it should not require KMS or blocked registry configuration", labels: []string{"hosted-cluster-custom-config"}},
		{name: "When no filter is requested, it should not require the native recovery fixture", labels: []string{"hosted-cluster-native-recovery"}},
		{name: "When native recovery is explicitly selected, it should preserve strict prerequisites", requested: "hosted-cluster-native-recovery", labels: []string{"hosted-cluster-native-recovery"}, selected: true},
		{name: "When initial fixture validation is explicitly selected, it should remain mandatory", requested: "hosted-cluster-health-configured-workers", labels: []string{"hosted-cluster-health-configured-workers"}, selected: true},
		{name: "When mapped CPO validation is explicitly selected, it should remain mandatory", requested: "hosted-cluster-cpo-required", labels: []string{"hosted-cluster-cpo-required"}, selected: true},
		{name: "When the caller provides an exclusion, it should preserve the caller filter", requested: "hosted-cluster-health && !Informing", labels: []string{"hosted-cluster-health", "Informing"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			actual := defaultFixtureLabelFilter(testCase.requested)
			if testCase.requested != "" && actual != testCase.requested {
				t.Fatalf("changed explicit caller filter %q to %q", testCase.requested, actual)
			}
			filter, err := types.ParseLabelFilter(actual)
			if err != nil {
				t.Fatal(err)
			}
			if got := filter(testCase.labels); got != testCase.selected {
				t.Fatalf("filter %q selected %v = %v, want %v", actual, testCase.labels, got, testCase.selected)
			}
		})
	}
}

func TestNetworkConfigurationLifecycleRegistration(t *testing.T) {
	filter, err := types.ParseLabelFilter("hosted-cluster-network-configuration")
	if err != nil {
		t.Fatal(err)
	}
	selected := 0
	for _, spec := range ginkgo.PreviewSpecs("network lifecycle selection regression").SpecReports {
		if !filter(spec.Labels()) {
			continue
		}
		selected++
		if !slices.Contains(spec.Labels(), "lifecycle") || !spec.IsSerial {
			t.Fatalf("mutating network spec lacks lifecycle/Serial metadata: %s %v", spec.FullText(), spec.Labels())
		}
	}
	if selected != 1 {
		t.Fatalf("selected %d network lifecycle bodies, want one", selected)
	}
}

func TestDefaultFixtureLabelFilterRegistrations(t *testing.T) {
	report := ginkgo.PreviewSpecs("fixture selection regression")
	defaultFilter, err := types.ParseLabelFilter(defaultFixtureLabelFilter(""))
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct {
		label           string
		defaultSelected bool
	}{
		{label: "hosted-cluster-break-glass-ha"},
		{label: "hosted-cluster-custom-config"},
		{label: "hosted-cluster-cpo-required"},
		{label: "hosted-cluster-health-configured-workers"},
		{label: "hosted-cluster-native-recovery"},
		{label: "native-etcd-recovery"},
		{label: "malformed-pull-secret-recovery"},
		{label: "aws-private-endpoint-transition"},
		{label: "hosted-cluster-api-validation", defaultSelected: true},
		{label: "hosted-cluster-break-glass", defaultSelected: true},
	} {
		t.Run("When "+fixture.label+" is registered, it should preserve default and explicit selection", func(t *testing.T) {
			explicitFilter, err := types.ParseLabelFilter(defaultFixtureLabelFilter(fixture.label))
			if err != nil {
				t.Fatal(err)
			}
			found := 0
			for _, spec := range report.SpecReports {
				labels := spec.Labels()
				if !explicitFilter(labels) {
					continue
				}
				found++
				if defaultFilter(labels) != fixture.defaultSelected {
					t.Errorf("default selection for actual inherited labels %v on %q does not match %v", labels, spec.FullText(), fixture.defaultSelected)
				}
			}
			if found == 0 {
				t.Fatal("no actual registered specs matched the explicit fixture selector")
			}
		})
	}
}
