package util

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/arn"
	"github.com/aws/aws-sdk-go/service/resourcegroupstaggingapi"
	"github.com/go-logr/zapr"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/aws"
	"github.com/openshift/hypershift/cmd/cluster/azure"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/cluster/none"
	"github.com/openshift/hypershift/cmd/cluster/openstack"
	"github.com/openshift/hypershift/cmd/cluster/powervs"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/test/e2e/util/dump"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// createClusterOpts mutates the cluster creation options according to the
// cluster's platform as necessary to deal with options the test caller doesn't
// know or care about in advance.
//
// TODO: Mutates the input, instead should use a copy of the input options
func createClusterOpts(ctx context.Context, client crclient.Client, hc *hyperv1.HostedCluster, opts *PlatformAgnosticOptions) (*PlatformAgnosticOptions, error) {
	opts.Namespace = hc.Namespace
	opts.Name = hc.Name

	switch hc.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		opts.InfraID = hc.Name
	case hyperv1.AzurePlatform:
		opts.InfraID = hc.Name
	case hyperv1.PowerVSPlatform:
		opts.InfraID = fmt.Sprintf("%s-infra", hc.Name)
	}

	return opts, nil
}

// createCluster calls the correct cluster create CLI function based on the
// cluster platform.
func createCluster(ctx context.Context, hc *hyperv1.HostedCluster, opts *PlatformAgnosticOptions, outputDir string) error {
	validCoreOpts, err := opts.RawCreateOptions.Validate(ctx)
	if err != nil {
		return fmt.Errorf("failed to validate core options: %w", err)
	}
	coreOpts, err := validCoreOpts.Complete()
	if err != nil {
		return fmt.Errorf("failed to complete core options: %w", err)
	}
	infraFile := filepath.Join(outputDir, "infrastructure.json")
	infraLogFile := filepath.Join(outputDir, "infrastructure.log")
	iamFile := filepath.Join(outputDir, "iam.json")
	iamLogFile := filepath.Join(outputDir, "iam.log")
	manifestsFile := filepath.Join(outputDir, "manifests.yaml")
	renderLogFile := filepath.Join(outputDir, "render.log")
	createLogFile := filepath.Join(outputDir, "create.log")

	infraLog, err := os.Create(infraLogFile)
	if err != nil {
		return fmt.Errorf("failed to create infra log: %w", err)
	}
	infraLogger := zap.New(zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.Lock(infraLog), zap.DebugLevel))
	defer func() {
		if err := infraLogger.Sync(); err != nil {
			fmt.Printf("failed to sync infraLogger: %v\n", err)
		}
	}()

	iamLog, err := os.Create(iamLogFile)
	if err != nil {
		return fmt.Errorf("failed to create iam log: %w", err)
	}
	iamLogger := zap.New(zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.Lock(iamLog), zap.DebugLevel))
	defer func() {
		if err := iamLogger.Sync(); err != nil {
			fmt.Printf("failed to sync iamLogger: %v\n", err)
		}
	}()

	switch hc.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		completer, err := opts.AWSPlatform.Validate(ctx, coreOpts)
		if err != nil {
			return fmt.Errorf("failed to validate AWS platform options: %w", err)
		}
		validOpts := completer.(*aws.ValidatedCreateOptions)

		infraOpts := aws.CreateInfraOptions(validOpts, coreOpts)
		infraOpts.OutputFile = infraFile
		infra, err := infraOpts.CreateInfra(ctx, zapr.NewLogger(infraLogger))
		if err != nil {
			return fmt.Errorf("failed to create infra: %w", err)
		}
		if err := infraOpts.Output(infra); err != nil {
			return fmt.Errorf("failed to write infra: %w", err)
		}

		client, err := util.GetClient()
		if err != nil {
			return err
		}
		iamOpts := aws.CreateIAMOptions(validOpts, infra)
		iamOpts.OutputFile = iamFile
		iam, err := iamOpts.CreateIAM(ctx, client, zapr.NewLogger(iamLogger))
		if err != nil {
			return fmt.Errorf("failed to create IAM: %w", err)
		}
		if err := iamOpts.Output(iam); err != nil {
			return fmt.Errorf("failed to write IAM: %w", err)
		}

		opts.InfrastructureJSON = infraFile
		opts.AWSPlatform.IAMJSON = iamFile
		return renderCreate(ctx, &opts.RawCreateOptions, &opts.AWSPlatform, manifestsFile, renderLogFile, createLogFile)
	case hyperv1.NonePlatform:
		return renderCreate(ctx, &opts.RawCreateOptions, &opts.NonePlatform, manifestsFile, renderLogFile, createLogFile)
	case hyperv1.KubevirtPlatform:
		return renderCreate(ctx, &opts.RawCreateOptions, &opts.KubevirtPlatform, manifestsFile, renderLogFile, createLogFile)
	case hyperv1.AzurePlatform:
		completer, err := opts.AzurePlatform.Validate(ctx, coreOpts)
		if err != nil {
			return fmt.Errorf("failed to validate Azure platform options: %w", err)
		}
		validOpts := completer.(*azure.ValidatedCreateOptions)

		infraOpts, err := azure.CreateInfraOptions(ctx, validOpts, coreOpts)
		if err != nil {
			return fmt.Errorf("failed to create infra options: %w", err)
		}
		infraOpts.OutputFile = infraFile
		if _, err := infraOpts.Run(ctx, zapr.NewLogger(infraLogger)); err != nil {
			return fmt.Errorf("failed to create infra: %w", err)
		}

		opts.InfrastructureJSON = infraFile
		return renderCreate(ctx, &opts.RawCreateOptions, &opts.AzurePlatform, manifestsFile, renderLogFile, createLogFile)
	case hyperv1.PowerVSPlatform:
		completer, err := opts.PowerVSPlatform.Validate(ctx, coreOpts)
		if err != nil {
			return fmt.Errorf("failed to validate PowerVS platform options: %w", err)
		}
		validOpts := completer.(*powervs.ValidatedCreateOptions)

		infraOpts, infra := powervs.CreateInfraOptions(validOpts, coreOpts)
		infraOpts.OutputFile = infraFile
		if err := infra.SetupInfra(ctx, zapr.NewLogger(infraLogger), infraOpts); err != nil {
			return fmt.Errorf("failed to setup infra: %w", err)
		}
		infraOpts.Output(infra, zapr.NewLogger(infraLogger))

		opts.InfrastructureJSON = infraFile
		return renderCreate(ctx, &opts.RawCreateOptions, &opts.PowerVSPlatform, manifestsFile, renderLogFile, createLogFile)
	case hyperv1.OpenStackPlatform:
		return renderCreate(ctx, &opts.RawCreateOptions, &opts.OpenStackPlatform, manifestsFile, renderLogFile, createLogFile)

	default:
		return fmt.Errorf("unsupported platform %s", hc.Spec.Platform.Type)
	}
}

