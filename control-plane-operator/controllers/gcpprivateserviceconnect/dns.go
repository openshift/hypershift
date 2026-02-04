// Package gcpprivateserviceconnect provides DNS zone management for GCP HCP clusters.
//
// This package is designed for use in Kubernetes reconciliation loops and follows
// idempotent patterns. All operations can be called repeatedly with the same result.
//
// Main entry points:
//   - ReconcileDNS: Reconciles DNS zones and records (called from PSC controller)
//   - DeleteDNS: Deletes DNS zones (called from PSC controller finalizer)
//
// Authentication:
// All functions use GOOGLE_APPLICATION_CREDENTIALS environment variable to
// authenticate with GCP. This should point to a service account JSON file
// mounted from the gcp-customer-credentials Kubernetes secret.
package gcpprivateserviceconnect

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"golang.org/x/oauth2/google"
	"google.golang.org/api/dns/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

const (
	// dnsAPITimeout is the timeout for individual GCP DNS API calls to prevent hung reconcilers.
	// This matches the timeout used in the PSC endpoint controller.
	dnsAPITimeout = 30 * time.Second
)

// newDNSClient initializes a Cloud DNS client using GOOGLE_APPLICATION_CREDENTIALS.
// The environment variable should point to a service account JSON file
// (typically /etc/gcp/service-account.json mounted from gcp-customer-credentials secret).
// This uses the same authentication pattern as InitCustomerGCPClient in psc_endpoint_controller.go.
func newDNSClient(ctx context.Context) (*dns.Service, error) {
	credFile := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
	if credFile == "" {
		return nil, fmt.Errorf("GOOGLE_APPLICATION_CREDENTIALS environment variable not set")
	}

	// Verify credentials file exists and is readable
	if _, err := os.Stat(credFile); err != nil {
		return nil, fmt.Errorf("credentials file not accessible at %s: %w", credFile, err)
	}

	// Create Google Cloud client using the WIF credentials file from environment
	// google.DefaultClient() automatically reads GOOGLE_APPLICATION_CREDENTIALS
	httpClient, err := google.DefaultClient(ctx, dns.CloudPlatformScope)
	if err != nil {
		return nil, fmt.Errorf("failed to create Google Cloud client using %s: %w", credFile, err)
	}

	return dns.NewService(ctx, option.WithHTTPClient(httpClient))
}

// ensureDNSDot ensures a DNS name ends with a dot (required by Cloud DNS).
func ensureDNSDot(name string) string {
	if !strings.HasSuffix(name, ".") {
		return name + "."
	}
	return name
}

// isNotFound checks if a GCP API error indicates a resource was not found.
func isNotFound(err error) bool {
	if apiErr, ok := err.(*googleapi.Error); ok {
		return apiErr.Code == 404 // HTTP 404 Not Found
	}
	// Fallback: check if error message contains "404" or "not found" patterns
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "error 404") || strings.Contains(errStr, "notfound") || strings.Contains(errStr, "not found")
}

// zoneNames contains the generated DNS zone names for a cluster.
type zoneNames struct {
	hypershiftLocalZoneName string
	publicIngressZoneName   string
	privateIngressZoneName  string
	ingressDNSName          string
}

