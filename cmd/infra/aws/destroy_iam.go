package aws

import (
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/iam"
	"github.com/aws/aws-sdk-go/service/iam/iamiface"
	"github.com/spf13/cobra"
)

type DestroyIAMOptions struct {
	Region             string
	AWSCredentialsFile string
	ProfileName        string
}

func NewDestroyIAMCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "aws",
		Short: "Destroys AWS instance profile for workers",
	}

	opts := DestroyIAMOptions{
		Region:      "us-east-1",
		ProfileName: "hypershift-worker-profile",
	}

	cmd.Flags().StringVar(&opts.AWSCredentialsFile, "aws-creds", opts.AWSCredentialsFile, "Path to an AWS credentials file (required)")
	cmd.Flags().StringVar(&opts.ProfileName, "profile-name", opts.ProfileName, "Name of IAM instance profile to destroy")
	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "Region where cluster infra lives")

	cmd.MarkFlagRequired("aws-creds")

	cmd.Run = func(cmd *cobra.Command, args []string) {
		if err := opts.DestroyIAM(); err != nil {
			log.Error(err, "Error")
			os.Exit(1)
		}
	}

	return cmd
}

func (o *DestroyIAMOptions) DestroyIAM() error {
	var err error
	client, err := IAMClient(o.AWSCredentialsFile, o.Region)
	if err != nil {
		return err
	}
	return o.DestroyWorkerInstanceProfile(client)
}

func (o *DestroyIAMOptions) DestroyWorkerInstanceProfile(client iamiface.IAMAPI) error {
	instanceProfile, err := existingInstanceProfile(client, o.ProfileName)
	if err != nil {
		return fmt.Errorf("cannot check for existing instance profile: %w", err)
	}
	if instanceProfile != nil {
		for _, role := range instanceProfile.Roles {
			_, err := client.RemoveRoleFromInstanceProfile(&iam.RemoveRoleFromInstanceProfileInput{
				InstanceProfileName: aws.String(o.ProfileName),
				RoleName:            role.RoleName,
			})
			if err != nil {
				return fmt.Errorf("cannot remove role %s from instance profile %s: %w", aws.StringValue(role.RoleName), o.ProfileName, err)
			}
			log.Info("Removed role from instance profile", "profile", o.ProfileName, "role", aws.StringValue(role.RoleName))
		}
		_, err := client.DeleteInstanceProfile(&iam.DeleteInstanceProfileInput{
			InstanceProfileName: aws.String(o.ProfileName),
		})
		if err != nil {
			return fmt.Errorf("cannot delete instance profile %s: %w", o.ProfileName, err)
		}
		log.Info("Deleted instance profile", "profile", o.ProfileName)
	}
	roleName := fmt.Sprintf("%s-role", o.ProfileName)
	policyName := fmt.Sprintf("%s-policy", o.ProfileName)
	role, err := existingRole(client, roleName)
	if err != nil {
		return fmt.Errorf("cannot check for existing role: %w", err)
	}
	if role != nil {
		hasPolicy, err := existingRolePolicy(client, roleName, policyName)
		if err != nil {
			return fmt.Errorf("cannot check for existing role policy: %w", err)
		}
		if hasPolicy {
			_, err := client.DeleteRolePolicy(&iam.DeleteRolePolicyInput{
				PolicyName: aws.String(policyName),
				RoleName:   aws.String(roleName),
			})
			if err != nil {
				return fmt.Errorf("cannot delete role policy %s from role %s: %w", policyName, roleName, err)
			}
			log.Info("Deleted role policy", "role", roleName, "policy", policyName)
		}
		_, err = client.DeleteRole(&iam.DeleteRoleInput{
			RoleName: aws.String(roleName),
		})
		if err != nil {
			return fmt.Errorf("cannot delete role %s: %w", roleName, err)
		}
		log.Info("Deleted role", "role", roleName)
	}
	return nil
}
