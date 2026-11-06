package classifier

import (
	"strings"

	"github.com/openshift/hypershift/contrib/hcp-infra/pkg/scanner"
)

// Classify categorizes each InfraGroup in the inventory as ACTIVE,
// CONFIRMED ORPHAN, PROBABLE ORPHAN, or UNKNOWN ORIGIN.
//
// activeHCInfraIDs maps infra-id -> HostedCluster name for clusters that
// currently exist on the management cluster. Pass nil if no management
// cluster kubeconfig is available (classification proceeds without it).
func Classify(inv *scanner.Inventory, activeHCInfraIDs map[string]string) {
	for _, g := range inv.Groups {
		// Set owner to cluster name; the --owner flag does prefix matching
		// on ClusterName at the scan level, so this is the correct value.
		g.Owner = g.ClusterName

		// 1. If the management cluster knows about this infra ID, it's active.
		if activeHCInfraIDs != nil {
			if hcName, ok := activeHCInfraIDs[g.InfraID]; ok {
				g.Category = scanner.CategoryActive
				g.HasHC = true
				if g.ClusterName == "" {
					g.ClusterName = hcName
					g.Owner = hcName
				}
				continue
			}
		}

		// 2. If a VPC still exists in this group, consider it active.
		// VPCs are the last thing destroyed, so their presence implies the
		// cluster is still being used or destruction is incomplete.
		if g.HasVPC {
			g.Category = scanner.CategoryActive
			continue
		}

		// 3-5. No VPC and not on management cluster → orphan classification.
		// Check whether all resources carry the infra-id tag (which they
		// should, since we found them via the Tagging API).
		allTagged := true
		for _, r := range g.Resources {
			if r.Tags == nil {
				allTagged = false
				break
			}
			if _, ok := r.Tags[scanner.TagInfraID]; !ok {
				allTagged = false
				break
			}
		}

		if allTagged {
			// All resources consistently tagged → confirmed orphan.
			g.Category = scanner.CategoryConfirmedOrphan
		} else {
			// Some resources lack the tag → probable orphan (may have
			// been partially cleaned up or tagged inconsistently).
			g.Category = scanner.CategoryProbableOrphan
		}
	}

	// Classify ungrouped resources as UNKNOWN ORIGIN.
	for i := range inv.Ungrouped {
		inv.Ungrouped[i].InfraID = ""
	}
}

// ClassifySummary holds aggregate statistics from classification.
type ClassifySummary struct {
	Active           int
	ConfirmedOrphans int
	ProbableOrphans  int
	Unknown          int
	TotalResources   int
	OrphanCost       float64
}

// Summarize returns aggregate counts and costs from a classified inventory.
func Summarize(inv *scanner.Inventory) ClassifySummary {
	var s ClassifySummary

	for _, g := range inv.Groups {
		s.TotalResources += len(g.Resources)

		switch g.Category {
		case scanner.CategoryActive:
			s.Active++
		case scanner.CategoryConfirmedOrphan:
			s.ConfirmedOrphans++
			s.OrphanCost += g.MonthlyCost
		case scanner.CategoryProbableOrphan:
			s.ProbableOrphans++
			s.OrphanCost += g.MonthlyCost
		case scanner.CategoryUnknown:
			s.Unknown++
		}
	}

	s.TotalResources += len(inv.Ungrouped)
	s.Unknown += len(inv.Ungrouped)

	return s
}

// FilterByOwner returns a new inventory containing only groups whose
// ClusterName has the given prefix. This is used by the --owner flag.
func FilterByOwner(inv *scanner.Inventory, ownerPrefix string) *scanner.Inventory {
	if ownerPrefix == "" {
		return inv
	}

	filtered := scanner.NewInventory(inv.AccountID, inv.Regions, inv.ScanMode)
	filtered.ScannedAt = inv.ScannedAt

	for id, g := range inv.Groups {
		if strings.HasPrefix(g.ClusterName, ownerPrefix) || strings.HasPrefix(g.Owner, ownerPrefix) {
			filtered.Groups[id] = g
		}
	}

	// Ungrouped resources can't be filtered by owner since they have no
	// cluster association.
	filtered.Ungrouped = inv.Ungrouped

	return filtered
}