// createZone is the unified zone creation function supporting both private and public zones.
// Uses "get first, create if not exists" pattern for idempotency.
//
// Parameters:
//   - svc: DNS service client
//   - projectID: GCP project ID where the zone will be created
//   - zoneName: DNS zone resource name (must match GCP naming: lowercase, hyphens, max 63 chars)
//   - dnsName: DNS domain name for the zone (will be normalized to end with a dot)
//   - visibility: "private" or "public"
//   - vpcNetworkURL: Full GCP VPC network URL (required for private zones, ignored for public)
func createZone(ctx context.Context, svc *dns.Service, projectID, zoneName, dnsName, visibility, vpcNetworkURL string) (*dns.ManagedZone, error) {
	dnsName = ensureDNSDot(dnsName)

	// Check if zone already exists
	existing, err := getZone(ctx, svc, projectID, zoneName)
	if err != nil {
		if !isNotFound(err) {
			return nil, err
		}
		// Zone doesn't exist, proceed to create it
	} else {
		return existing, nil // Zone already exists
	}

	// Build zone configuration
	zone := &dns.ManagedZone{
		Name:        zoneName,
		DnsName:     dnsName,
		Description: fmt.Sprintf("%s DNS zone for %s", visibility, dnsName),
		Visibility:  visibility,
	}

	// Add private visibility config if needed
	if visibility == "private" && vpcNetworkURL != "" {
		zone.PrivateVisibilityConfig = &dns.ManagedZonePrivateVisibilityConfig{
			Networks: []*dns.ManagedZonePrivateVisibilityConfigNetwork{
				{NetworkUrl: vpcNetworkURL},
			},
		}
	}

	apiCtx, cancel := context.WithTimeout(ctx, dnsAPITimeout)
	defer cancel()
	created, err := svc.ManagedZones.Create(projectID, zone).Context(apiCtx).Do()
	if err != nil {
		return nil, fmt.Errorf("failed to create %s DNS zone %s in project %s: %w", visibility, zoneName, projectID, err)
	}

	return created, nil
}

// getZone retrieves a Cloud DNS zone by name.
func getZone(ctx context.Context, svc *dns.Service, projectID, zoneName string) (*dns.ManagedZone, error) {
	apiCtx, cancel := context.WithTimeout(ctx, dnsAPITimeout)
	defer cancel()
	zone, err := svc.ManagedZones.Get(projectID, zoneName).Context(apiCtx).Do()
	if err != nil {
		return nil, fmt.Errorf("failed to get DNS zone %s in project %s: %w", zoneName, projectID, err)
	}
	return zone, nil
}

// deleteAllRecordsFromZone deletes all custom DNS records from a zone.
// SOA and NS records are managed by Google and cannot be deleted.
func deleteAllRecordsFromZone(ctx context.Context, svc *dns.Service, projectID, zoneName string) error {
	// List all records in the zone
	listCtx, listCancel := context.WithTimeout(ctx, dnsAPITimeout)
	defer listCancel()
	rrsets, err := svc.ResourceRecordSets.List(projectID, zoneName).Context(listCtx).Do()
	if err != nil {
		return fmt.Errorf("failed to list records in zone %s: %w", zoneName, err)
	}

	var recordsToDelete []*dns.ResourceRecordSet
	for _, record := range rrsets.Rrsets {
		// Skip SOA and NS records - these are managed by Google and cannot be deleted
		if record.Type == "SOA" || record.Type == "NS" {
			continue
		}
		recordsToDelete = append(recordsToDelete, record)
	}

	// If there are no custom records to delete, we're done
	if len(recordsToDelete) == 0 {
		return nil
	}

	// Delete all custom records in a single change
	change := &dns.Change{
		Deletions: recordsToDelete,
	}

	deleteCtx, deleteCancel := context.WithTimeout(ctx, dnsAPITimeout)
	defer deleteCancel()
	_, err = svc.Changes.Create(projectID, zoneName, change).Context(deleteCtx).Do()
	if err != nil {
		return fmt.Errorf("failed to delete custom records from zone %s: %w", zoneName, err)
	}

	return nil
}

// deleteZone deletes a Cloud DNS zone.
// It first removes all custom records, then deletes the zone.
func deleteZone(ctx context.Context, svc *dns.Service, projectID, zoneName string) error {
	// First, try to delete all custom records from the zone
	if err := deleteAllRecordsFromZone(ctx, svc, projectID, zoneName); err != nil {
		// If zone doesn't exist, that's fine - it's already deleted
		if isNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to clean up records before deleting zone %s: %w", zoneName, err)
	}

	// Now delete the zone itself
	apiCtx, cancel := context.WithTimeout(ctx, dnsAPITimeout)
	defer cancel()
	err := svc.ManagedZones.Delete(projectID, zoneName).Context(apiCtx).Do()
	if err != nil {
		if isNotFound(err) {
			return nil // Idempotent: already deleted
		}
		return fmt.Errorf("failed to delete DNS zone %s in project %s: %w", zoneName, projectID, err)
	}
	return nil
}

