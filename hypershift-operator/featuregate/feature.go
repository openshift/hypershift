package featuregate

import (
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/component-base/featuregate"
)

const (
	// OpenStack is a feature gate for running clusters on OpenStack.
	// owner: @username
	// alpha: v0.1.49
	// beta: x.y.z
	OpenStack featuregate.Feature = "OpenStack"

	// MinimumKubeletVersion is a feature gate for enabling the MinimumKubeletVersion feature.
	// When enabled and the corresponding HostedControlPlane API is set to a version v, hosted apiservers will
	// deny (most) authorization requests from kubelets that are older than v.
	// owner: @haircommander
	// alpha: v0.1.50
	// beta: x.y.z
	MinimumKubeletVersion featuregate.Feature = "MinimumKubeletVersion"
)

func init() {
	runtime.Must(MutableGates.Add(defaultHypershiftFeatureGates))
}

var defaultHypershiftFeatureGates = map[featuregate.Feature]featuregate.FeatureSpec{
	// Every feature should be initiated here:
	OpenStack:             {Default: false, PreRelease: featuregate.Alpha},
	MinimumKubeletVersion: {Default: false, PreRelease: featuregate.Alpha},

	// TODO(alberto): Add the rest of the features here
	// CPOV2:         {Default: false, PreRelease: featuregate.Alpha},
	// AWSTenancy:    {Default: false, PreRelease: featuregate.Alpha},
}
