package pkioperator

import (
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"
)

const (
	ComponentName = "control-plane-pki-operator"
)

var _ component.ComponentOptions = &pkiOperator{}

type pkiOperator struct {
	certRotationScale time.Duration
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (c *pkiOperator) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (c *pkiOperator) MultiZoneSpread() bool {
	return false
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (c *pkiOperator) NeedsManagementKASAccess() bool {
	return true
}

func NewComponent(certRotationScale time.Duration) component.ControlPlaneComponent {
	operator := &pkiOperator{
		certRotationScale: certRotationScale,
	}
	return component.NewDeploymentComponent(ComponentName, operator).
		WithAdaptFunction(operator.adaptDeployment).
		WithPredicate(predicate).
		Build()
}

func predicate(cpContext component.WorkloadContext) (bool, error) {
	_, disablePKI := cpContext.HCP.Annotations[hyperv1.DisablePKIReconciliationAnnotation]
	return !disablePKI, nil
}
