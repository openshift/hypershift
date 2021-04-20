package aws

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	awserrors "github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/cloudformation"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
	"github.com/aws/aws-sdk-go/service/elb"
	"github.com/aws/aws-sdk-go/service/elb/elbiface"
	"github.com/aws/aws-sdk-go/service/route53"
	"github.com/aws/aws-sdk-go/service/route53/route53iface"
	s3service "github.com/aws/aws-sdk-go/service/s3"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
)

type DestroyInfraOptions struct {
	AWSCredentialsFile string
	Region             string
	InfraID            string
}

func NewDestroyCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "aws",
		Short: "Destroys AWS infrastructure resources for a cluster",
	}

	opts := DestroyInfraOptions{
		Region: "us-east-1",
	}

	cmd.Flags().StringVar(&opts.InfraID, "infra-id", opts.InfraID, "The cluster infrastructure ID to destroy (required)")
	cmd.Flags().StringVar(&opts.AWSCredentialsFile, "aws-creds", opts.AWSCredentialsFile, "Path to an AWS credentials file (required)")
	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "Region where cluster infra should be created")

	cmd.MarkFlagRequired("infra-id")
	cmd.MarkFlagRequired("aws-creds")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithCancel(context.Background())
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT)
		go func() {
			<-sigs
			cancel()
		}()
		t := time.NewTicker(5 * time.Second)
		for {
			select {
			case <-ctx.Done():
				log.Info("Destroy was cancelled")
				return nil
			case <-t.C:
				if err := opts.DestroyInfra(ctx); err != nil {
					log.Error(err, "failed to destroy infrastructure, will retry")
				} else {
					log.Info("Successfully destroyed AWS infra")
					return nil
				}
			}
		}
	}
	return cmd
}

func (o *DestroyInfraOptions) DestroyInfra(ctx context.Context) error {
	awsSession := newSession()
	awsConfig := newConfig(o.AWSCredentialsFile, o.Region)
	r53Config := newConfig(o.AWSCredentialsFile, "us-east-1")

	cf := cloudformation.New(awsSession, awsConfig)
	s3 := s3service.New(awsSession, awsConfig)
	elbclient := elb.New(awsSession, awsConfig)
	ec2client := ec2.New(awsSession, awsConfig)
	r53 := route53.New(awsSession, r53Config)

	stack, err := getStack(cf, o.InfraID)
	if err != nil {
		if awserr, ok := err.(awserrors.Error); ok {
			// TODO: Where is this code constant in the aws sdk?
			if awserr.Code() == "ValidationError" {
				log.Error(err, "stack already deleted", "id", o.InfraID)
				return nil
			}
			return awserr
		}
		return fmt.Errorf("failed to get stack: %w", err)
	}
	log.Info("Found stack", "id", *stack.StackId)

	// Clean up the OIDC S3 bucket so it can be deleted along with the stack
	bucket := getStackOutput(stack, "OIDCBucketName")
	if err := emptyBucket(ctx, bucket, s3); err != nil {
		return fmt.Errorf("failed to empty the OIDC bucket: %w", err)
	}
	log.Info("Emptied OIDC bucket", "id", bucket)

	// Delete the NS record from the base domain hosted zone
	baseDomainZoneID := getStackOutput(stack, "BaseDomainHostedZoneId")
	subdomain := getStackOutput(stack, "Subdomain")
	if err := deleteRecord(ctx, r53, baseDomainZoneID, "NS", subdomain); err != nil {
		return fmt.Errorf("failed to clean up the base domain zone: %w", err)
	}
	log.Info("Cleaned up the base domain hosted zone", "id", baseDomainZoneID, "subdomain", subdomain)

	// Find and delete any non-default unmanaged DNS records
	subdomainPrivateZoneID := getStackOutput(stack, "SubdomainPrivateZoneId")
	if err := deleteNonDefaultRecords(ctx, r53, subdomainPrivateZoneID); err != nil {
		return fmt.Errorf("failed to clean up the subdomain private zone: %w", err)
	}
	log.Info("Cleaned up the subdomain private zone", "zone", subdomainPrivateZoneID)
	subdomainPublicZoneID := getStackOutput(stack, "SubdomainPublicZoneId")
	err = deleteNonDefaultRecords(ctx, r53, subdomainPublicZoneID)
	if err != nil {
		return fmt.Errorf("failed to clean up the subdomain public zone: %w", err)
	}
	log.Info("Cleaned up the subdomain public zone", "zone", subdomainPublicZoneID)

	vpcID := getStackOutput(stack, "VPCId")

	// Find and delete any unmanaged leaked load balancers
	if err := deleteUnmanagedELBs(ctx, elbclient, vpcID); err != nil {
		return fmt.Errorf("failed to delete unmanaged ELBs: %w", err)
	}
	log.Info("Cleaned up unmanaged ELBs")

	// Find and delete any unmanaged leaked security groups
	if err := deleteUnmanagedSecurityGroups(ctx, ec2client, vpcID); err != nil {
		return fmt.Errorf("failed to delete unmanaged security groups: %w", err)
	}
	log.Info("Cleaned up unmanaged security groups")

	// Delete the stack itself
	if err := deleteStack(ctx, cf, stack); err != nil {
		return fmt.Errorf("failed to delete stack: %w", err)
	}
	log.Info("Deleted the stack", "id", *stack.StackId)

	return nil
}

