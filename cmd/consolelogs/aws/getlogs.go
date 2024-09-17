package aws

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/cmd/util"
)

type ConsoleLogOpts struct {
	Name               string
	Namespace          string
	AWSCredentialsOpts awsutil.AWSCredentialsOptions
	OutputDir          string
}

func NewCommand() *cobra.Command {

	opts := &ConsoleLogOpts{
		Namespace: "clusters",
	}

	cmd := &cobra.Command{
		Use:          "aws",
		Short:        "Get AWS machine instance console logs",
		SilenceUsage: true,
	}

	cmd.Flags().StringVarP(&opts.Namespace, "namespace", "n", opts.Namespace, "A cluster namespace")
	cmd.Flags().StringVar(&opts.Name, "name", opts.Name, "A cluster name")
	cmd.Flags().StringVar(&opts.OutputDir, "output-dir", opts.OutputDir, "Directory where to place console logs (required)")

	opts.AWSCredentialsOpts.BindFlags(cmd.Flags())

	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("output-dir")

	logger := log.Log
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		err := opts.AWSCredentialsOpts.Validate()
		if err != nil {
			return err
		}
		if err := opts.Run(cmd.Context()); err != nil {
			logger.Error(err, "Failed to get console logs")
			return err
		}
		logger.Info("Successfully retrieved console logs")
		return nil
	}

	return cmd
}

func (o *ConsoleLogOpts) Run(ctx context.Context) error {
	c, err := util.GetClient()
	if err != nil {
		return err
	}

	var hostedCluster hyperv1.HostedCluster
	if err := c.Get(ctx, types.NamespacedName{Namespace: o.Namespace, Name: o.Name}, &hostedCluster); err != nil {
		return fmt.Errorf("failed to get hostedcluster: %w", err)
	}
	infraID := hostedCluster.Spec.InfraID
	region := hostedCluster.Spec.Platform.AWS.Region

	awsSession, err := o.AWSCredentialsOpts.GetSession("cli-console-logs", nil, region)
	if err != nil {
		return err
	}
	awsConfig := awsutil.NewConfig()
	ec2Client := ec2.New(awsSession, awsConfig)

	// Fetch any instances belonging to the cluster
	instances, err := getEC2Instances(ctx, ec2Client, infraID)
	if err != nil {
		return fmt.Errorf("failed to get AWS instances: %w", err)
	}
	// get the console output
	if err := getInstanceConsoleOutput(ctx, ec2Client, instances, o.OutputDir); err != nil {
		return fmt.Errorf("failed to get instance console output: %w", err)
	}

	return nil
}

func getEC2Instances(ctx context.Context, ec2Client *ec2.EC2, infraID string) (map[string]string, error) {
	ctxWithTimeout, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	clusterTagFilter := fmt.Sprintf("tag:kubernetes.io/cluster/%s", infraID)
	clusterTagValue := "owned"
	output, err := ec2Client.DescribeInstancesWithContext(ctxWithTimeout, &ec2.DescribeInstancesInput{
		Filters: []*ec2.Filter{
			{
				Name:   &clusterTagFilter,
				Values: []*string{&clusterTagValue},
			},
		},
	})
	if err != nil {
		return nil, err
	}
	instances := map[string]string{}
	for _, r := range output.Reservations {
		for _, instance := range r.Instances {
			if aws.StringValue(instance.State.Name) == "running" {
				nameKey := aws.StringValue(instance.InstanceId)
				for _, tag := range instance.Tags {
					if aws.StringValue(tag.Key) == "Name" {
						nameKey = aws.StringValue(tag.Value)
					}
				}
				instances[nameKey] = aws.StringValue(instance.InstanceId)
			}
		}
	}
	return instances, nil
}

func getInstanceConsoleOutput(ctx context.Context, ec2Client *ec2.EC2, instances map[string]string, outputDir string) error {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return err
	}
	var errs []error
	for name, instanceID := range instances {
		ctxWithTimeout, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		output, err := ec2Client.GetConsoleOutputWithContext(ctxWithTimeout, &ec2.GetConsoleOutputInput{
			InstanceId: aws.String(instanceID),
		})
		if err != nil {
			errs = append(errs, err)
			continue
		}
		logOutput, err := base64.StdEncoding.DecodeString(aws.StringValue(output.Output))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if err := os.WriteFile(filepath.Join(outputDir, name+".log"), logOutput, 0644); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return utilerrors.NewAggregate(errs)
	}
	return nil
}
