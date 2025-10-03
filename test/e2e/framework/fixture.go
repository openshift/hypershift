//go:build e2e
// +build e2e

package framework

// This file contains pure Ginkgo versions of test/e2e/util/fixture.go functions
// Surgically duplicated with testing.T replaced by Ginkgo logging

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/aws"
	"github.com/openshift/hypershift/cmd/cluster/azure"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/cluster/none"
	"github.com/openshift/hypershift/cmd/cluster/openstack"
	"github.com/openshift/hypershift/cmd/cluster/powervs"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"

	awssdk "github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/arn"
	"github.com/aws/aws-sdk-go/service/resourcegroupstaggingapi"

	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/go-logr/zapr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// DestroyCluster destroys a hosted cluster
// Pure Ginkgo version - surgically duplicated from util/fixture.go:destroyCluster
func DestroyCluster(ctx context.Context, hc *hyperv1.HostedCluster, createOpts *e2eutil.PlatformAgnosticOptions, outputDir string) error {
	GinkgoHelper()

	destroyLogFile := filepath.Join(outputDir, "destroy.log")
	destroyLog, err := os.Create(destroyLogFile)
	if err != nil {
		return fmt.Errorf("failed to destroy destroy log: %w", err)
	}
	destroyLogger := zap.New(zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.Lock(destroyLog), zap.DebugLevel))
	defer func() {
		if err := destroyLogger.Sync(); err != nil {
			fmt.Printf("failed to sync destroyLogger: %v\n", err)
		}
	}()

	opts := &core.DestroyOptions{
		Namespace:          hc.Namespace,
		Name:               hc.Name,
		InfraID:            createOpts.InfraID,
		ClusterGracePeriod: 15 * time.Minute,
		Log:                zapr.NewLogger(destroyLogger),
	}
	switch hc.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		opts.AWSPlatform = core.AWSPlatformDestroyOptions{
			BaseDomain:       createOpts.BaseDomain,
			Credentials:      createOpts.AWSPlatform.Credentials,
			PreserveIAM:      false,
			Region:           createOpts.AWSPlatform.Region,
			PostDeleteAction: validateAWSGuestResourcesDeletedFunc(ctx, hc.Spec.InfraID, createOpts.AWSPlatform.Credentials.AWSCredentialsFile, createOpts.AWSPlatform.Region),
		}
		return aws.DestroyCluster(ctx, opts)
	case hyperv1.NonePlatform, hyperv1.KubevirtPlatform:
		return none.DestroyCluster(ctx, opts)
	case hyperv1.AzurePlatform:
		opts.AzurePlatform = core.AzurePlatformDestroyOptions{
			CredentialsFile: createOpts.AzurePlatform.CredentialsFile,
			Location:        createOpts.AzurePlatform.Location,
		}
		return azure.DestroyCluster(ctx, opts)
	case hyperv1.PowerVSPlatform:
		opts.PowerVSPlatform = core.PowerVSPlatformDestroyOptions{
			BaseDomain:             createOpts.BaseDomain,
			ResourceGroup:          createOpts.PowerVSPlatform.ResourceGroup,
			Region:                 createOpts.PowerVSPlatform.Region,
			Zone:                   createOpts.PowerVSPlatform.Zone,
			VPCRegion:              createOpts.PowerVSPlatform.VPCRegion,
			CloudInstanceID:        createOpts.PowerVSPlatform.CloudInstanceID,
			VPC:                    createOpts.PowerVSPlatform.VPC,
			TransitGatewayLocation: createOpts.PowerVSPlatform.TransitGatewayLocation,
			TransitGateway:         createOpts.PowerVSPlatform.TransitGateway,
		}
		return powervs.DestroyCluster(ctx, opts)
	case hyperv1.OpenStackPlatform:
		return openstack.DestroyCluster(ctx, opts)

	default:
		return fmt.Errorf("unsupported cluster platform %s", hc.Spec.Platform.Type)
	}
}

