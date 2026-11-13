package reporter

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/openshift/hypershift/contrib/hcp-infra/pkg/scanner"
)

// TextReporter formats inventory output for terminal display.
type TextReporter struct{}

// Report writes a human-readable text report to w.
func (t *TextReporter) Report(w io.Writer, inv *scanner.Inventory) error {
	// Header
	printHeader(w, inv)

	if inv.ScanMode == "detect" {
		return t.reportDetectMode(w, inv)
	}
	return t.reportStandardMode(w, inv)
}

func printHeader(w io.Writer, inv *scanner.Inventory) {
	line := strings.Repeat("━", 72)
	fmt.Fprintf(w, "\n%s\n", line)
	fmt.Fprintf(w, "  HyperShift Infrastructure Report\n")
	fmt.Fprintf(w, "%s\n", line)
	fmt.Fprintf(w, "  Account:  %s\n", inv.AccountID)
	fmt.Fprintf(w, "  Regions:  %s\n", strings.Join(inv.Regions, ", "))
	fmt.Fprintf(w, "  Date:     %s\n", inv.ScannedAt.Format("2006-01-02 15:04 MST"))
	fmt.Fprintf(w, "  Mode:     %s\n", inv.ScanMode)
	fmt.Fprintf(w, "%s\n\n", line)
}

func (t *TextReporter) reportStandardMode(w io.Writer, inv *scanner.Inventory) error {
	orphaned := inv.OrphanedGroups()
	active := inv.ActiveGroups()

	// Collect remaining groups (developer-owned, unknown)
	var other []*scanner.InfraGroup
	for _, g := range inv.Groups {
		if g.Category != scanner.CategoryActive &&
			g.Category != scanner.CategoryConfirmedOrphan &&
			g.Category != scanner.CategoryProbableOrphan {
			other = append(other, g)
		}
	}

	sortGroupsByAge(orphaned)
	sortGroupsByAge(active)
	sortGroupsByAge(other)

	// ORPHANED section
	if len(orphaned) > 0 {
		printSectionHeader(w, "ORPHANED", len(orphaned))
		printGroupTable(w, orphaned)
		fmt.Fprintln(w)
	}

	// ACTIVE section
	if len(active) > 0 {
		printSectionHeader(w, "ACTIVE", len(active))
		printGroupTable(w, active)
		fmt.Fprintln(w)
	}

	// OTHER section
	if len(other) > 0 {
		printSectionHeader(w, "OTHER", len(other))
		printGroupTable(w, other)
		fmt.Fprintln(w)
	}

	// Summary
	printSummary(w, inv, orphaned, active)

	// Clean hint
	if len(orphaned) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "To clean:  hcp-infra clean --all\n")
		fmt.Fprintf(w, "           hcp-infra clean --infra-id <ID>\n")
	}

	fmt.Fprintln(w)
	return nil
}

func (t *TextReporter) reportDetectMode(w io.Writer, inv *scanner.Inventory) error {
	// Categorize groups
	var confirmed, probable, devOwned, active, unknown []*scanner.InfraGroup
	for _, g := range inv.Groups {
		switch g.Category {
		case scanner.CategoryConfirmedOrphan:
			confirmed = append(confirmed, g)
		case scanner.CategoryProbableOrphan:
			probable = append(probable, g)
		case scanner.CategoryDeveloperOwned:
			devOwned = append(devOwned, g)
		case scanner.CategoryActive:
			active = append(active, g)
		default:
			unknown = append(unknown, g)
		}
	}

	sortGroupsByAge(confirmed)
	sortGroupsByAge(probable)
	sortGroupsByAge(devOwned)
	sortGroupsByAge(active)
	sortGroupsByAge(unknown)

	if len(confirmed) > 0 {
		printSectionHeader(w, "CONFIRMED ORPHANS", len(confirmed))
		printGroupTable(w, confirmed)
		fmt.Fprintln(w)
	}

	if len(probable) > 0 {
		printSectionHeader(w, "PROBABLE ORPHANS", len(probable))
		printGroupTable(w, probable)
		fmt.Fprintln(w)
	}

	if len(devOwned) > 0 {
		printSectionHeader(w, "DEVELOPER-OWNED", len(devOwned))
		printGroupTable(w, devOwned)
		fmt.Fprintln(w)
	}

	if len(active) > 0 {
		printSectionHeader(w, "ACTIVE", len(active))
		printGroupTable(w, active)
		fmt.Fprintln(w)
	}

	if len(unknown) > 0 {
		printSectionHeader(w, "UNKNOWN ORIGIN", len(unknown))
		printGroupTable(w, unknown)
		fmt.Fprintln(w)
	}

	orphaned := append(confirmed, probable...)
	printSummary(w, inv, orphaned, active)

	if len(orphaned) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "To clean:  hcp-infra clean --all\n")
		fmt.Fprintf(w, "           hcp-infra clean --infra-id <ID>\n")
	}

	fmt.Fprintln(w)
	return nil
}

