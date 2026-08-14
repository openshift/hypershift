package main

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/route53"
)

const (
	prowDeckURL   = "https://prow.ci.openshift.org"
	serviceCIZone = "service.ci.hypershift.devcluster.openshift.com"
)

var (
	knownTestPrefixes = []string{
		"control-plane-upgrade",
		"karpenter-upgrade-control-plane",
		"request-serving-isolation",
		"scale-from-zero",
		"create-cluster",
		"node-pool",
		"autoscaling",
		"karpenter",
		"kms-verify",
		"ha-break-glass-creds",
		"custom-config",
		"ho-upgrade",
		"multi-hop-upgrade",
		"spot-demo",
		"private",
		"proxy",
	}

	namespaceFromRouteRe = regexp.MustCompile(`resource=route/([^/]+)/`)
)

func init() {
	sort.Slice(knownTestPrefixes, func(i, j int) bool {
		return len(knownTestPrefixes[i]) > len(knownTestPrefixes[j])
	})
}

// InferTestType extracts the e2e test type from a cluster name (VPC Name without -vpc suffix).
// Returns the test type and the cluster-specific suffix.
// Examples:
//
//	"create-cluster-hg78d" → ("create-cluster", "hg78d")
//	"node-pool-78vcg"      → ("node-pool", "78vcg")
//	"00ab3695c5f73d4354b9" → ("", "")  (hex infraID, no test type)
func InferTestType(infraID string) (testType, suffix string) {
	for _, prefix := range knownTestPrefixes {
		if strings.HasPrefix(infraID, prefix+"-") {
			suffix = strings.TrimPrefix(infraID, prefix+"-")
			return prefix, suffix
		}
	}

	if isHexInfraID(infraID) {
		return "e2e-generic", ""
	}

	return "", ""
}

var hexPattern = regexp.MustCompile(`^[0-9a-f]{16,}$`)

func isHexInfraID(s string) bool {
	return hexPattern.MatchString(s)
}

// ProwJobURL returns a direct Prow link when we have the job name and build ID,
// or a search URL as fallback.
func ProwJobURL(testType, prowJobID string) string {
	if prowJobID != "" {
		return fmt.Sprintf("%s/view/gs/test-platform-results/logs/%s", prowDeckURL, prowJobID)
	}

	if testType == "" || testType == "e2e-generic" {
		return ""
	}
	query := fmt.Sprintf("hypershift e2e %s", testType)
	return fmt.Sprintf("%s/?query=%s&type=periodic", prowDeckURL, url.QueryEscape(query))
}

// LookupNamespaceFromServiceZone searches the service.ci zone TXT records for a
// given cluster name and returns the HC namespace (e2e-clusters-XXXXX-<cluster>).
func LookupNamespaceFromServiceZone(ctx context.Context, r53 Route53API, serviceCIZoneID, clusterName string) string {
	if serviceCIZoneID == "" || clusterName == "" {
		return ""
	}

	target := clusterName + "/"
	input := &route53.ListResourceRecordSetsInput{
		HostedZoneId: aws.String(serviceCIZoneID),
	}

	for {
		out, err := r53.ListResourceRecordSets(ctx, input)
		if err != nil {
			return ""
		}

		for _, rr := range out.ResourceRecordSets {
			if rr.Type != "TXT" {
				continue
			}
			for _, r := range rr.ResourceRecords {
				val := aws.ToString(r.Value)
				if strings.Contains(val, target) {
					if ns := extractNamespace(val); ns != "" {
						return ns
					}
				}
			}
		}

		if !out.IsTruncated {
			break
		}
		input.StartRecordName = out.NextRecordName
		input.StartRecordType = out.NextRecordType
		input.StartRecordIdentifier = out.NextRecordIdentifier
	}

	return ""
}

// extractNamespace extracts the namespace from an external-dns TXT record value.
// Input:  "heritage=external-dns,...,external-dns/resource=route/e2e-clusters-7c84g-node-pool-78vcg/kube-apiserver"
// Output: "e2e-clusters-7c84g-node-pool-78vcg"
func extractNamespace(txtValue string) string {
	m := namespaceFromRouteRe.FindStringSubmatch(txtValue)
	if m != nil {
		return m[1]
	}
	return ""
}

// FindServiceCIZoneID finds the zone ID for service.ci.hypershift.devcluster.openshift.com
func FindServiceCIZoneID(ctx context.Context, r53 Route53API) string {
	var nextMarker *string
	for {
		out, err := r53.ListHostedZones(ctx, &route53.ListHostedZonesInput{
			Marker: nextMarker,
		})
		if err != nil {
			return ""
		}

		for _, z := range out.HostedZones {
			name := strings.TrimSuffix(aws.ToString(z.Name), ".")
			if name == serviceCIZone {
				return strings.TrimPrefix(aws.ToString(z.Id), "/hostedzone/")
			}
		}

		if !out.IsTruncated {
			break
		}
		nextMarker = out.NextMarker
	}
	return ""
}

// CorrelateWithProw enriches leaked InfraSets with test type, namespace, and Prow link.
func CorrelateWithProw(ctx context.Context, r53 Route53API, sets []InfraSet) {
	serviceCIZoneID := FindServiceCIZoneID(ctx, r53)

	for i, infra := range sets {
		if infra.Verdict != VerdictLeaked && infra.Verdict != VerdictUncertain {
			continue
		}

		testType, _ := InferTestType(infra.InfraID)
		sets[i].TestType = testType

		// Use the prow-job-id tag from any VPC if available (PR #8909)
		prowJobID := ""
		for _, vpc := range infra.VPCs {
			if vpc.ProwJobID != "" {
				prowJobID = vpc.ProwJobID
				break
			}
		}
		sets[i].ProwLink = ProwJobURL(testType, prowJobID)

		if serviceCIZoneID != "" && testType != "" && testType != "e2e-generic" {
			ns := LookupNamespaceFromServiceZone(ctx, r53, serviceCIZoneID, infra.InfraID)
			sets[i].Namespace = ns
		}
	}
}
