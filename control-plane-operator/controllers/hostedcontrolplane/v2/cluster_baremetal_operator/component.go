package cbo

import (
	component "github.com/openshift/hypershift/support/controlplane-component"
)

const (
	// This is the name of your manifests directory.
	ComponentName = "cluster-baremetal-operator"
)

var _ component.ComponentOptions = &clusterBaremetalOperator{}

type clusterBaremetalOperator struct {
}

// Specify whether this component serves requests outside its node.
func (m *clusterBaremetalOperator) IsRequestServing() bool {
	return false
}

// Specify whether this component's workload (pods) should be spread across availability zones
func (m *clusterBaremetalOperator) MultiZoneSpread() bool {
	return false
}

// Specify whether this component requires access to the kube-apiserver of the cluster where the workload is running
func (m *clusterBaremetalOperator) NeedsManagementKASAccess() bool {
	return true
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &clusterBaremetalOperator{}).
		WithDependencies().
		Build()
}
