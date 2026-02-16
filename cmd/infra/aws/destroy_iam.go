package aws

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/support/awsapi"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
)

type DestroyIAMOptions struct {
	Region             string
	AWSCredentialsOpts awsutil.AWSCredentialsOptions
	InfraID            string
	Log                logr.Logger

	VPCOwnerCredentialsOpts      awsutil.AWSCredentialsOptions
	PrivateZonesInClusterAccount bool

	CredentialsSecretData *util.CredentialsSecretData
}

func NewDestroyIAMCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "aws",
		Short:        "Destroys AWS instance profile for workers",
		SilenceUsage: true,
	}

	opts := DestroyIAMOptions{
		Region:  "us-east-1",
		InfraID: "",
		Log:     log.Log,
	}

	cmd.Flags().StringVar(&opts.InfraID, "infra-id", opts.InfraID, "Infrastructure ID to use for AWS resources.")
	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "Region where cluster infra lives")
	cmd.Flags().BoolVar(&opts.PrivateZonesInClusterAccount, "private-zones-in-cluster-account", opts.PrivateZonesInClusterAccount, "In shared VPC infrastructure, delete roles for private hosted zones from cluster account")

	opts.AWSCredentialsOpts.BindFlags(cmd.Flags())
	opts.VPCOwnerCredentialsOpts.BindVPCOwnerFlags(cmd.Flags())

	_ = cmd.MarkFlagRequired("infra-id")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		err := opts.AWSCredentialsOpts.Validate()
		if err != nil {
			return err
		}
		if err := opts.DestroyIAM(cmd.Context()); err != nil {
			return err
		}
		opts.Log.Info("Successfully destroyed IAM infra")
		return nil
	}

	return cmd
}

func (o *DestroyIAMOptions) Run(ctx context.Context) error {
	return wait.PollUntilContextCancel(ctx, 5*time.Second, true, func(ctx context.Context) (bool, error) {
		err := o.DestroyIAM(ctx)
		if err != nil {
			if !awsutil.IsErrorRetryable(err) {
				return false, err
			}
			o.Log.Info("WARNING: error during destroy, will retry", "error", err.Error())
			return false, nil
		}
		return true, nil
	})
}

func (o *DestroyIAMOptions) DestroyIAM(ctx context.Context) error {
	awsSession, err := o.AWSCredentialsOpts.GetSessionV2(ctx, "cli-destroy-iam", o.CredentialsSecretData, o.Region)
	if err != nil {
		return err
	}
	awsConfig := awsutil.NewConfigV2()
	iamClient := iam.NewFromConfig(*awsSession, func(o *iam.Options) {
		o.Retryer = awsConfig()
	})

	err = o.DestroyOIDCResources(ctx, iamClient)
	if err != nil {
		return err
	}
	err = o.DestroyWorkerInstanceProfile(ctx, iamClient)
	if err != nil {
		return err
	}

	if o.VPCOwnerCredentialsOpts.AWSCredentialsFile != "" {
		vpcOwnerAWSSession, err := o.VPCOwnerCredentialsOpts.GetSessionV2(ctx, "cli-destroy-iam", nil, o.Region)
		if err != nil {
			return err
		}
		vpcOwnerIAMClient := iam.NewFromConfig(*vpcOwnerAWSSession, func(o *iam.Options) {
			o.Retryer = awsConfig()
		})
		if err = o.DestroySharedVPCRoles(ctx, iamClient, vpcOwnerIAMClient); err != nil {
			return err
		}
	}

	return nil
}