// findRecord finds a DNS record by name and type in a zone.
// Returns nil if the record doesn't exist.
func findRecord(ctx context.Context, svc *dns.Service, projectID, zoneName, recordName, recordType string) (*dns.ResourceRecordSet, error) {
	recordName = ensureDNSDot(recordName)

	apiCtx, cancel := context.WithTimeout(ctx, dnsAPITimeout)
	defer cancel()
	rrsets, err := svc.ResourceRecordSets.List(projectID, zoneName).
		Name(recordName).
		Type(recordType).
		Context(apiCtx).Do()
	if err != nil {
		return nil, fmt.Errorf("failed to list %s records for %s: %w", recordType, recordName, err)
	}

	if len(rrsets.Rrsets) == 0 {
		return nil, nil // Record not found
	}

	return rrsets.Rrsets[0], nil
}

// upsertRecord creates or updates a DNS record using atomic delete+add.
// This mimics AWS Route53's UPSERT behavior using GCP's Change API.
// If oldRecord is nil, only adds the new record (create).
// If oldRecord is not nil, atomically deletes old and adds new (update).
func upsertRecord(ctx context.Context, svc *dns.Service, projectID, zoneName string, oldRecord, newRecord *dns.ResourceRecordSet) error {
	change := &dns.Change{
		Additions: []*dns.ResourceRecordSet{newRecord},
	}

	if oldRecord != nil {
		change.Deletions = []*dns.ResourceRecordSet{oldRecord}
	}

	apiCtx, cancel := context.WithTimeout(ctx, dnsAPITimeout)
	defer cancel()
	_, err := svc.Changes.Create(projectID, zoneName, change).Context(apiCtx).Do()
	if err != nil {
		action := "create"
		if oldRecord != nil {
			action = "update"
		}
		return fmt.Errorf("failed to %s %s record %s: %w", action, newRecord.Type, newRecord.Name, err)
	}

	return nil
}

// createCNAMERecord is the internal function that ensures a CNAME record exists with the correct value.
func createCNAMERecord(ctx context.Context, svc *dns.Service, projectID, zoneName, recordName, target string, ttl int64) error {
	recordName = ensureDNSDot(recordName)
	target = ensureDNSDot(target)

	existing, err := findRecord(ctx, svc, projectID, zoneName, recordName, "CNAME")
	if err != nil {
		return err
	}

	// If record exists with correct value, nothing to do
	if existing != nil && existing.Ttl == ttl && len(existing.Rrdatas) == 1 && existing.Rrdatas[0] == target {
		return nil
	}

	return upsertRecord(ctx, svc, projectID, zoneName, existing, &dns.ResourceRecordSet{
		Name:    recordName,
		Type:    "CNAME",
		Ttl:     ttl,
		Rrdatas: []string{target},
	})
}

// createARecord ensures an A record exists with the correct value.
func createARecord(ctx context.Context, svc *dns.Service, projectID, zoneName, recordName, ipAddress string, ttl int64) error {
	recordName = ensureDNSDot(recordName)

	existing, err := findRecord(ctx, svc, projectID, zoneName, recordName, "A")
	if err != nil {
		return err
	}

	// If record exists with correct value, nothing to do
	if existing != nil && existing.Ttl == ttl && len(existing.Rrdatas) == 1 && existing.Rrdatas[0] == ipAddress {
		return nil
	}

	return upsertRecord(ctx, svc, projectID, zoneName, existing, &dns.ResourceRecordSet{
		Name:    recordName,
		Type:    "A",
		Ttl:     ttl,
		Rrdatas: []string{ipAddress},
	})
}

