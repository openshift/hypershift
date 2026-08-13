package ocm

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

func TestResolveOCMVerbosity(t *testing.T) {
	logLevel := func(l hyperv1.LogLevel) hyperv1.OpenShiftControllerManagerOperatorSpec {
		return hyperv1.OpenShiftControllerManagerOperatorSpec{
			ComponentLogLevelSpec: hyperv1.ComponentLogLevelSpec{LogLevel: l},
		}
	}

	tests := []struct {
		name     string
		hcp      *hyperv1.HostedControlPlane
		expected int
	}{
		{
			name: "When no operatorConfiguration is set it should default to verbosity 2",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{},
			},
			expected: 2,
		},
		{
			name: "When operatorConfiguration exists but openShiftControllerManager logLevel is nil it should default to verbosity 2",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{},
				},
			},
			expected: 2,
		},
		{
			name: "When openShiftControllerManager logLevel is Normal it should return verbosity 2",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{
						OpenShiftControllerManager: logLevel(hyperv1.Normal),
					},
				},
			},
			expected: 2,
		},
		{
			name: "When openShiftControllerManager logLevel is Debug it should return verbosity 4",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{
						OpenShiftControllerManager: logLevel(hyperv1.Debug),
					},
				},
			},
			expected: 4,
		},
		{
			name: "When openShiftControllerManager logLevel is Trace it should return verbosity 6",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{
						OpenShiftControllerManager: logLevel(hyperv1.Trace),
					},
				},
			},
			expected: 6,
		},
		{
			name: "When openShiftControllerManager logLevel is TraceAll it should return verbosity 8",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{
						OpenShiftControllerManager: logLevel(hyperv1.TraceAll),
					},
				},
			},
			expected: 8,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(resolveOCMVerbosity(tt.hcp)).To(Equal(tt.expected))
		})
	}
}
