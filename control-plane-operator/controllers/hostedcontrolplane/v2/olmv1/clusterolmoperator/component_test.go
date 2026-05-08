package clusterolmoperator

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
			name:     "cluster-olm-operator is not request-serving",
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			co := &clusterOLMOperator{}
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
			name:     "cluster-olm-operator should spread across zones for HA",
			expected: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			co := &clusterOLMOperator{}
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
			name:     "cluster-olm-operator needs management cluster API access for ClusterOperator status reporting",
			expected: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			co := &clusterOLMOperator{}
			result := co.NeedsManagementKASAccess()

			g.Expect(result).To(Equal(tc.expected))
		})
	}
}
