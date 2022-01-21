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
	var vpcs []string
	for _, vpc := range output.Vpcs {
		vpcs = append(vpcs, *vpc.VpcId)
	}

	// Find all zones in use by VPCs
	zonesIdsInUse := make(map[string]string)
	for _, vpc := range vpcs {
		output, err := route53client.ListHostedZonesByVPCWithContext(ctx, &route53.ListHostedZonesByVPCInput{VPCId: aws.String(vpc), VPCRegion: aws.String("us-east-1")})
		if err != nil {
			log.Fatal(err)
		}
		for _, zoneSummary := range output.HostedZoneSummaries {
			zonesIdsInUse[*zoneSummary.Name] = *zoneSummary.HostedZoneId
		}
	}

	for name, id := range zonesIdsInUse {
		log.Printf("%s %s in use", name, id)
	}

	examplePrivateZoneName := regexp.MustCompile("example-([a-z0-9]{5}).hypershift.local.")

	publicZones := map[string]string{
		"ci.hypershift.devcluster.openshift.com.":   "",
		"hive.hypershift.devcluster.openshift.com.": "",
	}

	// Delete unused private zones
	err = route53client.ListHostedZonesPagesWithContext(ctx, &route53.ListHostedZonesInput{}, func(lhzo *route53.ListHostedZonesOutput, lastPage bool) bool {
		for _, zone := range lhzo.HostedZones {
			if zone == nil || zone.Name == nil || zone.Id == nil {
				continue
			}
			zoneId := strings.TrimPrefix(*zone.Id, "/hostedzone/")
			for zoneName := range publicZones {
				if *zone.Name == zoneName {
					publicZones[zoneName] = zoneId
				}
			}
			if zone.Config == nil || zone.Config.PrivateZone == nil || !*zone.Config.PrivateZone {
				continue
			}
			if !strings.HasSuffix(*zone.Name, ".ci.hypershift.devcluster.openshift.com.") &&
				!strings.HasSuffix(*zone.Name, ".hive.hypershift.devcluster.openshift.com.") &&
				!examplePrivateZoneName.MatchString(*zone.Name) {
				continue
			}
			inUse := false
			for _, zoneIdInUse := range zonesIdsInUse {
				if zoneId == zoneIdInUse {
					inUse = true
					break
				}
			}
			if inUse {
				continue
			}
			log.Printf("deleting hosted zone %s with id %s", *zone.Name, *zone.Id)
			deleteZone(ctx, strings.TrimSuffix(*zone.Name, "."), strings.TrimPrefix(*zone.Id, "/hostedzone/"), route53client)
		}
		return !lastPage
	})
	if err != nil {
		log.Fatal(err)
	}

	// Delete unused api.* records from public zone
	for _, publicZoneId := range publicZones {
		if publicZoneId == "" {
			continue
		}
		lrrsi := &route53.ListResourceRecordSetsInput{
			HostedZoneId: aws.String(publicZoneId),
			MaxItems:     aws.String("200"),
		}
		output, err := route53client.ListResourceRecordSetsWithContext(ctx, lrrsi)
		if err != nil {
			log.Fatal(err)
		}
		if len(output.ResourceRecordSets) == 0 {
			continue
		}
		var changeBatch route53.ChangeBatch
		var deleteRequired bool
		for _, rrs := range output.ResourceRecordSets {
			if *rrs.Type != "A" {
				continue
			}
			inUseRecord := false
			for zoneNameInUse := range zonesIdsInUse {
				if strings.HasSuffix(*rrs.Name, zoneNameInUse) {
					inUseRecord = true
					break
				}
			}
			if inUseRecord {
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
			continue
		}
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
}

func deleteZone(ctx context.Context, zoneName string, zoneId string, client route53iface.Route53API) error {
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
