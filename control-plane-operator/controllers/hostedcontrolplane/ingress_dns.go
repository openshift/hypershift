package hostedcontrolplane

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	"github.com/go-logr/logr"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	awsprivatelink "github.com/openshift/hypershift/control-plane-operator/controllers/awsprivatelink"
	"github.com/openshift/hypershift/support/awsapi"
	"github.com/openshift/hypershift/support/globalconfig"
)

var dnsEndpointGVK = schema.GroupVersionKind{
	Group:   "externaldns.k8s.io",
	Version: "v1alpha1",
	Kind:    "DNSEndpoint",
}

const (
	dnsReverifyInterval = 5 * time.Minute
	dnsRetryInterval    = 30 * time.Second
)

func (r *HostedControlPlaneReconciler) reconcileIngressDNSZones(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	// Managed ingress DNS is opt-in; nothing to do otherwise.
	if hcp.Spec.Platform.AWS == nil || hcp.Spec.Platform.AWS.ManagedDNS == nil {
		return nil
	}
	if !r.throttleDNSReconcile(hcp) {
		return nil
	}

	route53Client := route53.NewFromConfig(*r.awsSession)
	domain := ingressZoneDomain(hcp)

	// Public ingress zone: always managed for managed-DNS clusters.
	publicNS, err := r.reconcilePublicIngressZone(ctx, route53Client, hcp, domain)
	if err != nil {
		r.setManagedDNSCondition(hcp, metav1.ConditionFalse, hyperv1.AWSManagedDNSErrorReason, fmt.Sprintf("Failed to create public ingress zone: %v", err))
		return fmt.Errorf("failed to create public ingress zone: %w", err)
	}

	// Private ingress zone: standard clusters only (skipped for shared VPC).
	if err := r.reconcilePrivateIngressZone(ctx, route53Client, hcp, domain); err != nil {
		r.setManagedDNSCondition(hcp, metav1.ConditionFalse, hyperv1.AWSManagedDNSErrorReason, fmt.Sprintf("Failed to create private ingress zone: %v", err))
		return fmt.Errorf("failed to create private ingress zone: %w", err)
	}

	// NS delegation (ACME CNAME, DNSEndpoint, delegation verification) sets the
	// terminal condition itself.
	return r.reconcileNSDelegation(ctx, route53Client, hcp, domain, publicNS)
}

// throttleDNSReconcile rate-limits Route53 work per cluster. It returns false
// when the caller should skip this reconcile. When it returns true it records
// the attempt now (before any work), so error and pending paths are throttled
// too — not just the success path — keeping the create/NS-delegation window
// from hammering Route53.
func (r *HostedControlPlaneReconciler) throttleDNSReconcile(hcp *hyperv1.HostedControlPlane) bool {
	if last, ok := r.lastDNSReconcile.Load(hcp.Name); ok {
		elapsed := time.Since(last.(time.Time))
		existing := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.AWSManagedDNSAvailable))
		if existing != nil && existing.Status == metav1.ConditionTrue && elapsed < dnsReverifyInterval {
			return false
		}
		if elapsed < dnsRetryInterval {
			return false
		}
	}
	r.lastDNSReconcile.Store(hcp.Name, time.Now())
	return true
}

// ingressZoneDomain returns the managed ingress zone domain, e.g. "in.<base>".
func ingressZoneDomain(hcp *hyperv1.HostedControlPlane) string {
	prefix := hcp.Spec.Platform.AWS.ManagedDNS.IngressDomainPrefix
	if prefix == "" {
		prefix = "in"
	}
	return fmt.Sprintf("%s.%s", prefix, globalconfig.BaseDomain(hcp))
}

