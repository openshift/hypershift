//go:build envtest

// This file is adapted from github.com/openshift/api/tests/crd_filter.go
// The only change is matchesFeatureSet: openshift/api checks
// annotations["release.openshift.io/feature-set"], but HyperShift's featuregate
// manifests use spec.featureSet instead. We check both for compatibility.
package envtest

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/sets"

	kyaml "sigs.k8s.io/yaml"
)

var (
	clusterProfileToShortName = map[string]string{
		"include.release.openshift.io/ibm-cloud-managed":              "Hypershift",
		"include.release.openshift.io/self-managed-high-availability": "SelfManagedHA",
		"include.release.openshift.io/single-node-developer":          "SingleNode",
	}
)

func perTestRuntimeInfo(suitePath, crdName string, featureGates []string) (*PerTestRuntimeInfo, error) {
	crdFilesToCheck := []string{}

	relativePathForCRDs := filepath.Join(suitePath, "..", "..", "zz_generated.crd-manifests")

	generatedCRDs, err := os.ReadDir(relativePathForCRDs)
	if err != nil {
		return nil, err
	}
	for _, currCRDFile := range generatedCRDs {
		relativeFilename := filepath.Join(relativePathForCRDs, currCRDFile.Name())
		filename, err := filepath.Abs(relativeFilename)
		if err != nil {
			return nil, fmt.Errorf("could not generate absolute path for %q: %w", relativeFilename, err)
		}

		currCRD, err := loadCRDFromFile(filename)
		if err != nil {
			continue
		}
		if currCRD.Name != crdName {
			continue
		}
		if len(featureGates) == 0 {
			crdFilesToCheck = append(crdFilesToCheck, filename)
			continue
		}

		featureSetAnnotation := currCRD.Annotations["release.openshift.io/feature-set"]
		featureSets := sets.New[string]()
		if featureSetAnnotation != "" {
			for _, featureSet := range strings.Split(featureSetAnnotation, ",") {
				featureSets.Insert(featureSet)
			}
		}

		if featureSets.Has("CustomNoUpgrade") {
			if anyRequireDisabledFeatureGate(featureGates) {
				continue
			}
			crdFilesToCheck = append(crdFilesToCheck, filename)
			continue
		}
		clusterProfilesForCRD := clusterProfilesFrom(currCRD.Annotations)
		if len(clusterProfilesForCRD) == 0 {
			crdFilesToCheck = append(crdFilesToCheck, filename)
			continue
		}
		versionsForCRD := versionsFrom(currCRD.Annotations)

		clusterProfileToCheck := clusterProfilesForCRD.List()[0]
		featureGatePath := filepath.Join(suitePath, "..", "..", "payload-manifests", "featuregates")
		featureGateStatus, err := featureGatesForClusterProfileFeatureSetVersion(featureGatePath, clusterProfileToCheck, featureSets, versionsForCRD)
		if err != nil {
			return nil, fmt.Errorf("unable to find featureGates to check for %v: %w", filename, err)
		}

		keep := true
		for _, featureGate := range featureGates {
			requiresFeatureGateDisabled := strings.HasPrefix(featureGate, "-")

			var enabled, found bool
			switch {
			case requiresFeatureGateDisabled:
				disabledFeatureGate := strings.TrimPrefix(featureGate, "-")
				enabled, found = featureGateStatus[disabledFeatureGate]
				if !found {
					return nil, fmt.Errorf("unable to find featureGate/%v to check for %v", featureGate, filename)
				}
				if enabled {
					keep = false
				}
			default:
				enabled, found = featureGateStatus[featureGate]
				if !found {
					return nil, fmt.Errorf("unable to find featureGate/%v to check for %v", featureGate, filename)
				}
				if !enabled {
					keep = false
				}
			}
		}

		if keep {
			crdFilesToCheck = append(crdFilesToCheck, filename)
		}
	}

	return &PerTestRuntimeInfo{
		CRDFilenames: crdFilesToCheck,
	}, nil
}

func anyRequireDisabledFeatureGate(featureGates []string) bool {
	for _, fg := range featureGates {
		if strings.HasPrefix(fg, "-") {
			return true
		}
	}
	return false
}

func clusterProfilesFrom(annotations map[string]string) sets.String {
	ret := sets.NewString()
	for k, v := range annotations {
		if strings.HasPrefix(k, "include.release.openshift.io/") && v == "true" {
			ret.Insert(k)
		}
	}
	return ret
}

