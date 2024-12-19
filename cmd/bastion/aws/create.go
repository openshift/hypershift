package aws

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/cmd/util"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/ec2"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
)

type CreateBastionOpts struct {
	Namespace          string
	Name               string
	InfraID            string
	Region             string
	SSHKeyFile         string
	AWSCredentialsFile string
	AWSKey             string
	AWSSecretKey       string
	Wait               bool
}

func NewCreateCommand() *cobra.Command {
	opts := &CreateBastionOpts{
		Namespace: "clusters",
		Wait:      true,
	}

	cmd := &cobra.Command{
		Use:          "aws",
		Short:        "Creates AWS bastion instance",
		SilenceUsage: true,
	}
	cmd.Flags().StringVar(&opts.Namespace, "namespace", opts.Namespace, "The namespace of the hostedcluster")
	cmd.Flags().StringVar(&opts.Name, "name", opts.Name, "The name of the hostedcluster")
	cmd.Flags().StringVar(&opts.InfraID, "infra-id", opts.InfraID, "The infra ID to use for creating the bastion")
	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "The region to use for creating the bastion")
	cmd.Flags().StringVar(&opts.SSHKeyFile, "ssh-key-file", opts.SSHKeyFile, "File with public SSH key to use for bastion instance")
	cmd.Flags().StringVar(&opts.AWSCredentialsFile, "aws-creds", opts.AWSCredentialsFile, "File with AWS credentials")
	cmd.Flags().BoolVar(&opts.Wait, "wait", opts.Wait, "Wait for instance to be running")

	cmd.MarkFlagRequired("aws-creds")

	cmd.MarkFlagFilename("ssh-key-file")
	cmd.MarkFlagFilename("aws-creds")

	logger := log.Log
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := opts.Validate(); err != nil {
			logger.Error(err, "Invalid arguments")
			cmd.Usage()
			return nil
		}

		if instanceID, publicIP, err := opts.Run(cmd.Context(), logger); err != nil {
			logger.Error(err, "Failed to create bastion")
			return err
		} else {
			logger.Info("Successfully created bastion", "id", instanceID, "publicIP", publicIP)
		}
		return nil
	}
	return cmd
}

func (o *CreateBastionOpts) Validate() error {
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
		if len(o.SSHKeyFile) == 0 {
			return fmt.Errorf("ssh-key-file must be specified when not specifying a hosted cluster name")
		}
	}
	return nil
}

func (o *CreateBastionOpts) Run(ctx context.Context, logger logr.Logger) (string, string, error) {

	var infraID, region string
	var sshPublicKey []byte

	if len(o.Name) > 0 {
		// Find HostedCluster and get AWS creds
		c, err := util.GetClient()
		if err != nil {
			return "", "", err
		}

		var hostedCluster hyperv1.HostedCluster
		if err := c.Get(ctx, types.NamespacedName{Namespace: o.Namespace, Name: o.Name}, &hostedCluster); err != nil {
			return "", "", fmt.Errorf("failed to get hostedcluster: %w", err)
		}
		if hostedCluster.Spec.Platform.AWS == nil {
			return "", "", fmt.Errorf("hosted cluster's platform is not AWS")
		}

		infraID = hostedCluster.Spec.InfraID
		region = hostedCluster.Spec.Platform.AWS.Region
		logger.Info("Found hosted cluster", "namespace", hostedCluster.Namespace, "name", hostedCluster.Name, "infraID", infraID, "region", region)

		if len(o.SSHKeyFile) == 0 {
			if len(hostedCluster.Spec.SSHKey.Name) == 0 {
				return "", "", fmt.Errorf("hosted cluster does not have a public SSH key and no SSH key file was specified")
			}
			sshKeySecret := &corev1.Secret{}
			if err := c.Get(ctx, types.NamespacedName{Name: hostedCluster.Spec.SSHKey.Name, Namespace: o.Namespace}, sshKeySecret); err != nil {
				return "", "", fmt.Errorf("cannot get secret with SSH key (%s/%s): %w", o.Namespace, hostedCluster.Spec.SSHKey.Name, err)
			}
			sshPublicKey = sshKeySecret.Data["id_rsa.pub"]
		}
	} else {
		infraID = o.InfraID
		region = o.Region
	}

	// Read SSH public key
	if len(o.SSHKeyFile) > 0 {
		var err error
		sshPublicKey, err = os.ReadFile(o.SSHKeyFile)
		if err != nil {
			return "", "", fmt.Errorf("cannot read SSH public key from %s: %v", o.SSHKeyFile, err)
		}
	}

	awsSession := awsutil.NewSession("cli-create-bastion", o.AWSCredentialsFile, o.AWSKey, o.AWSSecretKey, region)
	awsConfig := awsutil.NewConfig()
	ec2Client := ec2.New(awsSession, awsConfig)

	// Ensure security group exists
	sgID, err := ensureBastionSecurityGroup(ctx, logger, ec2Client, infraID)
	if err != nil {
		return "", "", fmt.Errorf("failed to ensure security group for bastion: %w", err)
	}

	// Ensure keypair exists
	if err := ensureBastionKeyPair(ctx, logger, ec2Client, infraID, sshPublicKey); err != nil {
		return "", "", fmt.Errorf("failed to ensure bastion keypair: %w", err)
	}

	// Create ec2 instance
	instanceID, err := runEC2BastionInstance(ctx, logger, ec2Client, sgID, infraID)
	if err != nil {
		return "", "", fmt.Errorf("failed to run bastion machine instance: %w", err)
	}

	// Waiting for instance to be running
	var publicIP string
	if o.Wait {
		publicIP, err = waitForInstanceRunning(ctx, logger, ec2Client, instanceID)
		if err != nil {
			return "", "", fmt.Errorf("failed to wait for instance to be running: %w", err)
		}
	}

	return instanceID, publicIP, nil
}

