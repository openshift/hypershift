package aws

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/route53"
	"github.com/aws/aws-sdk-go/service/route53/route53iface"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"

	"github.com/go-logr/logr"
)

func (o *CreateInfraOptions) LookupPublicZone(ctx context.Context, logger logr.Logger, client route53iface.Route53API) (string, error) {
	name := o.BaseDomain
	id, err := LookupZone(ctx, client, name, false)
	if err != nil {
		logger.Error(err, "Public zone not found", "name", name)
		return "", err
	}
	logger.Info("Found existing public zone", "name", name, "id", id)
	return id, nil
}

func LookupZone(ctx context.Context, client route53iface.Route53API, name string, isPrivateZone bool) (string, error) {
	var res *route53.HostedZone
	f := func(resp *route53.ListHostedZonesOutput, lastPage bool) (shouldContinue bool) {
		for idx, zone := range resp.HostedZones {
			if zone.Config != nil && isPrivateZone == aws.BoolValue(zone.Config.PrivateZone) && strings.TrimSuffix(aws.StringValue(zone.Name), ".") == strings.TrimSuffix(name, ".") {
				res = resp.HostedZones[idx]
				return false
			}
		}
		return !lastPage
	}
	if err := retryRoute53WithBackoff(ctx, func() error {
		if err := client.ListHostedZonesPagesWithContext(ctx, &route53.ListHostedZonesInput{}, f); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return "", fmt.Errorf("failed to list hosted zones: %w", err)
	}
	if res == nil {
		return "", fmt.Errorf("hosted zone %s not found", name)
	}
	return cleanZoneID(*res.Id), nil
}

func (o *CreateInfraOptions) CreatePrivateZone(ctx context.Context, logger logr.Logger, client route53iface.Route53API, name, vpcID string, authorizeAssociation bool, vpcOwnerClient route53iface.Route53API, initialVPC string) (string, error) {
	id, err := LookupZone(ctx, client, name, true)
	if err == nil {
		logger.Info("Found existing private zone", "name", name, "id", id)
		err := setSOAMinimum(ctx, client, id, name)
		if err != nil {
			return "", err
		}
		return id, err
	}

	var res *route53.CreateHostedZoneOutput
	if err := retryRoute53WithBackoff(ctx, func() error {
		callRef := fmt.Sprintf("%d", time.Now().Unix())
		createRequest := &route53.CreateHostedZoneInput{
			CallerReference: aws.String(callRef),
			Name:            aws.String(name),
			HostedZoneConfig: &route53.HostedZoneConfig{
				PrivateZone: aws.Bool(true),
			},
			VPC: &route53.VPC{
				VPCId:     aws.String(vpcID),
				VPCRegion: aws.String(o.Region),
			},
		}
		if authorizeAssociation {
			createRequest.VPC.VPCId = aws.String(initialVPC)
		}
		if output, err := client.CreateHostedZoneWithContext(ctx, createRequest); err != nil {
			return err
		} else {
			res = output
			return nil
		}
	}); err != nil {
		return "", fmt.Errorf("failed to create hosted zone: %w", err)
	}
	if res == nil {
		return "", fmt.Errorf("unexpected output from hosted zone creation")
	}
	id = cleanZoneID(*res.HostedZone.Id)
	logger.Info("Created private zone", "name", name, "id", id)

	err = setSOAMinimum(ctx, client, id, name)
	if err != nil {
		return "", err
	}

	if authorizeAssociation {
		if _, err := client.CreateVPCAssociationAuthorization(&route53.CreateVPCAssociationAuthorizationInput{
			HostedZoneId: aws.String(id),
			VPC: &route53.VPC{
				VPCId:     aws.String(vpcID),
				VPCRegion: aws.String(o.Region),
			},
		}); err != nil {
			return "", fmt.Errorf("failed to create vpc association authorization: %w", err)
		}
		logger.Info("Created hosted zone vpc association authorization", "id", id, "vpc", vpcID)

		if _, err := vpcOwnerClient.AssociateVPCWithHostedZone(&route53.AssociateVPCWithHostedZoneInput{
			HostedZoneId: aws.String(id),
			VPC: &route53.VPC{
				VPCId:     aws.String(vpcID),
				VPCRegion: aws.String(o.Region),
			},
		}); err != nil {
			return "", fmt.Errorf("failed to associate VPC with hosted zone: %w", err)
		}
		logger.Info("Associated VPC with hosted zone", "vpc", vpcID, "hosted-zone", id)

		if _, err := client.DisassociateVPCFromHostedZone(&route53.DisassociateVPCFromHostedZoneInput{
			HostedZoneId: aws.String(id),
			VPC: &route53.VPC{
				VPCId:     aws.String(initialVPC),
				VPCRegion: aws.String(o.Region),
			},
		}); err != nil {
			return "", fmt.Errorf("failed to remove initial VPC association with hosted zone: %w", err)
		}
		logger.Info("Removed initial VPC association with hosted zone", "vpc", initialVPC, "hosted-zone", id)
	}

	return id, nil
}

func (o *DestroyInfraOptions) DestroyDNS(ctx context.Context, client route53iface.Route53API) []error {
	var errs []error
	errs = append(errs, o.CleanupPublicZone(ctx, client))
	return errs
}

func (o *DestroyInfraOptions) DestroyPrivateZones(ctx context.Context, listClient, recordsClient route53iface.Route53API, vpcID *string) []error {
	var output *route53.ListHostedZonesByVPCOutput
	if err := retryRoute53WithBackoff(ctx, func() (err error) {
		output, err = listClient.ListHostedZonesByVPCWithContext(ctx, &route53.ListHostedZonesByVPCInput{VPCId: vpcID, VPCRegion: aws.String(o.Region)})
		return err
	}); err != nil {
		return []error{fmt.Errorf("failed to list hosted zones for vpc %s: %w", *vpcID, err)}
	}

	var errs []error
	for _, zone := range output.HostedZoneSummaries {
		id := cleanZoneID(*zone.HostedZoneId)
		if err := deleteZone(ctx, id, recordsClient, o.Log); err != nil {
			return []error{fmt.Errorf("failed to delete private hosted zones for vpc %s: %w", *vpcID, err)}
		}
		o.Log.Info("Deleted private hosted zone", "id", id, "name", *zone.Name)
	}

	return errs
}

func (o *DestroyInfraOptions) CleanupPublicZone(ctx context.Context, client route53iface.Route53API) error {
	name := o.BaseDomain
	id, err := LookupZone(ctx, client, name, false)
	if err != nil {
		return nil
	}
	recordName := fmt.Sprintf("*.apps.%s.%s", o.Name, o.BaseDomain)
	err = deleteRecord(ctx, client, id, recordName)
	if err != nil {
		if !isRoute53RecordNotFoundErr(err) {
			return fmt.Errorf("failed to delete wildcard record from public zone %s: %w", id, err)
		}
	} else {
		o.Log.Info("Deleted wildcard record from public hosted zone", "id", id, "name", recordName)
	}
	return nil
}

func setSOAMinimum(ctx context.Context, client route53iface.Route53API, id, name string) error {
	recordSet, err := findRecord(ctx, client, id, name, "SOA")
	if err != nil {
		return err
	}
	if recordSet == nil || recordSet.ResourceRecords[0] == nil || recordSet.ResourceRecords[0].Value == nil {
		return fmt.Errorf("SOA record for private zone %s not found: %w", name, err)
	}
	record := recordSet.ResourceRecords[0]
	fields := strings.Split(*record.Value, " ")
	if len(fields) != 7 {
		return fmt.Errorf("SOA record value has %d fields, expected 7", len(fields))
	}
	fields[6] = "60"
	record.Value = aws.String(strings.Join(fields, " "))
	input := &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(id),
		ChangeBatch: &route53.ChangeBatch{
			Changes: []*route53.Change{
				{
					Action:            aws.String("UPSERT"),
					ResourceRecordSet: recordSet,
				},
			},
		},
	}
	_, err = client.ChangeResourceRecordSetsWithContext(ctx, input)
	return err
}