func renderCreate(ctx context.Context, opts *core.RawCreateOptions, platformOpts core.PlatformValidator, outputFile string, renderLogFile string, createLogFile string) error {
	renderLog, err := os.Create(renderLogFile)
	if err != nil {
		return fmt.Errorf("failed to render render log: %w", err)
	}
	renderLogger := zap.New(zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.Lock(renderLog), zap.DebugLevel))
	defer func() {
		if err := renderLogger.Sync(); err != nil {
			fmt.Printf("failed to sync renderLogger: %v\n", err)
		}
	}()

	opts.Render = true
	opts.RenderInto = outputFile
	opts.Log = zapr.NewLogger(renderLogger)
	if err := core.CreateCluster(ctx, opts, platformOpts); err != nil {
		return fmt.Errorf("failed to render cluster manifests: %w", err)
	}

	createLog, err := os.Create(createLogFile)
	if err != nil {
		return fmt.Errorf("failed to create create log: %w", err)
	}
	createLogger := zap.New(zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.Lock(createLog), zap.DebugLevel))
	defer func() {
		if err := createLogger.Sync(); err != nil {
			fmt.Printf("failed to sync createLogger: %v\n", err)
		}
	}()

	opts.Render = false
	opts.RenderInto = ""
	opts.Log = zapr.NewLogger(createLogger)
	return core.CreateCluster(ctx, opts, platformOpts)
}

