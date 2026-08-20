package awsprivatelink

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/openshift/hypershift/support/awsapi"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	smithy "github.com/aws/smithy-go"

	ctrl "sigs.k8s.io/controller-runtime"
)

func lookupZoneID(ctx context.Context, client awsapi.ROUTE53API, name string) (string, error) {
	log := ctrl.LoggerFrom(ctx)
	var res *route53types.HostedZone
	f := func(resp *route53.ListHostedZonesOutput, lastPage bool) (shouldContinue bool) {
		for idx, zone := range resp.HostedZones {
			if zone.Config != nil && zone.Config.PrivateZone && strings.TrimSuffix(aws.ToString(zone.Name), ".") == strings.TrimSuffix(name, ".") {
				res = &resp.HostedZones[idx]
				return false
			}
		}
		return !lastPage
	}
	paginator := route53.NewListHostedZonesPaginator(client, &route53.ListHostedZonesInput{})
	for paginator.HasMorePages() {
		resp, err := paginator.NextPage(ctx)
		if err != nil {
			log.Error(err, "failed to list hosted zones")
			return "", err
		}
		if !f(resp, !paginator.HasMorePages()) {
			break
		}
	}
	if res == nil {
		return "", fmt.Errorf("hosted zone %s not found", name)
	}
	return cleanZoneID(aws.ToString(res.Id)), nil
}

func CreateRecord(ctx context.Context, client awsapi.ROUTE53API, zoneID, name, value string, recordType route53types.RRType) error {
	log := ctrl.LoggerFrom(ctx)
	record := &route53types.ResourceRecordSet{
		Name: aws.String(name),
		Type: recordType,
		TTL:  aws.Int64(300),
		ResourceRecords: []route53types.ResourceRecord{
			{
				Value: aws.String(value),
			},
		},
	}

	changeBatch := &route53types.ChangeBatch{
		Changes: []route53types.Change{
			{
				Action:            route53types.ChangeActionUpsert,
				ResourceRecordSet: record,
			},
		},
	}

	input := &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
		ChangeBatch:  changeBatch,
	}

	_, err := client.ChangeResourceRecordSets(ctx, input)
	if err != nil {
		log.Error(err, "failed to create records in hosted zone", "zone", zoneID)
	}
	return err
}

func cleanZoneID(ID string) string {
	return strings.TrimPrefix(ID, "/hostedzone/")
}

func cleanRecordName(name string) string {
	str := name
	s, err := strconv.Unquote(`"` + str + `"`)
	if err != nil {
		return str
	}
	return s
}

func fqdn(name string) string {
	n := len(name)
	if n == 0 || name[n-1] == '.' {
		return name
	} else {
		return name + "."
	}
}

func FindRecord(ctx context.Context, client awsapi.ROUTE53API, id, name string, recordType route53types.RRType) (*route53types.ResourceRecordSet, error) {
	recordName := fqdn(strings.ToLower(name))
	input := &route53.ListResourceRecordSetsInput{
		HostedZoneId:    aws.String(id),
		StartRecordName: aws.String(recordName),
		StartRecordType: recordType,
		MaxItems:        aws.Int32(1),
	}

	resp, err := client.ListResourceRecordSets(ctx, input)
	if err != nil {
		return nil, err
	}
	if len(resp.ResourceRecordSets) == 0 {
		return nil, nil
	}

	recordSet := resp.ResourceRecordSets[0]
	responseName := strings.ToLower(cleanRecordName(aws.ToString(recordSet.Name)))

	if recordName != responseName {
		return nil, nil
	}
	if recordType != recordSet.Type {
		return nil, nil
	}

	return &recordSet, nil
}

func DeleteRecord(ctx context.Context, client awsapi.ROUTE53API, id string, record *route53types.ResourceRecordSet) error {
	changeBatch := &route53types.ChangeBatch{
		Changes: []route53types.Change{
			{
				Action:            route53types.ChangeActionDelete,
				ResourceRecordSet: record,
			},
		},
	}

	input := &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(id),
		ChangeBatch:  changeBatch,
	}

	_, err := client.ChangeResourceRecordSets(ctx, input)
	return err
}