func deleteZone(ctx context.Context, id string, client route53iface.Route53API, logger logr.Logger) error {
	err := deleteRecords(ctx, client, id, logger)
	if err != nil {
		return fmt.Errorf("failed to delete hosted zone records: %v", err)
	}
	if _, err = client.DeleteHostedZoneWithContext(ctx, &route53.DeleteHostedZoneInput{
		Id: aws.String(id),
	}); err != nil {
		return fmt.Errorf("failed to delete hosted zone: %v", err)
	}
	return nil
}

func deleteRecords(ctx context.Context, client route53iface.Route53API, id string, logger logr.Logger) error {
	lrrsi := &route53.ListResourceRecordSetsInput{
		HostedZoneId: aws.String(id),
	}
	output, err := client.ListResourceRecordSetsWithContext(ctx, lrrsi)
	if err != nil {
		return err
	}
	if len(output.ResourceRecordSets) == 0 {
		return nil
	}
	var changeBatch route53.ChangeBatch
	var deleteRequired bool
	for _, rrs := range output.ResourceRecordSets {
		if *rrs.Type == "NS" || *rrs.Type == "SOA" {
			continue
		}
		deleteRequired = true
		changeBatch.Changes = append(changeBatch.Changes, &route53.Change{
			Action:            aws.String("DELETE"),
			ResourceRecordSet: rrs,
		})
	}
	if !deleteRequired {
		return nil
	}
	crrsi := &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(id),
		ChangeBatch:  &changeBatch,
	}

	if _, err := client.ChangeResourceRecordSetsWithContext(ctx, crrsi); err != nil {
		return err
	}

	var deletedRecordNames []string
	for _, changeBatch := range changeBatch.Changes {
		deletedRecordNames = append(deletedRecordNames, *changeBatch.ResourceRecordSet.Name)
	}
	logger.Info("Deleted records from private hosted zone", "id", id, "names", deletedRecordNames)
	return nil
}

