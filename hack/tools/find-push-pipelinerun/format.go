package main

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
)

const logURLAnnotation = "pipelinesascode.tekton.dev/log-url"

var (
	pipelineRunTerminalStatuses = map[string]bool{
		"Completed": true, "Failed": true, "Succeeded": true, "Error": true,
	}
	releaseTerminalStatuses = map[string]bool{
		"Succeeded": true, "Failed": true, "Error": true, "Rejected": true,
	}
)

// HasPending returns true if any PipelineRun has a non-terminal status.
func HasPending(prs []PipelineRun) bool {
	for _, pr := range prs {
		if !pipelineRunTerminalStatuses[pipelineRunStatus(pr)] {
			return true
		}
	}
	return false
}

// HasPendingReleases returns true if any Release has a non-terminal status.
func HasPendingReleases(releases []Release) bool {
	for _, rel := range releases {
		if !releaseTerminalStatuses[releaseStatus(rel)] {
			return true
		}
	}
	return false
}

// FilterByComponent filters PipelineRuns whose name starts with the component prefix.
func FilterByComponent(prs []PipelineRun, component string) []PipelineRun {
	var filtered []PipelineRun
	for _, pr := range prs {
		if strings.HasPrefix(pr.Metadata.Name, component) {
			filtered = append(filtered, pr)
		}
	}
	return filtered
}

// FilterReleasesByComponent filters Releases by the appstudio.openshift.io/component label prefix.
func FilterReleasesByComponent(releases []Release, component string) []Release {
	var filtered []Release
	for _, rel := range releases {
		comp := rel.Metadata.Labels["appstudio.openshift.io/component"]
		if strings.HasPrefix(comp, component) {
			filtered = append(filtered, rel)
		}
	}
	return filtered
}

// FormatPipelineRuns writes a table of PipelineRuns to w.
// Returns false if there are no PipelineRuns to format.
func FormatPipelineRuns(w io.Writer, prs []PipelineRun) bool {
	if len(prs) == 0 {
		return false
	}

	sort.Slice(prs, func(i, j int) bool {
		return prs[i].Metadata.CreationTimestamp < prs[j].Metadata.CreationTimestamp
	})

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSTATUS\tCREATED\tURL\tIMAGE")
	for _, pr := range prs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			pr.Metadata.Name,
			pipelineRunStatus(pr),
			pr.Metadata.CreationTimestamp,
			pr.Metadata.Annotations[logURLAnnotation],
			pipelineRunImage(pr),
		)
	}
	tw.Flush()
	return true
}

// FormatReleases writes a table of Releases to w.
// Returns false if there are no Releases to format.
func FormatReleases(w io.Writer, releases []Release) bool {
	if len(releases) == 0 {
		return false
	}

	sort.Slice(releases, func(i, j int) bool {
		return releases[i].Metadata.CreationTimestamp < releases[j].Metadata.CreationTimestamp
	})

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tPLAN\tCOMPONENT\tSTATUS\tCREATED")
	for _, rel := range releases {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			rel.Metadata.Name,
			rel.Spec.ReleasePlan,
			rel.Metadata.Labels["appstudio.openshift.io/component"],
			releaseStatus(rel),
			rel.Metadata.CreationTimestamp,
		)
	}
	tw.Flush()
	return true
}

// FormatReleasesWithImages writes a table of Releases enriched with destination images.
func FormatReleasesWithImages(w io.Writer, releases []Release, images map[string]string) bool {
	if len(releases) == 0 {
		return false
	}

	sort.Slice(releases, func(i, j int) bool {
		return releases[i].Metadata.CreationTimestamp < releases[j].Metadata.CreationTimestamp
	})

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tPLAN\tCOMPONENT\tSTATUS\tCREATED\tDEST-IMAGE")
	for _, rel := range releases {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			rel.Metadata.Name,
			rel.Spec.ReleasePlan,
			rel.Metadata.Labels["appstudio.openshift.io/component"],
			releaseStatus(rel),
			rel.Metadata.CreationTimestamp,
			images[rel.Metadata.Name],
		)
	}
	tw.Flush()
	return true
}

// FormatReleasePipelineRuns writes a table of release PipelineRuns to w.
func FormatReleasePipelineRuns(w io.Writer, prs []PipelineRun) bool {
	if len(prs) == 0 {
		return false
	}

	sort.Slice(prs, func(i, j int) bool {
		return prs[i].Metadata.CreationTimestamp < prs[j].Metadata.CreationTimestamp
	})

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tRELEASE\tSTATUS\tCREATED")
	for _, pr := range prs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
			pr.Metadata.Name,
			pr.Metadata.Labels["release.appstudio.openshift.io/name"],
			pipelineRunStatus(pr),
			pr.Metadata.CreationTimestamp,
		)
	}
	tw.Flush()
	return true
}

// ResolveDestImages resolves destination images for a list of releases using
// the release plan mapping and snapshot data.
func ResolveDestImages(q Querier, releases []Release) map[string]string {
	images := make(map[string]string)
	for _, rel := range releases {
		img := resolveDestImage(q, rel)
		if img != "" {
			images[rel.Metadata.Name] = img
		}
	}
	return images
}

func resolveDestImage(q Querier, rel Release) string {
	mapping := getMappingForRelease(q, rel.Spec.ReleasePlan)
	if mapping == nil {
		return ""
	}

	compName := rel.Metadata.Labels["appstudio.openshift.io/component"]
	if compName == "" || rel.Spec.Snapshot == "" {
		return ""
	}

	snap, err := q.GetSnapshot(rel.Spec.Snapshot)
	if err != nil || snap == nil {
		return ""
	}

	var sourceDigest string
	for _, sc := range snap.Spec.Components {
		if sc.Name == compName {
			if idx := strings.LastIndex(sc.ContainerImage, "@"); idx >= 0 {
				sourceDigest = sc.ContainerImage[idx+1:]
			}
			break
		}
	}
	if sourceDigest == "" {
		return ""
	}

	for _, mc := range mapping.Components {
		if mc.Name == compName && len(mc.Repositories) > 0 {
			return mc.Repositories[0].URL + "@" + sourceDigest
		}
	}
	return ""
}

func getMappingForRelease(q Querier, planName string) *Mapping {
	plan, err := q.GetReleasePlan(planName)
	if err != nil || plan == nil {
		return nil
	}

	if plan.Spec.Data.Mapping != nil {
		return plan.Spec.Data.Mapping
	}

	rpaRef := plan.Status.ReleasePlanAdmission.Name
	if rpaRef == "" {
		return nil
	}

	parts := strings.SplitN(rpaRef, "/", 2)
	if len(parts) != 2 {
		return nil
	}

	rpa, err := q.GetReleasePlanAdmission(parts[0], parts[1])
	if err != nil || rpa == nil {
		return nil
	}

	return rpa.Spec.Data.Mapping
}
