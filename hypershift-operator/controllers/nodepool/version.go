package nodepool

import (
	"regexp"
	"sort"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"

	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
)

// versionKey is used as a map key for grouping machines by version.
type versionKey struct {
	ocpVersion     string
	kubeletVersion string
}

// nodeVersionsFromMachines aggregates version and health information from CAPI Machines.
// It groups machines by (ocpVersion, kubeletVersion) and counts ready/unready nodes
// based on the CAPI NodeHealthy condition.
func (r *NodePoolReconciler) nodeVersionsFromMachines(machines []*capiv1.Machine, nodePool *hyperv1.NodePool) []hyperv1.NodeVersion {
	type counts struct {
		ready   int32
		unready int32
	}
	versionCounts := make(map[versionKey]*counts)

	for _, machine := range machines {
		// Skip machines that haven't registered a node yet.
		if machine.Status.NodeInfo == nil {
			continue
		}

		kubeletVersion := machine.Status.NodeInfo.KubeletVersion

		// Resolve OCP version from Machine annotation.
		// For replace upgrades, the annotation is propagated via the MachineDeployment template at Machine creation.
		// For in-place upgrades, the annotation is set by the in-place upgrader (sourced from the token secret)
		// after each node completes its upgrade.
		// Fallback to nodePool.Status.Version for machines created before this annotation existed.
		ocpVersion := machine.Annotations[hyperv1.NodePoolReleaseVersionAnnotation]
		if ocpVersion == "" {
			ocpVersion = nodePool.Status.Version
		}

		key := versionKey{ocpVersion: ocpVersion, kubeletVersion: kubeletVersion}
		if _, exists := versionCounts[key]; !exists {
			versionCounts[key] = &counts{}
		}

		// Determine node health from CAPI NodeHealthy condition.
		condition := findMachineStatusCondition(machine, string(capiv1.MachineNodeHealthyCondition))
		if condition != nil && condition.Status == corev1.ConditionTrue {
			versionCounts[key].ready++
		} else {
			versionCounts[key].unready++
		}
	}

	if len(versionCounts) == 0 {
		return nil
	}

	result := make([]hyperv1.NodeVersion, 0, len(versionCounts))
	for key, c := range versionCounts {
		result = append(result, hyperv1.NodeVersion{
			OCPVersion:       key.ocpVersion,
			KubeletVersion:   key.kubeletVersion,
			ReadyNodeCount:   &c.ready,
			UnreadyNodeCount: &c.unready,
		})
	}

	// Sort for deterministic output: by ocpVersion, then kubeletVersion.
	sort.Slice(result, func(i, j int) bool {
		if result[i].OCPVersion != result[j].OCPVersion {
			return result[i].OCPVersion < result[j].OCPVersion
		}
		return result[i].KubeletVersion < result[j].KubeletVersion
	})

	return result
}

// setNodesInfoStatus aggregates node version and health information from CAPI Machines
// and sets it on nodePool.Status.NodesInfo.
func (r *NodePoolReconciler) setNodesInfoStatus(nodePool *hyperv1.NodePool, machines []*capiv1.Machine) {
	nodeVersions := r.nodeVersionsFromMachines(machines, nodePool)
	nodePool.Status.NodesInfo = hyperv1.NodePoolNodesInfo{
		NodeVersions: nodeVersions,
	}
}

// rhcosOSImageRe matches the RHCOS version from the NodeInfo.OSImage string.
// The first capture group is the leading digit of the RHCOS version (e.g. "4"
// in "419.97…"), which determines the RHEL generation (4xx → RHEL 9, 5xx → RHEL 10).
var rhcosOSImageRe = regexp.MustCompile(`Red Hat Enterprise Linux CoreOS (\d)\d{2}\.`)

// rhcosStreamFromOSImage parses a Machine's NodeInfo.OSImage string and
// returns the corresponding RHEL stream name. RHCOS versions starting with
// 4xx map to RHEL 9 and 5xx to RHEL 10.
// Returns empty string if the OS image string is unrecognized.
func rhcosStreamFromOSImage(osImage string) string {
	matches := rhcosOSImageRe.FindStringSubmatch(osImage)
	if len(matches) < 2 {
		return ""
	}
	switch matches[1] {
	case "4":
		return StreamRHEL9
	case "5":
		return StreamRHEL10
	default:
		return ""
	}
}

// osImageStreamFromMachines determines the observed RHEL stream by examining
// Machine NodeInfo.OSImage across the pool. Returns the stream name when a
// majority of observed machines report the same stream, or empty string when
// no majority exists or no machines have reported yet.
func osImageStreamFromMachines(machines []*capiv1.Machine) string {
	streamCounts := make(map[string]int)
	total := 0
	for _, machine := range machines {
		if machine.Status.NodeInfo == nil {
			continue
		}
		stream := rhcosStreamFromOSImage(machine.Status.NodeInfo.OSImage)
		if stream == "" {
			continue
		}
		streamCounts[stream]++
		total++
	}

	if total == 0 {
		return ""
	}

	// Set status when a strict majority (> N/2) of observed nodes agree.
	for stream, count := range streamCounts {
		if count > total/2 {
			return stream
		}
	}

	return ""
}

// setOSImageStreamStatus infers the RHEL stream from observed Machine
// NodeInfo.OSImage and sets nodePool.Status.OSImageStream when a majority
// of machines report a consistent stream.
// When no majority exists (e.g. during rolling upgrades, scale-to-zero, or
// unrecognized OS images), the status retains its previous value to avoid
// flapping. It reflects the "last-known majority" and is intentionally never
// reset to empty.
func (r *NodePoolReconciler) setOSImageStreamStatus(nodePool *hyperv1.NodePool, machines []*capiv1.Machine) {
	stream := osImageStreamFromMachines(machines)
	if stream != "" {
		nodePool.Status.OSImageStream = hyperv1.OSImageStreamReference{Name: stream}
	}
}
