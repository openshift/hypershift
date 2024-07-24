package featuregate

import (
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/component-base/featuregate"
)

const (
	// Every feature gate should add method here following this template:
	//
	// // owner: @username
	// // alpha: v1.X
	// MyFeature featuregate.Feature = "MyFeature".

	// AutoProvision is a feature gate for AutoProvision functionality implemented via karpenter.
	//
	// alpha: v0.1.39
	// beta: x.y.z
	AutoProvision featuregate.Feature = "AutoProvision"
)

func init() {
	runtime.Must(MutableGates.Add(defaultHypershiftFeatureGates))
}

var defaultHypershiftFeatureGates = map[featuregate.Feature]featuregate.FeatureSpec{
	// Every feature should be initiated here:
	AutoProvision: {Default: false, PreRelease: featuregate.Alpha},
}
