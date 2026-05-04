package awsprivatelink

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/awsapi"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"

	ctrl "sigs.k8s.io/controller-runtime"
)

type ingressDNSResult struct {
	PublicZoneID  string
	PrivateZoneID string
	NSRecords     []string
	IngressDNS    string
}

func ingressDNSName(hcp *hyperv1.HostedControlPlane) string {
	prefix := hcp.Name
	if hcp.Spec.DNS.BaseDomainPrefix != nil && *hcp.Spec.DNS.BaseDomainPrefix != "" {
		prefix = *hcp.Spec.DNS.BaseDomainPrefix
	}
	return fmt.Sprintf("%s.%s", prefix, hcp.Spec.DNS.BaseDomain)
}

func callerReference(hcp *hyperv1.HostedControlPlane, zoneName string) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s/%s/%s", hcp.Namespace, hcp.Name, zoneName)))
	return fmt.Sprintf("%x", h[:16])
}

func createOrGetPublicHostedZone(ctx context.Context, client awsapi.ROUTE53API, name, callerRef string) (string, []string, error) {
	log := ctrl.LoggerFrom(ctx)

	output, err := client.CreateHostedZone(ctx, &route53.CreateHostedZoneInput{
		Name:            aws.String(name),
		CallerReference: aws.String(callerRef),
		HostedZoneConfig: &route53types.HostedZoneConfig{
			Comment: aws.String("Public ingress zone managed by HyperShift"),
		},
	})
	if err != nil {
		var alreadyExists *route53types.HostedZoneAlreadyExists
		if !errors.As(err, &alreadyExists) {
			return "", nil, fmt.Errorf("failed to create public hosted zone %s: %w", name, err)
		}
		log.Info("Public hosted zone already exists, looking up", "name", name)
		zoneID, lookupErr := lookupIngressZoneID(ctx, client, name, false)
		if lookupErr != nil {
			return "", nil, fmt.Errorf("failed to lookup existing public zone %s: %w", name, lookupErr)
		}
		getOutput, getErr := client.GetHostedZone(ctx, &route53.GetHostedZoneInput{
			Id: aws.String(zoneID),
		})
		if getErr != nil {
			return "", nil, fmt.Errorf("failed to get public zone %s: %w", zoneID, getErr)
		}
		var ns []string
		if getOutput.DelegationSet != nil {
			ns = getOutput.DelegationSet.NameServers
		}
		return zoneID, ns, nil
	}

	zoneID := cleanZoneID(aws.ToString(output.HostedZone.Id))
	var ns []string
	if output.DelegationSet != nil {
		ns = output.DelegationSet.NameServers
	}
	log.Info("Created public hosted zone", "name", name, "zoneID", zoneID)
	return zoneID, ns, nil
}

func createOrGetPrivateHostedZone(ctx context.Context, client awsapi.ROUTE53API, name, callerRef, vpcID, region string) (string, error) {
	log := ctrl.LoggerFrom(ctx)

	output, err := client.CreateHostedZone(ctx, &route53.CreateHostedZoneInput{
		Name:            aws.String(name),
		CallerReference: aws.String(callerRef),
		HostedZoneConfig: &route53types.HostedZoneConfig{
			Comment:     aws.String("Private ingress zone managed by HyperShift"),
			PrivateZone: true,
		},
		VPC: &route53types.VPC{
			VPCId:     aws.String(vpcID),
			VPCRegion: route53types.VPCRegion(region),
		},
	})
	if err != nil {
		var alreadyExists *route53types.HostedZoneAlreadyExists
		if !errors.As(err, &alreadyExists) {
			return "", fmt.Errorf("failed to create private hosted zone %s: %w", name, err)
		}
		log.Info("Private hosted zone already exists, looking up", "name", name)
		zoneID, lookupErr := lookupIngressZoneID(ctx, client, name, true)
		if lookupErr != nil {
			return "", fmt.Errorf("failed to lookup existing private zone %s: %w", name, lookupErr)
		}
		getOutput, getErr := client.GetHostedZone(ctx, &route53.GetHostedZoneInput{
			Id: aws.String(zoneID),
		})
		if getErr != nil {
			return "", fmt.Errorf("failed to get private zone %s: %w", zoneID, getErr)
		}
		vpcAssociated := false
		for _, v := range getOutput.VPCs {
			if aws.ToString(v.VPCId) == vpcID {
				vpcAssociated = true
				break
			}
		}
		if !vpcAssociated {
			log.Info("Private zone not associated with expected VPC, associating", "zoneID", zoneID, "vpcID", vpcID)
			_, assocErr := client.AssociateVPCWithHostedZone(ctx, &route53.AssociateVPCWithHostedZoneInput{
				HostedZoneId: aws.String(zoneID),
				VPC: &route53types.VPC{
					VPCId:     aws.String(vpcID),
					VPCRegion: route53types.VPCRegion(region),
				},
			})
			if assocErr != nil {
				return "", fmt.Errorf("failed to associate VPC %s with private zone %s: %w", vpcID, zoneID, assocErr)
			}
		}
		return zoneID, nil
	}

	zoneID := cleanZoneID(aws.ToString(output.HostedZone.Id))
	log.Info("Created private hosted zone", "name", name, "zoneID", zoneID)
	return zoneID, nil
}

