package hostedcontrolplane

import (
	"math"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/clock"
)

// Pruning constants ported from CVO (pkg/cvo/status_history.go).
const (
	maxHistory           = 100
	maxFinalEntryIndex   = 4
	mostImportantWeight  = 1000.0
	interestingWeight    = 30.0
	partialMinorWeight   = 20.0
	partialZStreamWeight = -20.0
	sliceIndexWeight     = -1.01
)

// ensureControlPlaneVersionPartial ensures a Partial history entry exists for the
// desired version without updating observedGeneration. This is called on list
// failure so consumers know an upgrade was attempted, consistent with CVO's
// syncFailingStatus pattern.
func ensureControlPlaneVersionPartial(
	hcp *hyperv1.HostedControlPlane,
	clk clock.Clock,
	releaseVersion string,
	desiredImage string,
) *hyperv1.ControlPlaneVersionStatus {
	now := metav1.NewTime(clk.Now())

	existing := hcp.Status.ControlPlaneVersion
	if existing == nil {
		return &hyperv1.ControlPlaneVersionStatus{
			Desired: configv1.Release{Version: releaseVersion, Image: desiredImage},
			History: []hyperv1.ControlPlaneUpdateHistory{
				{
					State:       configv1.PartialUpdate,
					StartedTime: now,
					Version:     releaseVersion,
					Image:       desiredImage,
				},
			},
			// Do not set observedGeneration — preserve zero value to signal
			// the generation was not successfully processed.
		}
	}

	result := existing.DeepCopy()
	result.Desired = configv1.Release{Version: releaseVersion, Image: desiredImage}
	// Preserve existing observedGeneration — do not update it.

	// There is no history yet, or the latest entry is for a different release
	// than what we're now targeting
	if len(result.History) == 0 || !mergeEqualVersions(&result.History[0], result.Desired) {
		if len(result.History) > 0 && result.History[0].CompletionTime == nil {
			result.History[0].CompletionTime = &now
		}
		entry := hyperv1.ControlPlaneUpdateHistory{
			State:       configv1.PartialUpdate,
			StartedTime: now,
			Version:     releaseVersion,
			Image:       desiredImage,
		}
		result.History = append([]hyperv1.ControlPlaneUpdateHistory{entry}, result.History...)
		result.History = pruneHistory(result.History)
	}
	return result
}

// reconcileControlPlaneVersion aggregates ControlPlaneComponent status into
// hcp.Status.ControlPlaneVersion, implementing CVO mergeEqualVersions
// semantics for change detection, dual version+rollout completion checks,
// first-population behavior, and observedGeneration updates.
func reconcileControlPlaneVersion(
	hcp *hyperv1.HostedControlPlane,
	components []hyperv1.ControlPlaneComponent,
	clk clock.Clock,
	releaseVersion string,
	desiredImage string,
) *hyperv1.ControlPlaneVersionStatus {
	now := metav1.NewTime(clk.Now())

	existing := hcp.Status.ControlPlaneVersion
	if existing == nil {
		// First population: create initial Partial entry.
		return &hyperv1.ControlPlaneVersionStatus{
			Desired: configv1.Release{Version: releaseVersion, Image: desiredImage},
			History: []hyperv1.ControlPlaneUpdateHistory{
				{
					State:       configv1.PartialUpdate,
					StartedTime: now,
					Version:     releaseVersion,
					Image:       desiredImage,
				},
			},
			ObservedGeneration: hcp.Generation,
		}
	}

	result := existing.DeepCopy()
	desired := configv1.Release{Version: releaseVersion, Image: desiredImage}
	result.Desired = desired
	result.ObservedGeneration = hcp.Generation

	// Detect desired release change using CVO mergeEqualVersions semantics.
	// If the current history entry does NOT merge with the new desired release,
	// a new version transition has occurred.
	if len(result.History) > 0 && !mergeEqualVersions(&result.History[0], desired) {
		// Close out the previous entry by setting CompletionTime.
		entry := &result.History[0]
		if entry.CompletionTime == nil {
			entry.CompletionTime = &now
		}
		// Prepend new Partial entry for the new desired release.
		newEntry := hyperv1.ControlPlaneUpdateHistory{
			State:       configv1.PartialUpdate,
			StartedTime: now,
			Version:     releaseVersion,
			Image:       desiredImage,
		}
		result.History = append([]hyperv1.ControlPlaneUpdateHistory{newEntry}, result.History...)
		result.History = pruneHistory(result.History)
		return result
	}

	// No desired change — check completion.
	// All components must report Status.Version == desired version AND RolloutComplete=True.
	if len(result.History) > 0 && result.History[0].State == configv1.PartialUpdate {
		if allComponentsAtVersion(components, releaseVersion) {
			result.History[0].State = configv1.CompletedUpdate
			result.History[0].CompletionTime = &now
		}
	}

	result.History = pruneHistory(result.History)
	return result
}

