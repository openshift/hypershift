package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

func PrintTable(w io.Writer, sets []InfraSet) {
	sort.Slice(sets, func(i, j int) bool {
		order := map[Verdict]int{
			VerdictProtected: 0,
			VerdictActive:    1,
			VerdictTooYoung:  2,
			VerdictUncertain: 3,
			VerdictLeaked:    4,
		}
		if order[sets[i].Verdict] != order[sets[j].Verdict] {
			return order[sets[i].Verdict] < order[sets[j].Verdict]
		}
		return sets[i].InfraID < sets[j].InfraID
	})

	fmt.Fprintf(w, "%-24s %-12s %-4s %-42s %s\n", "VERDICT", "AGE", "VPCs", "INFRA-ID", "REASON")
	fmt.Fprintf(w, "%s\n", strings.Repeat("-", 120))

	for _, s := range sets {
		age := ""
		if s.Age > 0 {
			age = formatDuration(s.Age)
		}

		vpcCount := len(s.VPCs)
		fmt.Fprintf(w, "%-24s %-12s %-4d %-42s %s\n",
			s.Verdict, age, vpcCount, s.InfraID, s.VerdictReason)

		if s.Verdict == VerdictLeaked || s.Verdict == VerdictUncertain {
			if s.TestType != "" {
				prowInfo := fmt.Sprintf("test=%s", s.TestType)
				if s.Namespace != "" {
					prowInfo += fmt.Sprintf("  ns=%s", s.Namespace)
				}
				if s.ProwLink != "" {
					prowInfo += fmt.Sprintf("  prow=%s", s.ProwLink)
				}
				fmt.Fprintf(w, "  %-22s %s\n", "", prowInfo)
			}
			for _, vpc := range s.VPCs {
				subRes := formatSubResources(vpc)
				provenance := formatProvenance(vpc)
				fmt.Fprintf(w, "  %-22s vpc: %-25s name: %-30s %s%s\n", "", vpc.VPCID, vpc.Name, subRes, provenance)
			}
			for _, z := range s.HostedZones {
				zType := "public"
				if z.Private {
					zType = "private"
				}
				fmt.Fprintf(w, "  %-22s zone: %-25s %s (%s, %d records)\n", "", z.ZoneID, z.Name, zType, z.Records)
			}
			for _, oidc := range s.OIDCProviders {
				fmt.Fprintf(w, "  %-22s oidc: %s\n", "", oidc)
			}
			for _, role := range s.IAMRoles {
				fmt.Fprintf(w, "  %-22s role: %s\n", "", role)
			}
		}
	}
}

func PrintSummary(w io.Writer, sets []InfraSet) {
	counts := CountByVerdict(sets)
	totalVPCs := 0
	for _, s := range sets {
		totalVPCs += len(s.VPCs)
	}

	fmt.Fprintf(w, "\n")
	fmt.Fprintf(w, "==============================\n")
	fmt.Fprintf(w, "  Summary\n")
	fmt.Fprintf(w, "==============================\n")
	fmt.Fprintf(w, "  Protected:   %d (%d VPCs)\n", counts[VerdictProtected], countVPCs(sets, VerdictProtected))
	fmt.Fprintf(w, "  Active:      %d (%d VPCs)\n", counts[VerdictActive], countVPCs(sets, VerdictActive))
	fmt.Fprintf(w, "  Too young:   %d (%d VPCs)\n", counts[VerdictTooYoung], countVPCs(sets, VerdictTooYoung))
	fmt.Fprintf(w, "  Uncertain:   %d (%d VPCs)\n", counts[VerdictUncertain], countVPCs(sets, VerdictUncertain))
	fmt.Fprintf(w, "  Leaked:      %d (%d VPCs)\n", counts[VerdictLeaked], countVPCs(sets, VerdictLeaked))
	fmt.Fprintf(w, "  Total:       %d infra sets (%d VPCs)\n", len(sets), totalVPCs)
	fmt.Fprintf(w, "==============================\n")
}

func PrintJSON(w io.Writer, sets []InfraSet) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(sets)
}

func formatDuration(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	hours := int(d.Hours())
	if hours < 48 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dd%dh", hours/24, hours%24)
}

func formatSubResources(vpc VPCInfo) string {
	parts := []string{}
	if vpc.Endpoints > 0 {
		parts = append(parts, fmt.Sprintf("endpoints=%d", vpc.Endpoints))
	}
	if vpc.NATGateways > 0 {
		parts = append(parts, fmt.Sprintf("nats=%d", vpc.NATGateways))
	}
	if vpc.IGWs > 0 {
		parts = append(parts, fmt.Sprintf("igws=%d", vpc.IGWs))
	}
	if vpc.Subnets > 0 {
		parts = append(parts, fmt.Sprintf("subnets=%d", vpc.Subnets))
	}
	if vpc.SecurityGroups > 0 {
		parts = append(parts, fmt.Sprintf("sgs=%d", vpc.SecurityGroups))
	}
	if vpc.RouteTables > 0 {
		parts = append(parts, fmt.Sprintf("rtbs=%d", vpc.RouteTables))
	}
	if vpc.ENIs > 0 {
		parts = append(parts, fmt.Sprintf("enis=%d", vpc.ENIs))
	}
	if vpc.ELBs > 0 {
		parts = append(parts, fmt.Sprintf("elbs=%d", vpc.ELBs))
	}
	if vpc.EIPs > 0 {
		parts = append(parts, fmt.Sprintf("eips=%d", vpc.EIPs))
	}
	if vpc.EndpointServices > 0 {
		parts = append(parts, fmt.Sprintf("vpce-svcs=%d", vpc.EndpointServices))
	}
	if len(parts) == 0 {
		return "(empty VPC)"
	}
	return strings.Join(parts, " ")
}

func formatProvenance(vpc VPCInfo) string {
	if vpc.Source == "" && vpc.ProwJobID == "" {
		return ""
	}
	parts := []string{}
	if vpc.Source != "" {
		parts = append(parts, "source="+vpc.Source)
	}
	if vpc.ProwJobID != "" {
		parts = append(parts, "prow="+vpc.ProwJobID)
	}
	if vpc.ClusterName != "" {
		parts = append(parts, "cluster="+vpc.ClusterName)
	}
	return " [" + strings.Join(parts, " ") + "]"
}
