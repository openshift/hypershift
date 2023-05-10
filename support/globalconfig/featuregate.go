package globalconfig

import (
	"fmt"
	"sort"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
)

func FeatureGateConfig() *configv1.FeatureGate {
	return &configv1.FeatureGate{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
	}
}

// Logic mostly copied from https://github.com/openshift/cluster-config-operator/blob/master/pkg/operator/featuregates/featuregate_controller.go

func ReconcileFeatureGateConfig(cfg *configv1.FeatureGate, hcfg *hyperv1.ClusterConfiguration) {
	if hcfg != nil && hcfg.FeatureGate != nil {
		cfg.Spec = *hcfg.FeatureGate
	}
}

func ReconcileFeatureGateConfigStatus(cfg *configv1.FeatureGate, hcfg *hyperv1.ClusterConfiguration, clusterVersion *configv1.ClusterVersion, version string) {
	knownVersions := sets.NewString(version)
	for _, cvoVersion := range clusterVersion.Status.History {
		knownVersions.Insert(cvoVersion.Version)
	}

	currentDetails, err := FeaturesGateDetailsFromFeatureSets(configv1.FeatureSets, cfg, version)
	if err != nil {
		// Error only occurs if FeatureSet name is not valid, should be caught in validation
		return
	}
	desiredFeatureGates := []configv1.FeatureGateDetails{*currentDetails}
	for i := range cfg.Status.FeatureGates {
		featureGateValues := cfg.Status.FeatureGates[i]
		if featureGateValues.Version == version {
			continue
		}
		if !knownVersions.Has(featureGateValues.Version) {
			continue
		}
		desiredFeatureGates = append(desiredFeatureGates, featureGateValues)
	}
	cfg.Status.FeatureGates = desiredFeatureGates
}

func featuresGatesFromFeatureSets(knownFeatureSets map[configv1.FeatureSet]*configv1.FeatureGateEnabledDisabled, cfg *configv1.FeatureGate) ([]configv1.FeatureGateName, []configv1.FeatureGateName, error) {
	if cfg.Spec.FeatureSet == configv1.CustomNoUpgrade {
		if cfg.Spec.FeatureGateSelection.CustomNoUpgrade != nil {
			completeEnabled, completeDisabled := completeFeatureGates(knownFeatureSets, cfg.Spec.FeatureGateSelection.CustomNoUpgrade.Enabled, cfg.Spec.FeatureGateSelection.CustomNoUpgrade.Disabled)
			return completeEnabled, completeDisabled, nil
		}
		return []configv1.FeatureGateName{}, []configv1.FeatureGateName{}, nil
	}

	featureSet, ok := knownFeatureSets[cfg.Spec.FeatureSet]
	if !ok {
		return []configv1.FeatureGateName{}, []configv1.FeatureGateName{}, fmt.Errorf(".spec.featureSet %q not found", featureSet)
	}

	completeEnabled, completeDisabled := completeFeatureGates(knownFeatureSets, toFeatureGateNames(featureSet.Enabled), toFeatureGateNames(featureSet.Disabled))
	return completeEnabled, completeDisabled, nil
}

func toFeatureGateNames(in []configv1.FeatureGateDescription) []configv1.FeatureGateName {
	out := []configv1.FeatureGateName{}
	for _, curr := range in {
		out = append(out, curr.FeatureGateAttributes.Name)
	}

	return out
}

// completeFeatureGates identifies every known feature and ensures that is explicitly on or explicitly off
func completeFeatureGates(knownFeatureSets map[configv1.FeatureSet]*configv1.FeatureGateEnabledDisabled, enabled, disabled []configv1.FeatureGateName) ([]configv1.FeatureGateName, []configv1.FeatureGateName) {
	specificallyEnabledFeatureGates := sets.NewString()
	for _, name := range enabled {
		specificallyEnabledFeatureGates.Insert(string(name))
	}
	specificallyDisabledFeatureGates := sets.NewString()
	for _, name := range disabled {
		specificallyDisabledFeatureGates.Insert(string(name))
	}

	knownFeatureGates := sets.NewString()
	knownFeatureGates.Union(specificallyEnabledFeatureGates)
	knownFeatureGates.Union(specificallyDisabledFeatureGates)
	for _, known := range knownFeatureSets {
		for _, curr := range known.Disabled {
			knownFeatureGates.Insert(string(curr.FeatureGateAttributes.Name))
		}
		for _, curr := range known.Enabled {
			knownFeatureGates.Insert(string(curr.FeatureGateAttributes.Name))
		}
	}

	allDisabledFeatureGates := knownFeatureGates.Difference(specificallyEnabledFeatureGates).UnsortedList()
	allDisabledFeatureGatesNames := []configv1.FeatureGateName{}
	for _, name := range allDisabledFeatureGates {
		allDisabledFeatureGatesNames = append(allDisabledFeatureGatesNames, configv1.FeatureGateName(name))
	}

	return enabled, allDisabledFeatureGatesNames
}

func FeaturesGateDetailsFromFeatureSets(featureSetMap map[configv1.FeatureSet]*configv1.FeatureGateEnabledDisabled, cfg *configv1.FeatureGate, currentVersion string) (*configv1.FeatureGateDetails, error) {
	enabled, disabled, err := featuresGatesFromFeatureSets(featureSetMap, cfg)
	if err != nil {
		return nil, err
	}
	currentDetails := configv1.FeatureGateDetails{
		Version: currentVersion,
	}
	for _, gateName := range enabled {
		currentDetails.Enabled = append(currentDetails.Enabled, configv1.FeatureGateAttributes{
			Name: gateName,
		})
	}
	for _, gateName := range disabled {
		currentDetails.Disabled = append(currentDetails.Disabled, configv1.FeatureGateAttributes{
			Name: gateName,
		})
	}

	// sort for stability
	sort.Sort(byName(currentDetails.Enabled))
	sort.Sort(byName(currentDetails.Disabled))

	return &currentDetails, nil
}

type byName []configv1.FeatureGateAttributes

func (a byName) Len() int      { return len(a) }
func (a byName) Swap(i, j int) { a[i], a[j] = a[j], a[i] }
func (a byName) Less(i, j int) bool {
	if strings.Compare(string(a[i].Name), string(a[j].Name)) < 0 {
		return true
	}
	return false
}
