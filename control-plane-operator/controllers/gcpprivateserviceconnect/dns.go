// Package gcpprivateserviceconnect provides DNS zone management for GCP HCP clusters.
//
// This package is designed for use in Kubernetes reconciliation loops and follows
// idempotent patterns. All operations can be called repeatedly with the same result.
//
// Main entry point:
//   - ReconcileDNS: Reconciles DNS zones and records (called from PSC controller)
//
// DNS cleanup is handled by the cleanupDNS method in psc_endpoint_controller.go,
// which uses zone names stored in the GCPPrivateServiceConnect status.
//
// Authentication:
// All functions use GOOGLE_APPLICATION_CREDENTIALS environment variable to
// authenticate with GCP. This should point to a service account JSON file
// mounted from the gcp-customer-credentials Kubernetes secret.
package gcpprivateserviceconnect

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/gcputil"
	"github.com/openshift/hypershift/support/globalconfig"

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
	var apiErr *googleapi.Error
	if errors.As(err, &apiErr) {
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

// generateZoneNames generates Cloud DNS zone names and DNS names from cluster name and base domain.
// The zone-name derivation is shared with support/gcputil so that the guest dns.config
// wiring (support/globalconfig) computes the identical public/private ingress zone names.
// Returns an error if the generated zone names would violate GCP naming constraints.
func generateZoneNames(clusterName, baseDomain string) (zoneNames, error) {
	hypershiftLocal, err := gcputil.HypershiftLocalZoneName(clusterName)
	if err != nil {
		return zoneNames{}, err
	}
	public, err := gcputil.PublicIngressZoneName(baseDomain)
	if err != nil {
		return zoneNames{}, err
	}
	private, err := gcputil.PrivateIngressZoneName(baseDomain)
	if err != nil {
		return zoneNames{}, err
	}

	return zoneNames{
		hypershiftLocalZoneName: hypershiftLocal,
		publicIngressZoneName:   public,
		privateIngressZoneName:  private,
		ingressDNSName:          ensureDNSDot(baseDomain),
	}, nil
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
	// Whoever manages the parent zone of the base domain must delegate to
	// these for the ingress zone to be resolvable (e.g. an NS record created
	// by the deployment's own tooling; HyperShift does not do this itself).
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

	// ACMECNAME describes the outcome of the opt-in ACME challenge CNAME
	// reconciliation in the public ingress zone (see acmeChallengeRecord).
	ACMECNAME ACMECNAMEOutcome
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
// It returns the outcome of the opt-in ACME challenge CNAME reconciliation so
// the caller can report it in the GCPPrivateServiceConnect status.
func reconcileRecords(ctx context.Context, svc *dns.Service, projectID, hypershiftZone, publicZone, hypershiftDNSName, ingressDomain, baseDomain, acmeChallengeTarget, pscEndpointIP string) (ACMECNAMEOutcome, error) {
	// Create api A record in hypershift.local zone pointing to PSC endpoint
	apiRecordName := fmt.Sprintf("api.%s", hypershiftDNSName)
	if err := createARecord(ctx, svc, projectID, hypershiftZone, apiRecordName, pscEndpointIP, 60); err != nil {
		return ACMECNAMEOutcome{}, fmt.Errorf("failed to reconcile api A record: %w", err)
	}

	// Create *.apps A record in hypershift.local zone pointing to PSC endpoint
	appsRecordName := fmt.Sprintf("*.apps.%s", hypershiftDNSName)
	if err := createARecord(ctx, svc, projectID, hypershiftZone, appsRecordName, pscEndpointIP, 60); err != nil {
		return ACMECNAMEOutcome{}, fmt.Errorf("failed to reconcile apps A record: %w", err)
	}

	// Create the opt-in ACME challenge delegation CNAME in the public ingress
	// zone (see acmeChallengeRecord), after the critical records above so an
	// optional-feature failure cannot block them. When no target is configured,
	// remove the record an earlier controller revision (which created it
	// unconditionally) left at the legacy name: a leftover CNAME blocks the TXT
	// records an in-zone DNS-01 solver needs to write.
	acmeOutcome := acmeChallengeRecord(ingressDomain, baseDomain, acmeChallengeTarget)
	if acmeOutcome.RecordName != "" {
		if err := createCNAMERecord(ctx, svc, projectID, publicZone, acmeOutcome.RecordName, acmeOutcome.Target, 300); err != nil {
			return acmeOutcome, fmt.Errorf("failed to reconcile ACME challenge CNAME: %w", err)
		}
	} else if err := deleteCNAMERecord(ctx, svc, projectID, publicZone, legacyAcmeRecordName(baseDomain), legacyAcmeCNAMETarget(baseDomain)); err != nil {
		return acmeOutcome, fmt.Errorf("failed to clean up legacy ACME challenge CNAME: %w", err)
	}

	return acmeOutcome, nil
}

// ACMECNAMEOutcome describes the result of reconciling the opt-in ACME
// challenge delegation CNAME in the public ingress zone.
type ACMECNAMEOutcome struct {
	// Target is the configured delegation target; empty when none is configured.
	Target string
	// RecordName is the published challenge record name; empty when no record
	// was created.
	RecordName string
	// SkipReason names the reason a configured target produced no record. It is
	// empty when a record was created or no target is configured.
	SkipReason string
	// Message explains SkipReason in terms the cluster owner can act on.
	Message string
}

// acmeChallengeRecord returns the CNAME record delegating the ingress
// wildcard's ACME DNS-01 challenge to an external solver, or the reason no
// record is created.
//
// The record name is the DNS-01 challenge name of the ingress wildcard
// certificate (_acme-challenge prefixed to the cluster's ingress domain,
// apps.<baseDomainPrefix>.<baseDomain> by default, or the
// spec.configuration.ingress override when set), created in the public
// ingress zone this operator manages in the cluster's project. Pointing it at
// the configured target lets an external certificate issuer with a DNS-01
// solver for the target's zone (and CNAME following) solve the challenge for
// the wildcard certificate even though it cannot write the ingress zone
// itself.
//
// No record is created without an explicit target
// (spec.platform.gcp.delegation.acmeChallengeTarget on the HostedCluster): the
// target is a property of the deployment's DNS topology, not something
// derivable from the cluster. The record is skipped, and the reason returned
// so it can be surfaced as a condition, when the ingress domain is not inside
// the cluster's base domain (a spec.configuration.ingress override pointing
// elsewhere): the public ingress zone is not authoritative for such a name. A
// target equal to the record name is a self-referential CNAME no resolver can
// follow (compared case-insensitively, as DNS names are) and is likewise
// skipped.
func acmeChallengeRecord(ingressDomain, baseDomain, target string) ACMECNAMEOutcome {
	outcome := ACMECNAMEOutcome{Target: target}
	if target == "" {
		return outcome
	}
	recordName := fmt.Sprintf("_acme-challenge.%s", ingressDomain)
	if !dnsNameIsWithin(recordName, baseDomain) {
		outcome.SkipReason = hyperv1.GCPACMECNAMEOutsideBaseDomainReason
		outcome.Message = fmt.Sprintf("No ACME challenge CNAME was created: the ingress domain %s is not within the cluster base domain %s, so the public ingress zone is not authoritative for %s.", ingressDomain, baseDomain, recordName)
		return outcome
	}
	if strings.EqualFold(ensureDNSDot(target), ensureDNSDot(recordName)) {
		outcome.SkipReason = hyperv1.GCPACMECNAMESelfReferentialReason
		outcome.Message = fmt.Sprintf("No ACME challenge CNAME was created: the configured target equals the challenge record name %s, which would be a self-referential CNAME.", recordName)
		return outcome
	}
	outcome.RecordName = recordName
	return outcome
}

// dnsNameIsWithin reports whether name is equal to or a subdomain of parent
// (label-boundary aware, case-insensitive).
func dnsNameIsWithin(name, parent string) bool {
	name = strings.ToLower(ensureDNSDot(name))
	parent = strings.ToLower(ensureDNSDot(parent))
	return name == parent || strings.HasSuffix(name, "."+parent)
}

// legacyAcmeRecordName is the record name used by earlier controller
// revisions, which created the ACME challenge CNAME unconditionally at
// _acme-challenge.apps.<baseDomain> for every GCP cluster.
func legacyAcmeRecordName(baseDomain string) string {
	return fmt.Sprintf("_acme-challenge.apps.%s", baseDomain)
}

// legacyAcmeCNAMETarget is the value earlier controller revisions wrote at
// legacyAcmeRecordName. The cleanup path only removes a record carrying
// exactly this value; a CNAME with any other value at that name was put
// there by someone else and must be preserved.
func legacyAcmeCNAMETarget(baseDomain string) string {
	return ensureDNSDot(fmt.Sprintf("_acme-challenge.%s", baseDomain))
}

// deleteCNAMERecord removes the CNAME record at recordName when its value is
// removableTarget; a record with any other value is left untouched. It is a
// no-op when the record does not exist.
func deleteCNAMERecord(ctx context.Context, svc *dns.Service, projectID, zoneName, recordName, removableTarget string) error {
	recordName = ensureDNSDot(recordName)

	existing, err := findRecord(ctx, svc, projectID, zoneName, recordName, "CNAME")
	if err != nil {
		return err
	}
	if existing == nil {
		return nil
	}
	if len(existing.Rrdatas) != 1 || existing.Rrdatas[0] != ensureDNSDot(removableTarget) {
		return nil
	}

	change := &dns.Change{
		Deletions: []*dns.ResourceRecordSet{existing},
	}
	apiCtx, cancel := context.WithTimeout(ctx, dnsAPITimeout)
	defer cancel()
	if _, err := svc.Changes.Create(projectID, zoneName, change).Context(apiCtx).Do(); err != nil {
		return fmt.Errorf("failed to delete CNAME record %s: %w", recordName, err)
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
//  4. ACME challenge CNAME in public zone (opt-in; see acmeChallengeRecord)
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
	vpcNetwork := string(gcpSpec.NetworkConfig.Network.Name)

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

	// The ingress wildcard's ACME challenge is delegated to an external solver
	// only when the deployment configures an explicit target; the record name
	// follows the cluster's ingress domain (apps.<baseDomainPrefix>.<baseDomain>
	// by default, or the spec.configuration.ingress override when set).
	ingressDomain := globalconfig.IngressDomain(hcp)
	acmeChallengeTarget := gcpSpec.Delegation.ACMEChallengeTarget

	// Reconcile DNS records
	acmeOutcome, err := reconcileRecords(ctx, svc, projectID, names.hypershiftLocalZoneName, names.publicIngressZoneName, hypershiftDNSName, ingressDomain, baseDomain, acmeChallengeTarget, pscEndpointIP)
	if err != nil {
		return nil, err
	}

	// Build lists of created records for status population
	apiRecordName := fmt.Sprintf("api.%s", hypershiftDNSName)
	appsRecordName := fmt.Sprintf("*.apps.%s", hypershiftDNSName)

	hypershiftLocalCreatedRecords := []string{
		apiRecordName,
		appsRecordName,
	}

	// The ACME challenge CNAME is only created when the deployment configured
	// a delegation target (see acmeChallengeRecord); report it in the status
	// only then, so it never lists a record that does not exist.
	publicIngressCreatedRecords := []string{}
	if acmeOutcome.RecordName != "" {
		publicIngressCreatedRecords = append(publicIngressCreatedRecords, acmeOutcome.RecordName)
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
		ACMECNAME:                     acmeOutcome,
	}, nil
}
