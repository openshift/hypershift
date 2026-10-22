//go:build e2ev2

package tests

import (
	"testing"

	controlplaneoperatoroverrides "github.com/openshift/hypershift/hypershift-operator/controlplaneoperator-overrides"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2/types"
)

func TestCPOOverrideImageForTest(t *testing.T) {
	for _, testCase := range []struct {
		name, platform, version string
		required, wantError     bool
	}{
		{name: "When explicit override has no platform mapping, it should reject the fixture", platform: "unmapped", version: "4.17.43", required: true, wantError: true},
		{name: "When explicit override has no release mapping, it should reject the fixture", platform: "AWS", version: "0.0.0", required: true, wantError: true},
		{name: "When the required platform is empty, it should reject the fixture", version: "4.17.43", required: true, wantError: true},
		{name: "When the required observed version is empty, it should reject the fixture", platform: "AWS", required: true, wantError: true},
		{name: "When an ordinary release has no mapping, it should permit a skip", platform: "AWS", version: "0.0.0"},
		{name: "When an ordinary platform has no mapping, it should preserve optional behavior", platform: "unmapped", version: "4.17.43"},
		{name: "When the observed AWS latest override version is mapped, it should return its exact image", platform: "AWS", version: "4.17.43", required: true},
		{name: "When the observed AWS previous override version is mapped, it should return its exact image", platform: "AWS", version: "4.17.20", required: true},
		{name: "When an explicitly selected release is mapped, it should return its exact image", platform: "Azure", version: "4.19.7", required: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			image, err := cpoOverrideImageForTest(testCase.platform, testCase.version, testCase.required)
			if (err != nil) != testCase.wantError {
				t.Fatalf("unexpected fixture validation: %v", err)
			}
			if image != controlplaneoperatoroverrides.CPOImage(testCase.platform, testCase.version) {
				t.Fatal("expected image must exactly match the production override map")
			}
			if testCase.required && !testCase.wantError && image == "" {
				t.Fatal("the positive fixture must select a genuinely mapped override")
			}
		})
	}
}

func TestCPOOverrideRegistrationSelection(t *testing.T) {
	t.Setenv("TEST_CPO_OVERRIDE", "")
	report := ginkgo.PreviewSpecs("CPO override registration selection")
	for _, testCase := range []struct {
		name, selectedLabel, excludedLabel string
	}{
		{name: "When required override is selected without a process flag, it should select only the dedicated spec", selectedLabel: "hosted-cluster-cpo-required", excludedLabel: "hosted-cluster-cpo"},
		{name: "When the ordinary CPO label is selected, it should preserve the existing optional spec", selectedLabel: "hosted-cluster-cpo", excludedLabel: "hosted-cluster-cpo-required"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			matched := 0
			for _, spec := range report.SpecReports {
				if !spec.LeafNodeType.Is(types.NodeTypeIt) {
					continue
				}
				selected, err := spec.MatchesLabelFilter(testCase.selectedLabel)
				if err != nil {
					t.Fatal(err)
				}
				if !selected {
					continue
				}
				matched++
				excluded, err := spec.MatchesLabelFilter(testCase.excludedLabel)
				if err != nil || excluded {
					t.Fatalf("CPO registrations must not share their selection labels: %v", err)
				}
			}
			if matched != 1 {
				t.Fatalf("label %s must select exactly one native spec, got %d", testCase.selectedLabel, matched)
			}
		})
	}
}