// reconcilePublicIngressZone verifies or creates the public ingress zone and
// records it in status. It returns the zone's nameservers for NS delegation.
func (r *HostedControlPlaneReconciler) reconcilePublicIngressZone(ctx context.Context, route53Client awsapi.ROUTE53API, hcp *hyperv1.HostedControlPlane, domain string) ([]string, error) {
	log := ctrl.LoggerFrom(ctx)
	zoneID := getZoneIDFromStatus(hcp, hyperv1.PublicIngressZone)

	var nameServers []string
	if err := verifyOrCreateZone(ctx, route53Client, &zoneID, "public ingress", func() (string, error) {
		id, ns, err := awsprivatelink.CreatePublicHostedZone(ctx, route53Client, domain, hcp.Spec.Platform.AWS.ResourceTags)
		nameServers = ns
		return id, err
	}, log); err != nil {
		return nil, err
	}

	// createFn only yields nameservers on a fresh create; for an existing zone
	// look them up so NS delegation still works.
	if zoneID != "" && len(nameServers) == 0 {
		if out, err := route53Client.GetHostedZone(ctx, &route53.GetHostedZoneInput{Id: aws.String(zoneID)}); err == nil && out.DelegationSet != nil {
			nameServers = out.DelegationSet.NameServers
		}
	}

	setZoneInStatus(hcp, hyperv1.PublicIngressZone, zoneID, domain, nameServers)
	return nameServers, nil
}

// reconcilePrivateIngressZone verifies or creates the private ingress zone and
// records it in status. Shared VPC clusters use a private zone pre-created and
// owned by the VPC owner, so the CPO does not manage it and returns early.
func (r *HostedControlPlaneReconciler) reconcilePrivateIngressZone(ctx context.Context, route53Client awsapi.ROUTE53API, hcp *hyperv1.HostedControlPlane, domain string) error {
	if hcp.Spec.Platform.AWS.SharedVPC != nil {
		return nil
	}

	log := ctrl.LoggerFrom(ctx)
	zoneID := getZoneIDFromStatus(hcp, hyperv1.PrivateIngressZone)
	vpcID := hcp.Spec.Platform.AWS.CloudProviderConfig.VPC
	region := hcp.Spec.Platform.AWS.Region

	if err := verifyOrCreateZone(ctx, route53Client, &zoneID, "private ingress", func() (string, error) {
		return awsprivatelink.CreatePrivateHostedZone(ctx, route53Client, domain, vpcID, region, hcp.Spec.Platform.AWS.ResourceTags)
	}, log); err != nil {
		return err
	}
	setZoneInStatus(hcp, hyperv1.PrivateIngressZone, zoneID, domain, nil)
	return nil
}

// reconcileNSDelegation wires up NS delegation for the public zone and sets the
// terminal AWSManagedDNSAvailable condition. When delegation is not requested it
// simply reports success.
func (r *HostedControlPlaneReconciler) reconcileNSDelegation(ctx context.Context, route53Client awsapi.ROUTE53API, hcp *hyperv1.HostedControlPlane, domain string, publicNS []string) error {
	log := ctrl.LoggerFrom(ctx)
	managedDNS := hcp.Spec.Platform.AWS.ManagedDNS

	if managedDNS.Delegation.NSDelegation == "" {
		r.setManagedDNSCondition(hcp, metav1.ConditionTrue, hyperv1.AWSManagedDNSSuccessReason, "DNS zones created")
		return nil
	}

	publicZoneID := getZoneIDFromStatus(hcp, hyperv1.PublicIngressZone)

	// Create the ACME DNS01 challenge CNAME in the public zone (idempotent).
	if publicZoneID != "" {
		acmeFrom := fmt.Sprintf("_acme-challenge.apps.%s", domain)
		acmeTo := fmt.Sprintf("_acme-challenge.%s", globalconfig.BaseDomain(hcp))
		if err := awsprivatelink.CreateRecord(ctx, route53Client, publicZoneID, acmeFrom, acmeTo, route53types.RRTypeCname); err != nil {
			r.setManagedDNSCondition(hcp, metav1.ConditionFalse, hyperv1.AWSManagedDNSErrorReason, fmt.Sprintf("Failed to create ACME CNAME: %v", err))
			return fmt.Errorf("failed to create ACME challenge CNAME: %w", err)
		}
	}

	// Create the DNSEndpoint CR for NS delegation only in ExternalDNS mode;
	// publicNS is already known, so no extra Route53 call is needed. This is
	// best-effort and surfaced through the pending condition below.
	var dnsEndpointErr error
	if publicZoneID != "" && managedDNS.Delegation.NSDelegation == hyperv1.NSDelegationExternalDNS && len(publicNS) > 0 {
		if err := r.reconcileDNSEndpoint(ctx, hcp, domain, publicNS); err != nil {
			log.Error(err, "Failed to create DNSEndpoint for NS delegation")
			dnsEndpointErr = err
		}
	}

	return r.verifyNSDelegation(ctx, hcp, domain, dnsEndpointErr)
}

