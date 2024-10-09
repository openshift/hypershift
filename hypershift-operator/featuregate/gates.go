package featuregate

import (
	"k8s.io/component-base/featuregate"
)

var (
	// MutableGates is a mutable version of DefaultFeatureGate.
	// Only top-level commands/options setup and the k8s.io/component-base/featuregate/testing package should make use of this.
	// Tests that need to modify featuregate gates for the duration of their test should use:
	//   defer featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.<FeatureName>, <value>)()
	MutableGates featuregate.MutableFeatureGate = featuregate.NewFeatureGate()

	// Gates is a shared global FeatureGate.
	// Top-level commands/options setup that needs to modify this featuregate gate should use DefaultMutableFeatureGate.
	Gates featuregate.FeatureGate = MutableGates
)
