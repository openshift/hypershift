package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

type options struct {
	subscriptionID       string
	dnsZoneResourceGroup string
	dnsZoneName          string
	infraResourceGroup   string // Optional: if set, only clean records for clusters not in this RG
	dryRun               bool
	verbose              bool
}

func main() {
	opts := options{}

	flag.StringVar(&opts.subscriptionID, "subscription-id", "", "Azure subscription ID (required)")
	flag.StringVar(&opts.dnsZoneResourceGroup, "dns-zone-rg", "", "Resource group containing the DNS zone (required)")
	flag.StringVar(&opts.dnsZoneName, "dns-zone-name", "", "DNS zone name / base domain (required)")
	flag.StringVar(&opts.infraResourceGroup, "infra-rg", "", "Resource group containing cluster infrastructure (optional, for cross-referencing active clusters)")
	flag.BoolVar(&opts.dryRun, "dry-run", true, "If true, only print what would be deleted (default: true)")
	flag.BoolVar(&opts.verbose, "verbose", false, "Enable verbose logging")
	flag.Parse()

	if opts.subscriptionID == "" || opts.dnsZoneResourceGroup == "" || opts.dnsZoneName == "" {
		flag.Usage()
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		log.Println("Received interrupt signal, canceling...")
		cancel()
	}()

	if err := run(ctx, opts); err != nil {
		log.Fatalf("Error: %v", err)
	}
}

func run(ctx context.Context, opts options) error {
	// Create Azure credentials using DefaultAzureCredential
	// This supports environment variables, managed identity, Azure CLI, etc.
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return fmt.Errorf("failed to create Azure credentials: %w", err)
	}

	// Create DNS client
	dnsClient, err := armdns.NewRecordSetsClient(opts.subscriptionID, cred, nil)
	if err != nil {
		return fmt.Errorf("failed to create DNS record sets client: %w", err)
	}

	// Get list of active cluster infraIDs by scanning resource groups
	activeInfraIDs, err := getActiveInfraIDs(ctx, opts.subscriptionID, opts.infraResourceGroup, cred)
	if err != nil {
		return fmt.Errorf("failed to get active infra IDs: %w", err)
	}
	log.Printf("Found %d active cluster infrastructure IDs", len(activeInfraIDs))
	if opts.verbose {
		for _, id := range activeInfraIDs {
			log.Printf("  Active infraID: %s", id)
		}
	}

	// List all records in the DNS zone
	log.Printf("Listing DNS records in zone %s (resource group: %s)", opts.dnsZoneName, opts.dnsZoneResourceGroup)

	var recordsToDelete []recordInfo
	var totalRecords int

	pager := dnsClient.NewListAllByDNSZonePager(opts.dnsZoneResourceGroup, opts.dnsZoneName, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed to list DNS records: %w", err)
		}

		for _, record := range page.Value {
			if record.Name == nil || record.Type == nil {
				continue
			}
			totalRecords++

			recordType := extractRecordType(*record.Type)

			// Skip SOA and NS records - these are zone-level records
			if recordType == "SOA" || recordType == "NS" {
				continue
			}

			// Skip the zone apex (@) record
			if *record.Name == "@" {
				continue
			}

			// Determine if this record should be deleted
			shouldDelete, reason := shouldDeleteRecord(*record.Name, recordType, activeInfraIDs, opts)

			if shouldDelete {
				recordsToDelete = append(recordsToDelete, recordInfo{
					name:       *record.Name,
					recordType: recordType,
					reason:     reason,
				})
			}
		}
	}

	log.Printf("Total records in zone: %d", totalRecords)
	log.Printf("Records to delete: %d", len(recordsToDelete))

	if len(recordsToDelete) == 0 {
		log.Println("No orphaned records found")
		return nil
	}

	// Print or delete records
	var deleted, failed int
	for _, rec := range recordsToDelete {
		fqdn := fmt.Sprintf("%s.%s", rec.name, opts.dnsZoneName)

		if opts.dryRun {
			if opts.verbose {
				log.Printf("[DRY-RUN] Would delete %s record: %s (reason: %s)", rec.recordType, fqdn, rec.reason)
			}
		} else {
			log.Printf("Deleting %s record: %s (reason: %s)", rec.recordType, fqdn, rec.reason)
			_, err := dnsClient.Delete(ctx, opts.dnsZoneResourceGroup, opts.dnsZoneName,
				rec.name, armdns.RecordType(rec.recordType), nil)
			if err != nil {
				log.Printf("  ERROR: Failed to delete record: %v", err)
				failed++
			} else {
				deleted++
			}
		}
	}

	log.Println("")
	if opts.dryRun {
		log.Printf("DRY RUN: Would delete %d records", len(recordsToDelete))
		log.Println("To actually delete records, run with -dry-run=false")
	} else {
		log.Printf("Deleted %d records, %d failed", deleted, failed)
	}

	return nil
}

type recordInfo struct {
	name       string
	recordType string
	reason     string
}

