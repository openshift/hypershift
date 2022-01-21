package awsprivatelink

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/route53"
	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	"github.com/aws/aws-sdk-go/aws"
)

func lookupZoneID(ctx context.Context, client *route53.Client, name string) (string, error) {
	for paginator := route53.NewListHostedZonesPaginator(client, &route53.ListHostedZonesInput{}); paginator.HasMorePages(); {
		response, err := paginator.NextPage(ctx)
		if err != nil {
			return "", err
		}
		for _, zone := range response.HostedZones {
			if zone.Config != nil && zone.Config.PrivateZone && strings.TrimSuffix(aws.StringValue(zone.Name), ".") == strings.TrimSuffix(name, ".") {
				return cleanZoneID(*zone.Id), nil
			}
		}
	}
	return "", fmt.Errorf("hosted zone %s not found", name)
}

func createRecord(ctx context.Context, client *route53.Client, zoneID, name, value string) error {
	record := &route53types.ResourceRecordSet{
		Name: aws.String(name),
		Type: route53types.RRTypeCname,
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

func findRecord(ctx context.Context, client *route53.Client, id, name string) (*route53types.ResourceRecordSet, error) {
	recordName := fqdn(strings.ToLower(name))
	input := &route53.ListResourceRecordSetsInput{
		HostedZoneId:    aws.String(id),
		StartRecordName: aws.String(recordName),
		StartRecordType: route53types.RRTypeCname,
		MaxItems:        aws.Int32(1),
	}
	resp, err := client.ListResourceRecordSets(ctx, input)
	if err != nil {
		return nil, err
	}

	if len(resp.ResourceRecordSets) == 0 {
		return nil, fmt.Errorf("no record named %s found", recordName)
	}

	recordSet := resp.ResourceRecordSets[0]
	responseName := strings.ToLower(cleanRecordName(*recordSet.Name))
	responseType := strings.ToUpper(string(recordSet.Type))

	if recordName != responseName || string(route53types.RRTypeCname) != responseType {
		return nil, fmt.Errorf("no record named %s found", recordName)
	}

	return &recordSet, nil
}

func deleteRecord(ctx context.Context, client *route53.Client, id string, record *route53types.ResourceRecordSet) error {
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
