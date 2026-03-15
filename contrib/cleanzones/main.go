package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
)

const dryRun = false

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion("us-east-1"))
	if err != nil {
		log.Fatal(err)
	}
	route53client := route53.NewFromConfig(cfg)
	ec2client := ec2.NewFromConfig(cfg)

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT)
	go func() {
		<-sigs
		cancel()
	}()

	// List all VPCs
	output, err := ec2client.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{})
	if err != nil {
		log.Fatal(err)
	}

	// Get the list of infraIDs in use
	var infraIDsInUse []string
	for _, vpc := range output.Vpcs {
		// Get the VPC name out of the tags
		for _, tag := range vpc.Tags {
			if aws.ToString(tag.Key) == "Name" && len(strings.TrimSpace(aws.ToString(tag.Value))) > 0 {
				infraID := strings.TrimSuffix(aws.ToString(tag.Value), "-vpc")
				if len(infraID) > 0 {
					infraIDsInUse = append(infraIDsInUse, infraID)
				}
			}
		}
	}
	log.Println("infraIDs in use:", infraIDsInUse)

	examplePrivateZoneName := regexp.MustCompile("example-([a-z0-9]{5}).hypershift.local.")
	publicZones := map[string]string{
		"ci.hypershift.devcluster.openshift.com.":         "",
		"service.ci.hypershift.devcluster.openshift.com.": "",
	}

	// Delete unused private zones
	paginator := route53.NewListHostedZonesPaginator(route53client, &route53.ListHostedZonesInput{})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			log.Fatal(err)
		}
		for _, zone := range page.HostedZones {
			// Sanity check
			if zone.Name == nil || zone.Id == nil {
				continue
			}
			zoneName := aws.ToString(zone.Name)
			zoneId := strings.TrimPrefix(aws.ToString(zone.Id), "/hostedzone/")

			// Check if the zone is in the list of public zones
			// If it is, store the zoneId in publicZones value for the next step
			for pzoneName := range publicZones {
				if aws.ToString(zone.Name) == pzoneName {
					publicZones[pzoneName] = zoneId
				}
			}

			// Exclude public zones
			if zone.Config == nil || !zone.Config.PrivateZone {
				continue
			}

			// Exclude the CI public zones (redundant with the public zones check above for safety)
			// and private zones that are not example-<infraID>.hypershift.local.
			if !strings.HasSuffix(aws.ToString(zone.Name), ".ci.hypershift.devcluster.openshift.com.") &&
				!strings.HasSuffix(aws.ToString(zone.Name), ".service.ci.hypershift.devcluster.openshift.com.") &&
				!examplePrivateZoneName.MatchString(aws.ToString(zone.Name)) {
				continue
			}

			// Check if the zone is in use
			inUse := false
			for _, infraID := range infraIDsInUse {
				if strings.Contains(zoneName, infraID) {
					inUse = true
					break
				}
			}

			// If the zone is in use, skip it
			if inUse {
				continue
			}

			// The zone is not in use, delete it
			log.Printf("deleting hosted zone %s with id %s", zoneName, zoneId)
			deleteZone(ctx, zoneId, route53client)
		}
	}

	// Delete unused records from public zones
	for _, publicZoneId := range publicZones {
		// Sanity check
		if publicZoneId == "" {
			continue
		}
		lrrsi := &route53.ListResourceRecordSetsInput{
			HostedZoneId: aws.String(publicZoneId),
			MaxItems:     aws.Int32(100),
		}
		rrPaginator := route53.NewListResourceRecordSetsPaginator(route53client, lrrsi)
		for rrPaginator.HasMorePages() {
			page, err := rrPaginator.NextPage(ctx)
			if err != nil {
				log.Fatal(err)
			}
			var changes []route53types.Change
			var deleteRequired bool
			for _, rrs := range page.ResourceRecordSets {
				// Sanity exclusion checks
				if rrs.Type != route53types.RRTypeA && rrs.Type != route53types.RRTypeCname && rrs.Type != route53types.RRTypeTxt {
					continue
				}

				// Check if the record is in use
				inUseRecord := false
				for _, infraID := range infraIDsInUse {
					if strings.Contains(aws.ToString(rrs.Name), infraID) {
						inUseRecord = true
						break
					}
				}

				// If the record is in use, skip it
				if inUseRecord {
					continue
				}

				// The record is not in use, delete it
				deleteRequired = true
				log.Printf("deleting record %s", aws.ToString(rrs.Name))

				// Enqueue the record for deletion
				changes = append(changes, route53types.Change{
					Action:            route53types.ChangeActionDelete,
					ResourceRecordSet: &rrs,
				})
			}
			if deleteRequired {
				// At least one record from the current page needs to be deleted
				crrsi := &route53.ChangeResourceRecordSetsInput{
					HostedZoneId: aws.String(publicZoneId),
					ChangeBatch: &route53types.ChangeBatch{
						Changes: changes,
					},
				}

				if !dryRun {
					_, err = route53client.ChangeResourceRecordSets(ctx, crrsi)
					if err != nil {
						log.Fatal(err)
					}
				}
			}
		}
	}
}

func deleteZone(ctx context.Context, zoneId string, client *route53.Client) error {
	err := deleteRecords(ctx, client, zoneId)
	if err != nil {
		return fmt.Errorf("failed to delete zone records: %w", err)
	}
	if !dryRun {
		_, err = client.DeleteHostedZone(ctx, &route53.DeleteHostedZoneInput{
			Id: aws.String(zoneId),
		})
		return err
	}
	return nil
}

func deleteRecords(ctx context.Context, client *route53.Client, zoneId string) error {
	lrrsi := &route53.ListResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneId),
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
	for _, rrs := range output.ResourceRecordSets {
		if rrs.Type != route53types.RRTypeA && rrs.Type != route53types.RRTypeCname {
			continue
		}
		deleteRequired = true
		log.Printf("deleting record %s", aws.ToString(rrs.Name))
		changes = append(changes, route53types.Change{
			Action:            route53types.ChangeActionDelete,
			ResourceRecordSet: &rrs,
		})
	}
	if !deleteRequired {
		return nil
	}
	crrsi := &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneId),
		ChangeBatch: &route53types.ChangeBatch{
			Changes: changes,
		},
	}

	if !dryRun {
		_, err = client.ChangeResourceRecordSets(ctx, crrsi)
		return err
	}
	return nil
}