// gcpZoneNameRegexp validates GCP Cloud DNS managed zone names.
// Must start with a lowercase letter and contain only lowercase letters, numbers, and hyphens.
var gcpZoneNameRegexp = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// validateZoneName validates that a name meets GCP Cloud DNS managed zone naming constraints.
func validateZoneName(name string) error {
	if !gcpZoneNameRegexp.MatchString(name) {
		return fmt.Errorf("zone name %q is invalid: must start with a lowercase letter and contain only lowercase letters, numbers, and hyphens", name)
	}
	return nil
}

// truncateName truncates a name to the specified maximum length.
func truncateName(name string, maxLen int) string {
	if len(name) <= maxLen {
		return name
	}
	return name[:maxLen]
}

// generateZoneNames generates Cloud DNS zone names and DNS names from cluster name and base domain.
// Returns an error if the generated zone names would violate GCP naming constraints.
func generateZoneNames(clusterName, baseDomain string) (zoneNames, error) {
	// Convert base domain to zone name format (dots -> hyphens)
	baseZoneName := strings.ReplaceAll(baseDomain, ".", "-")

	// GCP DNS zone names must be <= 63 characters
	// Leave room for suffixes: "-public" (7) or "-private" (8) chars
	maxBaseNameLen := 63 - 8
	baseZoneName = truncateName(baseZoneName, maxBaseNameLen)

	names := zoneNames{
		hypershiftLocalZoneName: truncateName(fmt.Sprintf("%s-hypershift-local", clusterName), 63),
		publicIngressZoneName:   truncateName(fmt.Sprintf("%s-public", baseZoneName), 63),
		privateIngressZoneName:  truncateName(fmt.Sprintf("%s-private", baseZoneName), 63),
		ingressDNSName:          ensureDNSDot(baseDomain),
	}

	// Validate all generated zone names
	if err := validateZoneName(names.hypershiftLocalZoneName); err != nil {
		return zoneNames{}, fmt.Errorf("invalid hypershift.local zone name derived from cluster %q: %w", clusterName, err)
	}
	if err := validateZoneName(names.publicIngressZoneName); err != nil {
		return zoneNames{}, fmt.Errorf("invalid public ingress zone name derived from baseDomain %q: %w", baseDomain, err)
	}
	if err := validateZoneName(names.privateIngressZoneName); err != nil {
		return zoneNames{}, fmt.Errorf("invalid private ingress zone name derived from baseDomain %q: %w", baseDomain, err)
	}

	return names, nil
}

// DNSSetupResult contains the results of setting up cluster DNS zones.
type DNSSetupResult struct {
	// HypershiftLocalZone is the hypershift.local private zone
	HypershiftLocalZone *dns.ManagedZone

	// PublicIngressZone is the public ingress zone
	PublicIngressZone *dns.ManagedZone

	// PrivateIngressZone is the private ingress zone
	PrivateIngressZone *dns.ManagedZone

	// IngressDNSName is the DNS name for ingress zones (e.g., "in.{baseDomain}.")
	IngressDNSName string

	// PublicIngressNSRecords are the authoritative name servers for the public ingress zone.
	// These must be delegated from the regional zone by the CLS/CLM delegation controller.
	// Example: ["ns-cloud-a1.googledomains.com.", "ns-cloud-a2.googledomains.com.", ...]
	// Note: Private zones don't need delegation - they're only accessible within the VPC.
	PublicIngressNSRecords []string

	// HypershiftLocalCreatedRecords contains FQDNs of records created in hypershift.local zone
	// Example: ["api.{cluster}.hypershift.local.", "*.apps.{cluster}.hypershift.local."]
	HypershiftLocalCreatedRecords []string

	// PublicIngressCreatedRecords contains FQDNs of records created in public ingress zone
	// Example: ["*.apps.{cluster}.{baseDomain}."]
	PublicIngressCreatedRecords []string

	// PrivateIngressCreatedRecords contains FQDNs of records created in private ingress zone
	// Example: ["*.apps.{cluster}.{baseDomain}."]
	PrivateIngressCreatedRecords []string
}

