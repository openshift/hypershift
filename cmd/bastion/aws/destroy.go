package aws

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/openshift/hypershift/cmd/log"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/cmd/util"
)

type DestroyBastionOpts struct {
	Namespace          string
	Name               string
	InfraID            string
	Region             string
	AWSCredentialsFile string
	AWSKey             string
	AWSSecretKey       string
}

func NewDestroyCommand() *cobra.Command {
	opts := DestroyBastionOpts{
		Namespace: "clusters",
	}
	cmd := &cobra.Command{
		Use:          "aws",
		Short:        "Destroys AWS bastion instance",
		SilenceUsage: true,
	}

	cmd.Flags().StringVar(&opts.Namespace, "namespace", opts.Namespace, "The namespace of the hostedcluster")
	cmd.Flags().StringVar(&opts.Name, "name", opts.Name, "The name of the hostedcluster")
	cmd.Flags().StringVar(&opts.InfraID, "infra-id", opts.InfraID, "The infra ID to use for creating the bastion")
	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "The region to use for creating the bastion")
	cmd.Flags().StringVar(&opts.AWSCredentialsFile, "aws-creds", opts.AWSCredentialsFile, "File with AWS credentials")

	cmd.MarkFlagRequired("aws-creds")
	cmd.MarkFlagFilename("aws-creds")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := opts.Validate(); err != nil {
			log.Log.Error(err, "Invalid arguments")
			cmd.Usage()
			return nil
		}
		if err := opts.Run(cmd.Context()); err != nil {
			log.Log.Error(err, "Failed to create bastion")
			return err
		} else {
			log.Log.Info("Successfully destroyed bastion")
		}
		return nil
	}

	return cmd
}

func (o *DestroyBastionOpts) Validate() error {
	if len(o.Name) > 0 {
		if len(o.Namespace) == 0 {
			return fmt.Errorf("a namespace must be specified if specifying a hosted cluster name")
		}
		if len(o.InfraID) > 0 || len(o.Region) > 0 {
			return fmt.Errorf("infra id and region cannot be specified when specifying a hosted cluster name")
		}
	} else {
		if len(o.InfraID) == 0 || len(o.Region) == 0 {
			return fmt.Errorf("infra id and region must be specified when not specifying a hosted cluster name")
		}
	}
	return nil
}

func (o *DestroyBastionOpts) Run(ctx context.Context) error {

	var infraID, region string

	if len(o.Name) > 0 {
		// Find HostedCluster and get AWS creds
		c, err := util.GetClient()
		if err != nil {
			return err
		}

		var hostedCluster hyperv1.HostedCluster
		if err := c.Get(ctx, types.NamespacedName{Namespace: o.Namespace, Name: o.Name}, &hostedCluster); err != nil {
			return fmt.Errorf("failed to get hostedcluster: %w", err)
		}
		if hostedCluster.Spec.Platform.AWS == nil {
			return fmt.Errorf("hosted cluster's platform is not AWS")
		}

		infraID = hostedCluster.Spec.InfraID
		region = hostedCluster.Spec.Platform.AWS.Region
		log.Log.Info("Found hosted cluster", "namespace", hostedCluster.Namespace, "name", hostedCluster.Name, "infraID", infraID, "region", region)
	} else {
		infraID = o.InfraID
		region = o.Region
	}

	awsSession := awsutil.NewSession("cli-destroy-bastion", o.AWSCredentialsFile, o.AWSKey, o.AWSSecretKey, region)
	awsConfig := awsutil.NewConfig()
	ec2Client := ec2.New(awsSession, awsConfig)

	return wait.PollImmediateUntil(5*time.Second, func() (bool, error) {
		err := destroyBastion(ctx, ec2Client, infraID)
		if err != nil {
			if !awsutil.IsErrorRetryable(err) {
				return false, err
			}
			log.Log.Info("WARNING: error during destroy, will retry", "error", err.Error(), "type", fmt.Sprintf("%T,%+v", err, err))
			return false, nil
		}
		return true, nil
	}, ctx.Done())
}

func destroyBastion(ctx context.Context, ec2Client *ec2.EC2, infraID string) error {
	if err := destroyEC2Instance(ctx, ec2Client, infraID); err != nil {
		return err
	}
	if err := destroySecurityGroup(ctx, ec2Client, infraID); err != nil {
		return err
	}
	if err := destroyKeyPair(ctx, ec2Client, infraID); err != nil {
		return err
	}
	return nil
}

func destroyEC2Instance(ctx context.Context, ec2Client *ec2.EC2, infraID string) error {
	instanceID, err := existingInstance(ctx, ec2Client, infraID)
	if err != nil {
		return err
	}
	if len(instanceID) == 0 {
		return nil
	}
	terminateCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	_, err = ec2Client.TerminateInstancesWithContext(terminateCtx, &ec2.TerminateInstancesInput{
		InstanceIds: []*string{aws.String(instanceID)},
	})
	if err != nil {
		return fmt.Errorf("error deleting instance: %w", err)
	}
	log.Log.Info("Deleted bastion instance", "id", instanceID, "name", instanceName(infraID))
	return nil
}

func destroySecurityGroup(ctx context.Context, ec2Client *ec2.EC2, infraID string) error {
	sg, err := existingSecurityGroup(ctx, ec2Client, infraID)
	if err != nil {
		return err
	}
	if sg == nil {
		return nil
	}
	sgCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	_, err = ec2Client.DeleteSecurityGroupWithContext(sgCtx, &ec2.DeleteSecurityGroupInput{
		GroupId: sg.GroupId,
	})
	if err != nil {
		return fmt.Errorf("error deleting security group: %w", err)
	}
	log.Log.Info("Deleted security group", "id", aws.StringValue(sg.GroupId), "name", securityGroupName(infraID))
	return nil
}

func destroyKeyPair(ctx context.Context, ec2Client *ec2.EC2, infraID string) error {
	keyPairID, err := existingKeyPair(ctx, ec2Client, infraID)
	if err != nil {
		return err
	}
	if len(keyPairID) == 0 {
		return nil
	}
	kpCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	_, err = ec2Client.DeleteKeyPairWithContext(kpCtx, &ec2.DeleteKeyPairInput{
		KeyPairId: aws.String(keyPairID),
	})
	if err != nil {
		return fmt.Errorf("error deleting keypair: %w", err)
	}
	log.Log.Info("Deleted keypair", "id", keyPairID, "name", keyPairName(infraID))
	return nil
}
