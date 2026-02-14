package awsprivatelink

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/openshift/hypershift/support/awsapi"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	"github.com/aws/smithy-go"

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
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			log.Error(err, "failed to create records in hosted zone", "zone", zoneID)
			return fmt.Errorf("%s", apiErr.ErrorCode())
		}
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