func lookupIngressZoneID(ctx context.Context, client awsapi.ROUTE53API, name string, privateZone bool) (string, error) {
	normalizedName := strings.TrimSuffix(name, ".")
	resp, err := client.ListHostedZonesByName(ctx, &route53.ListHostedZonesByNameInput{
		DNSName: aws.String(normalizedName),
	})
	if err != nil {
		return "", fmt.Errorf("failed to list hosted zones by name: %w", err)
	}
	for _, zone := range resp.HostedZones {
		zoneName := strings.TrimSuffix(aws.ToString(zone.Name), ".")
		if zoneName != normalizedName {
			break
		}
		isPrivate := zone.Config != nil && zone.Config.PrivateZone
		if isPrivate == privateZone {
			return cleanZoneID(aws.ToString(zone.Id)), nil
		}
	}
	return "", fmt.Errorf("hosted zone %s (private=%v) not found", name, privateZone)
}

func createACMEChallengeRecord(ctx context.Context, client awsapi.ROUTE53API, publicZoneID, ingressDNS, baseDomain string) error {
	recordName := fmt.Sprintf("_acme-challenge.apps.%s", ingressDNS)
	target := fmt.Sprintf("_acme-challenge.%s", baseDomain)
	return CreateRecord(ctx, client, publicZoneID, recordName, target, route53types.RRTypeCname)
}