// destroyCluster calls the correct cluster destroy CLI function based on the
// cluster platform and the options used to create the cluster.
func destroyCluster(ctx context.Context, t *testing.T, hc *hyperv1.HostedCluster, createOpts *PlatformAgnosticOptions, outputDir string) error {
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
			PostDeleteAction: validateAWSGuestResourcesDeletedFunc(ctx, t, hc.Spec.InfraID, createOpts.AWSPlatform.Credentials.AWSCredentialsFile, createOpts.AWSPlatform.Region),
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
			CloudConnection:        createOpts.PowerVSPlatform.CloudConnection,
			VPC:                    createOpts.PowerVSPlatform.VPC,
			PER:                    createOpts.PowerVSPlatform.PER,
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
func validateAWSGuestResourcesDeletedFunc(ctx context.Context, t *testing.T, infraID, awsCreds, awsRegion string) func() {
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

			if hasGuestResources(t, output.ResourceTagMappingList) {
				return false, nil
			}

			return true, nil
		})

		if wait.Interrupted(err) {
			t.Errorf("Failed to wait for infra resources in guest cluster to be deleted: %v", err)
		} else if err != nil {
			// Failing to list tagged resource is not fatal, but we should log it
			t.Logf("Failed to list resources by tag: %v. Not verifying cluster is cleaned up.", err)
			return
		}

		// Log resources that still exists
		if hasGuestResources(t, output.ResourceTagMappingList) {
			t.Logf("Failed to clean up %d remaining resources for guest cluster", len(output.ResourceTagMappingList))
			for i := 0; i < len(output.ResourceTagMappingList); i++ {
				resourceARN, err := arn.Parse(awssdk.StringValue(output.ResourceTagMappingList[i].ResourceARN))
				if err != nil {
					// We are only decoding for additional information, proceed on error
					continue
				}
				t.Logf("Resource: %s, tags: %s, service: %s",
					awssdk.StringValue(output.ResourceTagMappingList[i].ResourceARN), resourceTags(output.ResourceTagMappingList[i].Tags), resourceARN.Service)
			}
		} else {
			t.Log("SUCCESS: found no remaining guest resources")
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

func hasGuestResources(t *testing.T, resourceTagMappings []*resourcegroupstaggingapi.ResourceTagMapping) bool {
	for _, mapping := range resourceTagMappings {
		resourceARN, err := arn.Parse(awssdk.StringValue(mapping.ResourceARN))
		if err != nil {
			t.Logf("WARNING: failed to parse ARN %s", awssdk.StringValue(mapping.ResourceARN))
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

// newClusterDumper returns a function that dumps important diagnostic data for
// a cluster based on the cluster's platform. The output directory will be named
// according to the test name. So, the returned dump function should be called
// at most once per unique test name.
func newClusterDumper(hc *hyperv1.HostedCluster, opts *PlatformAgnosticOptions, artifactDir string) func(ctx context.Context, t *testing.T, dumpGuestCluster bool) error {
	return func(ctx context.Context, t *testing.T, dumpGuestCluster bool) error {
		if len(artifactDir) == 0 {
			t.Logf("Skipping cluster dump because no artifact directory was provided")
			return nil
		}

		switch hc.Spec.Platform.Type {
		case hyperv1.AWSPlatform:
			var dumpErrors []error
			err := dump.DumpMachineConsoleLogs(ctx, hc, opts.AWSPlatform.Credentials, artifactDir)
			if err != nil {
				t.Logf("Failed saving machine console logs; this is nonfatal: %v", err)
			}
			err = dump.DumpHostedCluster(ctx, t, hc, dumpGuestCluster, artifactDir)
			if err != nil {
				dumpErrors = append(dumpErrors, fmt.Errorf("failed to dump hosted cluster: %w", err))
			}
			err = dump.DumpJournals(t, ctx, hc, artifactDir, opts.AWSPlatform.Credentials.AWSCredentialsFile)
			if err != nil {
				t.Logf("Failed to dump machine journals; this is nonfatal: %v", err)
			}
			return utilerrors.NewAggregate(dumpErrors)
		default:
			err := dump.DumpHostedCluster(ctx, t, hc, dumpGuestCluster, artifactDir)
			if err != nil {
				return fmt.Errorf("failed to dump hosted cluster: %w", err)
			}
			return nil
		}
	}
}

func artifactSubdirFor(t *testing.T) string {
	return strings.ReplaceAll(t.Name(), "/", "_")
}
