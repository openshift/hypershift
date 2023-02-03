//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/install"
	"github.com/openshift/hypershift/cmd/version"
	"github.com/openshift/hypershift/support/metrics"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"k8s.io/apimachinery/pkg/util/intstr"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	tmpDir = "/tmp"
)

var (
	StringTrue   string = "true"
	StringFalse  string = "false"
	OVNNamespace string = "openshift-ovn-kubernetes"
)

type DisasterRecoveryTestCase struct {
	name            string
	test            DisasterRecoveryTest
	manifestBuilder HostedClusterManifestBuilder
}

type DisasterRecoveryTest interface {
	Setup(t *testing.T)
	Run(t *testing.T, hostedCluster hyperv1.HostedCluster, hcNodePool hyperv1.NodePool)

	HostedClusterManifestBuilder
}

type HostedClusterManifestBuilder interface {
	BuildHostedClusterManifest() core.CreateOptions
}

func TestDisasterRecovery(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)
	ctx, cancel := context.WithCancel(testContext)
	etcdVars := map[string]string{
		"bin":              "/usr/bin/etcdctl",
		"CAPath":           "/etc/etcd/tls/etcd-ca/ca.crt",
		"CertPath":         "/etc/etcd/tls/client/etcd-client.crt",
		"CertKeyPath":      "/etc/etcd/tls/client/etcd-client.key",
		"snapshotPath":     "/var/lib/data/snapshot.db",
		"queryContentType": "application/x-compressed-tar",
	}

	defer func() {
		t.Log("Test group: Disaster Recovery finished")
		cancel()
	}()

	srcMgmtClient, err := e2eutil.GetClient()
	g.Expect(err).NotTo(HaveOccurred(), "failed to get Source MGMT Cluster client")
	srcMgmtConfig, err := e2eutil.GetConfig()
	g.Expect(err).NotTo(HaveOccurred(), "failed to get Source MGMT Cluster config")

	clusterOpts := globalOpts.DefaultClusterOptions(t)
	clusterOpts.BeforeApply = func(o crclient.Object) {
		nodePool, isNodepool := o.(*hyperv1.NodePool)
		if !isNodepool {
			return
		}
		nodePool.Spec.Management.Replace = &hyperv1.ReplaceUpgrade{
			Strategy: hyperv1.UpgradeStrategyRollingUpdate,
			RollingUpdate: &hyperv1.RollingUpdate{
				MaxUnavailable: func(v intstr.IntOrString) *intstr.IntOrString { return &v }(intstr.FromInt(1)),
				MaxSurge:       func(v intstr.IntOrString) *intstr.IntOrString { return &v }(intstr.FromInt(int(*nodePool.Spec.Replicas))),
			},
		}
	}

	// Create HC with a PublicAndPrivate endpoint access config
	clusterOpts.AWSPlatform.EndpointAccess = string(hyperv1.PublicAndPrivate)
	clusterOpts.AWSPlatform.InstanceType = "m5.xlarge"

	// This is our destination Management cluster
	dstMgmtCluster := e2eutil.CreateCluster(t, ctx, srcMgmtClient, &clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, globalOpts.ServiceAccountSigningKey)
	var url string
	if dstMgmtCluster.Spec.Platform.AWS.EndpointAccess == hyperv1.PublicAndPrivate {
		for _, service := range dstMgmtCluster.Spec.Services {
			if service.Service == hyperv1.APIServer {
				url = service.Route.Hostname
				break
			}
		}
	}
	_, err = e2eutil.WaitForDNS(t, ctx, url)
	g.Expect(err).NotTo(HaveOccurred(), "failed to reach DNS URL")

	dstMgmtClient := e2eutil.WaitForGuestClient(t, ctx, srcMgmtClient, dstMgmtCluster)

	// Reuse NodePool
	nodepools := &hyperv1.NodePoolList{}
	err = srcMgmtClient.List(ctx, nodepools, crclient.InNamespace(dstMgmtCluster.Namespace))
	g.Expect(err).NotTo(HaveOccurred(), "failed to list nodepools in namespace %s: %v", dstMgmtCluster.Namespace, err)
	g.Expect(len(nodepools.Items)).Should(Equal(1), "expected exactly one nodepool, got %d", len(nodepools.Items))

	// Wait for nodes to be Ready
	dstMgmtNodePool := &nodepools.Items[0]
	dstMgmtNumNodes := clusterOpts.NodePoolReplicas
	dstMgmtNodes := e2eutil.WaitForNReadyNodesByNodePool(t, ctx, dstMgmtClient, dstMgmtNumNodes, dstMgmtCluster.Spec.Platform.Type, dstMgmtNodePool.Name)
	t.Logf("Destination Management Cluster %s Nodes Ready: %d", dstMgmtCluster.Name, len(dstMgmtNodes))
	t.Logf("waiting for Destination MGMT cluster to rollout. Image: %s", globalOpts.LatestReleaseImage)
	e2eutil.WaitForImageRollout(t, ctx, srcMgmtClient, dstMgmtCluster, globalOpts.LatestReleaseImage)

	// Install Hypershift in the dstMgmtCluster
	opts := install.Options{
		AdditionalTrustBundle:                     "",
		Namespace:                                 "hypershift",
		HyperShiftImage:                           version.HyperShiftImage,
		ImageRefsFile:                             "",
		HyperShiftOperatorReplicas:                2,
		Development:                               false,
		EnableValidatingWebhook:                   false,
		EnableConversionWebhook:                   true,
		ExcludeEtcdManifests:                      false,
		PrivatePlatform:                           string(hyperv1.AWSPlatform),
		AWSPrivateCreds:                           globalOpts.configurableClusterOptions.AWSCredentialsFile,
		AWSPrivateCredentialsSecret:               "",
		AWSPrivateCredentialsSecretKey:            "credentials",
		AWSPrivateRegion:                          globalOpts.configurableClusterOptions.Region,
		OIDCStorageProviderS3Region:               globalOpts.configurableClusterOptions.Region,
		OIDCStorageProviderS3BucketName:           "",
		OIDCStorageProviderS3Credentials:          globalOpts.configurableClusterOptions.AWSCredentialsFile,
		OIDCStorageProviderS3CredentialsSecret:    "",
		OIDCStorageProviderS3CredentialsSecretKey: "credentials",
		ExternalDNSProvider:                       strings.ToLower(string(globalOpts.Platform)),
		ExternalDNSCredentials:                    globalOpts.configurableClusterOptions.AWSCredentialsFile,
		ExternalDNSDomainFilter:                   globalOpts.configurableClusterOptions.ExternalDNSDomain,
		ExternalDNSTxtOwnerId:                     "",
		EnableAdminRBACGeneration:                 false,
		MetricsSet:                                metrics.MetricsSetTelemetry,
		EnableUWMTelemetryRemoteWrite:             true,
		RHOBSMonitoring:                           false,
	}

	s3Details := make(map[string]string)
	s3Details["s3Creds"] = opts.OIDCStorageProviderS3Credentials
	s3Details["queryContentType"] = etcdVars["queryContentType"]
	s3Details["s3Region"] = opts.OIDCStorageProviderS3Region

	opts.ApplyDefaults()

	if os.Getenv("CI") == "true" {
		opts.PlatformMonitoring = metrics.PlatformMonitoringAll
		opts.EnableCIDebugOutput = true
	}

	t.Log("Deploying Hypershift")
	objects, err := install.HyperShiftOperatorManifests(opts)
	g.Expect(err).NotTo(HaveOccurred(), "failed to object conversion from installation options:", err)

	err = install.Apply(ctx, objects, dstMgmtClient)
	g.Expect(err).NotTo(HaveOccurred(), "failed to apply hypershift installation manifests:", err)

	err = install.WaitUntilAvailable(ctx, opts, dstMgmtClient)
	g.Expect(err).NotTo(HaveOccurred(), "failed waiting hypershift installation:", err)

	DisasterRecoveryTests := []DisasterRecoveryTestCase{
		{
			name: "TestDRMigrationPublicAndPrivate",
			test: NewDRMigrationPublicAndPrivateTest(ctx, srcMgmtClient, srcMgmtConfig, dstMgmtCluster, dstMgmtClient, etcdVars, clusterOpts, s3Details),
		},
		{
			name: "TestDRMigrationPrivate",
			test: NewDRMigrationPrivateTest(ctx, srcMgmtClient, srcMgmtConfig, dstMgmtCluster, dstMgmtClient, etcdVars, clusterOpts, s3Details),
		},
	}

	t.Run("Disaster Recovery Tests Group", func(t *testing.T) {
		for _, testCase := range DisasterRecoveryTests {
			t.Run(testCase.name, func(t *testing.T) {
				executeDisasterRecoveryTest(t, ctx, srcMgmtClient, testCase.test, testCase.manifestBuilder, s3Details, opts)
			})
		}
	})
}

