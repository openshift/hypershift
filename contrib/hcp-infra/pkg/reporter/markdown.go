package reporter

import (
	"fmt"
	"io"
	"strings"

	"github.com/openshift/hypershift/contrib/hcp-infra/pkg/scanner"
)

// MarkdownReporter formats inventory output as Markdown.
type MarkdownReporter struct{}

// Report writes a Markdown-formatted report to w.
func (m *MarkdownReporter) Report(w io.Writer, inv *scanner.Inventory) error {
	// Header
	fmt.Fprintf(w, "# HyperShift Infrastructure Report\n\n")
	fmt.Fprintf(w, "| Field | Value |\n")
	fmt.Fprintf(w, "|-------|-------|\n")
	fmt.Fprintf(w, "| Account | %s |\n", inv.AccountID)
	fmt.Fprintf(w, "| Regions | %s |\n", strings.Join(inv.Regions, ", "))
	fmt.Fprintf(w, "| Date | %s |\n", inv.ScannedAt.Format("2006-01-02 15:04 MST"))
	fmt.Fprintf(w, "| Mode | %s |\n\n", inv.ScanMode)

	if inv.ScanMode == "detect" {
		return m.reportDetectMode(w, inv)
	}
	return m.reportStandardMode(w, inv)
}

func (m *MarkdownReporter) reportStandardMode(w io.Writer, inv *scanner.Inventory) error {
	orphaned := inv.OrphanedGroups()
	active := inv.ActiveGroups()

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

	if len(orphaned) > 0 {
		printMarkdownSection(w, "ORPHANED", orphaned)
	}
	if len(active) > 0 {
		printMarkdownSection(w, "ACTIVE", active)
	}
	if len(other) > 0 {
		printMarkdownSection(w, "OTHER", other)
	}

	printMarkdownSummary(w, inv, orphaned, active)
	return nil
}

func (m *MarkdownReporter) reportDetectMode(w io.Writer, inv *scanner.Inventory) error {
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
		printMarkdownSection(w, "CONFIRMED ORPHANS", confirmed)
	}
	if len(probable) > 0 {
		printMarkdownSection(w, "PROBABLE ORPHANS", probable)
	}
	if len(devOwned) > 0 {
		printMarkdownSection(w, "DEVELOPER-OWNED", devOwned)
	}
	if len(active) > 0 {
		printMarkdownSection(w, "ACTIVE", active)
	}
	if len(unknown) > 0 {
		printMarkdownSection(w, "UNKNOWN ORIGIN", unknown)
	}

	orphaned := append(confirmed, probable...)
	printMarkdownSummary(w, inv, orphaned, active)
	return nil
}

func printMarkdownSection(w io.Writer, title string, groups []*scanner.InfraGroup) {
	fmt.Fprintf(w, "## %s (%d)\n\n", title, len(groups))
	fmt.Fprintf(w, "| Infra ID | Cluster Name | Age | Resources | Cost/mo |\n")
	fmt.Fprintf(w, "|----------|--------------|-----|-----------|--------|\n")

	for _, g := range groups {
		name := displayName(g)
		fmt.Fprintf(w, "| %s | %s | %s | %d | %s |\n",
			g.InfraID,
			name,
			formatAge(g.CreatedAt),
			len(g.Resources),
			formatCost(g.MonthlyCost),
		)
	}
	fmt.Fprintln(w)
}

func printMarkdownSummary(w io.Writer, inv *scanner.Inventory, orphaned, active []*scanner.InfraGroup) {
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

	fmt.Fprintf(w, "---\n\n")
	fmt.Fprintf(w, "**Summary:** %d clusters (%d orphaned, %d active), %d resources, ~$%.0f/mo total (~$%.0f/mo orphaned)\n\n",
		totalGroups, len(orphaned), len(active), totalResources, totalCost, orphanCost)

	if len(orphaned) > 0 {
		fmt.Fprintf(w, "```\nhcp-infra clean --all\nhcp-infra clean --infra-id <ID>\n```\n")
	}
}
