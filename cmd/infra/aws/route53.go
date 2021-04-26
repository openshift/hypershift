package aws

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/route53"
	"github.com/aws/aws-sdk-go/service/route53/route53iface"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
)

func (o *CreateInfraOptions) LookupPublicZone(ctx context.Context, client route53iface.Route53API) (string, error) {
	name := o.BaseDomain
	id, err := lookupZone(ctx, client, name, false)
	if err != nil {
		log.Error(err, "Public zone not found", "name", name)
		return "", err
	}
	log.Info("Found existing public zone", "name", name, "id", id)
	return id, nil
}

func lookupZone(ctx context.Context, client route53iface.Route53API, name string, isPrivateZone bool) (string, error) {
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

func (o *CreateInfraOptions) CreatePrivateZone(ctx context.Context, client route53iface.Route53API, vpcID string) (string, error) {
	name := fmt.Sprintf("%s.%s", o.Name, o.BaseDomain)
	id, err := lookupZone(ctx, client, name, true)
	if err == nil {
		log.Info("Found existing private zone", "name", name, "id", id)
		return id, err
	}

	var res *route53.CreateHostedZoneOutput
	if err := retryRoute53WithBackoff(ctx, func() error {
		callRef := fmt.Sprintf("%d", time.Now().Unix())
		if output, err := client.CreateHostedZoneWithContext(ctx, &route53.CreateHostedZoneInput{
			CallerReference: aws.String(callRef),
			Name:            aws.String(name),
			HostedZoneConfig: &route53.HostedZoneConfig{
				PrivateZone: aws.Bool(true),
			},
			VPC: &route53.VPC{
				VPCId:     aws.String(vpcID),
				VPCRegion: aws.String(o.Region),
			},
		}); err != nil {
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
	log.Info("Created private zone", "name", name, "id", id)
	return id, nil
}

func (o *DestroyInfraOptions) DestroyDNS(ctx context.Context, client route53iface.Route53API) []error {
	var errs []error
	errs = append(errs, o.DestroyPrivateZone(ctx, client))
	errs = append(errs, o.CleanupPublicZone(ctx, client))
	return errs
}

func (o *DestroyInfraOptions) DestroyPrivateZone(ctx context.Context, client route53iface.Route53API) error {
	name := fmt.Sprintf("%s.%s", o.Name, o.BaseDomain)
	id, err := lookupZone(ctx, client, name, true)
	if err != nil {
		return nil
	}
	recordName := o.wildcardRecordName()
	err = deleteRecord(ctx, client, id, recordName)
	if err == nil {
		log.Info("Deleted wildcard record from private zone", "id", id, "name", recordName)
	}
	_, err = client.DeleteHostedZoneWithContext(ctx, &route53.DeleteHostedZoneInput{
		Id: aws.String(id),
	})
	if err != nil {
		return err
	}
	log.Info("Deleted private zone", "id", id, "name", name)
	return nil
}

func (o *DestroyInfraOptions) CleanupPublicZone(ctx context.Context, client route53iface.Route53API) error {
	name := o.BaseDomain
	id, err := lookupZone(ctx, client, name, false)
	if err != nil {
		return nil
	}
	recordName := o.wildcardRecordName()
	err = deleteRecord(ctx, client, id, o.wildcardRecordName())
	if err == nil {
		log.Info("Deleted wildcard record from public zone", "id", id, "name", recordName)
	}
	return nil
}

func (o *DestroyInfraOptions) wildcardRecordName() string {
	return fmt.Sprintf("*.apps.%s.%s", o.Name, o.BaseDomain)
}

func deleteRecord(ctx context.Context, client route53iface.Route53API, id, recordName string) error {
	record, err := findRecord(ctx, client, id, recordName)
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

func findRecord(ctx context.Context, client route53iface.Route53API, id, name string) (*route53.ResourceRecordSet, error) {
	recordName := fqdn(strings.ToLower(name))
	recordType := "A"
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
	retriable := func(error) bool {
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
