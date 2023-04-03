package util

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/arn"
	"github.com/aws/aws-sdk-go/service/resourcegroupstaggingapi"
	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/aws"
	"github.com/openshift/hypershift/cmd/cluster/azure"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/cluster/kubevirt"
	"github.com/openshift/hypershift/cmd/cluster/none"
	"github.com/openshift/hypershift/cmd/cluster/powervs"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/test/e2e/util/dump"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// CreateCluster creates a new namespace and a HostedCluster in that namespace
// using the provided options.
//
// CreateCluster will install a teardown handler into the provided test T by
// calling T.Cleanup() with a function that destroys the cluster. This function
// will block until teardown completes. No explicit cluster cleanup logic is
// expected of the caller. Note that the teardown function explicitly ignores
// interruption and tries forever to do its work, the rationale being that we
// should do everything with can to release external resources with whatever
// time we have before being forcibly terminated.
//
// This function is intended (for now) to be the preferred default way of
// creating a hosted cluster during a test.
func CreateCluster(t *testing.T, ctx context.Context, client crclient.Client, opts *core.CreateOptions, platform hyperv1.PlatformType, artifactDir string, serviceAccountSigningKey []byte) *hyperv1.HostedCluster {
	g := NewWithT(t)
	start := time.Now()

	// Set up a namespace to contain the hostedcluster.
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: SimpleNameGenerator.GenerateName("e2e-clusters-"),
			Labels: map[string]string{
				"hypershift-e2e-component": "hostedclusters-namespace",
			},
		},
	}
	err := client.Create(ctx, namespace)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create namespace")

	// create serviceAccount signing key secret
	if len(serviceAccountSigningKey) > 0 {
		serviceAccountSigningKeySecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "e2e-sa-signing-key",
				Namespace: namespace.Name,
			},
			Type: corev1.SecretTypeOpaque,
			Data: map[string][]byte{
				hyperv1.ServiceAccountSigningKeySecretKey: serviceAccountSigningKey,
			},
		}
		err = client.Create(ctx, serviceAccountSigningKeySecret)
		g.Expect(err).NotTo(HaveOccurred(), "failed to create serviceAccountSigningKeySecret")

		originalBeforeApply := opts.BeforeApply
		opts.BeforeApply = func(o crclient.Object) {
			if originalBeforeApply != nil {
				originalBeforeApply(o)
			}

			switch v := o.(type) {
			case *hyperv1.HostedCluster:
				v.Spec.ServiceAccountSigningKey = &corev1.LocalObjectReference{
					Name: serviceAccountSigningKeySecret.Name,
				}
			}
		}
	}

	// Build the skeletal HostedCluster based on the provided platform.
	hc := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace.Name,
			Name:      SimpleNameGenerator.GenerateName("example-"),
		},
		Spec: hyperv1.HostedClusterSpec{
			Platform: hyperv1.PlatformSpec{
				Type: platform,
			},
		},
	}

	// Build options specific to the platform.
	opts, err = createClusterOpts(ctx, client, hc, opts)
	g.Expect(err).NotTo(HaveOccurred(), "failed to generate platform specific cluster options")

	// Try and create the cluster. If it fails, immediately try and clean up.
	t.Logf("Creating a new cluster. Options: %v", opts)
	if err := createCluster(ctx, hc, opts); err != nil {
		t.Logf("failed to create cluster, tearing down: %v", err)
		teardown(context.Background(), t, client, hc, opts, artifactDir)
		g.Expect(err).NotTo(HaveOccurred(), "failed to create cluster")
	}

	// Assert we can retrieve the cluster that was created. If this smoke check
	// fails, immediately try and clean up.
	if err := client.Get(ctx, crclient.ObjectKeyFromObject(hc), hc); err != nil {
		t.Logf("failed to get cluster that was created, tearing down: %v", err)
		teardown(context.Background(), t, client, hc, opts, artifactDir)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")
	}

	// Everything went well, so register the async cleanup handler and allow tests
	// to proceed.
	t.Logf("Successfully created hostedcluster %s/%s in %s", hc.Namespace, hc.Name, time.Since(start).Round(time.Second))

	t.Cleanup(func() { teardown(context.Background(), t, client, hc, opts, artifactDir) })

	t.Cleanup(func() { EnsureAllContainersHavePullPolicyIfNotPresent(t, context.Background(), client, hc) })
	t.Cleanup(func() { EnsureHCPContainersHaveResourceRequests(t, context.Background(), client, hc) })
	t.Cleanup(func() { EnsureNoPodsWithTooHighPriority(t, context.Background(), client, hc) })
	t.Cleanup(func() { NoticePreemptionOrFailedScheduling(t, context.Background(), client, hc) })
	t.Cleanup(func() { EnsureAllRoutesUseHCPRouter(t, context.Background(), client, hc) })

	return hc
}

