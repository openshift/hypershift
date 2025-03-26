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

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/route53"
	"github.com/aws/aws-sdk-go/service/route53/route53iface"
)

const dryRun = false

func main() {
	awsConfig := aws.NewConfig()
	awsConfig.Region = aws.String("us-east-1")
	awsSession := session.Must(session.NewSession())
	route53client := route53.New(awsSession, awsConfig)
	ec2client := ec2.New(awsSession, awsConfig)

	ctx, cancel := context.WithCancel(context.Background())
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT)
	go func() {
		<-sigs
		cancel()
	}()

	// List all VPCs
	output, err := ec2client.DescribeVpcsWithContext(ctx, &ec2.DescribeVpcsInput{})
	if err != nil {
		log.Fatal(err)
	}

	// Get the list of infraIDs in use
	var infraIDsInUse []string
	for _, vpc := range output.Vpcs {
		// Get the VPC name out of the tags
		for _, tag := range vpc.Tags {
			if *tag.Key == "Name" && len(strings.TrimSpace(*tag.Value)) > 0 {
				infraID := strings.TrimSuffix(*tag.Value, "-vpc")
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
	err = route53client.ListHostedZonesPagesWithContext(ctx, &route53.ListHostedZonesInput{}, func(lhzo *route53.ListHostedZonesOutput, lastPage bool) bool {
		for _, zone := range lhzo.HostedZones {
			// Sanity check
			if zone == nil || zone.Name == nil || zone.Id == nil {
				continue
			}
			zoneName := *zone.Name
			zoneId := strings.TrimPrefix(*zone.Id, "/hostedzone/")

			// Check if the zone is in the list of public zones
			// If it is, store the zoneId in publicZones value for the next step
			for zoneName := range publicZones {
				if *zone.Name == zoneName {
					publicZones[zoneName] = zoneId
				}
			}

			// Exclude public zones
			if zone.Config == nil || zone.Config.PrivateZone == nil || !*zone.Config.PrivateZone {
				continue
			}

			// Exclude the CI public zones (redundant with the public zones check above for safety)
			// and private zones that are not example-<infraID>.hypershift.local.
			if !strings.HasSuffix(*zone.Name, ".ci.hypershift.devcluster.openshift.com.") &&
				!strings.HasSuffix(*zone.Name, ".service.ci.hypershift.devcluster.openshift.com.") &&
				!examplePrivateZoneName.MatchString(*zone.Name) {
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
		return !lastPage
	})
	if err != nil {
		log.Fatal(err)
	}

	// Delete unused records from public zones
	for _, publicZoneId := range publicZones {
		// Sanity check
		if publicZoneId == "" {
			continue
		}
		lrrsi := &route53.ListResourceRecordSetsInput{
			HostedZoneId: aws.String(publicZoneId),
			MaxItems:     aws.String("100"),
		}
		err := route53client.ListResourceRecordSetsPagesWithContext(ctx, lrrsi, func(output *route53.ListResourceRecordSetsOutput, lastPage bool) bool {
			var changeBatch route53.ChangeBatch
			var deleteRequired bool
			for _, rrs := range output.ResourceRecordSets {
				// Sanity exclusion checks
				if *rrs.Type != "A" && *rrs.Type != "CNAME" && *rrs.Type != "TXT" {
					continue
				}

				// Check if the record is in use
				inUseRecord := false
				for _, infraID := range infraIDsInUse {
					if strings.Contains(*rrs.Name, infraID) {
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
				log.Printf("deleting record %s", *rrs.Name)

				// Enqueue the record for deletion
				changeBatch.Changes = append(changeBatch.Changes, &route53.Change{
					Action:            aws.String("DELETE"),
					ResourceRecordSet: rrs,
				})
			}
			if deleteRequired {
				// At least one record from the current page needs to be deleted
				crrsi := &route53.ChangeResourceRecordSetsInput{
					HostedZoneId: aws.String(publicZoneId),
					ChangeBatch:  &changeBatch,
				}

				if !dryRun {
					_, err = route53client.ChangeResourceRecordSetsWithContext(ctx, crrsi)
					if err != nil {
						log.Fatal(err)
					}
				}
			}
			return !lastPage
		})
		if err != nil {
			log.Fatal(err)
		}
	}
}

func deleteZone(ctx context.Context, zoneId string, client route53iface.Route53API) error {
	err := deleteRecords(ctx, client, zoneId)
	if err != nil {
		return fmt.Errorf("failed to delete zone records")
	}
	if !dryRun {
		_, err = client.DeleteHostedZoneWithContext(ctx, &route53.DeleteHostedZoneInput{
			Id: aws.String(zoneId),
		})
		return err
	}
	return nil
}

func deleteRecords(ctx context.Context, client route53iface.Route53API, zoneId string) error {
	lrrsi := &route53.ListResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneId),
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
		if *rrs.Type != "A" && *rrs.Type != "CNAME" {
			continue
		}
		deleteRequired = true
		log.Printf("deleting record %s", *rrs.Name)
		changeBatch.Changes = append(changeBatch.Changes, &route53.Change{
			Action:            aws.String("DELETE"),
			ResourceRecordSet: rrs,
		})
	}
	if !deleteRequired {
		return nil
	}
	crrsi := &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneId),
		ChangeBatch:  &changeBatch,
	}

	if !dryRun {
		_, err = client.ChangeResourceRecordSetsWithContext(ctx, crrsi)
		return err
	}
	return nil
}
