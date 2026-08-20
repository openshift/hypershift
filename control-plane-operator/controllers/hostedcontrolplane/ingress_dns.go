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
	log := ctrl.LoggerFrom(ctx)

	if last, ok := r.lastDNSReconcile.Load(hcp.Name); ok {
		elapsed := time.Since(last.(time.Time))
		existing := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.AWSManagedDNSAvailable))
		if existing != nil && existing.Status == metav1.ConditionTrue && elapsed < dnsReverifyInterval {
			return nil
		}
		if elapsed < dnsRetryInterval {
			return nil
		}
	}

	if hcp.Spec.Platform.AWS == nil || hcp.Spec.Platform.AWS.CloudProviderConfig == nil {
		return fmt.Errorf("cannot reconcile ingress DNS zones: AWS CloudProviderConfig not set")
	}

	route53Client := route53.NewFromConfig(*r.awsSession)

	managedDNS := hcp.Spec.Platform.AWS.ManagedDNS
	baseDomain := globalconfig.BaseDomain(hcp)
	prefix := managedDNS.IngressDomainPrefix
	if prefix == "" {
		prefix = "in"
	}
	ingressZoneDomain := fmt.Sprintf("%s.%s", prefix, baseDomain)
	isSharedVPC := hcp.Spec.Platform.AWS.SharedVPC != nil

	publicZoneID := getZoneIDFromStatus(hcp, hyperv1.PublicIngressZone)
	resourceTags := hcp.Spec.Platform.AWS.ResourceTags

	// Verify or create public ingress zone
	var publicNS []string
	if err := verifyOrCreateZone(ctx, route53Client, &publicZoneID, "public ingress", func() (string, error) {
		zoneID, ns, err := awsprivatelink.CreatePublicHostedZone(ctx, route53Client, ingressZoneDomain, resourceTags)
		publicNS = ns
		return zoneID, err
	}, log); err != nil {
		_ = r.setManagedDNSCondition(ctx, hcp, metav1.ConditionFalse, hyperv1.AWSManagedDNSErrorReason, fmt.Sprintf("Failed to create public ingress zone: %v", err))
		return fmt.Errorf("failed to create public ingress zone: %w", err)
	}
	if publicZoneID != "" && len(publicNS) == 0 {
		if getOutput, err := route53Client.GetHostedZone(ctx, &route53.GetHostedZoneInput{Id: aws.String(publicZoneID)}); err == nil && getOutput.DelegationSet != nil {
			publicNS = getOutput.DelegationSet.NameServers
		}
	}
	setZoneInStatus(hcp, hyperv1.PublicIngressZone, publicZoneID, ingressZoneDomain, publicNS)

	if managedDNS.Delegation.NSDelegation != "" {
		// Create ACME DNS01 challenge CNAME in the public zone (idempotent)
		if publicZoneID != "" {
			acmeFrom := fmt.Sprintf("_acme-challenge.apps.%s", ingressZoneDomain)
			acmeTo := fmt.Sprintf("_acme-challenge.%s", baseDomain)
			if err := awsprivatelink.CreateRecord(ctx, route53Client, publicZoneID, acmeFrom, acmeTo, route53types.RRTypeCname); err != nil {
				_ = r.setManagedDNSCondition(ctx, hcp, metav1.ConditionFalse, hyperv1.AWSManagedDNSErrorReason, fmt.Sprintf("Failed to create ACME CNAME: %v", err))
				return fmt.Errorf("failed to create ACME challenge CNAME: %w", err)
			}
		}

		// Create DNSEndpoint CR for NS delegation only when nsDelegation is ExternalDNS
		if publicZoneID != "" && managedDNS.Delegation.NSDelegation == hyperv1.NSDelegationExternalDNS {
			getOutput, getErr := route53Client.GetHostedZone(ctx, &route53.GetHostedZoneInput{
				Id: aws.String(publicZoneID),
			})
			if getErr != nil {
				log.Error(getErr, "Failed to get public zone NS records for DNSEndpoint")
			} else if getOutput.DelegationSet != nil && len(getOutput.DelegationSet.NameServers) > 0 {
				if err := r.reconcileDNSEndpoint(ctx, hcp, ingressZoneDomain, getOutput.DelegationSet.NameServers); err != nil {
					log.Error(err, "Failed to create DNSEndpoint for NS delegation")
				}
			}
		}
	}

	// Create private ingress zone (standard clusters only, not shared VPC)
	if !isSharedVPC {
		vpcID := hcp.Spec.Platform.AWS.CloudProviderConfig.VPC
		region := hcp.Spec.Platform.AWS.Region
		privateZoneID := getZoneIDFromStatus(hcp, hyperv1.PrivateIngressZone)

		if err := verifyOrCreateZone(ctx, route53Client, &privateZoneID, "private ingress", func() (string, error) {
			return awsprivatelink.CreatePrivateHostedZone(ctx, route53Client, ingressZoneDomain, vpcID, region, resourceTags)
		}, log); err != nil {
			_ = r.setManagedDNSCondition(ctx, hcp, metav1.ConditionFalse, hyperv1.AWSManagedDNSErrorReason, fmt.Sprintf("Failed to create private ingress zone: %v", err))
			return fmt.Errorf("failed to create private ingress zone: %w", err)
		}
		setZoneInStatus(hcp, hyperv1.PrivateIngressZone, privateZoneID, ingressZoneDomain, nil)
	}

	if managedDNS.Delegation.NSDelegation != "" {
		existingCond := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.AWSManagedDNSAvailable))

		lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		nsRecords, lookupErr := net.DefaultResolver.LookupNS(lookupCtx, ingressZoneDomain)
		if lookupErr != nil || len(nsRecords) == 0 {
			const maxDelegationWait = 10 * time.Minute
			msg := "DNS zones created; NS delegation not yet resolvable"
			if existingCond != nil && existingCond.Reason == hyperv1.AWSManagedDNSPendingReason &&
				time.Since(existingCond.LastTransitionTime.Time) > maxDelegationWait {
				msg = fmt.Sprintf("NS delegation not resolvable after %v; verify NS records exist in the parent zone for %s", maxDelegationWait, ingressZoneDomain)
			}
			return r.setManagedDNSCondition(ctx, hcp, metav1.ConditionFalse, hyperv1.AWSManagedDNSPendingReason, msg)
		}
		r.lastDNSReconcile.Store(hcp.Name, time.Now())
		return r.setManagedDNSCondition(ctx, hcp, metav1.ConditionTrue, hyperv1.AWSManagedDNSSuccessReason, fmt.Sprintf("DNS zones created; NS delegation verified (%d nameservers)", len(nsRecords)))
	}

	r.lastDNSReconcile.Store(hcp.Name, time.Now())
	return r.setManagedDNSCondition(ctx, hcp, metav1.ConditionTrue, hyperv1.AWSManagedDNSSuccessReason, "DNS zones created")
}

func (r *HostedControlPlaneReconciler) cleanupIngressDNSZones(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
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

	// Delete public ingress zone
	publicZoneID := getZoneIDFromStatus(hcp, hyperv1.PublicIngressZone)
	if publicZoneID != "" {
		if err := awsprivatelink.DeleteZoneBestEffort(ctx, route53Client, publicZoneID, "public ingress", log); err != nil {
			return err
		}
	}

	// Delete private ingress zone (standard clusters only, not shared VPC)
	if hcp.Spec.Platform.AWS == nil || hcp.Spec.Platform.AWS.SharedVPC == nil {
		privateZoneID := getZoneIDFromStatus(hcp, hyperv1.PrivateIngressZone)
		if privateZoneID != "" {
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

func (r *HostedControlPlaneReconciler) setManagedDNSCondition(_ context.Context, hcp *hyperv1.HostedControlPlane, status metav1.ConditionStatus, reason, message string) error {
	meta.SetStatusCondition(&hcp.Status.Conditions, metav1.Condition{
		Type:    string(hyperv1.AWSManagedDNSAvailable),
		Status:  status,
		Reason:  reason,
		Message: message,
	})
	return nil
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