// teardown will destroy the provided HostedCluster. If an artifact directory is
// provided, teardown will dump artifacts at various interesting points to aid
// in debugging.
//
// Note that most resource dumps are considered fatal to the tests. The reason
// is that these dumps are critical to our ability to debug issues in CI, and so
// we want to treat diagnostic dump failures as high priority bugs to resolve.
func teardown(ctx context.Context, t *testing.T, client crclient.Client, hc *hyperv1.HostedCluster, opts *core.CreateOptions, artifactDir string) {
	dumpCluster := newClusterDumper(hc, opts, artifactDir)

	// First, do a dump of the cluster before tearing it down
	t.Run("PreTeardownClusterDump", func(t *testing.T) {
		err := dumpCluster(ctx, t, true)
		if err != nil {
			t.Errorf("Failed to dump cluster: %v", err)
		}
	})

	// Try repeatedly to destroy the cluster gracefully. For each failure, dump
	// the current cluster to help debug teardown lifecycle issues.
	destroyAttempt := 1
	t.Run(fmt.Sprintf("DestroyCluster_%d", destroyAttempt), func(t *testing.T) {
		t.Logf("Waiting for cluster to be destroyed. Namespace: %s, name: %s", hc.Namespace, hc.Name)
		err := wait.PollImmediateUntil(5*time.Second, func() (bool, error) {
			err := destroyCluster(ctx, t, hc, opts)
			if err != nil {
				if strings.Contains(err.Error(), "required inputs are missing") {
					return false, err
				}
				if strings.Contains(err.Error(), "NoCredentialProviders") {
					return false, err
				}
				t.Logf("Failed to destroy cluster, will retry: %v", err)
				err := dumpCluster(ctx, t, false)
				if err != nil {
					t.Logf("Failed to dump cluster during destroy; this is nonfatal: %v", err)
				}
				destroyAttempt++
				return false, nil
			}
			return true, nil
		}, ctx.Done())
		if err != nil {
			t.Errorf("Failed to destroy cluster: %v", err)
		} else {
			t.Logf("Destroyed cluster. Namespace: %s, name: %s", hc.Namespace, hc.Name)
		}
	})

	// All clusters created during tests should ultimately conform to our API
	// budget. This should be checked after deletion to ensure that API operations
	// for the full lifecycle are accounted for.
	if !opts.SkipAPIBudgetVerification {
		EnsureAPIBudget(t, ctx, client, hc)
	}

	// Finally, delete the test namespace containing the HostedCluster/NodePool
	// resources.
	//
	// If the cluster was successfully destroyed and finalized, any further delay
	// in cleaning up the test namespace could be indicative of a resource
	// finalization bug. Give this namespace teardown a reasonable time to
	// complete and then dump resources to help debug.
	t.Run("DeleteTestNamespace", func(t *testing.T) {
		deleteTimeout, cancel := context.WithTimeout(ctx, 1*time.Minute)
		defer cancel()
		err := DeleteNamespace(t, deleteTimeout, client, hc.Name)
		if err != nil {
			t.Errorf("Failed to delete test namespace: %v", err)
			err := dumpCluster(ctx, t, false)
			if err != nil {
				t.Errorf("Failed to dump cluster: %v", err)
			}
		}
	})
}

// createClusterOpts mutates the cluster creation options according to the
// cluster's platform as necessary to deal with options the test caller doesn't
// know or care about in advance.
//
// TODO: Mutates the input, instead should use a copy of the input options
func createClusterOpts(ctx context.Context, client crclient.Client, hc *hyperv1.HostedCluster, opts *core.CreateOptions) (*core.CreateOptions, error) {
	opts.Namespace = hc.Namespace
	opts.Name = hc.Name
	opts.NonePlatform.ExposeThroughLoadBalancer = true

	switch hc.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		opts.InfraID = hc.Name
	case hyperv1.KubevirtPlatform:
		opts.KubevirtPlatform.RootVolumeSize = 16
	case hyperv1.PowerVSPlatform:
		opts.InfraID = fmt.Sprintf("%s-infra", hc.Name)
	}

	return opts, nil
}

// createCluster calls the correct cluster create CLI function based on the
// cluster platform.
func createCluster(ctx context.Context, hc *hyperv1.HostedCluster, opts *core.CreateOptions) error {
	switch hc.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		return aws.CreateCluster(ctx, opts)
	case hyperv1.NonePlatform:
		return none.CreateCluster(ctx, opts)
	case hyperv1.KubevirtPlatform:
		return kubevirt.CreateCluster(ctx, opts)
	case hyperv1.AzurePlatform:
		return azure.CreateCluster(ctx, opts)
	case hyperv1.PowerVSPlatform:
		return powervs.CreateCluster(ctx, opts)
	default:
		return fmt.Errorf("unsupported platform %s", hc.Spec.Platform.Type)
	}
}