// createZonesIfNeeded creates the required DNS zones for GCP HCP clusters.
func createZonesIfNeeded(ctx context.Context, svc *dns.Service, projectID, hypershiftZone, publicZone, privateZone, hypershiftDNSName, ingressDNS, vpcNetworkURL string) (*dns.ManagedZone, *dns.ManagedZone, *dns.ManagedZone, error) {
	hypershiftLocalZone, err := createZone(ctx, svc, projectID, hypershiftZone, hypershiftDNSName, "private", vpcNetworkURL)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to ensure hypershift.local zone: %w", err)
	}

	publicIngressZone, err := createZone(ctx, svc, projectID, publicZone, ingressDNS, "public", "")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to ensure public ingress zone: %w", err)
	}

	privateIngressZone, err := createZone(ctx, svc, projectID, privateZone, ingressDNS, "private", vpcNetworkURL)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to ensure private ingress zone: %w", err)
	}

	return hypershiftLocalZone, publicIngressZone, privateIngressZone, nil
}

// retrieveExistingZones retrieves pre-existing DNS zones.
// This is intended for future self-managed scenarios where zones are externally managed.
// Currently not used as DNS zones are always created by the operator.
func retrieveExistingZones(ctx context.Context, svc *dns.Service, projectID, hypershiftZone, publicZone, privateZone string) (*dns.ManagedZone, *dns.ManagedZone, *dns.ManagedZone, error) {
	hypershiftLocalZone, err := getZone(ctx, svc, projectID, hypershiftZone)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get hypershift.local zone %s: %w", hypershiftZone, err)
	}

	publicIngressZone, err := getZone(ctx, svc, projectID, publicZone)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get public ingress zone %s: %w", publicZone, err)
	}

	privateIngressZone, err := getZone(ctx, svc, projectID, privateZone)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get private ingress zone %s: %w", privateZone, err)
	}

	return hypershiftLocalZone, publicIngressZone, privateIngressZone, nil
}

// ensureZones creates or retrieves DNS zones based on the createZones flag.
// Currently always called with createZones=true as self-managed scenarios are not yet supported.
func ensureZones(ctx context.Context, svc *dns.Service, createZones bool, projectID, hypershiftZone, publicZone, privateZone, hypershiftDNSName, ingressDNS, vpcNetworkURL string) (*dns.ManagedZone, *dns.ManagedZone, *dns.ManagedZone, []string, error) {
	var hypershiftLocalZone, publicIngressZone, privateIngressZone *dns.ManagedZone
	var err error

	if createZones {
		hypershiftLocalZone, publicIngressZone, privateIngressZone, err = createZonesIfNeeded(ctx, svc, projectID, hypershiftZone, publicZone, privateZone, hypershiftDNSName, ingressDNS, vpcNetworkURL)
	} else {
		hypershiftLocalZone, publicIngressZone, privateIngressZone, err = retrieveExistingZones(ctx, svc, projectID, hypershiftZone, publicZone, privateZone)
	}

	if err != nil {
		return nil, nil, nil, nil, err
	}

	return hypershiftLocalZone, publicIngressZone, privateIngressZone, publicIngressZone.NameServers, nil
}