func deleteUnmanagedELBs(ctx context.Context, client elbiface.ELBAPI, vpcID string) error {
	var errs []error
	deleteLBs := func(out *elb.DescribeLoadBalancersOutput, _ bool) bool {
		for _, lb := range out.LoadBalancerDescriptions {
			if *lb.VPCId != vpcID {
				continue
			}
			tags, err := client.DescribeTags(&elb.DescribeTagsInput{
				LoadBalancerNames: []*string{lb.LoadBalancerName},
			})
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to describe tags for load balancer %s: %w", *lb.LoadBalancerName, err))
				continue
			}
			isManaged := false
			for _, tagDescription := range tags.TagDescriptions {
				for _, tag := range tagDescription.Tags {
					if *tag.Key == "hypershift.openshift.io/infra" && *tag.Value == "owned" {
						isManaged = true
					}
				}
			}
			if isManaged {
				log.Info("Ignoring managed load balancer", "name", *lb.LoadBalancerName, "vpcID", vpcID)
				continue
			}
			_, err = client.DeleteLoadBalancerWithContext(ctx, &elb.DeleteLoadBalancerInput{
				LoadBalancerName: lb.LoadBalancerName,
			})
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to delete load balancer %s: %w", *lb.LoadBalancerName, err))
				continue
			}
			log.Info("Deleted unmanaged load balancer", "name", *lb.LoadBalancerName, "vpcID", vpcID)
		}
		return true
	}
	err := client.DescribeLoadBalancersPagesWithContext(ctx,
		&elb.DescribeLoadBalancersInput{},
		deleteLBs)
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to delete load balancers: %w", err))
	}
	return errors.NewAggregate(errs)
}

