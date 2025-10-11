//go:build e2e
// +build e2e

package e2ev2

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2ev2/framework"
	"go.uber.org/zap/zapcore"
	ctrl "sigs.k8s.io/controller-runtime"
	crzap "sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	// Global test configuration and options
	testOpts = &framework.TestOptions{}

	// Root context for all tests, cancelled on signals
	testContext context.Context
	testCancel  context.CancelFunc

	// Logger for the test suite
	log = crzap.New(crzap.UseDevMode(true), crzap.JSONEncoder(func(o *zapcore.EncoderConfig) {
		o.EncodeTime = zapcore.RFC3339TimeEncoder
	}))

	// Test framework instance
	testFramework *framework.Framework
)

func TestE2EV2(t *testing.T) {
	RegisterFailHandler(Fail)

	// Configure Ginkgo suites with reporters
	suiteConfig, reporterConfig := GinkgoConfiguration()

	// Add JUnit reporter if artifact directory is specified
	if testOpts.ArtifactDir != "" {
		junitPath := filepath.Join(testOpts.ArtifactDir, "junit-e2ev2.xml")
		reporterConfig.JUnitReport = junitPath
	}

	RunSpecs(t, "HyperShift E2E v2 Suite", suiteConfig, reporterConfig)
}

var _ = BeforeSuite(func() {
	log.Info("Starting HyperShift E2E v2 test suite")

	// Initialize the test framework
	var err error
	testFramework, err = framework.NewFramework(testOpts, log)
	Expect(err).NotTo(HaveOccurred(), "Failed to initialize test framework")

	// Perform global setup
	err = testFramework.Setup(testContext)
	Expect(err).NotTo(HaveOccurred(), "Failed to setup test framework")

	log.Info("Test framework initialized successfully")
})

var _ = AfterSuite(func() {
	log.Info("Cleaning up HyperShift E2E v2 test suite")

	if testFramework != nil {
		err := testFramework.Cleanup(testContext)
		if err != nil {
			log.Error(err, "Failed to cleanup test framework")
		}
	}

	log.Info("Test suite cleanup completed")
})

func init() {
	// Set up signal handling and root context
	testContext, testCancel = context.WithCancel(context.Background())

	// Handle signals gracefully
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		log.Info("Test suite received shutdown signal, cancelling tests")
		testCancel()
	}()

	// Set up controller-runtime logger
	ctrl.SetLogger(log)

	// Define command line flags
	defineFlags()
}

func defineFlags() {
	// Platform-agnostic flags
	flag.StringVar(&testOpts.ArtifactDir, "e2e.artifact-dir", "", "Directory where test artifacts should be stored")
	flag.StringVar(&testOpts.Platform, "e2e.platform", string(hyperv1.AWSPlatform), "Platform to test against (aws, azure, kubevirt, etc.)")
	flag.StringVar(&testOpts.BaseDomain, "e2e.base-domain", "", "Base domain for cluster ingress")
	flag.StringVar(&testOpts.PullSecretFile, "e2e.pull-secret-file", "", "Path to pull secret file")
	flag.StringVar(&testOpts.SSHKeyFile, "e2e.ssh-key-file", "", "Path to SSH public key file")
	flag.StringVar(&testOpts.LatestReleaseImage, "e2e.latest-release-image", "", "Latest OCP release image")
	flag.StringVar(&testOpts.PreviousReleaseImage, "e2e.previous-release-image", "", "Previous OCP release image")
	flag.IntVar(&testOpts.NodePoolReplicas, "e2e.node-pool-replicas", 2, "Number of replicas for each node pool")
	flag.DurationVar(&testOpts.ClusterCreationTimeout, "e2e.cluster-creation-timeout", 30*time.Minute, "Timeout for cluster creation")
	flag.DurationVar(&testOpts.NodePoolReadyTimeout, "e2e.nodepool-ready-timeout", 15*time.Minute, "Timeout for nodepool to become ready")

	// AWS-specific flags
	flag.StringVar(&testOpts.AWSCredentialsFile, "e2e.aws-credentials-file", "", "Path to AWS credentials file")
	flag.StringVar(&testOpts.AWSRegion, "e2e.aws-region", "us-east-1", "AWS region")
	flag.StringVar(&testOpts.AWSOidcS3BucketName, "e2e.aws-oidc-s3-bucket-name", "", "AWS S3 bucket name for OIDC provider")

	// Azure-specific flags
	flag.StringVar(&testOpts.AzureCredentialsFile, "e2e.azure-credentials-file", "", "Path to Azure credentials file")
	flag.StringVar(&testOpts.AzureLocation, "e2e.azure-location", "eastus", "Azure location")

	// KubeVirt-specific flags
	flag.StringVar(&testOpts.KubeVirtInfraKubeconfigFile, "e2e.kubevirt-infra-kubeconfig", "", "Path to KubeVirt infra cluster kubeconfig")
	flag.StringVar(&testOpts.KubeVirtInfraNamespace, "e2e.kubevirt-infra-namespace", "", "KubeVirt infra namespace")

	// Test behavior flags
	flag.BoolVar(&testOpts.SkipTeardown, "e2e.skip-teardown", false, "Skip cluster teardown after tests")
	flag.BoolVar(&testOpts.ParallelTests, "e2e.parallel-tests", false, "Run tests in parallel")
	flag.StringVar(&testOpts.TestFilter, "e2e.test-filter", "", "Regular expression to filter which tests to run")
}

// TestMain handles flag parsing and validation
func TestMain(m *testing.M) {
	// Parse flags
	flag.Parse()

	// Validate and complete test options
	if err := testOpts.Complete(); err != nil {
		log.Error(err, "Failed to complete test options")
		os.Exit(1)
	}

	if err := testOpts.Validate(); err != nil {
		log.Error(err, "Invalid test options")
		os.Exit(1)
	}

	log.Info("Test configuration validated", "platform", testOpts.Platform, "artifactDir", testOpts.ArtifactDir)

	// Set the release image version for version gating tests
	releaseImage := testOpts.PreviousReleaseImage
	if releaseImage == "" {
		releaseImage = testOpts.LatestReleaseImage
	}
	if releaseImage != "" {
		err := util.SetReleaseImageVersion(testContext, releaseImage, testOpts.PullSecretFile)
		if err != nil {
			log.Error(err, "Failed to set release image version")
			os.Exit(1)
		}
	}

	// Run tests
	code := m.Run()

	// Cleanup
	testCancel()

	os.Exit(code)
}