func ensureBastionSecurityGroup(ctx context.Context, logger logr.Logger, ec2Client *ec2.EC2, infraID string) (string, error) {
	// find VPC
	vpcID, err := existingVPC(ctx, ec2Client, infraID)
	if err != nil {
		return "", err
	}
	if vpcID == "" {
		return "", fmt.Errorf("cannot find vpc associated with cluster")
	}
	sg, err := existingSecurityGroup(ctx, ec2Client, infraID)
	if err != nil {
		return "", err
	}
	if sg == nil {
		name := securityGroupName(infraID)
		sgCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		result, err := ec2Client.CreateSecurityGroupWithContext(sgCtx, &ec2.CreateSecurityGroupInput{
			GroupName:   aws.String(name),
			Description: aws.String("bastion security group"),
			VpcId:       aws.String(vpcID),
			TagSpecifications: []*ec2.TagSpecification{
				{
					ResourceType: aws.String("security-group"),
					Tags: []*ec2.Tag{
						{
							Key:   aws.String(fmt.Sprintf("kubernetes.io/cluster/%s", infraID)),
							Value: aws.String("owned"),
						},
						{
							Key:   aws.String("Name"),
							Value: aws.String(name),
						},
					},
				},
			},
		})
		if err != nil {
			return "", fmt.Errorf("failed to create bastion security group: %w", err)
		}
		backoff := wait.Backoff{
			Steps:    10,
			Duration: 3 * time.Second,
			Factor:   1.0,
			Jitter:   0.1,
		}
		var sgResult *ec2.DescribeSecurityGroupsOutput
		err = retry.OnError(backoff, func(error) bool { return true }, func() error {
			var err error
			describeCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()
			sgResult, err = ec2Client.DescribeSecurityGroupsWithContext(describeCtx, &ec2.DescribeSecurityGroupsInput{
				GroupIds: []*string{result.GroupId},
			})
			if err != nil || len(sgResult.SecurityGroups) == 0 {
				return fmt.Errorf("not found yet")
			}
			return nil
		})
		if err != nil {
			return "", fmt.Errorf("cannot find security group that was just created (%s)", aws.StringValue(result.GroupId))
		}
		sg = sgResult.SecurityGroups[0]
		logger.Info("Created security group", "name", name, "id", aws.StringValue(sg.GroupId))
	} else {
		logger.Info("Found existing security group", "name", aws.StringValue(sg.GroupName), "id", aws.StringValue(sg.GroupId))
	}

	permission := &ec2.IpPermission{
		IpProtocol: aws.String("tcp"),
		IpRanges: []*ec2.IpRange{
			{
				CidrIp: aws.String("0.0.0.0/0"),
			},
		},
		FromPort: aws.Int64(22),
		ToPort:   aws.Int64(22),
	}

	shouldAdd := true
	for _, p := range sg.IpPermissions {
		if p.String() == permission.String() {
			shouldAdd = false
		}
	}
	if shouldAdd {
		authCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		_, err = ec2Client.AuthorizeSecurityGroupIngressWithContext(authCtx, &ec2.AuthorizeSecurityGroupIngressInput{
			GroupId:       sg.GroupId,
			IpPermissions: []*ec2.IpPermission{permission},
		})
		if err != nil {
			return "", fmt.Errorf("cannot authorize ssh access on security group: %w", err)
		}
	}
	return aws.StringValue(sg.GroupId), nil
}