func deleteRecord(ctx context.Context, client route53iface.Route53API, id, recordName string) error {
	record, err := findRecord(ctx, client, id, recordName, "A")
	if err != nil {
		return err
	}

	// Change batch for deleting
	changeBatch := &route53.ChangeBatch{
		Changes: []*route53.Change{
			{
				Action:            aws.String("DELETE"),
				ResourceRecordSet: record,
			},
		},
	}

	input := &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(id),
		ChangeBatch:  changeBatch,
	}

	_, err = client.ChangeResourceRecordSetsWithContext(ctx, input)
	return err
}

func findRecord(ctx context.Context, client route53iface.Route53API, id, name string, recordType string) (*route53.ResourceRecordSet, error) {
	recordName := fqdn(strings.ToLower(name))
	input := &route53.ListResourceRecordSetsInput{
		HostedZoneId:    aws.String(id),
		StartRecordName: aws.String(recordName),
		StartRecordType: aws.String(recordType),
		MaxItems:        aws.String("1"),
	}

	var record *route53.ResourceRecordSet
	err := client.ListResourceRecordSetsPagesWithContext(ctx, input, func(resp *route53.ListResourceRecordSetsOutput, lastPage bool) bool {
		if len(resp.ResourceRecordSets) == 0 {
			return false
		}

		recordSet := resp.ResourceRecordSets[0]
		responseName := strings.ToLower(cleanRecordName(*recordSet.Name))
		responseType := strings.ToUpper(*recordSet.Type)

		if recordName != responseName {
			return false
		}
		if recordType != responseType {
			return false
		}

		record = recordSet
		return false
	})

	if err != nil {
		return nil, err
	}
	if record == nil {
		return nil, fmt.Errorf("record not found")
	}
	return record, nil
}

func fqdn(name string) string {
	n := len(name)
	if n == 0 || name[n-1] == '.' {
		return name
	} else {
		return name + "."
	}
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

func retryRoute53WithBackoff(ctx context.Context, fn func() error) error {
	backoff := wait.Backoff{
		Duration: 1 * time.Second,
		Steps:    10,
		Factor:   1.5,
	}
	retriable := func(e error) bool {
		if !awsutil.IsErrorRetryable(e) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		default:
			return true
		}
	}
	// TODO: inspect the error for throttling details?
	return retry.OnError(backoff, retriable, fn)
}

func isRoute53RecordNotFoundErr(err error) bool {
	if err != nil && strings.Contains(err.Error(), "record not found") {
		return true
	}

	return false
}
