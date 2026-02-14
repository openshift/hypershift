package aws

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/openshift/hypershift/support/awsapi"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"

	"github.com/go-logr/logr"
)

func (o *CreateInfraOptions) LookupPublicZone(ctx context.Context, logger logr.Logger, client awsapi.ROUTE53API) (string, error) {
	name := o.BaseDomain
	id, err := LookupZone(ctx, client, name, false)
	if err != nil {
		// redact base domain if requested
		if o.RedactBaseDomain {
			logger.Error(err, "Public zone not found", "name", "[REDACTED]")
		} else {
			logger.Error(err, "Public zone not found", "name", name)
		}
		return "", err
	}
	if o.RedactBaseDomain {
		logger.Info("Found existing public zone", "name", "[REDACTED]", "id", id)
	} else {
		logger.Info("Found existing public zone", "name", name, "id", id)
	}
	return id, nil
}

func LookupZone(ctx context.Context, client awsapi.ROUTE53API, name string, isPrivateZone bool) (string, error) {
	var res *route53types.HostedZone
	f := func(resp *route53.ListHostedZonesOutput, lastPage bool) (shouldContinue bool) {
		for idx, zone := range resp.HostedZones {
			if zone.Config != nil && isPrivateZone == zone.Config.PrivateZone && strings.TrimSuffix(aws.ToString(zone.Name), ".") == strings.TrimSuffix(name, ".") {
				res = &resp.HostedZones[idx]
				return false
			}
		}
		return !lastPage
	}
	if err := retryRoute53WithBackoff(ctx, func() error {
		paginator := route53.NewListHostedZonesPaginator(client, &route53.ListHostedZonesInput{})
		for paginator.HasMorePages() {
			resp, err := paginator.NextPage(ctx)
			if err != nil {
				return err
			}
			if !f(resp, !paginator.HasMorePages()) {
				break
			}
		}
		return nil
	}); err != nil {
		return "", fmt.Errorf("failed to list hosted zones: %w", err)
	}
	if res == nil {
		return "", fmt.Errorf("hosted zone %s not found", name)
	}
	return cleanZoneID(aws.ToString(res.Id)), nil
}

func (o *CreateInfraOptions) CreatePrivateZone(ctx context.Context, logger logr.Logger, client awsapi.ROUTE53API, name, vpcID string, authorizeAssociation bool, vpcOwnerClient awsapi.ROUTE53API, initialVPC string) (string, error) {
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
			HostedZoneConfig: &route53types.HostedZoneConfig{
				PrivateZone: true,
			},
			VPC: &route53types.VPC{
				VPCId:     aws.String(vpcID),
				VPCRegion: route53types.VPCRegion(o.Region),
			},
		}
		if authorizeAssociation {
			createRequest.VPC.VPCId = aws.String(initialVPC)
		}
		if output, err := client.CreateHostedZone(ctx, createRequest); err != nil {
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
	id = cleanZoneID(aws.ToString(res.HostedZone.Id))
	logger.Info("Created private zone", "name", name, "id", id)

	err = setSOAMinimum(ctx, client, id, name)
	if err != nil {
		return "", err
	}

	if authorizeAssociation {
		if _, err := client.CreateVPCAssociationAuthorization(ctx, &route53.CreateVPCAssociationAuthorizationInput{
			HostedZoneId: aws.String(id),
			VPC: &route53types.VPC{
				VPCId:     aws.String(vpcID),
				VPCRegion: route53types.VPCRegion(o.Region),
			},
		}); err != nil {
			return "", fmt.Errorf("failed to create vpc association authorization: %w", err)
		}
		logger.Info("Created hosted zone vpc association authorization", "id", id, "vpc", vpcID)

		if _, err := vpcOwnerClient.AssociateVPCWithHostedZone(ctx, &route53.AssociateVPCWithHostedZoneInput{
			HostedZoneId: aws.String(id),
			VPC: &route53types.VPC{
				VPCId:     aws.String(vpcID),
				VPCRegion: route53types.VPCRegion(o.Region),
			},
		}); err != nil {
			return "", fmt.Errorf("failed to associate VPC with hosted zone: %w", err)
		}
		logger.Info("Associated VPC with hosted zone", "vpc", vpcID, "hosted-zone", id)

		if _, err := client.DisassociateVPCFromHostedZone(ctx, &route53.DisassociateVPCFromHostedZoneInput{
			HostedZoneId: aws.String(id),
			VPC: &route53types.VPC{
				VPCId:     aws.String(initialVPC),
				VPCRegion: route53types.VPCRegion(o.Region),
			},
		}); err != nil {
			return "", fmt.Errorf("failed to remove initial VPC association with hosted zone: %w", err)
		}
		logger.Info("Removed initial VPC association with hosted zone", "vpc", initialVPC, "hosted-zone", id)
	}

	return id, nil
}

func (o *DestroyInfraOptions) DestroyDNS(ctx context.Context, client awsapi.ROUTE53API) []error {
	var errs []error
	errs = append(errs, o.CleanupPublicZone(ctx, client))
	return errs
}