func isAWSNotFoundError(err error) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode() == "NoSuchHostedZone" || apiErr.ErrorCode() == "HostedZoneNotFound"
	}
	return false
}

func isAWSConflictError(err error) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode() == "HostedZoneAlreadyExists" || apiErr.ErrorCode() == "ConflictingDomainExists"
	}
	return false
}

// CreatePrivateHostedZone creates a Route53 private hosted zone associated with the given VPC.
// It uses a get-first-create-if-not-exists pattern for idempotency.
// Returns the zone ID.
func CreatePrivateHostedZone(ctx context.Context, client awsapi.ROUTE53API, zoneName, vpcID, region string) (string, error) {
	log := ctrl.LoggerFrom(ctx)

	zoneID, err := lookupZoneID(ctx, client, zoneName)
	if err == nil {
		log.Info("Private hosted zone already exists", "zone", zoneName, "zoneID", zoneID)
		return zoneID, nil
	}

	log.Info("Creating private hosted zone", "zone", zoneName, "vpc", vpcID)
	output, err := client.CreateHostedZone(ctx, &route53.CreateHostedZoneInput{
		Name:            aws.String(fqdn(zoneName)),
		CallerReference: aws.String(fmt.Sprintf("%s-%d", zoneName, time.Now().UnixNano())),
		HostedZoneConfig: &route53types.HostedZoneConfig{
			PrivateZone: true,
			Comment:     aws.String("Managed by HyperShift control-plane-operator"),
		},
		VPC: &route53types.VPC{
			VPCId:     aws.String(vpcID),
			VPCRegion: route53types.VPCRegion(region),
		},
	})
	if err != nil {
		if isAWSConflictError(err) {
			zoneID, lookupErr := lookupZoneID(ctx, client, zoneName)
			if lookupErr != nil {
				return "", fmt.Errorf("zone conflict but lookup failed: %w", lookupErr)
			}
			log.Info("Private hosted zone already exists (conflict)", "zone", zoneName, "zoneID", zoneID)
			return zoneID, nil
		}
		return "", fmt.Errorf("failed to create private hosted zone %s: %w", zoneName, err)
	}

	zoneID = cleanZoneID(aws.ToString(output.HostedZone.Id))
	log.Info("Created private hosted zone", "zone", zoneName, "zoneID", zoneID)
	return zoneID, nil
}

// CreatePublicHostedZone creates a Route53 public hosted zone.
// Returns the zone ID and the delegation set name servers.
func CreatePublicHostedZone(ctx context.Context, client awsapi.ROUTE53API, zoneName string) (string, []string, error) {
	log := ctrl.LoggerFrom(ctx)

	zoneID, err := lookupPublicZoneID(ctx, client, zoneName)
	if err == nil {
		output, getErr := client.GetHostedZone(ctx, &route53.GetHostedZoneInput{
			Id: aws.String(zoneID),
		})
		if getErr != nil {
			return "", nil, fmt.Errorf("failed to get existing public zone %s: %w", zoneID, getErr)
		}
		var nameServers []string
		if output.DelegationSet != nil {
			nameServers = output.DelegationSet.NameServers
		}
		log.Info("Public hosted zone already exists", "zone", zoneName, "zoneID", zoneID)
		return zoneID, nameServers, nil
	}

	log.Info("Creating public hosted zone", "zone", zoneName)
	output, err := client.CreateHostedZone(ctx, &route53.CreateHostedZoneInput{
		Name:            aws.String(fqdn(zoneName)),
		CallerReference: aws.String(fmt.Sprintf("%s-%d", zoneName, time.Now().UnixNano())),
		HostedZoneConfig: &route53types.HostedZoneConfig{
			Comment: aws.String("Managed by HyperShift control-plane-operator"),
		},
	})
	if err != nil {
		if isAWSConflictError(err) {
			zoneID, lookupErr := lookupPublicZoneID(ctx, client, zoneName)
			if lookupErr != nil {
				return "", nil, fmt.Errorf("zone conflict but lookup failed: %w", lookupErr)
			}
			getOutput, getErr := client.GetHostedZone(ctx, &route53.GetHostedZoneInput{
				Id: aws.String(zoneID),
			})
			if getErr != nil {
				return "", nil, fmt.Errorf("failed to get conflicting zone %s: %w", zoneID, getErr)
			}
			var nameServers []string
			if getOutput.DelegationSet != nil {
				nameServers = getOutput.DelegationSet.NameServers
			}
			log.Info("Public hosted zone already exists (conflict)", "zone", zoneName, "zoneID", zoneID)
			return zoneID, nameServers, nil
		}
		return "", nil, fmt.Errorf("failed to create public hosted zone %s: %w", zoneName, err)
	}

	zoneID = cleanZoneID(aws.ToString(output.HostedZone.Id))
	var nameServers []string
	if output.DelegationSet != nil {
		nameServers = output.DelegationSet.NameServers
	}
	log.Info("Created public hosted zone", "zone", zoneName, "zoneID", zoneID, "nameServers", nameServers)
	return zoneID, nameServers, nil
}