// reconcileRecords ensures required DNS records exist with correct values.
// All record operations are idempotent (similar to AWS Route53 UPSERT).
func reconcileRecords(ctx context.Context, svc *dns.Service, projectID, hypershiftZone, publicZone, hypershiftDNSName, ingressDNS, baseDomain, pscEndpointIP string) error {
	// Create ACME challenge CNAME record in public zone
	acmeRecordName := fmt.Sprintf("_acme-challenge.apps.%s", ingressDNS)
	acmeTarget := ensureDNSDot(fmt.Sprintf("_acme-challenge.%s", baseDomain))
	if err := createCNAMERecord(ctx, svc, projectID, publicZone, acmeRecordName, acmeTarget, 300); err != nil {
		return fmt.Errorf("failed to reconcile ACME challenge CNAME: %w", err)
	}

	// Create api A record in hypershift.local zone pointing to PSC endpoint
	apiRecordName := fmt.Sprintf("api.%s", hypershiftDNSName)
	if err := createARecord(ctx, svc, projectID, hypershiftZone, apiRecordName, pscEndpointIP, 60); err != nil {
		return fmt.Errorf("failed to reconcile api A record: %w", err)
	}

	// Create *.apps A record in hypershift.local zone pointing to PSC endpoint
	appsRecordName := fmt.Sprintf("*.apps.%s", hypershiftDNSName)
	if err := createARecord(ctx, svc, projectID, hypershiftZone, appsRecordName, pscEndpointIP, 60); err != nil {
		return fmt.Errorf("failed to reconcile apps A record: %w", err)
	}

	return nil
}

// validateReconcileInput validates the input parameters for DNS reconciliation.
func validateReconcileInput(hcp *hyperv1.HostedControlPlane, pscEndpointIP string) error {
	if hcp.Spec.Platform.GCP == nil {
		return fmt.Errorf("GCP platform spec is nil")
	}

	gcpSpec := hcp.Spec.Platform.GCP

	if hcp.Spec.DNS.BaseDomain == "" {
		return fmt.Errorf("DNS baseDomain is required")
	}
	if gcpSpec.Project == "" {
		return fmt.Errorf("GCP project is required")
	}
	if gcpSpec.NetworkConfig.Network.Name == "" {
		return fmt.Errorf("VPC network name is required")
	}
	if pscEndpointIP == "" {
		return fmt.Errorf("PSC endpoint IP is required")
	}

	return nil
}

// ReconcileDNS reconciles DNS zones and records for a GCP HCP cluster.
// This is the main entry point from the PSC controller reconciliation loop.
//
// This function is fully idempotent and can be called repeatedly. It will:
//   - Create missing DNS zones
//   - Ensure required DNS records exist with correct values
//   - Skip operations if resources already exist in desired state
//   - Return zone information for status updates
//
// Note: DNS zones are always created by the operator. Self-managed scenarios
// where zones are externally managed are not yet supported.
//
// DNS Resources Managed:
//  1. Private hypershift.local zone
//  2. Public ingress zone
//  3. Private ingress zone
//  4. ACME challenge CNAME in public zone (delegates to regional zone)
//  5. A record for api.{cluster}.hypershift.local -> PSC endpoint IP
//
// Parameters:
//   - ctx: Context for the operation
//   - hcp: HostedControlPlane CR containing cluster configuration
//   - pscEndpointIP: IP address of the Private Service Connect endpoint
//
// Returns:
//   - DNSSetupResult: Contains zone information and NS records for status updates
//   - error: Any error encountered during reconciliation
func ReconcileDNS(ctx context.Context, hcp *hyperv1.HostedControlPlane, pscEndpointIP string) (*DNSSetupResult, error) {
	if err := validateReconcileInput(hcp, pscEndpointIP); err != nil {
		return nil, err
	}

	gcpSpec := hcp.Spec.Platform.GCP

	// Extract configuration
	clusterName := hcp.Name
	baseDomain := hcp.Spec.DNS.BaseDomain
	projectID := gcpSpec.Project
	vpcNetwork := gcpSpec.NetworkConfig.Network.Name

	// Generate zone names
	names, err := generateZoneNames(clusterName, baseDomain)
	if err != nil {
		return nil, err
	}

	// Construct VPC network URL
	vpcNetworkURL := fmt.Sprintf(
		"https://www.googleapis.com/compute/v1/projects/%s/global/networks/%s",
		projectID, vpcNetwork)

	// Generate DNS names
	hypershiftDNSName := ensureDNSDot(fmt.Sprintf("%s.hypershift.local", clusterName))

	// Create DNS client (reused for all operations in this reconciliation)
	svc, err := newDNSClient(ctx)
	if err != nil {
		return nil, err
	}

	// Ensure zones exist (create or retrieve)
	hypershiftLocalZone, publicIngressZone, privateIngressZone, publicNSRecords, err := ensureZones(
		ctx, svc, true, projectID,
		names.hypershiftLocalZoneName, names.publicIngressZoneName, names.privateIngressZoneName,
		hypershiftDNSName, names.ingressDNSName, vpcNetworkURL,
	)
	if err != nil {
		return nil, err
	}

	// Reconcile DNS records
	if err := reconcileRecords(ctx, svc, projectID, names.hypershiftLocalZoneName, names.publicIngressZoneName, hypershiftDNSName, names.ingressDNSName, baseDomain, pscEndpointIP); err != nil {
		return nil, err
	}

	// Build lists of created records for status population
	apiRecordName := fmt.Sprintf("api.%s", hypershiftDNSName)
	appsRecordName := fmt.Sprintf("*.apps.%s", hypershiftDNSName)
	acmeRecordName := fmt.Sprintf("_acme-challenge.apps.%s", names.ingressDNSName)

	hypershiftLocalCreatedRecords := []string{
		apiRecordName,
		appsRecordName,
	}

	publicIngressCreatedRecords := []string{
		acmeRecordName,
	}

	// Return result with zone information and created records
	return &DNSSetupResult{
		HypershiftLocalZone:           hypershiftLocalZone,
		PublicIngressZone:             publicIngressZone,
		PrivateIngressZone:            privateIngressZone,
		IngressDNSName:                names.ingressDNSName,
		PublicIngressNSRecords:        publicNSRecords,
		HypershiftLocalCreatedRecords: hypershiftLocalCreatedRecords,
		PublicIngressCreatedRecords:   publicIngressCreatedRecords,
		PrivateIngressCreatedRecords:  []string{}, // Currently no records created in private zone
	}, nil
}

