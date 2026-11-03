package gcpprivateserviceconnect

import (
	"errors"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/google/uuid"
	"google.golang.org/api/dns/v1"
)

// ownershipLabelKey marks the cluster that owns a Cloud DNS managed zone. The key is a
// valid Cloud DNS label key: it starts with a lowercase letter and contains only lowercase
// letters, digits and hyphens.
//
// The zones this controller manages are named after the HostedControlPlane name and the
// base domain, neither of which is guaranteed unique within a GCP project. Cloud DNS
// requires managed-zone names to be unique per project and returns an existing zone rather
// than failing, so a name alone cannot establish ownership. The label is the only evidence
// the controller trusts before mutating or deleting a zone.
const ownershipLabelKey = "hypershift-cluster-id"

var (
	// errZoneUnowned means a zone with the expected name exists but carries no ownership
	// label. It may predate ownership labeling or belong to the customer. It is never
	// adopted, because a matching name is not proof that this cluster created it.
	errZoneUnowned = errors.New("DNS zone exists but has no cluster ownership label")

	// errZoneOwnedByOther means a zone with the expected name is labeled for a different
	// cluster. Mutating it would corrupt that cluster's DNS.
	errZoneOwnedByOther = errors.New("DNS zone is owned by a different cluster")
)

// clusterOwnerID returns the ownership label value for an HCP: its cluster ID, lowercased.
//
// The value is parsed as an RFC 4122 UUID rather than checked against the Cloud DNS label
// grammar directly. A UUID in its canonical textual form only ever contains lowercase
// hexadecimal digits and hyphens once lowercased, which is always a valid Cloud DNS label
// value, so parsing is both the stricter and the simpler check. Cloud DNS permits a label
// value to begin with a digit, so a UUID starting with one is fine.
//
// HostedCluster validates clusterID as a UUID, but HostedControlPlane only bounds its
// length, and the field is optional on both. An HCP that has not been reconciled yet, or
// one written by hand, can therefore carry an empty or malformed value. Callers must treat
// an error here as fatal for the reconcile and must not fall back to name-based ownership.
func clusterOwnerID(hcp *hyperv1.HostedControlPlane) (string, error) {
	if hcp == nil {
		return "", errors.New("cannot determine DNS zone ownership: HostedControlPlane is nil")
	}
	if hcp.Spec.ClusterID == "" {
		return "", errors.New("cannot determine DNS zone ownership: HostedControlPlane has no clusterID")
	}

	parsed, err := uuid.Parse(hcp.Spec.ClusterID)
	if err != nil {
		return "", fmt.Errorf("cannot determine DNS zone ownership: clusterID %q is not a valid UUID: %w", hcp.Spec.ClusterID, err)
	}

	// uuid.UUID.String always renders lowercase, which normalizes the uppercase hexadecimal
	// that the HostedCluster API permits.
	return parsed.String(), nil
}

// zoneOwnership is the result of comparing a fetched zone's ownership label against the
// cluster that expects to own it.
type zoneOwnership int

const (
	// zoneOwnedByCluster means the zone carries this cluster's ownership label.
	zoneOwnedByCluster zoneOwnership = iota
	// zoneUnlabeled means the zone carries no ownership label.
	zoneUnlabeled
	// zoneOwnedByOther means the zone carries a different cluster's ownership label.
	zoneOwnedByOther
)

// classifyZoneOwnership compares a fetched zone's ownership label with the expected owner.
// It never mutates the zone and never infers ownership from the zone name.
func classifyZoneOwnership(zone *dns.ManagedZone, ownerID string) zoneOwnership {
	if zone == nil {
		return zoneUnlabeled
	}
	switch zone.Labels[ownershipLabelKey] {
	case "":
		return zoneUnlabeled
	case ownerID:
		return zoneOwnedByCluster
	default:
		return zoneOwnedByOther
	}
}

// verifyZoneOwnership returns nil only when the zone is labeled for ownerID. The returned
// errors are sentinels so callers can distinguish an unlabeled zone from a conflicting one.
func verifyZoneOwnership(zone *dns.ManagedZone, ownerID string) error {
	if zone == nil {
		return fmt.Errorf("%w: no zone to verify, expected owner %q", errZoneUnowned, ownerID)
	}

	switch classifyZoneOwnership(zone, ownerID) {
	case zoneOwnedByCluster:
		return nil
	case zoneOwnedByOther:
		return fmt.Errorf("%w: zone %q is labeled %s=%q, expected %q",
			errZoneOwnedByOther, zone.Name, ownershipLabelKey, zone.Labels[ownershipLabelKey], ownerID)
	default:
		return fmt.Errorf("%w: zone %q has no %s label, expected %q",
			errZoneUnowned, zone.Name, ownershipLabelKey, ownerID)
	}
}

// zoneLabels builds the label set applied to the managed zones of a cluster: the
// configured HostedControlPlane resource labels plus the ownership label.
//
// The ownership label is applied last so a resource label cannot shadow it.
func zoneLabels(resourceLabels map[string]string, ownerID string) map[string]string {
	labels := make(map[string]string, len(resourceLabels)+1)
	for key, value := range resourceLabels {
		labels[key] = value
	}
	labels[ownershipLabelKey] = ownerID
	return labels
}

// missingZoneLabels returns the labels in desired that are absent or differ on the zone.
// It returns nil when the zone is already up to date, so callers can skip a no-op patch.
//
// Reconciliation is additive: a label that this controller previously applied and that has
// since been removed from the HostedControlPlane is left on the zone rather than deleted.
// Distinguishing a withdrawn managed label from one the customer set themselves needs a
// record of previously managed keys, which is tracked on the GCPPrivateServiceConnect CR
// for the PSC Address and ForwardingRule but is not reachable from this call path. Zones
// are per-cluster and are deleted with the cluster, so a stale label is bounded.
func missingZoneLabels(zone *dns.ManagedZone, desired map[string]string) map[string]string {
	var drifted map[string]string
	for key, value := range desired {
		if zone != nil && zone.Labels[key] == value {
			continue
		}
		if drifted == nil {
			drifted = map[string]string{}
		}
		drifted[key] = value
	}
	return drifted
}

// mergedZoneLabels returns the zone's existing labels with desired applied on top,
// preserving any label this controller does not manage.
func mergedZoneLabels(zone *dns.ManagedZone, desired map[string]string) map[string]string {
	merged := map[string]string{}
	if zone != nil {
		for key, value := range zone.Labels {
			merged[key] = value
		}
	}
	for key, value := range desired {
		merged[key] = value
	}
	return merged
}
