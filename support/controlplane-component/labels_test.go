package controlplanecomponent

import (
	"os"
	"regexp"
	"strings"
	"testing"

	. "github.com/onsi/gomega"
)

func TestFilterUserLabels(t *testing.T) {
	tests := []struct {
		name            string
		labels          map[string]string
		expectedLabels  map[string]string
		expectedWarning string
	}{
		{
			name:           "When labels are nil it should preserve nil",
			labels:         nil,
			expectedLabels: nil,
		},
		{
			name:           "When labels are empty it should preserve an empty map",
			labels:         map[string]string{},
			expectedLabels: map[string]string{},
		},
		{
			name:           "When labels are valid it should retain them",
			labels:         map[string]string{"team": "platform", "example.com/tier": ""},
			expectedLabels: map[string]string{"team": "platform", "example.com/tier": ""},
		},
		{
			name:            "When a key is invalid it should drop the label",
			labels:          map[string]string{"not/a valid key": "platform"},
			expectedLabels:  map[string]string{},
			expectedWarning: `label "not/a valid key" has an invalid key`,
		},
		{
			name:            "When a value is invalid it should drop the label",
			labels:          map[string]string{"team": "invalid/value"},
			expectedLabels:  map[string]string{},
			expectedWarning: `label "team" has an invalid value`,
		},
		{
			name:            "When a reserved key is supplied it should drop the label",
			labels:          map[string]string{"control-plane": "attacker"},
			expectedLabels:  map[string]string{},
			expectedWarning: `label "control-plane" is reserved for HyperShift control-plane use`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			filtered, warnings := FilterUserLabels(tt.labels)

			g.Expect(filtered).To(Equal(tt.expectedLabels))
			if tt.expectedWarning == "" {
				g.Expect(warnings).To(BeEmpty())
				return
			}
			g.Expect(warnings).To(HaveLen(1))
			g.Expect(strings.Contains(warnings[0], tt.expectedWarning)).To(BeTrue(), "warning %q does not contain %q", warnings[0], tt.expectedWarning)
		})
	}
}

func TestIsReservedLabelKey(t *testing.T) {
	t.Run("When a key is reserved it should return true", func(t *testing.T) {
		if !isReservedLabelKey("control-plane") {
			t.Error("expected control-plane to be reserved")
		}
	})

	t.Run("When a key is not reserved it should return false", func(t *testing.T) {
		if isReservedLabelKey("team") {
			t.Error("expected team not to be reserved")
		}
	})

	t.Run("When CEL reserved key rules are declared they should match the Go set", func(t *testing.T) {
		reservedKeyPattern := regexp.MustCompile(`key != '([^']+)'`)

		for _, filename := range []string{
			"../../api/hypershift/v1beta1/hostedcluster_types.go",
			"../../api/hypershift/v1beta1/hosted_controlplane.go",
		} {
			t.Run(filename, func(t *testing.T) {
				contents, err := os.ReadFile(filename)
				if err != nil {
					t.Fatalf("failed to read %s: %v", filename, err)
				}

				validatedKeys := map[string]struct{}{}
				for _, line := range strings.Split(string(contents), "\n") {
					if !strings.Contains(line, "+kubebuilder:validation:XValidation") || !strings.Contains(line, "label key is reserved for HyperShift control-plane use") {
						continue
					}
					for _, match := range reservedKeyPattern.FindAllStringSubmatch(line, -1) {
						validatedKeys[match[1]] = struct{}{}
					}
				}

				if len(validatedKeys) != len(reservedControlPlaneLabelKeys) {
					t.Fatalf("reserved label keys differ: CEL has %d keys, Go has %d", len(validatedKeys), len(reservedControlPlaneLabelKeys))
				}
				for key := range reservedControlPlaneLabelKeys {
					if _, found := validatedKeys[key]; !found {
						t.Errorf("reserved label key %q is missing from the CEL rule", key)
					}
				}
				for key := range validatedKeys {
					if !isReservedLabelKey(key) {
						t.Errorf("CEL reserves label key %q but the Go set does not", key)
					}
				}
			})
		}
	})
}
