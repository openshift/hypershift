package konnectivity

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestKonnectivityAgentIsRequestServing(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		expected bool
	}{
		{
			name:     "When called it should return false",
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			ka := &konnectivityAgent{}
			result := ka.IsRequestServing()

			g.Expect(result).To(Equal(tc.expected))
		})
	}
}

func TestKonnectivityAgentMultiZoneSpread(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		expected bool
	}{
		{
			name:     "When called it should return true",
			expected: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			ka := &konnectivityAgent{}
			result := ka.MultiZoneSpread()

			g.Expect(result).To(Equal(tc.expected))
		})
	}
}

func TestKonnectivityAgentNeedsManagementKASAccess(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		expected bool
	}{
		{
			name:     "When called it should return false",
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			ka := &konnectivityAgent{}
			result := ka.NeedsManagementKASAccess()

			g.Expect(result).To(Equal(tc.expected))
		})
	}
}