func (o *DestroyIAMOptions) DestroyOIDCResources(ctx context.Context, iamClient awsapi.IAMAPI) error {
	oidcProviderList, err := iamClient.ListOpenIDConnectProviders(ctx, &iam.ListOpenIDConnectProvidersInput{})
	if err != nil {
		return err
	}

	for _, provider := range oidcProviderList.OpenIDConnectProviderList {
		// OIDC Provider ARN is of the form arn:aws:iam::<account-id>:oidc-provider/hypershift-ci-2-oidc.s3.us-east-1.amazonaws.com/<infra-id>
		arnTokens := strings.Split(*provider.Arn, "/")
		arnInfraID := arnTokens[len(arnTokens)-1]
		if arnInfraID == o.InfraID {
			_, err := iamClient.DeleteOpenIDConnectProvider(ctx, &iam.DeleteOpenIDConnectProviderInput{
				OpenIDConnectProviderArn: provider.Arn,
			})
			if err != nil {
				var nse *iamtypes.NoSuchEntityException
				if !errors.As(err, &nse) {
					o.Log.Error(err, "Error deleting OIDC provider", "providerARN", provider.Arn)
					return err
				}
			} else {
				o.Log.Info("Deleted OIDC provider", "providerARN", provider.Arn)
			}
			break
		}
	}

	// Delete the shared role
	removed := false
	if removed, err = o.DestroyOIDCRole(ctx, iamClient, "shared-role"); err != nil {
		return err
	}
	if removed {
		// The cluster was created with a single shared role, so we are done.
		// Save on additional API calls and just return here.
		return nil
	}
	// Delete individual component roles
	if err = o.DestroyOIDCRoleWithRetry(ctx, iamClient, "openshift-ingress"); err != nil {
		return err
	}
	if _, err = o.DestroyOIDCRole(ctx, iamClient, "openshift-image-registry"); err != nil {
		return err
	}
	if _, err = o.DestroyOIDCRole(ctx, iamClient, "aws-ebs-csi-driver-controller"); err != nil {
		return err
	}
	if _, err = o.DestroyOIDCRole(ctx, iamClient, "cloud-controller"); err != nil {
		return err
	}
	if _, err = o.DestroyOIDCRole(ctx, iamClient, "node-pool"); err != nil {
		return err
	}
	if _, err = o.DestroyOIDCRole(ctx, iamClient, "control-plane-operator"); err != nil {
		return err
	}
	if _, err = o.DestroyOIDCRole(ctx, iamClient, "cloud-network-config-controller"); err != nil {
		return err
	}
	if _, err = o.DestroyOIDCRole(ctx, iamClient, "kms-provider"); err != nil {
		return err
	}
	if _, err = o.DestroyOIDCRole(ctx, iamClient, "karpenter"); err != nil {
		return err
	}

	return nil
}

// DestroyOIDCRoleWithRetry retries the entire DestroyOIDCRole operation if it fails due to attached policies
func (o *DestroyIAMOptions) DestroyOIDCRoleWithRetry(ctx context.Context, client awsapi.IAMAPI, name string) error {
	return wait.PollUntilContextTimeout(ctx, 10*time.Second, 3*time.Minute, true, func(ctx context.Context) (bool, error) {
		_, err := o.DestroyOIDCRole(ctx, client, name)
		if err != nil {
			// Check if the error message indicates a delete conflict
			if strings.Contains(err.Error(), "DeleteConflict") {
				o.Log.Info("Role deletion failed due to attached policies, retrying entire operation", "role", fmt.Sprintf("%s-%s", o.InfraID, name))
				return false, nil
			}
			return false, err
		}
		return true, nil
	})
}

// DestroyOIDCRole deletes an IAM Role with all its policies
func (o *DestroyIAMOptions) DestroyOIDCRole(ctx context.Context, client awsapi.IAMAPI, name string) (removed bool, reterr error) {
	roleName := fmt.Sprintf("%s-%s", o.InfraID, name)
	role, err := existingRole(ctx, client, roleName)
	if err != nil {
		return false, fmt.Errorf("cannot check for existing role: %w", err)
	}

	if role == nil {
		o.Log.Info("Role already deleted!", "role", roleName)
		return false, nil
	}

	// Detach managed policies
	attachedPolicies, err := client.ListAttachedRolePolicies(ctx, &iam.ListAttachedRolePoliciesInput{
		RoleName: aws.String(roleName),
	})
	if err != nil {
		return false, fmt.Errorf("failed to list attached policies for role %s: %w", roleName, err)
	}

	for _, policy := range attachedPolicies.AttachedPolicies {
		_, err = client.DetachRolePolicy(ctx, &iam.DetachRolePolicyInput{
			PolicyArn: policy.PolicyArn,
			RoleName:  aws.String(roleName),
		})
		if err != nil {
			return false, fmt.Errorf("failed to detach policy %s from role %s: %w", *policy.PolicyArn, roleName, err)
		}
		o.Log.Info("Detached role policy", "role", roleName, "policy", *policy.PolicyArn)
	}

	// List and delete all inline policies
	listPoliciesOutput, err := client.ListRolePolicies(ctx, &iam.ListRolePoliciesInput{
		RoleName: aws.String(roleName),
	})
	if err != nil {
		return false, fmt.Errorf("failed to list inline policies for role %s: %w", roleName, err)
	}

	for _, policyName := range listPoliciesOutput.PolicyNames {
		_, err := client.DeleteRolePolicy(ctx, &iam.DeleteRolePolicyInput{
			PolicyName: aws.String(policyName),
			RoleName:   aws.String(roleName),
		})
		if err != nil {
			var nse *iamtypes.NoSuchEntityException
			if !errors.As(err, &nse) {
				o.Log.Error(err, "Error deleting role policy", "role", roleName, "policy", policyName)
				return false, err
			}
		} else {
			o.Log.Info("Deleted role policy", "role", roleName, "policy", policyName)
		}
	}

	// Delete the role
	_, err = client.DeleteRole(ctx, &iam.DeleteRoleInput{
		RoleName: aws.String(roleName),
	})
	if err != nil {
		return false, fmt.Errorf("failed to delete role %s: %w", roleName, err)
	}
	o.Log.Info("Deleted role", "role", roleName)

	return true, nil
}

