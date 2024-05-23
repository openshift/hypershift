package aws

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/util/errors"

	"github.com/openshift/hypershift/cmd/cluster/core"
	awsinfra "github.com/openshift/hypershift/cmd/infra/aws"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/cmd/util"
)

func NewDestroyCommand(opts *core.DestroyOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "aws",
		Short:        "Destroys a HostedCluster and its associated infrastructure on AWS",
		SilenceUsage: true,
	}

	opts.AWSPlatform = core.AWSPlatformDestroyOptions{
		PreserveIAM: false,
		Region:      "us-east-1",
	}

	cmd.Flags().BoolVar(&opts.AWSPlatform.PreserveIAM, "preserve-iam", opts.AWSPlatform.PreserveIAM, "If true, skip deleting IAM. Otherwise destroy any default generated IAM along with other infra.")
	cmd.Flags().StringVar(&opts.AWSPlatform.Region, "region", opts.AWSPlatform.Region, "Cluster's region; inferred from the hosted cluster by default")
	cmd.Flags().StringVar(&opts.AWSPlatform.BaseDomain, "base-domain", opts.AWSPlatform.BaseDomain, "Cluster's base domain; inferred from the hosted cluster by default")
	cmd.Flags().StringVar(&opts.AWSPlatform.BaseDomainPrefix, "base-domain-prefix", opts.AWSPlatform.BaseDomainPrefix, "Cluster's base domain prefix; inferred from the hosted cluster by default")
	cmd.Flags().StringVar(&opts.CredentialSecretName, "secret-creds", opts.CredentialSecretName, "A Kubernetes secret with a platform credential, pull-secret and base-domain. The secret must exist in the supplied \"--namespace\"")
	cmd.Flags().DurationVar(&opts.AWSPlatform.AwsInfraGracePeriod, "aws-infra-grace-period", opts.AWSPlatform.AwsInfraGracePeriod, "Timeout for destroying infrastructure in minutes")

	opts.AWSPlatform.AWSCredentialsOpts.BindFlags(cmd.Flags())

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		err := ValidateCredentialInfo(opts.AWSPlatform.AWSCredentialsOpts, opts.CredentialSecretName, opts.Namespace)
		if err != nil {
			return err
		}

		if err = DestroyCluster(cmd.Context(), opts); err != nil {
			log.Log.Error(err, "Failed to destroy cluster")
			return err
		}

		return nil
	}

	return cmd
}

func destroyPlatformSpecifics(ctx context.Context, o *core.DestroyOptions) error {
	if o.AWSPlatform.PostDeleteAction != nil {
		o.AWSPlatform.PostDeleteAction()
	}
	infraID := o.InfraID
	baseDomain := o.AWSPlatform.BaseDomain
	baseDomainPrefix := o.AWSPlatform.BaseDomainPrefix
	region := o.AWSPlatform.Region

	// Override the credentialSecret with credentialFile
	var err error
	var secretData *util.CredentialsSecretData
	if len(o.AWSPlatform.AWSCredentialsOpts.AWSCredentialsFile) == 0 && len(o.CredentialSecretName) > 0 {
		secretData, err = util.ExtractOptionsFromSecret(nil, o.CredentialSecretName, o.Namespace, "")
		if err != nil {
			return err
		}
	}

	o.Log.Info("Destroying infrastructure", "infraID", infraID)
	destroyInfraOpts := awsinfra.DestroyInfraOptions{
		Region:                region,
		InfraID:               infraID,
		AWSCredentialsOpts:    o.AWSPlatform.AWSCredentialsOpts,
		Name:                  o.Name,
		BaseDomain:            baseDomain,
		BaseDomainPrefix:      baseDomainPrefix,
		AwsInfraGracePeriod:   o.AWSPlatform.AwsInfraGracePeriod,
		Log:                   o.Log,
		CredentialsSecretData: secretData,
	}
	if err := destroyInfraOpts.Run(ctx); err != nil {
		return fmt.Errorf("failed to destroy infrastructure: %w", err)
	}

	if !o.AWSPlatform.PreserveIAM {
		o.Log.Info("Destroying IAM", "infraID", infraID)
		destroyOpts := awsinfra.DestroyIAMOptions{
			Region:                region,
			AWSCredentialsOpts:    o.AWSPlatform.AWSCredentialsOpts,
			InfraID:               infraID,
			Log:                   o.Log,
			CredentialsSecretData: secretData,
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

		if hostedCluster.Spec.DNS.BaseDomainPrefix != nil {
			if *hostedCluster.Spec.DNS.BaseDomainPrefix == "" {
				o.AWSPlatform.BaseDomainPrefix = "none"
			} else {
				o.AWSPlatform.BaseDomainPrefix = *hostedCluster.Spec.DNS.BaseDomainPrefix
			}
		}
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

// ValidateCredentialInfo validates if the credentials secret name is empty, the aws-creds or sts-creds mutually exclusive and are not empty; validates if
// the credentials secret is not empty, that it can be retrieved.
func ValidateCredentialInfo(opts awsutil.AWSCredentialsOptions, credentialSecretName, namespace string) error {
	if len(credentialSecretName) == 0 {
		if err := opts.Validate(); err != nil {
			return err
		}
		return nil
	}

	if opts.AWSCredentialsFile == "" {
		if err := util.ValidateRequiredOption("role-arn", opts.RoleArn); err != nil {
			return err
		}
	}
	// Check the secret exists now, otherwise stop
	if _, err := util.GetSecret(credentialSecretName, namespace); err != nil {
		return err
	}

	return nil
}