func reconcileIngressDNS(ctx context.Context, route53Client awsapi.ROUTE53API, hcp *hyperv1.HostedControlPlane, existingPublicZoneID, existingPrivateZoneID string) (*ingressDNSResult, error) {
	log := ctrl.LoggerFrom(ctx)

	if hcp.Spec.Platform.AWS == nil {
		return nil, fmt.Errorf("AWS platform spec is nil")
	}
	if hcp.Spec.DNS.BaseDomain == "" {
		return nil, fmt.Errorf("DNS baseDomain is required")
	}

	ingressDNS := ingressDNSName(hcp)
	log.Info("Reconciling ingress DNS", "ingressDNS", ingressDNS)

	result := &ingressDNSResult{
		IngressDNS:    ingressDNS,
		PublicZoneID:  existingPublicZoneID,
		PrivateZoneID: existingPrivateZoneID,
	}

	if existingPublicZoneID == "" {
		publicCallerRef := callerReference(hcp, ingressDNS+"-public")
		zoneID, ns, err := createOrGetPublicHostedZone(ctx, route53Client, ingressDNS, publicCallerRef)
		if err != nil {
			return nil, fmt.Errorf("failed to create public ingress zone: %w", err)
		}
		result.PublicZoneID = zoneID
		result.NSRecords = ns
	} else {
		getOutput, err := route53Client.GetHostedZone(ctx, &route53.GetHostedZoneInput{
			Id: aws.String(existingPublicZoneID),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to get existing public zone %s: %w", existingPublicZoneID, err)
		}
		if getOutput.DelegationSet != nil {
			result.NSRecords = getOutput.DelegationSet.NameServers
		}
	}

	if existingPrivateZoneID == "" {
		awsSpec := hcp.Spec.Platform.AWS
		vpcID := ""
		if awsSpec.CloudProviderConfig != nil {
			vpcID = awsSpec.CloudProviderConfig.VPC
		}
		if vpcID == "" {
			return nil, fmt.Errorf("VPC ID is required for private ingress zone creation")
		}
		privateCallerRef := callerReference(hcp, ingressDNS+"-private")
		zoneID, err := createOrGetPrivateHostedZone(ctx, route53Client, ingressDNS, privateCallerRef, vpcID, awsSpec.Region)
		if err != nil {
			return nil, fmt.Errorf("failed to create private ingress zone: %w", err)
		}
		result.PrivateZoneID = zoneID
	}

	if err := createACMEChallengeRecord(ctx, route53Client, result.PublicZoneID, ingressDNS, hcp.Spec.DNS.BaseDomain); err != nil {
		return nil, fmt.Errorf("failed to create ACME challenge record: %w", err)
	}

	log.Info("Ingress DNS reconciliation complete",
		"publicZoneID", result.PublicZoneID,
		"privateZoneID", result.PrivateZoneID,
		"nsRecords", result.NSRecords)

	return result, nil
}

func cleanupIngressDNSZone(ctx context.Context, client awsapi.ROUTE53API, zoneID string) error {
	log := ctrl.LoggerFrom(ctx)

	var changes []route53types.Change
	input := &route53.ListResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
	}
	for {
		resp, err := client.ListResourceRecordSets(ctx, input)
		if err != nil {
			var noSuchZone *route53types.NoSuchHostedZone
			if errors.As(err, &noSuchZone) {
				log.Info("Zone already deleted", "zoneID", zoneID)
				return nil
			}
			return fmt.Errorf("failed to list records in zone %s: %w", zoneID, err)
		}
		for _, record := range resp.ResourceRecordSets {
			if record.Type == route53types.RRTypeSoa || record.Type == route53types.RRTypeNs {
				continue
			}
			changes = append(changes, route53types.Change{
				Action:            route53types.ChangeActionDelete,
				ResourceRecordSet: &record,
			})
		}
		if !resp.IsTruncated {
			break
		}
		input.StartRecordName = resp.NextRecordName
		input.StartRecordType = resp.NextRecordType
		input.StartRecordIdentifier = resp.NextRecordIdentifier
	}

	if len(changes) > 0 {
		_, err := client.ChangeResourceRecordSets(ctx, &route53.ChangeResourceRecordSetsInput{
			HostedZoneId: aws.String(zoneID),
			ChangeBatch:  &route53types.ChangeBatch{Changes: changes},
		})
		if err != nil {
			return fmt.Errorf("failed to delete records in zone %s: %w", zoneID, err)
		}
		log.Info("Deleted custom records from zone", "zoneID", zoneID, "count", len(changes))
	}

	_, err := client.DeleteHostedZone(ctx, &route53.DeleteHostedZoneInput{
		Id: aws.String(zoneID),
	})
	if err != nil {
		var noSuchZone *route53types.NoSuchHostedZone
		if errors.As(err, &noSuchZone) {
			return nil
		}
		return fmt.Errorf("failed to delete zone %s: %w", zoneID, err)
	}

	log.Info("Deleted hosted zone", "zoneID", zoneID)
	return nil
}

func cleanupIngressDNS(ctx context.Context, client awsapi.ROUTE53API, publicZoneID, privateZoneID string) error {
	if publicZoneID != "" {
		if err := cleanupIngressDNSZone(ctx, client, publicZoneID); err != nil {
			return fmt.Errorf("failed to cleanup public ingress zone: %w", err)
		}
	}
	if privateZoneID != "" {
		if err := cleanupIngressDNSZone(ctx, client, privateZoneID); err != nil {
			return fmt.Errorf("failed to cleanup private ingress zone: %w", err)
		}
	}
	return nil
}
