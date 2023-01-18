//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"os"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/cmd/install"
	"github.com/openshift/hypershift/cmd/version"
	"github.com/openshift/hypershift/support/metrics"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
)

const (
	awsCredentialsSecretKey = "credentials"
	awsRegion               = "us-east-1"
)

// TestOperatorUpgrade implements a test that installs the latest HyperShift operator, upgrades the HyperShift operator
// based on the PR it's tested against, and ensures there were no restarted/crashed pods in the guest cluster during the upgrade.
func TestOperatorUpgrade(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	client, err := e2eutil.GetClient()
	g.Expect(err).NotTo(HaveOccurred(), "failed to get k8s client")

	// Create HC with a PublicAndPrivate endpoint access config
	clusterOpts := globalOpts.DefaultClusterOptions(t)
	clusterOpts.AWSPlatform.EndpointAccess = string(hyperv1.PublicAndPrivate)
	clusterOpts.AWSPlatform.InstanceType = "m5.xlarge"

	zones := strings.Split(globalOpts.configurableClusterOptions.Zone.String(), ",")
	if len(zones) >= 3 {
		// CreateCluster also tests multi-zone workers work properly if a sufficient number of zones are configured
		t.Logf("Sufficient zones available for InfrastructureAvailabilityPolicy HighlyAvailable")
		clusterOpts.AWSPlatform.Zones = zones
		clusterOpts.InfrastructureAvailabilityPolicy = string(hyperv1.HighlyAvailable)
		clusterOpts.NodePoolReplicas = 1
	}

	// Create a guest cluster
	hostedCluster := e2eutil.CreateCluster(t, ctx, client, &clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, globalOpts.ServiceAccountSigningKey)

	// Sanity check the cluster by waiting for the nodes to report ready
	t.Logf("Waiting for guest client to become available")
	guestClient := e2eutil.WaitForGuestClient(t, testContext, client, hostedCluster)

	// Wait for Nodes to be Ready
	numNodes := clusterOpts.NodePoolReplicas * int32(len(clusterOpts.AWSPlatform.Zones))
	e2eutil.WaitForNReadyNodes(t, testContext, guestClient, numNodes, hostedCluster.Spec.Platform.Type)

	// Wait for the rollout to be complete
	t.Logf("Waiting for cluster rollout. Image: %s", globalOpts.LatestReleaseImage)
	e2eutil.WaitForImageRollout(t, testContext, client, hostedCluster, globalOpts.LatestReleaseImage)
	err = client.Get(testContext, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get hosted cluster")

	e2eutil.EnsureNodeCountMatchesNodePoolReplicas(t, testContext, client, guestClient, hostedCluster.Namespace)
	e2eutil.EnsureNoCrashingPods(t, ctx, client, hostedCluster)
	e2eutil.EnsureNodeCommunication(t, ctx, client, hostedCluster)

	// Set installation options
	opts := install.Options{
		HyperShiftImage:                           version.HyperShiftImage,
		Namespace:                                 "hypershift",
		PrivatePlatform:                           string(hyperv1.AWSPlatform),
		OIDCStorageProviderS3BucketName:           "operator-upgrade-hosted-" + clusterOpts.AWSPlatform.Region,
		OIDCStorageProviderS3Credentials:          globalOpts.configurableClusterOptions.AWSCredentialsFile,
		OIDCStorageProviderS3Region:               awsRegion,
		OIDCStorageProviderS3CredentialsSecret:    "",
		OIDCStorageProviderS3CredentialsSecretKey: awsCredentialsSecretKey,
		EnableConversionWebhook:                   true,
		AWSPrivateCreds:                           globalOpts.configurableClusterOptions.AWSCredentialsFile,
		AWSPrivateRegion:                          awsRegion,
		AWSPrivateCredentialsSecret:               "",
		AWSPrivateCredentialsSecretKey:            awsCredentialsSecretKey,
		EnableValidatingWebhook:                   false,
	}

	// Ensure S3 bucket is available
	s3Details := make(map[string]string)
	s3Details["s3Creds"] = opts.OIDCStorageProviderS3Credentials
	s3Details["seedName"] = e2eutil.SimpleNameGenerator.GenerateName("")
	s3Details["s3Region"] = opts.OIDCStorageProviderS3Region

	if opts.OIDCStorageProviderS3BucketName == "" {
		bucket, err := e2eutil.CreateS3Bucket(ctx, s3Details)
		g.Expect(err).NotTo(HaveOccurred(), "error creating S3 bucket")
		opts.OIDCStorageProviderS3BucketName = bucket
		defer func() {
			awsConfig := &aws.Config{
				Region:      aws.String(s3Details["s3Region"]),
				Credentials: credentials.NewSharedCredentials(s3Details["s3Creds"], "default"),
			}
			if err := e2eutil.DeleteS3Bucket(ctx, *awsConfig, bucket); err != nil {
				t.Errorf("error deleting S3 bucket: %s", bucket)
			}
		}()
	}

	g.Expect(opts.OIDCStorageProviderS3BucketName).NotTo(BeEmpty(), "S3 bucket name parameter it's empty")
	s3Details["bucketName"] = opts.OIDCStorageProviderS3BucketName

	opts.ApplyDefaults()

	if os.Getenv("CI") == "true" {
		opts.PlatformMonitoring = metrics.PlatformMonitoringAll
		opts.EnableCIDebugOutput = true
	}

	// Deploy latest HyperShift operator on guest cluster
	t.Log("Deploying Latest Hypershift Version: " + version.HyperShiftImage)
	objects, err := install.HyperShiftOperatorManifests(opts)
	g.Expect(err).NotTo(HaveOccurred(), "failed to object conversion from installation options:", err)

	err = install.Apply(ctx, objects)
	g.Expect(err).NotTo(HaveOccurred(), "failed to apply hypershift installation manifests:", err)

	err = install.WaitUntilAvailable(ctx, opts)
	g.Expect(err).NotTo(HaveOccurred(), "failed waiting hypershift installation:", err)

	// Deploy CI HyperShift operator on guest cluster
	t.Log("Deploying CI Hypershift Version: " + globalOpts.configurableClusterOptions.CI_HyperShiftOperator)
	opts.HyperShiftImage = globalOpts.configurableClusterOptions.CI_HyperShiftOperator
	objects, err = install.HyperShiftOperatorManifests(opts)
	g.Expect(err).NotTo(HaveOccurred(), "failed to object conversion from installation options:", err)

	err = install.Apply(ctx, objects)
	g.Expect(err).NotTo(HaveOccurred(), "failed to apply CI hypershift installation manifests:", err)

	err = install.WaitUntilAvailable(ctx, opts)
	g.Expect(err).NotTo(HaveOccurred(), "failed waiting CI hypershift installation:", err)
}