// verifyNSDelegation checks that the ingress zone's NS records are resolvable
// and sets the terminal condition accordingly (True once resolvable, Pending
// while we wait).
func (r *HostedControlPlaneReconciler) verifyNSDelegation(ctx context.Context, hcp *hyperv1.HostedControlPlane, domain string, dnsEndpointErr error) error {
	lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	nsRecords, lookupErr := net.DefaultResolver.LookupNS(lookupCtx, domain)
	if lookupErr == nil && len(nsRecords) > 0 {
		r.setManagedDNSCondition(hcp, metav1.ConditionTrue, hyperv1.AWSManagedDNSSuccessReason, fmt.Sprintf("DNS zones created; NS delegation verified (%d nameservers)", len(nsRecords)))
		return nil
	}

	const maxDelegationWait = 10 * time.Minute
	existingCond := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.AWSManagedDNSAvailable))
	msg := "DNS zones created; NS delegation not yet resolvable"
	switch {
	case dnsEndpointErr != nil:
		msg = fmt.Sprintf("DNS zones created; DNSEndpoint for NS delegation not yet reconciled: %v", dnsEndpointErr)
	case existingCond != nil && existingCond.Reason == hyperv1.AWSManagedDNSPendingReason &&
		time.Since(existingCond.LastTransitionTime.Time) > maxDelegationWait:
		msg = fmt.Sprintf("NS delegation not resolvable after %v; verify NS records exist in the parent zone for %s", maxDelegationWait, domain)
	}
	r.setManagedDNSCondition(hcp, metav1.ConditionFalse, hyperv1.AWSManagedDNSPendingReason, msg)
	return nil
}

func (r *HostedControlPlaneReconciler) cleanupIngressDNSZones(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	// Managed ingress DNS is opt-in; nothing was created, so nothing to clean up.
	if hcp.Spec.Platform.AWS == nil || hcp.Spec.Platform.AWS.ManagedDNS == nil {
		return nil
	}
	defer r.lastDNSReconcile.Delete(hcp.Name)

	log := ctrl.LoggerFrom(ctx)
	route53Client := route53.NewFromConfig(*r.awsSession)

	// Delete DNSEndpoint CR (best-effort)
	dnsEndpoint := &unstructured.Unstructured{}
	dnsEndpoint.SetGroupVersionKind(dnsEndpointGVK)
	dnsEndpoint.SetName(hcp.Name + "-ingress-delegation")
	dnsEndpoint.SetNamespace(hcp.Namespace)
	if err := r.Delete(ctx, dnsEndpoint); err != nil && !apierrors.IsNotFound(err) {
		log.Error(err, "failed to delete DNSEndpoint")
	} else if err == nil {
		log.Info("Deleted DNSEndpoint")
	}

	// Delete public ingress zone.
	if publicZoneID := getZoneIDFromStatus(hcp, hyperv1.PublicIngressZone); publicZoneID != "" {
		if err := awsprivatelink.DeleteZoneBestEffort(ctx, route53Client, publicZoneID, "public ingress", log); err != nil {
			return err
		}
	}

	// Delete private ingress zone: standard clusters only. Shared VPC uses a zone
	// owned by the VPC owner, so there is nothing for us to delete.
	if hcp.Spec.Platform.AWS.SharedVPC == nil {
		if privateZoneID := getZoneIDFromStatus(hcp, hyperv1.PrivateIngressZone); privateZoneID != "" {
			if err := awsprivatelink.DeleteZoneBestEffort(ctx, route53Client, privateZoneID, "private ingress", log); err != nil {
				return err
			}
		}
	}

	return nil
}

