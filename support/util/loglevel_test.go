package util

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

func TestLogLevelToKlogVerbosity(t *testing.T) {
	tests := []struct {
		name     string
		level    hyperv1.LogLevel
		expected int
	}{
		{
			name:     "When LogLevel is empty, it should default to verbosity 2",
			level:    hyperv1.LogLevel(""),
			expected: 2,
		},
		{
			name:     "When LogLevel is Normal, it should return verbosity 2",
			level:    hyperv1.Normal,
			expected: 2,
		},
		{
			name:     "When LogLevel is Debug, it should return verbosity 4",
			level:    hyperv1.Debug,
			expected: 4,
		},
		{
			name:     "When LogLevel is Trace, it should return verbosity 6",
			level:    hyperv1.Trace,
			expected: 6,
		},
		{
			name:     "When LogLevel is TraceAll, it should return verbosity 8",
			level:    hyperv1.TraceAll,
			expected: 8,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(LogLevelToKlogVerbosity(tt.level)).To(Equal(tt.expected))
		})
	}
}

func TestLogLevelToEtcdLevel(t *testing.T) {
	tests := []struct {
		name        string
		level       hyperv1.LogLevel
		expected    string
		expectPanic bool
	}{
		{
			name:     "When LogLevel is empty, it should return etcd level info",
			level:    hyperv1.LogLevel(""),
			expected: "info",
		},
		{
			name:     "When LogLevel is Normal, it should return etcd level info",
			level:    hyperv1.Normal,
			expected: "info",
		},
		{
			name:     "When LogLevel is Debug, it should return etcd level debug",
			level:    hyperv1.Debug,
			expected: "debug",
		},
		{
			name:        "When LogLevel is Trace, it should panic",
			level:       hyperv1.Trace,
			expectPanic: true,
		},
		{
			name:        "When LogLevel is TraceAll, it should panic",
			level:       hyperv1.TraceAll,
			expectPanic: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			if tt.expectPanic {
				// Trace levels are rejected by etcd's API-level validation. Panic if
				// one reaches this mapping through a non-CRD path.
				g.Expect(func() { LogLevelToEtcdLevel(tt.level) }).To(Panic())
				return
			}
			g.Expect(LogLevelToEtcdLevel(tt.level)).To(Equal(tt.expected))
		})
	}
}