func existingSecurityGroup(ctx context.Context, ec2Client *ec2.EC2, infraID string) (*ec2.SecurityGroup, error) {
	filters := []*ec2.Filter{
		{
			Name:   aws.String(fmt.Sprintf("tag:kubernetes.io/cluster/%s", infraID)),
			Values: []*string{aws.String("owned")},
		},
		{
			Name:   aws.String("tag:Name"),
			Values: []*string{aws.String(securityGroupName(infraID))},
		},
	}
	sgCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	result, err := ec2Client.DescribeSecurityGroupsWithContext(sgCtx, &ec2.DescribeSecurityGroupsInput{Filters: filters})
	if err != nil {
		return nil, fmt.Errorf("cannot list security groups: %w", err)
	}
	for _, sg := range result.SecurityGroups {
		return sg, nil
	}
	return nil, nil
}

func existingVPC(ctx context.Context, ec2Client *ec2.EC2, infraID string) (string, error) {
	var vpcID string
	vpcCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	vpcFilter := []*ec2.Filter{
		{
			Name:   aws.String(fmt.Sprintf("tag:kubernetes.io/cluster/%s", infraID)),
			Values: []*string{aws.String("owned")},
		},
		{
			Name:   aws.String("tag:Name"),
			Values: []*string{aws.String(vpcName(infraID))},
		},
	}
	result, err := ec2Client.DescribeVpcsWithContext(vpcCtx, &ec2.DescribeVpcsInput{Filters: vpcFilter})
	if err != nil {
		return "", fmt.Errorf("cannot list vpcs: %w", err)
	}
	for _, vpc := range result.Vpcs {
		vpcID = aws.StringValue(vpc.VpcId)
		break
	}
	return vpcID, nil
}

