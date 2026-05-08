package consoleoperator

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestIsRequestServing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		expected bool
	}{
		{
			name:     "console-operator is not request-serving",
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			co := &consoleOperator{}
			result := co.IsRequestServing()

			g.Expect(result).To(Equal(tc.expected))
		})
	}
}

func TestMultiZoneSpread(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		expected bool
	}{
		{
			name:     "console-operator should spread across zones for HA",
			expected: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			co := &consoleOperator{}
			result := co.MultiZoneSpread()

			g.Expect(result).To(Equal(tc.expected))
		})
	}
}

func TestNeedsManagementKASAccess(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		expected bool
	}{
		{
			name:     "console-operator only needs hosted cluster API access",
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			co := &consoleOperator{}
			result := co.NeedsManagementKASAccess()

			g.Expect(result).To(Equal(tc.expected))
		})
	}
}
