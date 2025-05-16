package controlplaneoperator

import (
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"

	configv1 "github.com/openshift/api/config/v1"
)

const (
	ComponentName = "control-plane-operator"
)

var _ component.ComponentOptions = &ControlPlaneOperatorOptions{}

type ControlPlaneOperatorOptions struct {
	HostedCluster *hyperv1.HostedCluster

	Image          string
	UtilitiesImage string
	HasUtilities   bool

	CertRotationScale           time.Duration
	RegistryOverrideCommandLine string
	OpenShiftRegistryOverrides  string
	DefaultIngressDomain        string

	FeatureSet configv1.FeatureSet
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (c *ControlPlaneOperatorOptions) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (c *ControlPlaneOperatorOptions) MultiZoneSpread() bool {
	return false
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (c *ControlPlaneOperatorOptions) NeedsManagementKASAccess() bool {
	return true
}

func NewComponent(options *ControlPlaneOperatorOptions) component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, options).
		WithAdaptFunction(options.adaptDeployment).
		WithManifestAdapter(
			"role.yaml",
			component.WithAdaptFunction(adaptRole),
		).
		WithManifestAdapter(
			"podmonitor.yaml",
			component.WithAdaptFunction(options.adaptPodMonitor),
		).
		InjectTokenMinterContainer(component.TokenMinterContainerOptions{
			TokenType:               component.CloudToken,
			ServiceAccountName:      "control-plane-operator",
			ServiceAccountNameSpace: "kube-system",
			KubeconfigSecretName:    "service-network-admin-kubeconfig",
		}).
		Build()
}