func printSectionHeader(w io.Writer, title string, count int) {
	fmt.Fprintf(w, "  %s (%d)\n", title, count)
	fmt.Fprintf(w, "  %s\n", strings.Repeat("─", 68))
}

func printGroupTable(w io.Writer, groups []*scanner.InfraGroup) {
	if len(groups) == 0 {
		return
	}

	// Calculate column widths dynamically
	infraIDWidth := 10 // minimum
	clusterWidth := 12 // minimum
	for _, g := range groups {
		if len(g.InfraID) > infraIDWidth {
			infraIDWidth = len(g.InfraID)
		}
		name := displayName(g)
		if len(name) > clusterWidth {
			clusterWidth = len(name)
		}
	}

	// Cap widths
	if infraIDWidth > 30 {
		infraIDWidth = 30
	}
	if clusterWidth > 30 {
		clusterWidth = 30
	}

	// Header row
	fmtStr := fmt.Sprintf("  %%-%ds  %%-%ds  %%5s  %%4s  %%8s\n", infraIDWidth, clusterWidth)
	fmt.Fprintf(w, fmtStr, "Infra ID", "Cluster Name", "Age", "Res", "Cost/mo")

	for _, g := range groups {
		name := displayName(g)
		if len(name) > clusterWidth {
			name = name[:clusterWidth-1] + "…"
		}
		infraID := g.InfraID
		if len(infraID) > infraIDWidth {
			infraID = infraID[:infraIDWidth-1] + "…"
		}

		fmt.Fprintf(w, fmtStr,
			infraID,
			name,
			formatAge(g.CreatedAt),
			fmt.Sprintf("%d", len(g.Resources)),
			formatCost(g.MonthlyCost),
		)
	}
}

func printSummary(w io.Writer, inv *scanner.Inventory, orphaned, active []*scanner.InfraGroup) {
	totalGroups := len(inv.Groups)
	totalResources := 0
	totalCost := 0.0
	orphanCost := 0.0

	for _, g := range inv.Groups {
		totalResources += len(g.Resources)
		totalCost += g.MonthlyCost
	}
	for _, g := range orphaned {
		orphanCost += g.MonthlyCost
	}

	fmt.Fprintf(w, "  Summary: %d clusters (%d orphaned, %d active), %d resources, ~$%.0f/mo total (~$%.0f/mo orphaned)\n",
		totalGroups, len(orphaned), len(active), totalResources, totalCost, orphanCost)
}

func displayName(g *scanner.InfraGroup) string {
	if g.ClusterName != "" {
		return g.ClusterName
	}
	return "(unknown)"
}

func formatAge(t time.Time) string {
	if t.IsZero() {
		return "?"
	}
	days := int(math.Floor(time.Since(t).Hours() / 24))
	if days < 0 {
		days = 0
	}
	return fmt.Sprintf("%dd", days)
}

func formatCost(cost float64) string {
	if cost == 0 {
		return "-"
	}
	return fmt.Sprintf("~$%.0f", cost)
}

func sortGroupsByAge(groups []*scanner.InfraGroup) {
	sort.Slice(groups, func(i, j int) bool {
		// Oldest first (most likely orphaned)
		if groups[i].CreatedAt.IsZero() {
			return false
		}
		if groups[j].CreatedAt.IsZero() {
			return true
		}
		return groups[i].CreatedAt.Before(groups[j].CreatedAt)
	})
}