func deleteUnmanagedSecurityGroups(ctx context.Context, client ec2iface.EC2API, vpcID string) error {
	managedGroups := sets.NewString()
	unmanagedGroups := sets.NewString()
	err := client.DescribeSecurityGroupsPagesWithContext(ctx,
		&ec2.DescribeSecurityGroupsInput{Filters: vpcFilter(vpcID)},
		func(out *ec2.DescribeSecurityGroupsOutput, _ bool) bool {
			for _, sg := range out.SecurityGroups {
				if *sg.GroupName == "default" {
					continue
				}
				isManaged := false
				for _, tag := range sg.Tags {
					if *tag.Key == "hypershift.openshift.io/infra" && *tag.Value == "owned" {
						isManaged = true
					}
				}
				if isManaged {
					managedGroups.Insert(*sg.GroupId)
				} else {
					unmanagedGroups.Insert(*sg.GroupId)
				}
			}
			return false
		})
	if err != nil {
		return fmt.Errorf("failed to describe security groups: %w", err)
	}

	var errs []error

	// Revoke all managed group ingress rules that reference unmanaged groups
	// so the unmanaged groups can be deleted
	err = client.DescribeSecurityGroupsPagesWithContext(ctx,
		&ec2.DescribeSecurityGroupsInput{Filters: vpcFilter(vpcID)},
		func(out *ec2.DescribeSecurityGroupsOutput, _ bool) bool {
			for i := range out.SecurityGroups {
				sg := out.SecurityGroups[i]
				if !managedGroups.Has(*sg.GroupId) {
					continue
				}
				var unmanagedPermissions []*ec2.IpPermission
				for _, perm := range sg.IpPermissions {
					for _, pair := range perm.UserIdGroupPairs {
						if !managedGroups.Has(*pair.GroupId) {
							unmanagedPermissions = append(unmanagedPermissions, perm)
							break
						}
					}
				}
				if len(unmanagedPermissions) > 0 {
					_, err := client.RevokeSecurityGroupIngressWithContext(ctx, &ec2.RevokeSecurityGroupIngressInput{
						GroupId:       sg.GroupId,
						IpPermissions: unmanagedPermissions,
					})
					if err != nil {
						errs = append(errs, fmt.Errorf("failed to revoke unmanaged security group ingress from managed group %s: %w", *sg.GroupId, err))
					} else {
						log.Info("Cleaned up unmanaged security group ingress permissions", "id", *sg.GroupId, "permissions", unmanagedPermissions)
					}
				}
			}
			return false
		})
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to revoke security group ingress rules: %w", err))
	}

	// Delete unmanaged security groups
	err = client.DescribeSecurityGroupsPagesWithContext(ctx,
		&ec2.DescribeSecurityGroupsInput{Filters: vpcFilter(vpcID)},
		func(out *ec2.DescribeSecurityGroupsOutput, _ bool) bool {
			for _, sg := range out.SecurityGroups {
				if !unmanagedGroups.Has(*sg.GroupId) {
					continue
				}
				_, err := client.DeleteSecurityGroupWithContext(ctx, &ec2.DeleteSecurityGroupInput{
					GroupId: sg.GroupId,
				})
				if err != nil {
					errs = append(errs, fmt.Errorf("failed to delete security group %s: %w", *sg.GroupId, err))
					continue
				}
				log.Info("Deleted unmanaged security group", "id", *sg.GroupId)
			}
			return true
		})
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to delete security groups: %w", err))
	}

	return errors.NewAggregate(errs)
}

func deleteNonDefaultRecords(ctx context.Context, client route53iface.Route53API, zoneID string) error {
	typesToPreserve := sets.NewString("SOA", "NS")

	input := &route53.ListResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
	}
	var recordsToDelete []*route53.ResourceRecordSet
	err := client.ListResourceRecordSetsPagesWithContext(ctx, input, func(resp *route53.ListResourceRecordSetsOutput, lastPage bool) bool {
		for i, rrs := range resp.ResourceRecordSets {
			if typesToPreserve.Has(*rrs.Type) {
				continue
			}
			recordsToDelete = append(recordsToDelete, resp.ResourceRecordSets[i])
		}
		return false
	})
	if len(recordsToDelete) == 0 {
		return nil
	}

	// Change batch for deleting
	changeBatch := &route53.ChangeBatch{
		Changes: []*route53.Change{},
	}
	for i, rec := range recordsToDelete {
		changeBatch.Changes = append(changeBatch.Changes, &route53.Change{
			Action:            aws.String("DELETE"),
			ResourceRecordSet: recordsToDelete[i],
		})
		log.Info("Deleting unmanaged record", "zone", zoneID, "name", *rec.Name)
	}

	_, err = client.ChangeResourceRecordSetsWithContext(ctx, &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
		ChangeBatch:  changeBatch,
	})
	if err != nil {
		return fmt.Errorf("failed to delete records: %w", err)
	}
	log.Info("Deleted unmanaged non-default records from zone", "zone", zoneID)
	return nil
}
