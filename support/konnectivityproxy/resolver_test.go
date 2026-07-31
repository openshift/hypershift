package konnectivityproxy

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
)

func TestFilterIPv4(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{
			name:     "When mixed IPv4 and IPv6, it should return only IPv4",
			input:    []string{"2001:db8::1", "192.168.1.1", "fd00::2", "10.0.0.1"},
			expected: []string{"192.168.1.1", "10.0.0.1"},
		},
		{
			name:     "When only IPv4, it should return all",
			input:    []string{"192.168.1.1", "10.0.0.1"},
			expected: []string{"192.168.1.1", "10.0.0.1"},
		},
		{
			name:     "When only IPv6, it should return empty",
			input:    []string{"2001:db8::1", "fd00::2"},
			expected: nil,
		},
		{
			name:     "When empty, it should return empty",
			input:    []string{},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(filterIPv4(tt.input)).To(Equal(tt.expected))
		})
	}
}

func TestResolvePreferIPv4(t *testing.T) {
	t.Run("When resolving localhost, it should return an IPv4 address", func(t *testing.T) {
		g := NewWithT(t)
		_, ip, err := resolvePreferIPv4(context.Background(), "localhost")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(ip).ToNot(BeNil())
		g.Expect(ip.To4()).ToNot(BeNil(), "expected IPv4 address for localhost")
	})

	t.Run("When resolving an IPv6 literal, it should return an IPv6 address", func(t *testing.T) {
		g := NewWithT(t)
		_, ip, err := resolvePreferIPv4(context.Background(), "::1")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(ip).ToNot(BeNil())
		g.Expect(ip.To4()).To(BeNil(), "expected IPv6 address when no IPv4 is available")
	})

	t.Run("When context is canceled, it should return context error", func(t *testing.T) {
		g := NewWithT(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		cancel()
		_, _, err := resolvePreferIPv4(ctx, "localhost")
		g.Expect(err).To(HaveOccurred())
		g.Expect(err).To(MatchError(context.Canceled))
	})
}