// mergeEqualVersions implements CVO's mergeEqualVersions semantics (pkg/cvo/status.go).
// It returns true if the current history entry and desired release refer to the same
// version, meaning no new history entry should be created. It also updates the current
// entry's version/image when the other side is empty, matching CVO behavior.
func mergeEqualVersions(current *hyperv1.ControlPlaneUpdateHistory, desired configv1.Release) bool {
	if len(desired.Image) > 0 && desired.Image == current.Image {
		if len(desired.Version) == 0 {
			return true
		}
		if len(current.Version) == 0 || desired.Version == current.Version {
			current.Version = desired.Version
			return true
		}
	}
	if len(desired.Version) > 0 && desired.Version == current.Version {
		if len(current.Image) == 0 || desired.Image == current.Image {
			current.Image = desired.Image
			return true
		}
	}
	return false
}

// allComponentsAtVersion returns true when every component reports
// Status.Version == desiredVersion AND has the RolloutComplete condition
// set to True. Returns false when there are no components.
// Components with no Status.Version or a mismatching version are treated
// as not matching the desired version and keep the state Partial.
func allComponentsAtVersion(components []hyperv1.ControlPlaneComponent, desiredVersion string) bool {
	if len(components) == 0 {
		return false
	}
	for i := range components {
		if components[i].Status.Version != desiredVersion {
			return false
		}
		cond := meta.FindStatusCondition(components[i].Status.Conditions, string(hyperv1.ControlPlaneComponentRolloutComplete))
		if cond == nil || cond.Status != metav1.ConditionTrue {
			return false
		}
	}
	return true
}

// pruneHistory caps the history at maxHistory entries by repeatedly removing
// the lowest-ranked entry, faithfully porting the CVO algorithm from
// pkg/cvo/status_history.go.
func pruneHistory(history []hyperv1.ControlPlaneUpdateHistory) []hyperv1.ControlPlaneUpdateHistory {
	for len(history) > maxHistory {
		history = pruneHistoryWithMax(history, maxHistory)
	}
	return history
}

// pruneHistoryWithMax is the core pruning implementation. maxSize is configurable
// to ease unit testing with smaller history slices.
func pruneHistoryWithMax(history []hyperv1.ControlPlaneUpdateHistory, maxSize int) []hyperv1.ControlPlaneUpdateHistory {
	if len(history) <= maxSize {
		return history
	}
	mostRecentCompletedIdx := -1
	for i := range history {
		if history[i].State == configv1.CompletedUpdate {
			mostRecentCompletedIdx = i
			break
		}
	}

	lowestRank := math.MaxFloat64
	var lowestRankIdx int

	for i := range history {
		rank := 0.0
		if i == maxSize || i <= maxFinalEntryIndex || i == mostRecentCompletedIdx {
			rank = mostImportantWeight
		} else if isTheFirstOrLastCompletedInAMinor(i, history, maxSize) {
			rank += interestingWeight
		} else if isPartialPortionOfMinorTransition(i, history, maxSize) {
			rank += partialMinorWeight
		} else if isPartialWithinAZStream(i, history, maxSize) {
			rank += partialZStreamWeight
		}
		rank += sliceIndexWeight * float64(i)

		if rank < lowestRank {
			lowestRank = rank
			lowestRankIdx = i
		}
	}

	if lowestRankIdx == maxSize {
		return history[:maxSize]
	}
	return append(history[:lowestRankIdx], history[lowestRankIdx+1:]...)
}

