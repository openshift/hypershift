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
		name     string
		level    hyperv1.LogLevel
		expected string
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(LogLevelToEtcdLevel(tt.level)).To(Equal(tt.expected))
		})
	}
}

func TestLogLevelToEtcdLevelPanicsOnUnsupported(t *testing.T) {
	// Trace and TraceAll are valid LogLevel values but are not supported by etcd
	// and are rejected at the API level (CEL). If one ever reaches this mapping
	// (e.g. via a non-CRD path), it must panic rather than silently degrade.
	tests := []struct {
		name  string
		level hyperv1.LogLevel
	}{
		{
			name:  "When LogLevel is Trace, it should panic",
			level: hyperv1.Trace,
		},
		{
			name:  "When LogLevel is TraceAll, it should panic",
			level: hyperv1.TraceAll,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(func() { LogLevelToEtcdLevel(tt.level) }).To(Panic())
		})
	}
}
