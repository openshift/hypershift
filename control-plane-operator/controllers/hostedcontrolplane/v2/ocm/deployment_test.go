package ocm

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

func TestResolveOCMVerbosity(t *testing.T) {
	logLevel := func(l hyperv1.LogLevel) hyperv1.OpenShiftControllerManagerOperatorSpec {
		return hyperv1.OpenShiftControllerManagerOperatorSpec{
			ComponentLogLevelSpec: hyperv1.ComponentLogLevelSpec{LogLevel: &l},
		}
	}

	tests := []struct {
		name        string
		hcp         *hyperv1.HostedControlPlane
		expectedV   int
		expectedSet bool
	}{
		{
			name: "When no operatorConfiguration is set it should not override verbosity",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{},
			},
			expectedV:   0,
			expectedSet: false,
		},
		{
			name: "When operatorConfiguration exists but openShiftControllerManager logLevel is nil it should not override verbosity",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{},
				},
			},
			expectedV:   0,
			expectedSet: false,
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
			expectedV:   2,
			expectedSet: true,
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
			expectedV:   4,
			expectedSet: true,
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
			expectedV:   6,
			expectedSet: true,
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
			expectedV:   8,
			expectedSet: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			v, ok := resolveOCMVerbosity(tt.hcp)
			g.Expect(ok).To(Equal(tt.expectedSet))
			g.Expect(v).To(Equal(tt.expectedV))
		})
	}
}
