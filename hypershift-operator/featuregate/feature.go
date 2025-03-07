package featuregate

import (
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/component-base/featuregate"
)

const (
	// AROHCPManagedIdentities is a feature gate for enabling HCP components to authenticate with Azure by client certificate
	// owner: @username
	// alpha: v0.1.49
	// beta x.y.z
	AROHCPManagedIdentities featuregate.Feature = "AROHCPManagedIdentities"

	// OpenStack is a feature gate for running clusters on OpenStack.
	// owner: @username
	// alpha: v0.1.49
	// beta: x.y.z
	OpenStack featuregate.Feature = "OpenStack"

	// DisableClusterCapabilities gates whether Hypershift supports
	// disabling cluster capabilities at install time.
	// owner: @fmissi
	// alpha: v0.1.54
	// beta: x.y.z
	DisableClusterCapabilities featuregate.Feature = "DisableClusterCapabilities"

	// MinimumKubeletVersion gates whether hosted clusters will
	// obey the MinimumKubeletVersion configuration field on the NodeSpec object.
	// When enabled and the field is set, no kubelets can join a hosted cluster
	// that has an older version than the minimumm version specified.
	// owner: @haircommander
	// alpha: v0.1.51
	// beta: x.y.z
	MinimumKubeletVersion featuregate.Feature = "MinimumKubeletVersion"
)

func init() {
	runtime.Must(MutableGates.Add(defaultHypershiftFeatureGates))
}

var defaultHypershiftFeatureGates = map[featuregate.Feature]featuregate.FeatureSpec{
	// Every feature should be initiated here:
	AROHCPManagedIdentities:    {Default: false, PreRelease: featuregate.Alpha},
	OpenStack:                  {Default: false, PreRelease: featuregate.Alpha},
	DisableClusterCapabilities: {Default: false, PreRelease: featuregate.Alpha},
	MinimumKubeletVersion:      {Default: false, PreRelease: featuregate.Alpha},

	// TODO(alberto): Add the rest of the features here
	// CPOV2:         {Default: false, PreRelease: featuregate.Alpha},
	// AWSTenancy:    {Default: false, PreRelease: featuregate.Alpha},
}
