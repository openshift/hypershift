package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/iam"
	"github.com/spf13/pflag"
)

var dryRun = false

func main() {
	awsConfig := aws.NewConfig()
	awsConfig.Region = aws.String("us-east-1")
	awsSession := session.Must(session.NewSession())
	ec2client := ec2.New(awsSession, awsConfig)

	ctx, cancel := context.WithCancel(context.Background())
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT)
	go func() {
		<-sigs
		cancel()
	}()

	// Add a "dry-run" flag to the command line using pflag
	pflag.BoolVar(&dryRun, "dry-run", dryRun, "Do not actually delete roles, just print what would be deleted")
	pflag.Parse()

	// List all VPCs
	var vpcs []*ec2.Vpc
	err := ec2client.DescribeVpcsPagesWithContext(ctx, &ec2.DescribeVpcsInput{}, func(page *ec2.DescribeVpcsOutput, lastPage bool) bool {
		vpcs = append(vpcs, page.Vpcs...)
		return !lastPage
	})
	if err != nil {
		log.Fatal(err)
	}

	// Get the list of infraIDs in use
	var infraIDsInUse []string
	for _, vpc := range vpcs {
		// Get the VPC name out of the tags
		for _, tag := range vpc.Tags {
			if *tag.Key == "Name" && len(strings.TrimSpace(*tag.Value)) > 0 {
				infraID := strings.TrimSuffix(*tag.Value, "-vpc")
				if len(infraID) > 0 {
					infraIDsInUse = append(infraIDsInUse, infraID)
				}
			}
		}
	}

	// Create an IAM client
	iamClient := iam.New(awsSession, awsConfig)

	// List all roles
	var roles []*iam.Role
	err = iamClient.ListRolesPagesWithContext(ctx, &iam.ListRolesInput{}, func(page *iam.ListRolesOutput, lastPage bool) bool {
		roles = append(roles, page.Roles...)
		return !lastPage
	})
	if err != nil {
		log.Fatal(err)
	}

	// Log number of roles
	log.Println("number of roles:", len(roles))

	// For each role, check if it is in use
	numDeleted := 0
	for _, role := range roles {
		roleName := *role.RoleName

		// Check if the role is in use
		roleInUse := false
		for _, infraID := range infraIDsInUse {
			if strings.HasPrefix(roleName, infraID) {
				roleInUse = true
				break
			}
		}
		if roleInUse {
			continue
		}

		// Filter known role suffixes
		if !strings.HasSuffix(roleName, "aws-ebs-csi-driver-controller") &&
			!strings.HasSuffix(roleName, "cloud-controller") &&
			!strings.HasSuffix(roleName, "cloud-network-config-controller") &&
			!strings.HasSuffix(roleName, "control-plane-operator") &&
			!strings.HasSuffix(roleName, "node-pool") &&
			!strings.HasSuffix(roleName, "openshift-image-registry") &&
			!strings.HasSuffix(roleName, "openshift-ingress") &&
			!strings.HasSuffix(roleName, "worker-role") {
			continue
		}

		log.Println("role not in use, deleting:", roleName)
		numDeleted++
		if !dryRun {
			// Delete any role policies
			output, err := iamClient.ListRolePoliciesWithContext(ctx, &iam.ListRolePoliciesInput{
				RoleName: &roleName,
			})
			if err != nil {
				log.Println("error listing role policies:", roleName, err)
			}
			for _, policyName := range output.PolicyNames {
				_, err := iamClient.DeleteRolePolicyWithContext(ctx, &iam.DeleteRolePolicyInput{
					PolicyName: policyName,
					RoleName:   &roleName,
				})
				if err != nil {
					log.Println("error deleting role policy:", roleName, *policyName, err)
				}
			}

			// Delete any managed policies attached to the role
			outputAttachedRoles, err := iamClient.ListAttachedRolePoliciesWithContext(ctx, &iam.ListAttachedRolePoliciesInput{
				RoleName: &roleName,
			})
			if err != nil {
				log.Println("error listing attached role policies:", roleName, err)
			}
			for _, policy := range outputAttachedRoles.AttachedPolicies {
				_, err := iamClient.DetachRolePolicyWithContext(ctx, &iam.DetachRolePolicyInput{
					PolicyArn: policy.PolicyArn,
					RoleName:  &roleName,
				})
				if err != nil {
					log.Println("error detaching role policy:", roleName, *policy.PolicyArn, err)
				}
			}

			// If the role name ends with worker-role, delete the instance profile
			if strings.HasSuffix(roleName, "worker-role") {
				instanceProfileName := strings.TrimSuffix(roleName, "-role")
				_, err := iamClient.RemoveRoleFromInstanceProfileWithContext(ctx, &iam.RemoveRoleFromInstanceProfileInput{
					InstanceProfileName: &instanceProfileName,
					RoleName:            &roleName,
				})
				if err != nil {
					log.Println("error removing role from instance profile:", roleName, err)
				}
				_, err = iamClient.DeleteInstanceProfileWithContext(ctx, &iam.DeleteInstanceProfileInput{
					InstanceProfileName: &instanceProfileName,
				})
				if err != nil {
					log.Println("error deleting instance profile:", roleName, err)
				}
			}

			_, err = iamClient.DeleteRoleWithContext(ctx, &iam.DeleteRoleInput{
				RoleName: &roleName,
			})
			if err != nil {
				log.Println("error deleting role:", roleName, err)
			}
		}
	}
	// show number of roles deleted
	log.Println("number of roles deleted:", numDeleted)
}