func versionsFrom(annotations map[string]string) sets.Set[uint64] {
	var versionString string
	for k, v := range annotations {
		if strings.HasPrefix(k, "release.openshift.io/major-version") {
			versionString = v
			break
		}
	}

	versions := sets.New[uint64]()
	for _, version := range strings.Split(versionString, ",") {
		versionInt, err := strconv.ParseUint(strings.TrimSpace(version), 10, 64)
		if err != nil {
			continue
		}
		versions.Insert(versionInt)
	}

	return versions
}

func clusterProfilesShortNamesFrom(annotations map[string]string) sets.String {
	ret := sets.NewString()
	for k, v := range annotations {
		if strings.HasPrefix(k, "include.release.openshift.io/") && v == "true" {
			ret.Insert(clusterProfileToShortName[k])
		}
	}
	return ret
}

func featureGatesForClusterProfileFeatureSetVersion(payloadFeatureGatePath, clusterProfile string, featureSetNames sets.Set[string], crdVersions sets.Set[uint64]) (map[string]bool, error) {
	if len(featureSetNames) == 0 {
		featureSetNames.Insert("Default")
	}

	var uncastFeatureGate map[string]interface{}

	if err := filepath.WalkDir(payloadFeatureGatePath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("unable to walk directory %q: %w", payloadFeatureGatePath, err)
		}
		if d.IsDir() {
			return nil
		}

		rawFile, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		featureGate := map[string]interface{}{}
		if err := kyaml.Unmarshal(rawFile, &featureGate); err != nil {
			return err
		}

		annotations, _, err := unstructured.NestedStringMap(featureGate, "metadata", "annotations")
		if err != nil {
			return err
		}

		if matchesClusterProfile(annotations, clusterProfile) && matchesFeatureSet(featureGate, featureSetNames) && matchesVersions(annotations, crdVersions) {
			uncastFeatureGate = featureGate
			return filepath.SkipAll
		}

		return nil
	}); err != nil {
		return nil, err
	}

	if uncastFeatureGate == nil {
		return nil, fmt.Errorf("no feature gate found for cluster profile %q, feature set %q, and versions %v", clusterProfile, strings.Join(featureSetNames.UnsortedList(), ","), crdVersions)
	}

	uncastFeatureGateSlice, _, err := unstructured.NestedSlice(uncastFeatureGate, "status", "featureGates")
	if err != nil {
		return nil, fmt.Errorf("no slice found %w", err)
	}
	enabledFeatureGates, _, err := unstructured.NestedSlice(uncastFeatureGateSlice[0].(map[string]interface{}), "enabled")
	if err != nil {
		return nil, fmt.Errorf("no enabled found %w", err)
	}
	disabledFeatureGates, _, err := unstructured.NestedSlice(uncastFeatureGateSlice[0].(map[string]interface{}), "disabled")
	if err != nil {
		return nil, fmt.Errorf("no disabled found %w", err)
	}

	featureGateMapping := map[string]bool{}
	for _, currGate := range enabledFeatureGates {
		featureGateName, _, err := unstructured.NestedString(currGate.(map[string]interface{}), "name")
		if err != nil {
			return nil, fmt.Errorf("no gate name found %w", err)
		}
		featureGateMapping[featureGateName] = true
	}
	for _, currGate := range disabledFeatureGates {
		featureGateName, _, err := unstructured.NestedString(currGate.(map[string]interface{}), "name")
		if err != nil {
			return nil, fmt.Errorf("no gate name found %w", err)
		}
		featureGateMapping[featureGateName] = false
	}

	return featureGateMapping, nil
}

func matchesClusterProfile(annotations map[string]string, clusterProfile string) bool {
	_, ok := annotations[clusterProfile]
	return ok
}

// matchesFeatureSet checks if the featuregate manifest matches the required feature sets.
// It checks both annotations["release.openshift.io/feature-set"] (openshift/api convention)
// and spec.featureSet (HyperShift convention) for compatibility.
func matchesFeatureSet(featureGate map[string]interface{}, crdFeatureSets sets.Set[string]) bool {
	// Check annotation first (openshift/api convention).
	annotations, _, _ := unstructured.NestedStringMap(featureGate, "metadata", "annotations")
	if fs, ok := annotations["release.openshift.io/feature-set"]; ok {
		return crdFeatureSets.Has(fs)
	}

	// Fall back to spec.featureSet (HyperShift convention).
	specFS, _, _ := unstructured.NestedString(featureGate, "spec", "featureSet")
	// Empty spec.featureSet means "Default".
	if specFS == "" {
		specFS = "Default"
	}
	return crdFeatureSets.Has(specFS)
}

func matchesVersions(annotations map[string]string, crdVersions sets.Set[uint64]) bool {
	return len(crdVersions) == 0 || versionsFrom(annotations).Intersection(crdVersions).Len() > 0
}
