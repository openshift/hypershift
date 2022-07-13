package aws

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/util/errors"

	"github.com/openshift/hypershift/cmd/cluster/core"
	awsinfra "github.com/openshift/hypershift/cmd/infra/aws"
	"github.com/openshift/hypershift/cmd/log"
)

func NewDestroyCommand(opts *core.DestroyOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "aws",
		Short:        "Destroys a HostedCluster and its associated infrastructure on AWS.",
		SilenceUsage: true,
	}

	opts.AWSPlatform = core.AWSPlatformDestroyOptions{
		AWSCredentialsFile: "",
		PreserveIAM:        false,
		Region:             "us-east-1",
	}

	cmd.Flags().StringVar(&opts.AWSPlatform.AWSCredentialsFile, "aws-creds", opts.AWSPlatform.AWSCredentialsFile, "Path to an AWS credentials file (required)")
	cmd.Flags().BoolVar(&opts.AWSPlatform.PreserveIAM, "preserve-iam", opts.AWSPlatform.PreserveIAM, "If true, skip deleting IAM. Otherwise destroy any default generated IAM along with other infra.")
	cmd.Flags().StringVar(&opts.AWSPlatform.Region, "region", opts.AWSPlatform.Region, "Cluster's region; inferred from the hosted cluster by default")
	cmd.Flags().StringVar(&opts.AWSPlatform.BaseDomain, "base-domain", opts.AWSPlatform.BaseDomain, "Cluster's base domain; inferred from the hosted cluster by default")

	cmd.MarkFlagRequired("aws-creds")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := DestroyCluster(cmd.Context(), opts); err != nil {
			log.Log.Error(err, "Failed to destroy cluster")
			return err
		}

		return nil
	}

	return cmd
}

func destroyPlatformSpecifics(ctx context.Context, o *core.DestroyOptions) error {
	infraID := o.InfraID
	baseDomain := o.AWSPlatform.BaseDomain
	region := o.AWSPlatform.Region

	o.Log.Info("Destroying infrastructure", "infraID", infraID)
	destroyInfraOpts := awsinfra.DestroyInfraOptions{
		Region:             region,
		InfraID:            infraID,
		AWSCredentialsFile: o.AWSPlatform.AWSCredentialsFile,
		Name:               o.Name,
		BaseDomain:         baseDomain,
		Log:                o.Log,
	}
	if err := destroyInfraOpts.Run(ctx); err != nil {
		return fmt.Errorf("failed to destroy infrastructure: %w", err)
	}

	if !o.AWSPlatform.PreserveIAM {
		o.Log.Info("Destroying IAM", "infraID", infraID)
		destroyOpts := awsinfra.DestroyIAMOptions{
			Region:             region,
			AWSCredentialsFile: o.AWSPlatform.AWSCredentialsFile,
			InfraID:            infraID,
			Log:                o.Log,
		}
		if err := destroyOpts.Run(ctx); err != nil {
			return fmt.Errorf("failed to destroy IAM: %w", err)
		}
	}
	return nil
}

func DestroyCluster(ctx context.Context, o *core.DestroyOptions) error {

	hostedCluster, err := core.GetCluster(ctx, o)
	if err != nil {
		return err
	}
	if hostedCluster != nil {
		o.InfraID = hostedCluster.Spec.InfraID
		o.AWSPlatform.Region = hostedCluster.Spec.Platform.AWS.Region
		o.AWSPlatform.BaseDomain = hostedCluster.Spec.DNS.BaseDomain
	}

	var inputErrors []error
	if len(o.InfraID) == 0 {
		inputErrors = append(inputErrors, fmt.Errorf("infrastructure ID is required"))
	}
	if len(o.AWSPlatform.BaseDomain) == 0 {
		inputErrors = append(inputErrors, fmt.Errorf("base domain is required"))
	}
	if len(o.AWSPlatform.Region) == 0 {
		inputErrors = append(inputErrors, fmt.Errorf("region is required"))
	}
	if err := errors.NewAggregate(inputErrors); err != nil {
		return fmt.Errorf("required inputs are missing: %w", err)
	}

	return core.DestroyCluster(ctx, hostedCluster, o, destroyPlatformSpecifics)
}