func (o *DestroyInfraOptions) DestroyPrivateZones(ctx context.Context, listClient, recordsClient awsapi.ROUTE53API, vpcID *string) []error {
	var output *route53.ListHostedZonesByVPCOutput
	if err := retryRoute53WithBackoff(ctx, func() (err error) {
		output, err = listClient.ListHostedZonesByVPC(ctx, &route53.ListHostedZonesByVPCInput{VPCId: vpcID, VPCRegion: route53types.VPCRegion(o.Region)})
		return err
	}); err != nil {
		return []error{fmt.Errorf("failed to list hosted zones for vpc %s: %w", *vpcID, err)}
	}

	var errs []error
	for _, zone := range output.HostedZoneSummaries {
		id := cleanZoneID(aws.ToString(zone.HostedZoneId))
		if err := deleteZone(ctx, id, recordsClient, o.Log); err != nil {
			return []error{fmt.Errorf("failed to delete private hosted zones for vpc %s: %w", *vpcID, err)}
		}
		o.Log.Info("Deleted private hosted zone", "id", id, "name", aws.ToString(zone.Name))
	}

	return errs
}

func (o *DestroyInfraOptions) CleanupPublicZone(ctx context.Context, client awsapi.ROUTE53API) error {
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
		if o.RedactBaseDomain {
			o.Log.Info("Deleted wildcard record from public hosted zone", "id", id, "name", fmt.Sprintf("*.apps.%s.[REDACTED]", o.Name))
		} else {
			o.Log.Info("Deleted wildcard record from public hosted zone", "id", id, "name", recordName)
		}
	}
	return nil
}

func setSOAMinimum(ctx context.Context, client awsapi.ROUTE53API, id, name string) error {
	recordSet, err := findRecord(ctx, client, id, name, route53types.RRTypeSoa)
	if err != nil {
		return err
	}
	if recordSet == nil || len(recordSet.ResourceRecords) == 0 || recordSet.ResourceRecords[0].Value == nil {
		return fmt.Errorf("SOA record for private zone %s not found: %w", name, err)
	}
	record := &recordSet.ResourceRecords[0]
	fields := strings.Split(aws.ToString(record.Value), " ")
	if len(fields) != 7 {
		return fmt.Errorf("SOA record value has %d fields, expected 7", len(fields))
	}
	fields[6] = "60"
	record.Value = aws.String(strings.Join(fields, " "))
	input := &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(id),
		ChangeBatch: &route53types.ChangeBatch{
			Changes: []route53types.Change{
				{
					Action:            route53types.ChangeActionUpsert,
					ResourceRecordSet: recordSet,
				},
			},
		},
	}
	_, err = client.ChangeResourceRecordSets(ctx, input)
	return err
}

func deleteZone(ctx context.Context, id string, client awsapi.ROUTE53API, logger logr.Logger) error {
	err := deleteRecords(ctx, client, id, logger)
	if err != nil {
		return fmt.Errorf("failed to delete hosted zone records: %v", err)
	}
	if _, err = client.DeleteHostedZone(ctx, &route53.DeleteHostedZoneInput{
		Id: aws.String(id),
	}); err != nil {
		return fmt.Errorf("failed to delete hosted zone: %v", err)
	}
	return nil
}

func deleteRecords(ctx context.Context, client awsapi.ROUTE53API, id string, logger logr.Logger) error {
	lrrsi := &route53.ListResourceRecordSetsInput{
		HostedZoneId: aws.String(id),
	}
	output, err := client.ListResourceRecordSets(ctx, lrrsi)
	if err != nil {
		return err
	}
	if len(output.ResourceRecordSets) == 0 {
		return nil
	}
	var changes []route53types.Change
	var deleteRequired bool
	for i := range output.ResourceRecordSets {
		rrs := &output.ResourceRecordSets[i]
		if rrs.Type == route53types.RRTypeNs || rrs.Type == route53types.RRTypeSoa {
			continue
		}
		deleteRequired = true
		changes = append(changes, route53types.Change{
			Action:            route53types.ChangeActionDelete,
			ResourceRecordSet: rrs,
		})
	}
	if !deleteRequired {
		return nil
	}
	crrsi := &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(id),
		ChangeBatch: &route53types.ChangeBatch{
			Changes: changes,
		},
	}

	if _, err := client.ChangeResourceRecordSets(ctx, crrsi); err != nil {
		return err
	}

	var deletedRecordNames []string
	for _, change := range changes {
		deletedRecordNames = append(deletedRecordNames, aws.ToString(change.ResourceRecordSet.Name))
	}
	logger.Info("Deleted records from private hosted zone", "id", id, "names", deletedRecordNames)
	return nil
}

func deleteRecord(ctx context.Context, client awsapi.ROUTE53API, id, recordName string) error {
	record, err := findRecord(ctx, client, id, recordName, route53types.RRTypeA)
	if err != nil {
		return err
	}

	// Change batch for deleting
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

	_, err = client.ChangeResourceRecordSets(ctx, input)
	return err
}

func findRecord(ctx context.Context, client awsapi.ROUTE53API, id, name string, recordType route53types.RRType) (*route53types.ResourceRecordSet, error) {
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
		return nil, fmt.Errorf("record not found")
	}

	recordSet := resp.ResourceRecordSets[0]
	responseName := strings.ToLower(cleanRecordName(aws.ToString(recordSet.Name)))

	if recordName != responseName {
		return nil, fmt.Errorf("record not found")
	}
	if recordType != recordSet.Type {
		return nil, fmt.Errorf("record not found")
	}
	return &recordSet, nil
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