// shouldDeleteRecord determines if a DNS record should be deleted
func shouldDeleteRecord(name, recordType string, activeInfraIDs []string, opts options) (bool, string) {
	nameLower := strings.ToLower(name)

	// Check if this looks like a HyperShift/ARO HCP record
	// Common patterns observed in ARO HCP CI:
	// - api-<cluster-name>-<infraID> (e.g., api-autoscaling-7589p)
	// - a-api-<cluster-name>-<infraID>-external-dns (TXT ownership record for above)
	// - apps-<cluster-name>-<infraID>
	// - oauth-<cluster-name>-<infraID>
	// - <something>-<infraID> where infraID is 5 alphanumeric chars

	isClusterRecord := false
	var matchReason string
	var matchedInfraID string

	// Pattern 1: ExternalDNS TXT ownership records
	// Format: a-<record-name>-external-dns or cname-<record-name>-external-dns
	if recordType == "TXT" && strings.HasSuffix(nameLower, "-external-dns") {
		if strings.HasPrefix(nameLower, "a-") ||
			strings.HasPrefix(nameLower, "cname-") ||
			strings.HasPrefix(nameLower, "aaaa-") {
			isClusterRecord = true
			matchReason = "external-dns TXT ownership record"
			matchedInfraID = extractInfraIDFromRecord(nameLower)
		}
	}

	// Pattern 2: Records with infraID embedded (5-char alphanumeric segment)
	// e.g., api-autoscaling-7589p, apps-mycluster-abc12
	if !isClusterRecord {
		if infraID := extractInfraIDFromRecord(nameLower); infraID != "" {
			isClusterRecord = true
			matchReason = fmt.Sprintf("contains infraID '%s'", infraID)
			matchedInfraID = infraID
		}
	}

	// Pattern 3: Records containing common HCP/cluster prefixes
	if !isClusterRecord {
		clusterPatterns := []string{
			"api.",
			"api-",
			"apps.",
			"apps-",
			"oauth-",
			"oauth.",
			"console-",
			"console.",
			"kube-apiserver",
		}

		for _, pattern := range clusterPatterns {
			if strings.Contains(nameLower, pattern) {
				isClusterRecord = true
				matchReason = fmt.Sprintf("matches cluster pattern '%s'", pattern)
				break
			}
		}
	}

	if !isClusterRecord {
		return false, ""
	}

	// If we have active infraIDs, check if the record belongs to an active cluster
	if len(activeInfraIDs) > 0 {
		// First check if we extracted a specific infraID from this record
		if matchedInfraID != "" {
			for _, activeID := range activeInfraIDs {
				if strings.EqualFold(matchedInfraID, activeID) {
					// Record belongs to an active cluster, don't delete
					return false, ""
				}
			}
			return true, matchReason + fmt.Sprintf(" (infraID '%s' not in active clusters)", matchedInfraID)
		}

		// Fall back to substring matching
		for _, infraID := range activeInfraIDs {
			if strings.Contains(nameLower, strings.ToLower(infraID)) {
				// Record belongs to an active cluster, don't delete
				return false, ""
			}
		}
		return true, matchReason + " (not in active clusters)"
	}

	// No active cluster list provided - be more conservative
	// Only delete records that look definitively orphaned
	if opts.verbose {
		log.Printf("  Record %s matches pattern but no active cluster list to verify against", name)
	}

	return true, matchReason + " (no active cluster verification)"
}

// extractInfraIDFromRecord extracts the infraID from a DNS record name.
// InfraIDs are the last segment before any "-external-dns" suffix.
// Examples:
//   - "a-api-autoscaling-7589p-external-dns" -> "7589p"
//   - "oauth-node-pool-xht64" -> "xht64"
//   - "api-create-cluster-abc12" -> "abc12"
func extractInfraIDFromRecord(name string) string {
	// Remove the external-dns ownership suffix if present
	name = strings.TrimSuffix(name, "-external-dns")

	// Find the last hyphen and extract everything after it
	lastIdx := strings.LastIndex(name, "-")
	if lastIdx == -1 || lastIdx == len(name)-1 {
		return ""
	}

	candidate := name[lastIdx+1:]

	// Validate it looks like an infraID (5 alphanumeric chars)
	if len(candidate) != 5 {
		return ""
	}

	for _, c := range candidate {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')) {
			return ""
		}
	}

	return candidate
}

// getActiveInfraIDs retrieves infraIDs of active clusters by looking at resource groups
func getActiveInfraIDs(ctx context.Context, subscriptionID, resourceGroupPrefix string, cred *azidentity.DefaultAzureCredential) ([]string, error) {
	rgClient, err := armresources.NewResourceGroupsClient(subscriptionID, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource groups client: %w", err)
	}

	infraIDSet := make(map[string]bool)

	pager := rgClient.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list resource groups: %w", err)
		}

		for _, rg := range page.Value {
			if rg.Name == nil {
				continue
			}

			name := *rg.Name

			// If a prefix is specified, only consider RGs with that prefix
			if resourceGroupPrefix != "" && !strings.HasPrefix(name, resourceGroupPrefix) {
				continue
			}

			// Extract infraID from resource group name using same logic as DNS records
			infraID := extractInfraIDFromRecord(name)
			if infraID != "" {
				infraIDSet[infraID] = true
			}
		}
	}

	// Convert map to slice
	var infraIDs []string
	for id := range infraIDSet {
		infraIDs = append(infraIDs, id)
	}

	return infraIDs, nil
}

// extractRecordType extracts the record type from the full Azure resource type
// e.g., "Microsoft.Network/dnszones/A" -> "A"
func extractRecordType(fullType string) string {
	parts := strings.Split(fullType, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return fullType
}