// DeleteDNS deletes DNS zones and records created for a cluster.
// This should be called from the PSC controller finalizer when deleting a cluster.
//
// This function is idempotent and can be called multiple times safely.
// It will skip zones that don't exist (already deleted or never created).
//
// Note: Since DNS zones are always created by the operator in the current
// implementation, this function always attempts to delete them.
//
// Parameters:
//   - ctx: Context for the operation
//   - hcp: HostedControlPlane CR containing cluster configuration
//
// Returns:
//   - error: Any error encountered during deletion (ignores not found errors)
func DeleteDNS(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	if hcp.Spec.Platform.GCP == nil {
		return nil // Nothing to clean up
	}

	gcpSpec := hcp.Spec.Platform.GCP

	if hcp.Spec.DNS.BaseDomain == "" || gcpSpec.Project == "" {
		return nil // Can't determine what to delete
	}

	clusterName := hcp.Name
	baseDomain := hcp.Spec.DNS.BaseDomain
	projectID := gcpSpec.Project

	// Generate zone names
	names, err := generateZoneNames(clusterName, baseDomain)
	if err != nil {
		return err
	}

	// Create DNS client (reused for all operations)
	svc, err := newDNSClient(ctx)
	if err != nil {
		return err
	}

	// Delete all zones (errors are ignored if zones don't exist)
	var errs []error

	if err := deleteZone(ctx, svc, projectID, names.hypershiftLocalZoneName); err != nil {
		errs = append(errs, fmt.Errorf("failed to delete hypershift.local zone: %w", err))
	}

	if err := deleteZone(ctx, svc, projectID, names.publicIngressZoneName); err != nil {
		errs = append(errs, fmt.Errorf("failed to delete public ingress zone: %w", err))
	}

	if err := deleteZone(ctx, svc, projectID, names.privateIngressZoneName); err != nil {
		errs = append(errs, fmt.Errorf("failed to delete private ingress zone: %w", err))
	}

	if len(errs) > 0 {
		return fmt.Errorf("DNS cleanup errors: %v", errs)
	}

	return nil
}
