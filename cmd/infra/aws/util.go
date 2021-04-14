package aws

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	awserrors "github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/cloudformation"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/route53"
	"github.com/aws/aws-sdk-go/service/route53/route53iface"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3iface"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/util/wait"
)

func emptyBucket(ctx context.Context, bucket string, s3Client s3iface.S3API) error {
	params := &s3.ListObjectsInput{
		Bucket: aws.String(bucket),
	}
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("bucket deletion was cancelled")
		default:
		}
		objects, err := s3Client.ListObjects(params)
		if err != nil {
			if awserr, ok := err.(awserrors.Error); ok {
				if awserr.Code() == "NoSuchBucket" {
					// Nothing to do
					return nil
				}
			}
			return err
		}
		if len(objects.Contents) == 0 {
			return nil
		}
		objectsToDelete := make([]*s3.ObjectIdentifier, 0, 1000)
		for _, object := range objects.Contents {
			objectsToDelete = append(objectsToDelete, &s3.ObjectIdentifier{
				Key: object.Key,
			})
		}
		deleteParams := &s3.DeleteObjectsInput{
			Bucket: aws.String(bucket),
			Delete: &s3.Delete{Objects: objectsToDelete},
		}
		_, err = s3Client.DeleteObjects(deleteParams)
		if err != nil {
			return err
		}
		log.Info("Deleted bucket objects", "name", bucket, "count", len(objectsToDelete))
		if *objects.IsTruncated {
			params.Marker = deleteParams.Delete.Objects[len(deleteParams.Delete.Objects)-1].Key
		} else {
			break
		}
	}
	return nil
}

func getStack(cf *cloudformation.CloudFormation, id string) (*cloudformation.Stack, error) {
	output, err := cf.DescribeStacks(&cloudformation.DescribeStacksInput{
		StackName: aws.String(id),
	})
	if err != nil {
		if awserr, ok := err.(awserrors.Error); ok {
			return nil, awserr
		}
		return nil, fmt.Errorf("failed to describe stack: %w", err)
	}
	if count := len(output.Stacks); count != 1 {
		return nil, fmt.Errorf("expected exactly 1 stack, got %d", count)
	}
	return output.Stacks[0], nil
}

func getStackOutput(stack *cloudformation.Stack, key string) string {
	for i, o := range stack.Outputs {
		if o.OutputKey != nil && *o.OutputKey == key {
			return *stack.Outputs[i].OutputValue
		}
	}
	return ""
}

func deleteStack(ctx context.Context, cf *cloudformation.CloudFormation, stack *cloudformation.Stack) error {
	_, err := cf.DeleteStack(&cloudformation.DeleteStackInput{
		StackName: stack.StackId,
	})
	if err != nil {
		return fmt.Errorf("failed to delete stack: %w", err)
	}
	log.Info("Waiting for stack to be deleted", "id", *stack.StackId)
	err = wait.PollUntil(5*time.Second, func() (bool, error) {
		output, err := cf.DescribeStacks(&cloudformation.DescribeStacksInput{
			StackName: stack.StackId,
		})
		if err != nil {
			if awserr, ok := err.(awserrors.Error); ok {
				log.Error(err, "error describing stack", "code", awserr.Code(), "message", awserr.Message())
				return true, nil
			}
			return false, fmt.Errorf("failed to describe stack: %w", err)
		}
		if count := len(output.Stacks); count != 1 {
			return false, fmt.Errorf("expected exactly 1 stack, got %d", count)
		}
		stack := output.Stacks[0]
		switch *stack.StackStatus {
		case cloudformation.StackStatusDeleteComplete:
			return true, nil
		case cloudformation.StackStatusDeleteInProgress:
			return false, nil
		case cloudformation.StackStatusDeleteFailed:
			return false, fmt.Errorf("stack deletion failed")
		default:
			log.Info("Stack is still pending deletion", "id", *stack.StackId, "status", *stack.StackStatus)
			return false, nil
		}
	}, ctx.Done())
	if err != nil {
		return fmt.Errorf("failed to delete stack: %w", err)
	}
	log.Info("Finished deleting stack", "id", *stack.StackId)
	return nil
}

func deleteRecord(ctx context.Context, client route53iface.Route53API, id, recordType, recordName string) error {
	record, err := findRecord(ctx, client, id, recordType, recordName)
	if err != nil {
		return err
	}

	if record == nil {
		return nil
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
	if err != nil {
		return err
	}
	log.Info("Deleted record", "type", recordType, "name", recordName)
	return err
}

func findRecord(ctx context.Context, client route53iface.Route53API, id, recordType, name string) (*route53.ResourceRecordSet, error) {
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

func fqdn(name string) string {
	n := len(name)
	if n == 0 || name[n-1] == '.' {
		return name
	} else {
		return name + "."
	}
}

func cleanRecordName(name string) string {
	str := name
	s, err := strconv.Unquote(`"` + str + `"`)
	if err != nil {
		return str
	}
	return s
}

func lookupZone(client route53iface.Route53API, name string, isPrivateZone bool) (string, error) {
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
	if err := client.ListHostedZonesPages(&route53.ListHostedZonesInput{}, f); err != nil {
		return "", err
	}
	if res == nil {
		return "", errors.Errorf("Hosted zone %s not found", name)
	}
	return strings.TrimPrefix(*res.Id, "/hostedzone/"), nil
}

func vpcFilter(vpcID string) []*ec2.Filter {
	return []*ec2.Filter{
		{
			Name:   aws.String("vpc-id"),
			Values: []*string{aws.String(vpcID)},
		},
	}
}

type sortableStackEvents []*cloudformation.StackEvent

func (e sortableStackEvents) Len() int {
	return len(e)
}

func (e sortableStackEvents) Less(i, j int) bool {
	return (*e[i].Timestamp).Before(*e[j].Timestamp)
}

func (e sortableStackEvents) Swap(i, j int) {
	e[i], e[j] = e[j], e[i]
}