// validateAWSGuestResourcesDeletedFunc waits for 15min or until the guest cluster resources are gone.
// Pure Ginkgo version - surgically duplicated from util/fixture.go:validateAWSGuestResourcesDeletedFunc
func validateAWSGuestResourcesDeletedFunc(ctx context.Context, infraID, awsCreds, awsRegion string) func() {
	if e2eutil.IsLessThan(e2eutil.Version415) {
		return func() {
			logf("SKIPPED: skipping AWS cleanup validation for OCP <= 4.14")
		}
	}

	return func() {
		awsSession := awsutil.NewSession("cleanup-validation", awsCreds, "", "", awsRegion)
		awsConfig := awsutil.NewConfig()
		taggingClient := resourcegroupstaggingapi.New(awsSession, awsConfig)
		var output *resourcegroupstaggingapi.GetResourcesOutput

		// Find load balancers, persistent volumes, or s3 buckets belonging to the guest cluster
		err := wait.PollUntilContextTimeout(ctx, 5*time.Second, 15*time.Minute, false, func(ctx context.Context) (bool, error) {
			// Filter get cluster resources.
			var err error
			output, err = taggingClient.GetResourcesWithContext(ctx, &resourcegroupstaggingapi.GetResourcesInput{
				ResourceTypeFilters: []*string{
					awssdk.String("elasticloadbalancing:loadbalancer"),
					awssdk.String("ec2:volume"),
					awssdk.String("s3"),
				},
				TagFilters: []*resourcegroupstaggingapi.TagFilter{
					{
						Key:    awssdk.String(clusterTag(infraID)),
						Values: []*string{awssdk.String("owned")},
					},
				},
			})
			if err != nil {
				return true, err
			}

			if hasGuestResources(output.ResourceTagMappingList) {
				return false, nil
			}

			return true, nil
		})

		if wait.Interrupted(err) {
			logf("Failed to wait for infra resources in guest cluster to be deleted: %v", err)
		} else if err != nil {
			// Failing to list tagged resource is not fatal, but we should log it
			logf("Failed to list resources by tag: %v. Not verifying cluster is cleaned up.", err)
			return
		}

		// Log resources that still exists
		if hasGuestResources(output.ResourceTagMappingList) {
			logf("Failed to clean up %d remaining resources for guest cluster", len(output.ResourceTagMappingList))
			for i := 0; i < len(output.ResourceTagMappingList); i++ {
				resourceARN, err := arn.Parse(awssdk.StringValue(output.ResourceTagMappingList[i].ResourceARN))
				if err != nil {
					// We are only decoding for additional information, proceed on error
					continue
				}
				logf("Resource: %s, tags: %s, service: %s",
					awssdk.StringValue(output.ResourceTagMappingList[i].ResourceARN), resourceTags(output.ResourceTagMappingList[i].Tags), resourceARN.Service)
			}
		} else {
			logf("SUCCESS: found no remaining guest resources")
		}
	}
}

func resourceTags(tags []*resourcegroupstaggingapi.Tag) string {
	tagStrings := make([]string, len(tags))
	for i, tag := range tags {
		tagStrings[i] = fmt.Sprintf("%s=%s", awssdk.StringValue(tag.Key), awssdk.StringValue(tag.Value))
	}
	return strings.Join(tagStrings, ",")
}

func hasGuestResources(resourceTagMappings []*resourcegroupstaggingapi.ResourceTagMapping) bool {
	for _, mapping := range resourceTagMappings {
		resourceARN, err := arn.Parse(awssdk.StringValue(mapping.ResourceARN))
		if err != nil {
			logf("WARNING: failed to parse ARN %s", awssdk.StringValue(mapping.ResourceARN))
			continue
		}
		if resourceARN.Service == "ec2" { // Resource is a volume, check whether it's a PV volume by looking at tags
			for _, tag := range mapping.Tags {
				if awssdk.StringValue(tag.Key) == "kubernetes.io/created-for/pv/name" {
					return true
				}
			}
			continue
		} else {
			return true
		}
	}
	return false
}

func clusterTag(infraID string) string {
	return fmt.Sprintf("kubernetes.io/cluster/%s", infraID)
}
