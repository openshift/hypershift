package scanner

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	tagtypes "github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi/types"

	awspkg "github.com/openshift/hypershift/contrib/hcp-infra/pkg/aws"
)

// ScanOptions configures the tag-based scan.
type ScanOptions struct {
	InfraID         string
	Owner           string
	OlderThan       time.Duration
	ExcludeInfraIDs []string
	OIDCBuckets     []string
}

// ScanTags discovers HyperShift infrastructure resources via the Resource Groups
// Tagging API. It queries for ALL resources tagged with the well-known
// hypershift.openshift.io/infra-id key in a single paginated call per region,
// then groups them by infra ID.
func ScanTags(ctx context.Context, clients *awspkg.Clients, regions []string, opts ScanOptions) (*Inventory, error) {
	inv := NewInventory(clients.AccountID, regions, "tags")

	// Build the set of excluded infra IDs for fast lookup.
	excludeSet := make(map[string]bool, len(opts.ExcludeInfraIDs))
	for _, id := range opts.ExcludeInfraIDs {
		excludeSet[id] = true
	}

	cutoff := time.Time{}
	if opts.OlderThan > 0 {
		cutoff = time.Now().Add(-opts.OlderThan)
	}

	// Scan each region: one paginated GetResources call per region finds ALL
	// tagged resources regardless of infra ID. This is O(regions) API calls
	// instead of O(regions * infraIDs).
	for _, region := range regions {
		if err := scanAllTaggedResources(ctx, clients, region, inv, opts, excludeSet, cutoff); err != nil {
			log.Printf("WARN: scanning region %s: %v", region, err)
		}
	}

	return inv, nil
}

// scanAllTaggedResources queries a single region for ALL resources tagged with
// the hypershift.openshift.io/infra-id key. If opts.InfraID is set, it filters
// to that specific value. Otherwise it returns everything in one paginated walk.
func scanAllTaggedResources(ctx context.Context, clients *awspkg.Clients, region string, inv *Inventory, opts ScanOptions, excludeSet map[string]bool, cutoff time.Time) error {
	tagging := clients.Tagging(region)

	input := &resourcegroupstaggingapi.GetResourcesInput{
		TagFilters: []tagtypes.TagFilter{
			{
				Key: aws.String(TagInfraID),
			},
		},
	}

	// If a specific infra ID is requested, add it as a value filter.
	if opts.InfraID != "" {
		input.TagFilters[0].Values = []string{opts.InfraID}
	}

	paginator := resourcegroupstaggingapi.NewGetResourcesPaginator(tagging, input)
	pageCount := 0

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("GetResources in %s: %w", region, err)
		}
		pageCount++

		for _, mapping := range page.ResourceTagMappingList {
			arn := aws.ToString(mapping.ResourceARN)
			tags := extractTags(mapping.Tags)

			infraID := tags[TagInfraID]
			if infraID == "" {
				continue
			}

			// Apply exclusion filter.
			if excludeSet[infraID] {
				continue
			}

			// Apply owner filter: match cluster-name prefix.
			if opts.Owner != "" {
				clusterName := tags[TagClusterName]
				if !strings.HasPrefix(clusterName, opts.Owner) {
					continue
				}
			}

			resType, resourceID := parseARN(arn)
			if resType == "" {
				log.Printf("WARN: unrecognized ARN format: %s", arn)
				continue
			}

			r := Resource{
				Type:        resType,
				ID:          resourceID,
				ARN:         arn,
				Region:      region,
				InfraID:     infraID,
				ClusterName: tags[TagClusterName],
				Tags:        tags,
				MonthlyCost: estimateCost(resType),
			}

			// Set name from tags.
			if name, ok := tags["Name"]; ok {
				r.Name = name
			}

			// Apply older-than filter.
			if !cutoff.IsZero() && !r.CreatedAt.IsZero() && r.CreatedAt.After(cutoff) {
				continue
			}

			inv.AddResource(r)
		}
	}

	return nil
}

// extractTags converts the Tagging API tag list to a map.
func extractTags(tags []tagtypes.Tag) map[string]string {
	m := make(map[string]string, len(tags))
	for _, t := range tags {
		m[aws.ToString(t.Key)] = aws.ToString(t.Value)
	}
	return m
}

// arnServiceResourceMap maps "service:resource-type" from ARN components to our
// ResourceType constants.
var arnServiceResourceMap = map[string]ResourceType{
	"ec2:vpc":                  ResVPC,
	"ec2:subnet":               ResSubnet,
	"ec2:natgateway":           ResNATGateway,
	"ec2:elastic-ip":           ResElasticIP,
	"ec2:internet-gateway":     ResInternetGateway,
	"ec2:route-table":          ResRouteTable,
	"ec2:security-group":       ResSecurityGroup,
	"ec2:vpc-endpoint":         ResVPCEndpoint,
	"ec2:dhcp-options":         ResDHCPOptions,
	"ec2:instance":             ResEC2Instance,
	"ec2:volume":               ResEBSVolume,
	"ec2:key-pair":             ResEC2KeyPair,
	"ec2:capacity-reservation": ResCapacityReservation,
}

// parseARN extracts the ResourceType and resource ID from an ARN.
func parseARN(arn string) (ResourceType, string) {
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) < 6 {
		return "", ""
	}

	service := parts[2]
	resource := parts[5]

	switch service {
	case "ec2":
		return parseEC2ARN(resource)
	case "iam":
		return parseIAMARN(resource)
	case "route53":
		return parseRoute53ARN(resource)
	case "elasticloadbalancing":
		return parseELBARN(resource)
	case "s3":
		return ResS3Prefix, resource
	default:
		return "", ""
	}
}

func parseEC2ARN(resource string) (ResourceType, string) {
	slashIdx := strings.Index(resource, "/")
	if slashIdx < 0 {
		return "", ""
	}
	resType := resource[:slashIdx]
	resID := resource[slashIdx+1:]
	key := "ec2:" + resType
	if rt, ok := arnServiceResourceMap[key]; ok {
		return rt, resID
	}
	return "", ""
}

func parseIAMARN(resource string) (ResourceType, string) {
	switch {
	case strings.HasPrefix(resource, "role/"):
		name := resource[len("role/"):]
		if idx := strings.LastIndex(name, "/"); idx >= 0 {
			name = name[idx+1:]
		}
		return ResIAMRole, name
	case strings.HasPrefix(resource, "instance-profile/"):
		name := resource[len("instance-profile/"):]
		if idx := strings.LastIndex(name, "/"); idx >= 0 {
			name = name[idx+1:]
		}
		return ResInstanceProfile, name
	case strings.HasPrefix(resource, "oidc-provider/"):
		return ResOIDCProvider, resource[len("oidc-provider/"):]
	default:
		return "", ""
	}
}

func parseRoute53ARN(resource string) (ResourceType, string) {
	if strings.HasPrefix(resource, "hostedzone/") {
		return ResRoute53Zone, resource[len("hostedzone/"):]
	}
	return "", ""
}

func parseELBARN(resource string) (ResourceType, string) {
	if !strings.HasPrefix(resource, "loadbalancer/") {
		return "", ""
	}
	remainder := resource[len("loadbalancer/"):]
	if strings.HasPrefix(remainder, "app/") || strings.HasPrefix(remainder, "net/") {
		return ResELBv2, remainder
	}
	return ResELB, remainder
}

func estimateCost(rt ResourceType) float64 {
	switch rt {
	case ResNATGateway:
		return CostNATGateway
	case ResElasticIP:
		return CostElasticIP
	default:
		return 0
	}
}
