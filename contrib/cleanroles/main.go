package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/spf13/pflag"
)

var dryRun = false

func main() {
	cfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		log.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT)
	go func() {
		<-sigs
		cancel()
	}()

	// Add a "dry-run" flag to the command line using pflag
	pflag.BoolVar(&dryRun, "dry-run", dryRun, "Print the roles that would be deleted, but do not delete them")
	pflag.Parse()

	// List all VPCs
	var vpcs []types.Vpc
	for _, region := range []string{"us-east-1", "us-east-2", "us-west-1", "us-west-2", "eu-west-1"} {
		regionCfg := cfg.Copy()
		regionCfg.Region = region
		ec2client := ec2.NewFromConfig(regionCfg)

		paginator := ec2.NewDescribeVpcsPaginator(ec2client, &ec2.DescribeVpcsInput{})
		for paginator.HasMorePages() {
			page, err := paginator.NextPage(ctx)
			if err != nil {
				log.Fatal(err)
			}
			vpcs = append(vpcs, page.Vpcs...)
		}
	}

	// Get the list of infraIDs in use
	var infraIDsInUse []string
	for _, vpc := range vpcs {
		// Get the VPC name out of the tags
		for _, tag := range vpc.Tags {
			if aws.ToString(tag.Key) == "Name" && len(strings.TrimSpace(aws.ToString(tag.Value))) > 0 {
				infraID := strings.TrimSuffix(aws.ToString(tag.Value), "-vpc")
				if len(infraID) > 0 {
					infraIDsInUse = append(infraIDsInUse, infraID)
				}
			}
		}
	}

	log.Println("infra IDs in use:", infraIDsInUse)

	// Create an IAM client
	iamClient := iam.NewFromConfig(cfg)

	// List all roles
	var roles []iamtypes.Role
	paginator := iam.NewListRolesPaginator(iamClient, &iam.ListRolesInput{})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			log.Fatal(err)
		}
		roles = append(roles, page.Roles...)
	}

	// Log number of roles
	log.Println("number of roles:", len(roles))

	// For each role, check if it is in use
	numDeleted := 0
	for _, role := range roles {
		// if role CreateDate is less than 1 day old, skip it
		if role.CreateDate != nil && role.CreateDate.Add(24*time.Hour).After(time.Now()) {
			continue
		}

		roleName := aws.ToString(role.RoleName)

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

		// Skip if roleName does not match any of these suffixes (component role names)
		if !strings.HasSuffix(roleName, "aws-ebs-csi-driver-controller") &&
			!strings.HasSuffix(roleName, "cloud-controller") &&
			!strings.HasSuffix(roleName, "cloud-network-config-controller") &&
			!strings.HasSuffix(roleName, "control-plane-operator") &&
			!strings.HasSuffix(roleName, "node-pool") &&
			!strings.HasSuffix(roleName, "openshift-image-registry") &&
			!strings.HasSuffix(roleName, "openshift-ingress") &&
			!strings.HasSuffix(roleName, "worker-role") &&
			!strings.HasSuffix(roleName, "worker-ROSA-Worker-Role") {
			continue
		}

		// Skip if roleName does not match any of these prefixes (test cases names)
		if !strings.HasPrefix(roleName, "autoscaling") &&
			!strings.HasPrefix(roleName, "azure-scheduler") &&
			!strings.HasPrefix(roleName, "ha-etcd-chaos") &&
			!strings.HasPrefix(roleName, "control-plane-upgrade") &&
			!strings.HasPrefix(roleName, "request-serving-isolation") &&
			!strings.HasPrefix(roleName, "custom-config") &&
			!strings.HasPrefix(roleName, "none") &&
			!strings.HasPrefix(roleName, "private") &&
			!strings.HasPrefix(roleName, "external-oidc") &&
			!strings.HasPrefix(roleName, "karpenter") &&
			!strings.HasPrefix(roleName, "node-pool") &&
			!strings.HasPrefix(roleName, "olm") &&
			!strings.HasPrefix(roleName, "ho-upgrade") &&
			!strings.HasPrefix(roleName, "private") &&
			!strings.HasPrefix(roleName, "proxy") &&
			!strings.HasPrefix(roleName, "create-cluster") {
			continue
		}

		log.Println("role not in use, deleting:", roleName)
		numDeleted++
		if !dryRun {
			// Delete any role policies
			output, err := iamClient.ListRolePolicies(ctx, &iam.ListRolePoliciesInput{
				RoleName: aws.String(roleName),
			})
			if err != nil {
				log.Println("error listing role policies:", roleName, err)
			}
			for _, policyName := range output.PolicyNames {
				_, err := iamClient.DeleteRolePolicy(ctx, &iam.DeleteRolePolicyInput{
					PolicyName: aws.String(policyName),
					RoleName:   aws.String(roleName),
				})
				if err != nil {
					log.Println("error deleting role policy:", roleName, policyName, err)
				}
			}

			// Delete any managed policies attached to the role
			outputAttachedRoles, err := iamClient.ListAttachedRolePolicies(ctx, &iam.ListAttachedRolePoliciesInput{
				RoleName: aws.String(roleName),
			})
			if err != nil {
				log.Println("error listing attached role policies:", roleName, err)
			}
			for _, policy := range outputAttachedRoles.AttachedPolicies {
				_, err := iamClient.DetachRolePolicy(ctx, &iam.DetachRolePolicyInput{
					PolicyArn: policy.PolicyArn,
					RoleName:  aws.String(roleName),
				})
				if err != nil {
					log.Println("error detaching role policy:", roleName, aws.ToString(policy.PolicyArn), err)
				}
			}

			// If the role name ends with worker-role, delete the instance profile
			if strings.HasSuffix(roleName, "worker-role") || strings.HasSuffix(roleName, "worker-ROSA-Worker-Role") {
				instanceProfileName := strings.TrimSuffix(roleName, "-role")
				instanceProfileName = strings.TrimSuffix(instanceProfileName, "-ROSA-Worker-Role")
				_, err := iamClient.RemoveRoleFromInstanceProfile(ctx, &iam.RemoveRoleFromInstanceProfileInput{
					InstanceProfileName: aws.String(instanceProfileName),
					RoleName:            aws.String(roleName),
				})
				if err != nil {
					log.Println("error removing role from instance profile:", roleName, err)
				}
				_, err = iamClient.DeleteInstanceProfile(ctx, &iam.DeleteInstanceProfileInput{
					InstanceProfileName: aws.String(instanceProfileName),
				})
				if err != nil {
					log.Println("error deleting instance profile:", roleName, err)
				}
			}

			_, err = iamClient.DeleteRole(ctx, &iam.DeleteRoleInput{
				RoleName: aws.String(roleName),
			})
			if err != nil {
				log.Println("error deleting role:", roleName, err)
			}
		}
	}
	// show number of roles deleted
	log.Println("number of roles deleted:", numDeleted)
}
