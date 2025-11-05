//go:build reqserving
// +build reqserving

package reqservinge2e

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/util/reqserving"
	"go.uber.org/zap/zapcore"
	ctrl "sigs.k8s.io/controller-runtime"
	crzap "sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	// opts are global options for the request serving test suite
	globalOpts = &e2eutil.Options{}

	// testContext should be used as the parent context for any test code
	testContext context.Context

	log = crzap.New(crzap.UseDevMode(true), crzap.JSONEncoder(func(o *zapcore.EncoderConfig) {
		o.EncodeTime = zapcore.RFC3339TimeEncoder
	}))

	reqServingDryRun bool
)

// TestMain deals with global options and setting up a signal-bound context
// for all request serving tests to use.
func TestMain(m *testing.M) {
	ctrl.SetLogger(log)

	flag.StringVar(&globalOpts.ArtifactDir, "e2e.artifact-dir", "", "The directory where cluster resources and logs should be dumped. If empty, nothing is dumped")
	flag.StringVar(&globalOpts.LatestReleaseImage, "e2e.latest-release-image", "", "The latest OCP release image for use by tests")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.BaseDomain, "e2e.base-domain", "", "The ingress base domain for the cluster")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.ControlPlaneOperatorImage, "e2e.control-plane-operator-image", "", "The image to use for the control plane operator. If none specified, the default is used.")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.ExternalDNSDomain, "e2e.external-dns-domain", "", "domain that external-dns will use to create DNS records for HCP endpoints")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.PullSecretFile, "e2e.pull-secret-file", "", "path to pull secret")
	flag.Var(&globalOpts.ConfigurableClusterOptions.Annotations, "e2e.annotations", "Annotations to apply to the HostedCluster (key=value). Can be specified multiple times")
	flag.Var(&globalOpts.ConfigurableClusterOptions.ClusterCIDR, "e2e.cluster-cidr", "The CIDR of the cluster network. Can be specified multiple times.")
	flag.Var(&globalOpts.ConfigurableClusterOptions.ServiceCIDR, "e2e.service-cidr", "The CIDR of the service network. Can be specified multiple times.")
	flag.StringVar(&globalOpts.HyperShiftOperatorLatestImage, "e2e.hypershift-operator-latest-image", "quay.io/hypershift/hypershift-operator:latest", "The latest HyperShift Operator image to deploy. If e2e.hypershift-operator-initial-image is set (e.g. to run an upgrade test), this image will be considered the latest HyperShift Operator image to upgrade to.")
	flag.StringVar(&globalOpts.HOInstallationOptions.AWSPrivateCredentialsFile, "e2e.aws-private-credentials-file", "/etc/hypershift-pool-aws-credentials/credentials", "path to AWS private credentials. This is a HyperShift Operator installation option")
	flag.StringVar(&globalOpts.HOInstallationOptions.AWSPrivateRegion, "e2e.aws-private-region", "us-east-1", "AWS region where private clusters are supported by the HyperShift Operator. This is a HyperShift Operator installation option")
	flag.StringVar(&globalOpts.HOInstallationOptions.AWSOidcS3Credentials, "e2e.aws-oidc-s3-credentials", "/etc/hypershift-pool-aws-credentials/credentials", "AWS S3 credentials for the setup of the OIDC provider. This is a HyperShift Operator installation option")
	flag.StringVar(&globalOpts.HOInstallationOptions.AWSOidcS3Region, "e2e.aws-oidc-s3-region", "us-east-1", "AWS S3 region for the setup of the OIDC provider. This is a HyperShift Operator installation option")
	flag.StringVar(&globalOpts.HOInstallationOptions.ExternalDNSProvider, "e2e.external-dns-provider", "aws", "Provider to use for managing DNS records using external-dns. This is a HyperShift Operator installation option")
	flag.StringVar(&globalOpts.HOInstallationOptions.ExternalDNSDomainFilter, "e2e.external-dns-domain-filter", "service.ci.hypershift.devcluster.openshift.com", "restrict external-dns to changes within the specified domain. This is a HyperShift Operator installation option")
	flag.StringVar(&globalOpts.HOInstallationOptions.ExternalDNSCredentials, "e2e.external-dns-credentials", "/etc/hypershift-pool-aws-credentials/credentials", "path to credentials file to use for managing DNS records using external-dns. This is a HyperShift Operator installation option")
	flag.StringVar(&globalOpts.HOInstallationOptions.PlatformMonitoring, "e2e.platform-monitoring", "All", "The option for enabling platform cluster monitoring when installing the HyperShift Operator. Valid values are: None, OperatorOnly, All. This is a HyperShift Operator installation option")

	// AWS specific flags
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.AWSCredentialsFile, "e2e.aws-credentials-file", "", "path to AWS credentials")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.AWSOidcS3BucketName, "e2e.aws-oidc-s3-bucket-name", "", "AWS S3 Bucket Name to setup the OIDC provider in")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.Region, "e2e.aws-region", "us-east-1", "AWS region for clusters")
	flag.Var(&globalOpts.AdditionalTags, "e2e.additional-tags", "Additional tags to set on AWS resources")
	flag.BoolVar(&reqServingDryRun, "e2e.dry-run", false, "specify to only output YAMLs to be created in artifact dir")

	flag.Parse()

	// Request serving test only supports AWS platform and PublicAndPrivate endpoint access
	globalOpts.Platform = hyperv1.AWSPlatform
	globalOpts.ConfigurableClusterOptions.AWSEndpointAccess = string(hyperv1.PublicAndPrivate)

	// Set defaults for the test options
	if err := globalOpts.Complete(); err != nil {
		log.Error(err, "failed to set up global test options")
		os.Exit(1)
	}

	// Validate the test options
	if err := globalOpts.Validate(); err != nil {
		log.Error(err, "invalid global test options")
		os.Exit(1)
	}

	globalOpts.RequestServingIsolation = true

	os.Exit(main(m))
}