// destroyCluster calls the correct cluster destroy CLI function based on the
// cluster platform and the options used to create the cluster.
func destroyCluster(ctx context.Context, t *testing.T, hc *hyperv1.HostedCluster, createOpts *core.CreateOptions) error {
	opts := &core.DestroyOptions{
		Namespace:          hc.Namespace,
		Name:               hc.Name,
		InfraID:            createOpts.InfraID,
		ClusterGracePeriod: 15 * time.Minute,
		Log:                NewLogr(t),
	}
	switch hc.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		opts.AWSPlatform = core.AWSPlatformDestroyOptions{
			BaseDomain:         createOpts.BaseDomain,
			AWSCredentialsFile: createOpts.AWSPlatform.AWSCredentialsFile,
			PreserveIAM:        false,
			Region:             createOpts.AWSPlatform.Region,
			PostDeleteAction:   validateAWSGuestResourcesDeletedFunc(ctx, t, hc.Spec.InfraID, createOpts.AWSPlatform.AWSCredentialsFile, createOpts.AWSPlatform.Region),
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
			BaseDomain:    createOpts.BaseDomain,
			ResourceGroup: createOpts.PowerVSPlatform.ResourceGroup,
			Region:        createOpts.PowerVSPlatform.Region,
			Zone:          createOpts.PowerVSPlatform.Zone,
			VPCRegion:     createOpts.PowerVSPlatform.VPCRegion,
		}
		return powervs.DestroyCluster(ctx, opts)

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

		// Find load balancers, persistent volumes, or s3 buckets belonging to the guest cluster
		err := wait.PollImmediate(5*time.Second, 15*time.Minute, func() (bool, error) {
			// Filter get cluster resources.
			output, err := taggingClient.GetResourcesWithContext(ctx, &resourcegroupstaggingapi.GetResourcesInput{
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
				t.Logf("WARNING: failed to list resources by tag: %v. Not verifying cluster is cleaned up.", err)
				return true, nil
			}

			// Log resources that still exists
			if hasGuestResources(t, output.ResourceTagMappingList) {
				t.Logf("WARNING: found %d remaining resources for guest cluster", len(output.ResourceTagMappingList))
				for i := 0; i < len(output.ResourceTagMappingList); i++ {
					resourceARN, err := arn.Parse(awssdk.StringValue(output.ResourceTagMappingList[i].ResourceARN))
					if err != nil {
						t.Logf("WARNING: failed to parse resource: %v. Not verifying cluster is cleaned up.", err)
						return false, nil
					}
					t.Logf("Resource: %s, tags: %s, service: %s",
						awssdk.StringValue(output.ResourceTagMappingList[i].ResourceARN), resourceTags(output.ResourceTagMappingList[i].Tags), resourceARN.Service)
				}
				return false, nil
			}

			t.Log("SUCCESS: found no remaining guest resources")
			return true, nil
		})

		if err != nil {
			t.Errorf("Failed to wait for infra resources in guest cluster to be deleted: %v", err)
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
func newClusterDumper(hc *hyperv1.HostedCluster, opts *core.CreateOptions, artifactDir string) func(ctx context.Context, t *testing.T, dumpGuestCluster bool) error {
	return func(ctx context.Context, t *testing.T, dumpGuestCluster bool) error {
		if len(artifactDir) == 0 {
			t.Logf("Skipping cluster dump because no artifact directory was provided")
			return nil
		}
		dumpDir := filepath.Join(artifactDir, strings.ReplaceAll(t.Name(), "/", "_"))

		switch hc.Spec.Platform.Type {
		case hyperv1.AWSPlatform:
			var dumpErrors []error
			err := dump.DumpMachineConsoleLogs(ctx, hc, opts.AWSPlatform.AWSCredentialsFile, dumpDir)
			if err != nil {
				t.Logf("Failed saving machine console logs; this is nonfatal: %v", err)
			}
			err = dump.DumpHostedCluster(ctx, t, hc, dumpGuestCluster, dumpDir)
			if err != nil {
				dumpErrors = append(dumpErrors, fmt.Errorf("failed to dump hosted cluster: %w", err))
			}
			err = dump.DumpJournals(t, ctx, hc, dumpDir, opts.AWSPlatform.AWSCredentialsFile)
			if err != nil {
				t.Logf("Failed to dump machine journals; this is nonfatal: %v", err)
			}
			return utilerrors.NewAggregate(dumpErrors)
		default:
			err := dump.DumpHostedCluster(ctx, t, hc, dumpGuestCluster, dumpDir)
			if err != nil {
				return fmt.Errorf("failed to dump hosted cluster: %w", err)
			}
			return nil
		}
	}
}