func (o *DestroyIAMOptions) DestroyWorkerInstanceProfile(ctx context.Context, client awsapi.IAMAPI) error {
	profileName := DefaultProfileName(o.InfraID)
	instanceProfile, err := existingInstanceProfile(ctx, client, profileName)
	if err != nil {
		return fmt.Errorf("cannot check for existing instance profile: %w", err)
	}
	if instanceProfile != nil {
		for _, role := range instanceProfile.Roles {
			_, err := client.RemoveRoleFromInstanceProfile(ctx, &iam.RemoveRoleFromInstanceProfileInput{
				InstanceProfileName: aws.String(profileName),
				RoleName:            role.RoleName,
			})
			if err != nil {
				return fmt.Errorf("cannot remove role %s from instance profile %s: %w", aws.ToString(role.RoleName), profileName, err)
			}
			o.Log.Info("Removed role from instance profile", "profile", profileName, "role", aws.ToString(role.RoleName))
		}
		_, err := client.DeleteInstanceProfile(ctx, &iam.DeleteInstanceProfileInput{
			InstanceProfileName: aws.String(profileName),
		})
		if err != nil {
			return fmt.Errorf("cannot delete instance profile %s: %w", profileName, err)
		}
		o.Log.Info("Deleted instance profile", "profile", profileName)
	}
	roleName := fmt.Sprintf("%s-role", profileName)
	policyName := fmt.Sprintf("%s-policy", profileName)
	role, err := existingRole(ctx, client, roleName)
	if err != nil {
		return fmt.Errorf("cannot check for existing role: %w", err)
	}
	if role != nil {
		hasPolicy, err := existingRolePolicy(ctx, client, roleName, policyName)
		if err != nil {
			return fmt.Errorf("cannot check for existing role policy: %w", err)
		}
		if hasPolicy {
			_, err := client.DeleteRolePolicy(ctx, &iam.DeleteRolePolicyInput{
				PolicyName: aws.String(policyName),
				RoleName:   aws.String(roleName),
			})
			if err != nil {
				return fmt.Errorf("cannot delete role policy %s from role %s: %w", policyName, roleName, err)
			}
			o.Log.Info("Deleted role policy", "role", roleName, "policy", policyName)
		}
		_, err = client.DeleteRole(ctx, &iam.DeleteRoleInput{
			RoleName: aws.String(roleName),
		})
		if err != nil {
			return fmt.Errorf("cannot delete role %s: %w", roleName, err)
		}
		o.Log.Info("Deleted role", "role", roleName)
	}

	// when using ROSA managed policies to create iam, the worker role name will have ROSAWorkerRoleNameSuffix suffix.
	roleName = fmt.Sprintf("%s-%s", profileName, ROSAWorkerRoleNameSuffix)
	role, err = existingRole(ctx, client, roleName)
	if err != nil {
		return fmt.Errorf("cannot check for existing role: %w", err)
	}
	if role != nil {
		attachedPolicies, err := client.ListAttachedRolePolicies(ctx, &iam.ListAttachedRolePoliciesInput{
			RoleName: aws.String(roleName),
		})
		if err != nil {
			return fmt.Errorf("failed to list attached policies for role %s: %w", roleName, err)
		}

		for _, policy := range attachedPolicies.AttachedPolicies {
			_, err = client.DetachRolePolicy(ctx, &iam.DetachRolePolicyInput{
				PolicyArn: policy.PolicyArn,
				RoleName:  aws.String(roleName),
			})
			if err != nil {
				return fmt.Errorf("failed to detach policy %s from role %s: %w", *policy.PolicyArn, roleName, err)
			}
			o.Log.Info("Detached role policy", "role", roleName, "policy", *policy.PolicyArn)
		}

		_, err = client.DeleteRole(ctx, &iam.DeleteRoleInput{
			RoleName: aws.String(roleName),
		})
		if err != nil {
			return fmt.Errorf("failed to delete role %s: %w", roleName, err)
		}
		o.Log.Info("Deleted role", "role", roleName)
	}

	return nil
}

func (o *DestroyIAMOptions) DestroySharedVPCRoles(ctx context.Context, iamClient, vpcOwnerIAMClient awsapi.IAMAPI) error {
	var err error
	ingressRoleClient := vpcOwnerIAMClient
	if o.PrivateZonesInClusterAccount {
		ingressRoleClient = iamClient
	}
	if _, err = o.DestroyOIDCRole(ctx, ingressRoleClient, "shared-vpc-ingress"); err != nil {
		return err
	}
	if _, err = o.DestroyOIDCRole(ctx, vpcOwnerIAMClient, "shared-vpc-control-plane"); err != nil {
		return err
	}
	return nil
}