// main is used to allow us to use `defer` to defer cleanup tasks
func main(m *testing.M) int {
	// Set up a root context for all tests and set up signal handling
	var cancel context.CancelFunc
	testContext, cancel = context.WithCancel(context.Background())
	defer cancel()
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		log.Info("request serving tests received shutdown signal and will be cancelled")
		cancel()
	}()
	ctrl.LoggerInto(testContext, log)

	// Set the semantic version of the release image
	releaseImage := globalOpts.LatestReleaseImage
	if releaseImage != "" {
		err := e2eutil.SetReleaseImageVersion(testContext, releaseImage, globalOpts.ConfigurableClusterOptions.PullSecretFile)
		if err != nil {
			log.Error(err, "failed to set release image version")
			return 1
		}
	}

	// Setup dry-run output dir
	var dryRunOpts *reqserving.DryRunOptions
	if reqServingDryRun {
		dryRunOpts = &reqserving.DryRunOptions{
			Dir: fmt.Sprintf("%s/setup", globalOpts.ArtifactDir),
		}
		if globalOpts.ArtifactDir == "" {
			log.Error(fmt.Errorf("artifact directory must be specified for dry run"), "failed to setup dry run")
			return 1
		}
		if err := os.MkdirAll(dryRunOpts.Dir, 0o755); err != nil {
			log.Error(err, "failed to create setup dry run directory")
			return 1
		}
	}

	// Set up the management cluster
	log.Info("configuring management cluster")

	// Configure the AWS zone for the hosted cluster (single-zone)
	zones, err := reqserving.InferAWSAvailabilityZones(testContext)
	if err != nil {
		log.Error(err, "failed to infer AWS availability zones from management cluster")
		return 1
	}
	if len(zones) > 0 {
		_ = globalOpts.ConfigurableClusterOptions.Zone.Set(zones[0])
		log.Info("inferred AWS availability zone for test", "zone", zones[0])
	}

	// If a base domain was not provided, infer it from the management cluster
	if globalOpts.ConfigurableClusterOptions.BaseDomain == "" || globalOpts.ConfigurableClusterOptions.BaseDomain == e2eutil.DefaultCIBaseDomain {
		baseDomain, err := reqserving.InferBaseDomain(testContext, globalOpts.ConfigurableClusterOptions.AWSCredentialsFile)
		if err != nil {
			log.Error(err, "failed to infer base domain from management cluster")
			return 1
		}
		if baseDomain != "" {
			globalOpts.ConfigurableClusterOptions.BaseDomain = baseDomain
			log.Info("inferred base domain for tests")
		}
	}

	// Configure the management cluster
	if err := reqserving.ConfigureManagementCluster(testContext, dryRunOpts); err != nil {
		log.Error(err, "failed to configure management cluster")
		return 1
	}

	// Install the HyperShift operator
	log.Info("installing HyperShift operator")
	// Set default options
	globalOpts.HOInstallationOptions.EnableCPOOverrides = true
	globalOpts.HOInstallationOptions.EnableEtcdRecovery = true
	globalOpts.HOInstallationOptions.EnableDedicatedRequestServingIsolation = true
	globalOpts.HOInstallationOptions.EnableSizeTagging = true
	globalOpts.HOInstallationOptions.PrivatePlatform = string(hyperv1.AWSPlatform)
	if reqServingDryRun {
		globalOpts.HOInstallationOptions.DryRun = true
		globalOpts.HOInstallationOptions.DryRunDir = dryRunOpts.Dir
	}
	if err := e2eutil.InstallHyperShiftOperator(testContext, globalOpts.HOInstallationOptions); err != nil {
		log.Error(err, "failed to install HyperShift operator")
		return 1
	}
	log.Info("HyperShift operator installed successfully")

	// Configure ClusterSizingConfiguration for the management cluster
	log.Info("configuring ClusterSizingConfiguration for the management cluster")
	if err := reqserving.ConfigureClusterSizingConfiguration(testContext, dryRunOpts); err != nil {
		log.Error(err, "failed to configure ClusterSizingConfiguration")
		return 1
	}

	if reqServingDryRun {
		log.Info("dry run complete, exiting")
		return 0
	}

	// Verify that the request serving environment is configured correctly
	log.Info("verifying request serving environment")
	if err := reqserving.VerifyRequestServingEnvironment(testContext); err != nil {
		log.Error(err, "failed to verify request serving environment")
		return 1
	}
	log.Info("request serving environment verified successfully")

	if err := e2eutil.SetupSharedOIDCProvider(globalOpts, globalOpts.ArtifactDir); err != nil {
		log.Error(err, "failed to setup shared OIDC provider")
		return 1
	}
	defer e2eutil.CleanupSharedOIDCProvider(globalOpts, log)

	// Everything's okay to run tests
	log.Info("executing request serving e2e tests")
	return m.Run()
}