func lookupPublicZoneID(ctx context.Context, client awsapi.ROUTE53API, name string) (string, error) {
	var res *route53types.HostedZone
	paginator := route53.NewListHostedZonesPaginator(client, &route53.ListHostedZonesInput{})
	for paginator.HasMorePages() {
		resp, err := paginator.NextPage(ctx)
		if err != nil {
			return "", err
		}
		for idx, zone := range resp.HostedZones {
			isPrivate := zone.Config != nil && zone.Config.PrivateZone
			if !isPrivate && strings.TrimSuffix(aws.ToString(zone.Name), ".") == strings.TrimSuffix(name, ".") {
				res = &resp.HostedZones[idx]
				break
			}
		}
		if res != nil {
			break
		}
	}
	if res == nil {
		return "", fmt.Errorf("public hosted zone %s not found", name)
	}
	return cleanZoneID(aws.ToString(res.Id)), nil
}

// DeleteHostedZoneWithRecords drains all non-SOA/NS records from a zone and then deletes it.
func DeleteHostedZoneWithRecords(ctx context.Context, client awsapi.ROUTE53API, zoneID string) error {
	log := ctrl.LoggerFrom(ctx)

	if err := deleteAllCustomRecords(ctx, client, zoneID); err != nil {
		return fmt.Errorf("failed to drain records from zone %s: %w", zoneID, err)
	}

	log.Info("Deleting hosted zone", "zoneID", zoneID)
	_, err := client.DeleteHostedZone(ctx, &route53.DeleteHostedZoneInput{
		Id: aws.String(zoneID),
	})
	if err != nil {
		if isAWSNotFoundError(err) {
			log.Info("Hosted zone already deleted", "zoneID", zoneID)
			return nil
		}
		return fmt.Errorf("failed to delete hosted zone %s: %w", zoneID, err)
	}
	log.Info("Deleted hosted zone", "zoneID", zoneID)
	return nil
}

func deleteAllCustomRecords(ctx context.Context, client awsapi.ROUTE53API, zoneID string) error {
	log := ctrl.LoggerFrom(ctx)
	paginator := route53.NewListResourceRecordSetsPaginator(client, &route53.ListResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
	})

	var changes []route53types.Change
	for paginator.HasMorePages() {
		resp, err := paginator.NextPage(ctx)
		if err != nil {
			if isAWSNotFoundError(err) {
				return nil
			}
			return err
		}
		for _, rs := range resp.ResourceRecordSets {
			if rs.Type == route53types.RRTypeSoa || rs.Type == route53types.RRTypeNs {
				continue
			}
			changes = append(changes, route53types.Change{
				Action:            route53types.ChangeActionDelete,
				ResourceRecordSet: &rs,
			})
		}
	}

	if len(changes) == 0 {
		return nil
	}

	log.Info("Deleting custom records from zone", "zoneID", zoneID, "count", len(changes))
	_, err := client.ChangeResourceRecordSets(ctx, &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
		ChangeBatch:  &route53types.ChangeBatch{Changes: changes},
	})
	return err
}