func executeDisasterRecoveryTest(t *testing.T, ctx context.Context, srcMgmtClient crclient.Client, disasterRecoveryTest DisasterRecoveryTest, manifestBuilder HostedClusterManifestBuilder, s3Details map[string]string, opts install.Options) {
	t.Parallel()

	disasterRecoveryTest.Setup(t)
	g := NewWithT(t)

	// Customize Cluster Name
	s3Details["seedName"] = e2eutil.SimpleNameGenerator.GenerateName("")

	// Create Bucket (if necessary) for the testCase
	if opts.OIDCStorageProviderS3BucketName == "" {
		awsConfig := &aws.Config{
			Region:      aws.String(s3Details["s3Region"]),
			Credentials: credentials.NewSharedCredentials(s3Details["s3Creds"], "default"),
		}
		bucket := fmt.Sprintf("%s-%s-%s", "test-disaster-recovery", s3Details["s3Region"], s3Details["seedName"])

		err := e2eutil.CreateS3Bucket(ctx, *awsConfig, bucket)
		g.Expect(err).NotTo(HaveOccurred(), "error creating S3 bucket:", err)
		opts.OIDCStorageProviderS3BucketName = bucket
		defer func() {
			if err := e2eutil.DeleteS3Bucket(ctx, *awsConfig, bucket); err != nil {
				t.Errorf("error deleting S3 bucket %s: %v", bucket, err)
			}
		}()
	}

	g.Expect(opts.OIDCStorageProviderS3BucketName).NotTo(BeEmpty(), "S3 bucket name parameter it's empty")
	s3Details["bucketName"] = opts.OIDCStorageProviderS3BucketName

	if manifestBuilder == nil {
		manifestBuilder = disasterRecoveryTest
	}
	hostedClusterOpts := manifestBuilder.BuildHostedClusterManifest()

	// This is our HostedCluster, which will be migrated to a destination cluster
	hostedCluster := e2eutil.CreateCluster(t, ctx, srcMgmtClient, &hostedClusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, globalOpts.ServiceAccountSigningKey)
	var url string
	if hostedCluster.Spec.Platform.AWS.EndpointAccess == hyperv1.PublicAndPrivate {
		for _, service := range hostedCluster.Spec.Services {
			if service.Service == hyperv1.APIServer {
				url = service.Route.Hostname
				break
			}
		}
	}
	_, err := e2eutil.WaitForDNS(t, ctx, url)
	g.Expect(err).NotTo(HaveOccurred(), "failed to reach DNS URL")
	t.Logf("waiting for HostedCluster to rollout. Image: %s", globalOpts.LatestReleaseImage)
	e2eutil.WaitForImageRollout(t, ctx, srcMgmtClient, hostedCluster, globalOpts.LatestReleaseImage)

	// Grab the HC's NodePool
	hcNodePools := &hyperv1.NodePoolList{}
	err = srcMgmtClient.List(ctx, hcNodePools, crclient.InNamespace(hostedCluster.Namespace))
	g.Expect(err).NotTo(HaveOccurred(), "failed to list nodepools in namespace %s: %v", hostedCluster.Namespace, err)
	g.Expect(len(hcNodePools.Items)).Should(Equal(1), "expected exactly one nodepool, got %d", len(hcNodePools.Items))
	hcNodePool := &hcNodePools.Items[0]

	t.Logf("waiting for HostedCluster to rollout. Image: %s", globalOpts.LatestReleaseImage)
	e2eutil.WaitForImageRollout(t, ctx, srcMgmtClient, hostedCluster, globalOpts.LatestReleaseImage)

	// run test validations
	disasterRecoveryTest.Run(t, *hostedCluster, *hcNodePool)
}