// isTheFirstOrLastCompletedInAMinor returns true if the entry at idx is the first or last
// completed update for a given minor version.
func isTheFirstOrLastCompletedInAMinor(idx int, h []hyperv1.ControlPlaneUpdateHistory, maxSize int) bool {
	if h[idx].State == configv1.PartialUpdate {
		return false
	}
	if idx == 0 || idx == maxSize {
		return true
	}
	nextOlder := findNextOlderCompleted(idx, h)
	if nextOlder == idx || !sameMinorVersion(h[idx], h[nextOlder]) {
		return true
	}
	nextNewer := findNextNewerCompleted(idx, h)
	if nextNewer == idx || !sameMinorVersion(h[idx], h[nextNewer]) {
		return true
	}
	return false
}

// isPartialPortionOfMinorTransition returns true if the entry at idx is a partial update
// between completed updates that transition from one minor version to another.
func isPartialPortionOfMinorTransition(idx int, h []hyperv1.ControlPlaneUpdateHistory, maxSize int) bool {
	if h[idx].State == configv1.CompletedUpdate || idx == 0 || idx == maxSize {
		return false
	}
	prevIdx := findNextOlderCompleted(idx, h)
	if prevIdx == idx {
		return false
	}
	nextIdx := findNextNewerCompleted(idx, h)
	if nextIdx == idx || sameMinorVersion(h[prevIdx], h[nextIdx]) {
		return false
	}
	return true
}

// isPartialWithinAZStream returns true if the entry at idx is a partial update between
// completed updates that transition from one z-stream version to another within the same minor.
func isPartialWithinAZStream(idx int, h []hyperv1.ControlPlaneUpdateHistory, maxSize int) bool {
	if h[idx].State == configv1.CompletedUpdate || idx == 0 || idx == maxSize {
		return false
	}
	prevIdx := findNextOlderCompleted(idx, h)
	if prevIdx == idx {
		return false
	}
	nextIdx := findNextNewerCompleted(idx, h)
	if nextIdx == idx || sameZStreamVersion(h[prevIdx], h[nextIdx]) {
		return false
	}
	return true
}

// findNextOlderCompleted returns the index of the next older (higher index) completed entry.
// Returns idx if none found.
func findNextOlderCompleted(idx int, h []hyperv1.ControlPlaneUpdateHistory) int {
	for i := idx + 1; i < len(h); i++ {
		if h[i].State == configv1.CompletedUpdate {
			return i
		}
	}
	return idx
}

// findNextNewerCompleted returns the index of the next newer (lower index) completed entry.
// Returns idx if none found.
func findNextNewerCompleted(idx int, h []hyperv1.ControlPlaneUpdateHistory) int {
	for i := idx - 1; i >= 0; i-- {
		if h[i].State == configv1.CompletedUpdate {
			return i
		}
	}
	return idx
}

// sameMinorVersion returns true if both entries have the same minor version.
func sameMinorVersion(a, b hyperv1.ControlPlaneUpdateHistory) bool {
	return getEffectiveMinor(a.Version) == getEffectiveMinor(b.Version)
}

// sameZStreamVersion returns true if both entries have the same minor and micro version.
func sameZStreamVersion(a, b hyperv1.ControlPlaneUpdateHistory) bool {
	return getEffectiveMinor(a.Version) == getEffectiveMinor(b.Version) &&
		getEffectiveMicro(a.Version) == getEffectiveMicro(b.Version)
}

// getEffectiveMinor returns the minor component (y) from a version string x.y[.z].
func getEffectiveMinor(version string) string {
	splits := strings.Split(version, ".")
	if len(splits) < 2 {
		return ""
	}
	return splits[1]
}

// getEffectiveMicro returns the micro/z-stream component (z) from a version string x.y.z.
func getEffectiveMicro(version string) string {
	splits := strings.Split(version, ".")
	if len(splits) < 3 {
		return ""
	}
	return splits[2]
}
