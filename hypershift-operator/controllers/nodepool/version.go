package nodepool

import (
	"context"
	"fmt"
	"sort"
	"strings"

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
func (r *NodePoolReconciler) nodeVersionsFromMachines(_ context.Context, machines []*capiv1.Machine, nodePool *hyperv1.NodePool) []hyperv1.NodeVersion {
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
// and sets it on nodePool.Status.NodesInfo and infers the OS stream for status.osImageStream.
func (r *NodePoolReconciler) setNodesInfoStatus(ctx context.Context, nodePool *hyperv1.NodePool) error {
	machines, err := r.getMachinesForNodePool(ctx, nodePool)
	if err != nil {
		return fmt.Errorf("failed to get Machines for NodesInfo: %w", err)
	}

	nodeVersions := r.nodeVersionsFromMachines(ctx, machines, nodePool)
	nodePool.Status.NodesInfo = hyperv1.NodePoolNodesInfo{
		NodeVersions: nodeVersions,
	}

	// Infer OS stream from node OSImage and set status.osImageStream.
	if stream := inferOSStreamFromMachines(machines); stream != "" {
		nodePool.Status.OSImageStream = hyperv1.OSImageStreamReference{
			Name: stream,
		}
	}

	return nil
}

// inferOSStreamFromMachines inspects CAPI Machine NodeInfo to determine the RHEL OS stream.
// RHCOS version strings starting with "4" (e.g., "4xx.x.x") indicate RHEL 9, while
// version strings starting with "5" (e.g., "5xx.x.x") indicate RHEL 10.
// Returns the stream observed on the plurality of nodes (RHEL 10 wins ties), or empty if no determination can be made.
func inferOSStreamFromMachines(machines []*capiv1.Machine) string {
	rhel9Count := 0
	rhel10Count := 0

	for _, machine := range machines {
		if machine.Status.NodeInfo == nil {
			continue
		}
		osImage := machine.Status.NodeInfo.OSImage
		if osImage == "" {
			continue
		}

		// RHCOS versions follow the pattern "Red Hat Enterprise Linux CoreOS 4xx.yy.zzzz..."
		// or similar. The RHCOS major version maps to the RHEL stream.
		stream := inferStreamFromOSImage(osImage)
		switch stream {
		case RHELStreamRHEL9:
			rhel9Count++
		case RHELStreamRHEL10:
			rhel10Count++
		}
	}

	if rhel10Count > 0 && rhel10Count >= rhel9Count {
		return RHELStreamRHEL10
	}
	if rhel9Count > 0 {
		return RHELStreamRHEL9
	}
	return ""
}

// inferStreamFromOSImage extracts the RHEL stream from a node's OSImage string.
// RHCOS versions starting with "4" map to RHEL 9, versions starting with "5" map to RHEL 10.
func inferStreamFromOSImage(osImage string) string {
	// Expected format: "Red Hat Enterprise Linux CoreOS 417.94.202501011234-0" or similar.
	// Match tokens that look like a RHCOS version: 3+ digits, a dot, then more digits (e.g. "417.94").
	parts := strings.Fields(osImage)
	for _, part := range parts {
		dotIdx := strings.IndexByte(part, '.')
		if dotIdx < 3 || dotIdx >= len(part)-1 {
			continue
		}
		allDigitsBeforeDot := true
		for i := 0; i < dotIdx; i++ {
			if part[i] < '0' || part[i] > '9' {
				allDigitsBeforeDot = false
				break
			}
		}
		if !allDigitsBeforeDot {
			continue
		}
		if part[0] == '4' {
			return RHELStreamRHEL9
		}
		if part[0] == '5' {
			return RHELStreamRHEL10
		}
	}
	return ""
}