func ensureBastionKeyPair(ctx context.Context, logger logr.Logger, ec2Client *ec2.EC2, infraID string, publicKey []byte) error {
	keyPairID, err := existingKeyPair(ctx, ec2Client, infraID)
	if err != nil {
		return fmt.Errorf("failed to check for existing keypair: %w", err)
	}
	if keyPairID != "" {
		logger.Info("Found existing key pair", "id", keyPairID, "name", keyPairName(infraID))
		return nil
	}
	kpCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	result, err := ec2Client.ImportKeyPairWithContext(kpCtx, &ec2.ImportKeyPairInput{
		KeyName:           aws.String(keyPairName(infraID)),
		PublicKeyMaterial: publicKey,
		TagSpecifications: []*ec2.TagSpecification{
			{
				ResourceType: aws.String("key-pair"),
				Tags: []*ec2.Tag{
					{
						Key:   aws.String(fmt.Sprintf("kubernetes.io/cluster/%s", infraID)),
						Value: aws.String("owned"),
					},
					{
						Key:   aws.String("Name"),
						Value: aws.String(keyPairName(infraID)),
					},
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to import keypair: %w", err)
	}
	logger.Info("Created key pair", "id", aws.StringValue(result.KeyPairId), "name", aws.StringValue(result.KeyName))
	return nil
}

func existingKeyPair(ctx context.Context, ec2Client *ec2.EC2, infraID string) (string, error) {
	kpCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	result, err := ec2Client.DescribeKeyPairsWithContext(kpCtx, &ec2.DescribeKeyPairsInput{
		KeyNames: []*string{aws.String(keyPairName(infraID))},
	})
	if err != nil {
		if awsErr, ok := err.(awserr.Error); ok {
			if awsErr.Code() == "InvalidKeyPair.NotFound" {
				return "", nil
			}
		}
		return "", err
	}
	var keyPairID string
	for _, kp := range result.KeyPairs {
		keyPairID = aws.StringValue(kp.KeyPairId)
	}
	return keyPairID, nil
}

func runEC2BastionInstance(ctx context.Context, logger logr.Logger, ec2Client *ec2.EC2, sgID, infraID string) (string, error) {
	// find existing instance
	instanceID, err := existingInstance(ctx, ec2Client, infraID)
	if err != nil {
		return "", fmt.Errorf("cannot check for existing instances: %w", err)
	}
	if len(instanceID) > 0 {
		logger.Info("Found existing instance", "id", instanceID)
		return instanceID, nil
	}

	// find public subnet
	subnetID, err := existingSubnet(ctx, ec2Client, infraID)
	if err != nil {
		return "", fmt.Errorf("cannot lookup existing subnet: %w", err)
	}
	if len(subnetID) == 0 {
		return "", fmt.Errorf("no public subnet was found")
	}

	runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	result, err := ec2Client.RunInstancesWithContext(runCtx, &ec2.RunInstancesInput{
		ImageId:      aws.String("resolve:ssm:/aws/service/ami-amazon-linux-latest/amzn2-ami-hvm-x86_64-gp2"),
		MaxCount:     aws.Int64(1),
		MinCount:     aws.Int64(1),
		InstanceType: aws.String("t2.micro"),
		KeyName:      aws.String(keyPairName(infraID)),
		NetworkInterfaces: []*ec2.InstanceNetworkInterfaceSpecification{
			{
				DeviceIndex:              aws.Int64(0),
				AssociatePublicIpAddress: aws.Bool(true),
				SubnetId:                 aws.String(subnetID),
				Groups:                   []*string{aws.String(sgID)},
			},
		},
		TagSpecifications: []*ec2.TagSpecification{
			{
				ResourceType: aws.String("instance"),
				Tags: []*ec2.Tag{
					{
						Key:   aws.String(fmt.Sprintf("kubernetes.io/cluster/%s", infraID)),
						Value: aws.String("owned"),
					},
					{
						Key:   aws.String("Name"),
						Value: aws.String(instanceName(infraID)),
					},
				},
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to launch bastion instance: %w", err)
	}
	for _, instance := range result.Instances {
		instanceID := aws.StringValue(instance.InstanceId)
		logger.Info("Created ec2 instance", "id", instanceID, "name", instanceName(infraID))
		return instanceID, nil
	}
	return "", fmt.Errorf("no instances were created")
}

func existingSubnet(ctx context.Context, ec2Client *ec2.EC2, infraID string) (string, error) {
	var subnetID string
	subCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	subnetFilter := []*ec2.Filter{
		{
			Name:   aws.String(fmt.Sprintf("tag:kubernetes.io/cluster/%s", infraID)),
			Values: []*string{aws.String("owned")},
		},
	}
	result, err := ec2Client.DescribeSubnetsWithContext(subCtx, &ec2.DescribeSubnetsInput{
		Filters: subnetFilter,
	})
	if err != nil {
		return "", fmt.Errorf("cannot list subnets: %w", err)
	}
	nameRe := regexp.MustCompile(fmt.Sprintf("%s-public-[a-z,0-9,-]+", infraID))
	for _, subnet := range result.Subnets {
		var name string
		for _, tag := range subnet.Tags {
			if aws.StringValue(tag.Key) == "Name" {
				name = aws.StringValue(tag.Value)
				break
			}
		}
		if len(name) == 0 {
			continue
		}
		if nameRe.MatchString(name) {
			subnetID = aws.StringValue(subnet.SubnetId)
			break
		}
	}
	return subnetID, nil
}

func existingInstance(ctx context.Context, ec2Client *ec2.EC2, infraID string) (string, error) {
	instanceCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	instanceFilter := []*ec2.Filter{
		{
			Name:   aws.String(fmt.Sprintf("tag:kubernetes.io/cluster/%s", infraID)),
			Values: []*string{aws.String("owned")},
		},
		{
			Name:   aws.String("tag:Name"),
			Values: []*string{aws.String(instanceName(infraID))},
		},
	}
	result, err := ec2Client.DescribeInstancesWithContext(instanceCtx, &ec2.DescribeInstancesInput{
		Filters: instanceFilter,
	})
	if err != nil {
		return "", err
	}
	for _, reservation := range result.Reservations {
		for _, instance := range reservation.Instances {
			if aws.StringValue(instance.State.Name) == "terminated" {
				continue
			}
			return aws.StringValue(instance.InstanceId), nil
		}
	}
	return "", nil
}

func waitForInstanceRunning(ctx context.Context, logger logr.Logger, ec2Client *ec2.EC2, instanceID string) (string, error) {

	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	err := wait.PollImmediateUntil(5*time.Second, func() (bool, error) {
		err := ec2Client.WaitUntilInstanceRunningWithContext(ctx, &ec2.DescribeInstancesInput{
			InstanceIds: []*string{aws.String(instanceID)},
		})
		if err != nil {
			if awsErr, ok := err.(awserr.Error); ok {
				if awsErr.Code() == "ResourceNotReady" {
					return false, nil
				}
			}
			return false, fmt.Errorf("error waiting for instance running: %w", err)
		}
		return true, nil
	}, waitCtx.Done())
	if err != nil {
		return "", err
	}
	describeCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	result, err := ec2Client.DescribeInstancesWithContext(describeCtx, &ec2.DescribeInstancesInput{
		InstanceIds: []*string{aws.String(instanceID)},
	})
	if err != nil {
		return "", err
	}
	for _, res := range result.Reservations {
		for _, instance := range res.Instances {
			return aws.StringValue(instance.PublicIpAddress), nil
		}
	}
	return "", nil
}

func securityGroupName(infraID string) string {
	return fmt.Sprintf("%s-bastion-sg", infraID)
}

func vpcName(infraID string) string {
	return fmt.Sprintf("%s-vpc", infraID)
}

func keyPairName(infraID string) string {
	return fmt.Sprintf("%s-bastion", infraID)
}

func instanceName(infraID string) string {
	return fmt.Sprintf("%s-bastion", infraID)
}
