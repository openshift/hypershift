package awsprivatelink

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/route53"
	"github.com/aws/aws-sdk-go/service/route53/route53iface"
	ctrl "sigs.k8s.io/controller-runtime"
)

func lookupZoneID(ctx context.Context, client route53iface.Route53API, name string) (string, error) {
	log := ctrl.LoggerFrom(ctx)
	var res *route53.HostedZone
	f := func(resp *route53.ListHostedZonesOutput, lastPage bool) (shouldContinue bool) {
		for idx, zone := range resp.HostedZones {
			if zone.Config != nil && aws.BoolValue(zone.Config.PrivateZone) && strings.TrimSuffix(aws.StringValue(zone.Name), ".") == strings.TrimSuffix(name, ".") {
				res = resp.HostedZones[idx]
				return false
			}
		}
		return !lastPage
	}
	if err := client.ListHostedZonesPagesWithContext(ctx, &route53.ListHostedZonesInput{}, f); err != nil {
		log.Error(err, "failed to list hosted zones")
		return "", err
	}
	if res == nil {
		return "", fmt.Errorf("hosted zone %s not found", name)
	}
	return cleanZoneID(*res.Id), nil
}

func CreateRecord(ctx context.Context, client route53iface.Route53API, zoneID, name, value, recordType string) error {
	log := ctrl.LoggerFrom(ctx)
	record := &route53.ResourceRecordSet{
		Name: aws.String(name),
		Type: aws.String(recordType),
		TTL:  aws.Int64(300),
		ResourceRecords: []*route53.ResourceRecord{
			{
				Value: aws.String(value),
			},
		},
	}

	changeBatch := &route53.ChangeBatch{
		Changes: []*route53.Change{
			{
				Action:            aws.String("UPSERT"),
				ResourceRecordSet: record,
			},
		},
	}

	input := &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
		ChangeBatch:  changeBatch,
	}

	_, err := client.ChangeResourceRecordSetsWithContext(ctx, input)
	if awsErr, ok := err.(awserr.Error); ok {
		log.Error(err, "failed to create records in hosted zone", "zone", zoneID)
		return errors.New(awsErr.Code())
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

func FindRecord(ctx context.Context, client route53iface.Route53API, id, name, recordType string) (*route53.ResourceRecordSet, error) {
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
	return record, nil
}

func DeleteRecord(ctx context.Context, client route53iface.Route53API, id string, record *route53.ResourceRecordSet) error {
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

	_, err := client.ChangeResourceRecordSetsWithContext(ctx, input)
	return err
}