func (r *HostedControlPlaneReconciler) reconcileDNSEndpoint(ctx context.Context, hcp *hyperv1.HostedControlPlane, ingressDNSName string, nameServers []string) error {
	log := ctrl.LoggerFrom(ctx)

	dnsName := strings.TrimSuffix(ingressDNSName, ".")
	nsTargets := make([]interface{}, len(nameServers))
	for i, ns := range nameServers {
		nsTargets[i] = strings.TrimSuffix(ns, ".")
	}

	dnsEndpoint := &unstructured.Unstructured{}
	dnsEndpoint.SetGroupVersionKind(dnsEndpointGVK)
	dnsEndpoint.SetName(hcp.Name + "-ingress-delegation")
	dnsEndpoint.SetNamespace(hcp.Namespace)

	result, err := controllerutil.CreateOrUpdate(ctx, r.Client, dnsEndpoint, func() error {
		if err := controllerutil.SetControllerReference(hcp, dnsEndpoint, r.Client.Scheme()); err != nil {
			return fmt.Errorf("failed to set controller reference on DNSEndpoint: %w", err)
		}
		dnsEndpoint.Object["spec"] = map[string]interface{}{
			"endpoints": []interface{}{
				map[string]interface{}{
					"dnsName":    dnsName,
					"recordType": "NS",
					"targets":    nsTargets,
					"recordTTL":  int64(300),
				},
			},
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile DNSEndpoint: %w", err)
	}

	if result != controllerutil.OperationResultNone {
		log.Info("Reconciled DNSEndpoint", "result", result, "name", dnsEndpoint.GetName(), "dnsName", dnsName, "nameservers", nameServers)
	}

	return nil
}

func (r *HostedControlPlaneReconciler) setManagedDNSCondition(hcp *hyperv1.HostedControlPlane, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&hcp.Status.Conditions, metav1.Condition{
		Type:    string(hyperv1.AWSManagedDNSAvailable),
		Status:  status,
		Reason:  reason,
		Message: message,
	})
}

func getZoneIDFromStatus(hcp *hyperv1.HostedControlPlane, zoneType hyperv1.AWSDNSZoneType) string {
	if hcp.Status.Platform == nil || hcp.Status.Platform.AWS == nil {
		return ""
	}
	for _, z := range hcp.Status.Platform.AWS.DNSZones {
		if z.ZoneType == zoneType {
			return z.ZoneID
		}
	}
	return ""
}

func setZoneInStatus(hcp *hyperv1.HostedControlPlane, zoneType hyperv1.AWSDNSZoneType, zoneID, name string, nameServers []string) {
	if hcp.Status.Platform == nil {
		hcp.Status.Platform = &hyperv1.PlatformStatus{}
	}
	if hcp.Status.Platform.AWS == nil {
		hcp.Status.Platform.AWS = &hyperv1.AWSPlatformStatus{}
	}
	for i, z := range hcp.Status.Platform.AWS.DNSZones {
		if z.ZoneType == zoneType {
			hcp.Status.Platform.AWS.DNSZones[i].ZoneID = zoneID
			hcp.Status.Platform.AWS.DNSZones[i].Name = name
			hcp.Status.Platform.AWS.DNSZones[i].NameServers = nameServers
			return
		}
	}
	hcp.Status.Platform.AWS.DNSZones = append(hcp.Status.Platform.AWS.DNSZones, hyperv1.AWSDNSZoneStatus{
		ZoneID:      zoneID,
		ZoneType:    zoneType,
		Name:        name,
		NameServers: nameServers,
	})
}

func verifyOrCreateZone(ctx context.Context, route53Client awsapi.ROUTE53API, zoneID *string, label string, createFn func() (string, error), log logr.Logger) error {
	if *zoneID != "" {
		if _, err := route53Client.GetHostedZone(ctx, &route53.GetHostedZoneInput{Id: aws.String(*zoneID)}); err != nil {
			var noSuchZone *route53types.NoSuchHostedZone
			if errors.As(err, &noSuchZone) {
				log.Info("zone deleted externally, clearing for recreation", "zone", label, "zoneID", *zoneID)
				*zoneID = ""
			} else {
				return fmt.Errorf("failed to verify %s zone %s: %w", label, *zoneID, err)
			}
		}
	}
	if *zoneID == "" {
		id, err := createFn()
		if err != nil {
			return err
		}
		*zoneID = id
		log.Info("Created zone", "zone", label, "zoneID", id)
	}
	return nil
}
