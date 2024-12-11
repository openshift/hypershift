package csi

import (
	component "github.com/openshift/hypershift/support/controlplane-component"
)

const (
	ComponentName = "container-storage-interface"
)

var _ component.ComponentOptions = &containerStorageInterface{}

type containerStorageInterface struct{}

func (s *containerStorageInterface) IsRequestServing() bool {
	return false
}

func (s *containerStorageInterface) MultiZoneSpread() bool {
	return false
}

func (s *containerStorageInterface) NeedsManagementKASAccess() bool {
	return false
}

func NewComponent() component.ControlPlaneComponent {
	csi := &containerStorageInterface{}

	return component.NewDeploymentComponent(ComponentName, csi).
		WithManifestAdapter(
			"serviceaccount.yaml",
			component.WithAdaptFunction(adaptServiceAccount),
		).
		WithManifestAdapter("role.yaml").
		WithManifestAdapter(
			"rolebinding.yaml",
			component.WithAdaptFunction(adaptRoleBinding)).
		WithManifestAdapter(
			"deployment.yaml",
			component.WithAdaptFunction(adaptDeployment)).
		Build()
}
