package main

import (
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// HyperShift resource tag keys (from support/awsutil/tags.go in PR #8909).
const (
	TagHypershiftInfraID     = "hypershift.openshift.io/infra-id"
	TagHypershiftClusterName = "hypershift.openshift.io/cluster-name"
	TagHypershiftSource      = "hypershift.openshift.io/source"
	TagHypershiftProwJobID   = "hypershift.openshift.io/prow-job-id"

	TagDoNotDelete = "hypershift.openshift.io/do-not-delete"
	TagCICluster   = "hypershift.openshift.io/ci-cluster"
)

func extractInfraID(tags []ec2types.Tag) string {
	if v := getTagValue(tags, TagHypershiftInfraID); v != "" {
		return v
	}
	for _, tag := range tags {
		key := aws.ToString(tag.Key)
		if strings.HasPrefix(key, "kubernetes.io/cluster/") {
			return strings.TrimPrefix(key, "kubernetes.io/cluster/")
		}
	}
	return ""
}

func getTagValue(tags []ec2types.Tag, key string) string {
	for _, tag := range tags {
		if aws.ToString(tag.Key) == key {
			return aws.ToString(tag.Value)
		}
	}
	return ""
}

func oldestVPC(vpcs []VPCInfo) time.Time {
	var oldest time.Time
	for _, vpc := range vpcs {
		if vpc.CreatedAt.IsZero() {
			continue
		}
		if oldest.IsZero() || vpc.CreatedAt.Before(oldest) {
			oldest = vpc.CreatedAt
		}
	}
	return oldest
}

func parseExpirationDate(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04-07:00",
		"2006-01-02",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// VPCCreateTime extracts the creation time from a VPC's tags.
func VPCCreateTime(vpc ec2types.Vpc) time.Time {
	for _, tag := range vpc.Tags {
		if aws.ToString(tag.Key) == "creation_date" {
			if t, err := time.Parse(time.RFC3339, aws.ToString(tag.Value)); err == nil {
				return t
			}
		}
	}
	return time.Time{}
}

// ZoneMatchesInfra determines if a Route53 zone belongs to a given infra set.
func ZoneMatchesInfra(zoneName string, infraID string) bool {
	name := strings.TrimSuffix(zoneName, ".")

	if strings.HasPrefix(name, infraID+".") {
		return true
	}
	if name == infraID+".hypershift.local" {
		return true
	}

	ciSuffix := ".ci.hypershift.devcluster.openshift.com"
	if strings.HasSuffix(name, ciSuffix) {
		prefix := strings.TrimSuffix(name, ciSuffix)
		parts := strings.SplitN(prefix, ".", 2)
		if len(parts) > 0 && parts[0] == infraID {
			return true
		}
	}

	return false
}

func IsProtectedVPC(vpcName string, protectedVPCs []string) bool {
	for _, p := range protectedVPCs {
		if vpcName == p {
			return true
		}
	}
	return false
}

func IsProtectedUser(value string, protectedUsers []string) (bool, string) {
	for _, user := range protectedUsers {
		if strings.Contains(value, user) {
			return true, user
		}
	}
	return false, ""
}

func FilterLeaked(sets []InfraSet) []InfraSet {
	var leaked []InfraSet
	for _, s := range sets {
		if s.Verdict == VerdictLeaked {
			leaked = append(leaked, s)
		}
	}
	return leaked
}

func CountByVerdict(sets []InfraSet) map[Verdict]int {
	counts := make(map[Verdict]int)
	for _, s := range sets {
		counts[s.Verdict]++
	}
	return counts
}

func countVPCs(sets []InfraSet, v Verdict) int {
	total := 0
	for _, s := range sets {
		if s.Verdict == v {
			total += len(s.VPCs)
		}
	}
	return total
}
