//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/go-logr/logr"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/cluster/kubevirt"
	awsinfra "github.com/openshift/hypershift/cmd/infra/aws"
	"github.com/openshift/hypershift/cmd/version"
	"github.com/openshift/hypershift/test/e2e/podtimingcontroller"
	"github.com/openshift/hypershift/test/e2e/util"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap/zapcore"
	apierr "k8s.io/apimachinery/pkg/util/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/oidc"
)

var (
	// opts are global options for the test suite bound in TestMain.
	globalOpts = &options{}

	// testContext should be used as the parent context for any test code, and will
	// be cancelled if a SIGINT or SIGTERM is received. It's set up in TestMain.
	testContext context.Context

	log = zap.New(zap.UseDevMode(true), zap.JSONEncoder(func(o *zapcore.EncoderConfig) {
		o.EncodeTime = zapcore.RFC3339TimeEncoder
	}))
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

// TestMain deals with global options and setting up a signal-bound context
// for all tests to use.
func TestMain(m *testing.M) {
	flag.StringVar(&globalOpts.configurableClusterOptions.AWSCredentialsFile, "e2e.aws-credentials-file", "", "path to AWS credentials")
	flag.StringVar(&globalOpts.configurableClusterOptions.Region, "e2e.aws-region", "us-east-1", "AWS region for clusters")
	flag.Var(&globalOpts.configurableClusterOptions.Zone, "e2e.aws-zones", "Deprecated, use -e2e.availability-zones instead")
	flag.Var(&globalOpts.configurableClusterOptions.Zone, "e2e.availability-zones", "Availability zones for clusters")
	flag.StringVar(&globalOpts.configurableClusterOptions.AWSOidcS3BucketName, "e2e.aws-oidc-s3-bucket-name", "", "AWS S3 Bucket Name to setup the OIDC provider in")
	flag.StringVar(&globalOpts.configurableClusterOptions.AWSKmsKeyAlias, "e2e.aws-kms-key-alias", "", "AWS KMS Key Alias to use when creating encrypted nodepools, when empty the default EBS KMS Key will be used")
	flag.StringVar(&globalOpts.configurableClusterOptions.PullSecretFile, "e2e.pull-secret-file", "", "path to pull secret")
	flag.StringVar(&globalOpts.configurableClusterOptions.AWSEndpointAccess, "e2e.aws-endpoint-access", "", "endpoint access profile for the cluster")
	flag.StringVar(&globalOpts.configurableClusterOptions.ExternalDNSDomain, "e2e.external-dns-domain", "", "domain that external-dns will use to create DNS records for HCP endpoints")
	flag.StringVar(&globalOpts.configurableClusterOptions.KubeVirtContainerDiskImage, "e2e.kubevirt-container-disk-image", "", "DEPRECATED (ignored will be removed soon)")
	flag.StringVar(&globalOpts.configurableClusterOptions.KubeVirtNodeMemory, "e2e.kubevirt-node-memory", "8Gi", "the amount of memory to provide to each workload node")
	flag.UintVar(&globalOpts.configurableClusterOptions.KubeVirtNodeCores, "e2e.kubevirt-node-cores", 2, "The number of cores provided to each workload node")
	flag.UintVar(&globalOpts.configurableClusterOptions.KubeVirtRootVolumeSize, "e2e.kubevirt-root-volume-size", 32, "The root volume size in Gi")
	flag.StringVar(&globalOpts.configurableClusterOptions.KubeVirtRootVolumeVolumeMode, "e2e.kubevirt-root-volume-volume-mode", "Filesystem", "The root pvc volume mode")
	flag.StringVar(&globalOpts.configurableClusterOptions.KubeVirtInfraKubeconfigFile, "e2e.kubevirt-infra-kubeconfig", "", "path to the kubeconfig file of the external infra cluster")
	flag.StringVar(&globalOpts.configurableClusterOptions.KubeVirtInfraNamespace, "e2e.kubevirt-infra-namespace", "", "the namespace on the infra cluster the workers will be created on")
	flag.IntVar(&globalOpts.configurableClusterOptions.NodePoolReplicas, "e2e.node-pool-replicas", 2, "the number of replicas for each node pool in the cluster")
	flag.StringVar(&globalOpts.LatestReleaseImage, "e2e.latest-release-image", "", "The latest OCP release image for use by tests")
	flag.StringVar(&globalOpts.PreviousReleaseImage, "e2e.previous-release-image", "", "The previous OCP release image relative to the latest")
	flag.StringVar(&globalOpts.ArtifactDir, "e2e.artifact-dir", "", "The directory where cluster resources and logs should be dumped. If empty, nothing is dumped")
	flag.StringVar(&globalOpts.configurableClusterOptions.BaseDomain, "e2e.base-domain", "", "The ingress base domain for the cluster")
	flag.StringVar(&globalOpts.configurableClusterOptions.ControlPlaneOperatorImage, "e2e.control-plane-operator-image", "", "The image to use for the control plane operator. If none specified, the default is used.")
	flag.Var(&globalOpts.additionalTags, "e2e.additional-tags", "Additional tags to set on AWS resources")
	flag.StringVar(&globalOpts.configurableClusterOptions.AzureCredentialsFile, "e2e.azure-credentials-file", "", "Path to an Azure credentials file")
	flag.StringVar(&globalOpts.configurableClusterOptions.AzureLocation, "e2e.azure-location", "eastus", "The location to use for Azure")
	flag.StringVar(&globalOpts.configurableClusterOptions.SSHKeyFile, "e2e.ssh-key-file", "", "Path to a ssh public key")
	flag.StringVar(&globalOpts.platformRaw, "e2e.platform", string(hyperv1.AWSPlatform), "The platform to use for the tests")
	flag.StringVar(&globalOpts.configurableClusterOptions.NetworkType, "network-type", "", "The network type to use. If unset, will default based on the OCP version.")
	flag.StringVar(&globalOpts.configurableClusterOptions.PowerVSResourceGroup, "e2e.powervs-resource-group", "", "IBM Cloud Resource group")
	flag.StringVar(&globalOpts.configurableClusterOptions.PowerVSRegion, "e2e.powervs-region", "us-south", "IBM Cloud region. Default is us-south")
	flag.StringVar(&globalOpts.configurableClusterOptions.PowerVSZone, "e2e.powervs-zone", "us-south", "IBM Cloud zone. Default is us-sout")
	flag.StringVar(&globalOpts.configurableClusterOptions.PowerVSVpcRegion, "e2e.powervs-vpc-region", "us-south", "IBM Cloud VPC Region for VPC resources. Default is us-south")
	flag.StringVar(&globalOpts.configurableClusterOptions.PowerVSSysType, "e2e.powervs-sys-type", "s922", "System type used to host the instance(e.g: s922, e980, e880). Default is s922")
	flag.Var(&globalOpts.configurableClusterOptions.PowerVSProcType, "e2e.powervs-proc-type", "Processor type (dedicated, shared, capped). Default is shared")
	flag.StringVar(&globalOpts.configurableClusterOptions.PowerVSProcessors, "e2e.powervs-processors", "0.5", "Number of processors allocated. Default is 0.5")
	flag.IntVar(&globalOpts.configurableClusterOptions.PowerVSMemory, "e2e.powervs-memory", 32, "Amount of memory allocated (in GB). Default is 32")
	flag.StringVar(&globalOpts.configurableClusterOptions.PowerVSCloudInstanceID, "e2e-powervs-cloud-instance-id", "", "IBM Cloud PowerVS Service Instance ID. Use this flag to reuse an existing PowerVS Service Instance resource for cluster's infra")
	flag.StringVar(&globalOpts.configurableClusterOptions.PowerVSCloudConnection, "e2e-powervs-cloud-connection", "", "Cloud Connection in given zone. Use this flag to reuse an existing Cloud Connection resource for cluster's infra")
	flag.StringVar(&globalOpts.configurableClusterOptions.PowerVSVPC, "e2e-powervs-vpc", "", "IBM Cloud VPC Name. Use this flag to reuse an existing VPC resource for cluster's infra")
	flag.BoolVar(&globalOpts.SkipAPIBudgetVerification, "e2e.skip-api-budget", false, "Bool to avoid send metrics to E2E Server on local test execution.")
	flag.StringVar(&globalOpts.configurableClusterOptions.EtcdStorageClass, "e2e.etcd-storage-class", "", "The persistent volume storage class for etcd data volumes")
	flag.BoolVar(&globalOpts.RequestServingIsolation, "e2e.test-request-serving-isolation", false, "If set, TestCreate creates a cluster with request serving isolation topology")

	flag.Parse()

	globalOpts.Platform = hyperv1.PlatformType(globalOpts.platformRaw)

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

	os.Exit(main(m))
}

// main is used to allow us to use `defer` to defer cleanup task
// to after the tests are run. We can't do this in `TestMain` because
// it does an os.Exit (We could avoid that but then the deferred
// calls do not get executed after the tests, just at the end of
// TestMain()).
func main(m *testing.M) int {
	// Set up a root context for all tests and set up signal handling
	var cancel context.CancelFunc
	testContext, cancel = context.WithCancel(context.Background())
	defer cancel()
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		log.Info("tests received shutdown signal and will be cancelled")
		cancel()

		if globalOpts.Platform == hyperv1.AWSPlatform {
			cleanupSharedOIDCProvider()
		}
	}()

	if globalOpts.ArtifactDir != "" {
		go setupMetricsEndpoint(testContext, log)
		go e2eObserverControllers(testContext, log, globalOpts.ArtifactDir)
		defer dumpTestMetrics(log, globalOpts.ArtifactDir)
	}
	defer alertSLOs(testContext)

	if globalOpts.Platform == hyperv1.AWSPlatform {
		if err := setupSharedOIDCProvider(); err != nil {
			log.Error(err, "failed to setup shared OIDC provider")
			return -1
		}
		defer cleanupSharedOIDCProvider()
	}

	// Everything's okay to run tests
	log.Info("executing e2e tests", "options", globalOpts)
	return m.Run()
}

// setup a shared OIDC provider to be used by all HostedClusters
func setupSharedOIDCProvider() error {
	if globalOpts.configurableClusterOptions.AWSOidcS3BucketName == "" {
		return errors.New("please supply a public S3 bucket name with --e2e.aws-oidc-s3-bucket-name")
	}

	iamClient := e2eutil.GetIAMClient(globalOpts.configurableClusterOptions.AWSCredentialsFile, globalOpts.configurableClusterOptions.Region)
	s3Client := e2eutil.GetS3Client(globalOpts.configurableClusterOptions.AWSCredentialsFile, globalOpts.configurableClusterOptions.Region)

	providerID := e2eutil.SimpleNameGenerator.GenerateName("e2e-oidc-provider-")
	issuerURL := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", globalOpts.configurableClusterOptions.AWSOidcS3BucketName, globalOpts.configurableClusterOptions.Region, providerID)

	key, err := certs.PrivateKey()
	if err != nil {
		return fmt.Errorf("failed generating a private key: %w", err)
	}

	keyBytes := certs.PrivateKeyToPem(key)
	publicKeyBytes, err := certs.PublicKeyToPem(&key.PublicKey)
	if err != nil {
		return fmt.Errorf("failed to generate public key from private key: %w", err)
	}

	// create openid configuration
	params := oidc.ODICGeneratorParams{
		IssuerURL: issuerURL,
		PubKey:    publicKeyBytes,
	}

	oidcGenerators := map[string]oidc.OIDCDocumentGeneratorFunc{
		"/.well-known/openid-configuration": oidc.GenerateConfigurationDocument,
		oidc.JWKSURI:                        oidc.GenerateJWKSDocument,
	}

	for path, generator := range oidcGenerators {
		bodyReader, err := generator(params)
		if err != nil {
			return fmt.Errorf("failed to generate OIDC document %s: %w", path, err)
		}
		_, err = s3Client.PutObject(&s3.PutObjectInput{
			Body:   bodyReader,
			Bucket: aws.String(globalOpts.configurableClusterOptions.AWSOidcS3BucketName),
			Key:    aws.String(providerID + path),
		})
		if err != nil {
			wrapped := fmt.Errorf("failed to upload %s to the %s s3 bucket", path, globalOpts.configurableClusterOptions.AWSOidcS3BucketName)
			if awsErr, ok := err.(awserr.Error); ok {
				// Generally, the underlying message from AWS has unique per-request
				// info not suitable for publishing as condition messages, so just
				// return the code.
				wrapped = fmt.Errorf("%w: aws returned an error: %s", wrapped, awsErr.Code())
			}
			return wrapped
		}
	}

	iamOptions := awsinfra.CreateIAMOptions{
		IssuerURL:      issuerURL,
		AdditionalTags: globalOpts.additionalTags,
	}
	iamOptions.ParseAdditionalTags()

	if _, err := iamOptions.CreateOIDCProvider(iamClient); err != nil {
		return fmt.Errorf("failed to create OIDC provider: %w", err)
	}

	globalOpts.IssuerURL = issuerURL
	globalOpts.ServiceAccountSigningKey = keyBytes

	return nil
}

func cleanupSharedOIDCProvider() {
	iamClient := e2eutil.GetIAMClient(globalOpts.configurableClusterOptions.AWSCredentialsFile, globalOpts.configurableClusterOptions.Region)
	s3Client := e2eutil.GetS3Client(globalOpts.configurableClusterOptions.AWSCredentialsFile, globalOpts.configurableClusterOptions.Region)

	e2eutil.DestroyOIDCProvider(log, iamClient, globalOpts.IssuerURL)
	e2eutil.CleanupOIDCBucketObjects(log, s3Client, globalOpts.configurableClusterOptions.AWSOidcS3BucketName, globalOpts.IssuerURL)
}

// alertSLOs creates alert for our SLO/SLIs and log when firing.
// TODO(alberto): have a global t.Run which runs all tests first then TestAlertSLOs.
func alertSLOs(ctx context.Context) error {
	// Query fairing for SLOs.
	firingAlertQuery := `
sort_desc(
count_over_time(ALERTS{alertstate="firing",severity="slo",alertname!~"HypershiftSLO"}[10000s:1s])
) > 0
`
	prometheusClient, err := util.NewPrometheusClient(ctx)
	if err != nil {
		return err
	}

	result, err := util.RunQueryAtTime(ctx, log, prometheusClient, firingAlertQuery, time.Now())
	if err != nil {
		return err
	}
	for _, series := range result.Data.Result {
		log.Info(fmt.Sprintf("alert %s fired for %s second", series.Metric["alertname"], series.Value))
	}

	return nil
}

func e2eObserverControllers(ctx context.Context, log logr.Logger, artifactDir string) {
	config, err := e2eutil.GetConfig()
	if err != nil {
		log.Error(err, "failed to construct config for observers")
		return
	}
	mgr, err := ctrl.NewManager(config, manager.Options{MetricsBindAddress: "0"})
	if err != nil {
		log.Error(err, "failed to construct manager for observers")
		return
	}
	if err := podtimingcontroller.SetupWithManager(mgr, log, artifactDir); err != nil {
		log.Error(err, "failed to set up podtimingcontroller")
		return
	}

	if err := mgr.Start(ctx); err != nil {
		log.Error(err, "Mgr ended")
	}
}

const metricsServerAddr = "127.0.0.1:8080"

func setupMetricsEndpoint(ctx context.Context, log logr.Logger) {
	log.Info("Setting up metrics endpoint")
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	server := &http.Server{Addr: metricsServerAddr, Handler: mux}

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Error(err, "metrics server ended unexpectedly")
	}
}

func dumpTestMetrics(log logr.Logger, artifactDir string) {
	log.Info("Fetching test metrics")
	response, err := http.Get("http://" + metricsServerAddr + "/metrics")
	if err != nil {
		log.Error(err, "error fetching test metrics")
		return
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		log.Error(fmt.Errorf("status code %d", response.StatusCode), "Got unexpected status code from metrics endpoint")
		return
	}

	body, err := io.ReadAll(response.Body)
	if err != nil {
		log.Error(err, "failed to read response body from metrics endpoint")
		return
	}

	path := filepath.Join(artifactDir, "e2e-metrics-raw.prometheus")
	log = log.WithValues("path", path)
	if err := os.WriteFile(path, body, 0644); err != nil {
		log.Error(err, "failed to write e2e metrics to artifacts")
	}
	log.Info("Successfully wrote metrics to artifacts")
}

// options are global test options applicable to all scenarios.
type options struct {
	LatestReleaseImage   string
	PreviousReleaseImage string
	IsRunningInCI        bool
	ArtifactDir          string

	// BeforeApply is a function passed to the CLI create command giving the test
	// code an opportunity to inspect or mutate the resources the CLI will create
	// before they're applied.
	BeforeApply func(crclient.Object) `json:"-"`

	Platform    hyperv1.PlatformType
	platformRaw string

	configurableClusterOptions configurableClusterOptions
	additionalTags             stringSliceVar

	IssuerURL                string
	ServiceAccountSigningKey []byte

	// SkipAPIBudgetVerification implies that you are executing the e2e tests
	// from local to verify that them works fine before push
	SkipAPIBudgetVerification bool

	// If set, the CreateCluster test will create a cluster with request serving
	// isolation topology.
	RequestServingIsolation bool
}

type configurableClusterOptions struct {
	AWSCredentialsFile           string
	AzureCredentialsFile         string
	AzureLocation                string
	Region                       string
	Zone                         stringSliceVar
	PullSecretFile               string
	BaseDomain                   string
	ControlPlaneOperatorImage    string
	AWSEndpointAccess            string
	AWSOidcS3BucketName          string
	AWSKmsKeyAlias               string
	ExternalDNSDomain            string
	KubeVirtContainerDiskImage   string
	KubeVirtNodeMemory           string
	KubeVirtRootVolumeSize       uint
	KubeVirtRootVolumeVolumeMode string
	KubeVirtNodeCores            uint
	KubeVirtInfraKubeconfigFile  string
	KubeVirtInfraNamespace       string
	NodePoolReplicas             int
	SSHKeyFile                   string
	NetworkType                  string
	PowerVSResourceGroup         string
	PowerVSRegion                string
	PowerVSZone                  string
	PowerVSVpcRegion             string
	PowerVSSysType               string
	PowerVSProcType              hyperv1.PowerVSNodePoolProcType
	PowerVSProcessors            string
	PowerVSMemory                int
	PowerVSCloudInstanceID       string
	PowerVSCloudConnection       string
	PowerVSVPC                   string
	EtcdStorageClass             string
}

var nextAWSZoneIndex = 0

func (o *options) DefaultClusterOptions(t *testing.T) core.CreateOptions {
	createOption := core.CreateOptions{
		ReleaseImage:                     o.LatestReleaseImage,
		NodePoolReplicas:                 2,
		ControlPlaneAvailabilityPolicy:   string(hyperv1.SingleReplica),
		InfrastructureAvailabilityPolicy: string(hyperv1.SingleReplica),
		NetworkType:                      string(o.configurableClusterOptions.NetworkType),
		BaseDomain:                       o.configurableClusterOptions.BaseDomain,
		PullSecretFile:                   o.configurableClusterOptions.PullSecretFile,
		ControlPlaneOperatorImage:        o.configurableClusterOptions.ControlPlaneOperatorImage,
		ExternalDNSDomain:                o.configurableClusterOptions.ExternalDNSDomain,
		NodeUpgradeType:                  hyperv1.UpgradeTypeReplace,
		AWSPlatform: core.AWSPlatformOptions{
			RootVolumeSize:     64,
			RootVolumeType:     "gp3",
			AWSCredentialsFile: o.configurableClusterOptions.AWSCredentialsFile,
			Region:             o.configurableClusterOptions.Region,
			EndpointAccess:     o.configurableClusterOptions.AWSEndpointAccess,
			IssuerURL:          o.IssuerURL,
		},
		KubevirtPlatform: core.KubevirtPlatformCreateOptions{
			ServicePublishingStrategy: kubevirt.IngressServicePublishingStrategy,
			Cores:                     uint32(o.configurableClusterOptions.KubeVirtNodeCores),
			Memory:                    o.configurableClusterOptions.KubeVirtNodeMemory,
			InfraKubeConfigFile:       o.configurableClusterOptions.KubeVirtInfraKubeconfigFile,
			InfraNamespace:            o.configurableClusterOptions.KubeVirtInfraNamespace,
			RootVolumeSize:            uint32(o.configurableClusterOptions.KubeVirtRootVolumeSize),
			RootVolumeVolumeMode:      o.configurableClusterOptions.KubeVirtRootVolumeVolumeMode,
		},
		AzurePlatform: core.AzurePlatformOptions{
			CredentialsFile: o.configurableClusterOptions.AzureCredentialsFile,
			Location:        o.configurableClusterOptions.AzureLocation,
			InstanceType:    "Standard_D4s_v4",
			DiskSizeGB:      120,
		},
		PowerVSPlatform: core.PowerVSPlatformOptions{
			ResourceGroup:   o.configurableClusterOptions.PowerVSResourceGroup,
			Region:          o.configurableClusterOptions.PowerVSRegion,
			Zone:            o.configurableClusterOptions.PowerVSZone,
			VPCRegion:       o.configurableClusterOptions.PowerVSVpcRegion,
			SysType:         o.configurableClusterOptions.PowerVSSysType,
			ProcType:        o.configurableClusterOptions.PowerVSProcType,
			Processors:      o.configurableClusterOptions.PowerVSProcessors,
			Memory:          int32(o.configurableClusterOptions.PowerVSMemory),
			CloudInstanceID: o.configurableClusterOptions.PowerVSCloudInstanceID,
			CloudConnection: o.configurableClusterOptions.PowerVSCloudConnection,
			VPC:             o.configurableClusterOptions.PowerVSVPC,
		},
		ServiceCIDR: []string{"172.31.0.0/16"},
		ClusterCIDR: []string{"10.132.0.0/14"},
		BeforeApply: o.BeforeApply,
		Log:         util.NewLogr(t),
		Annotations: []string{
			fmt.Sprintf("%s=true", hyperv1.CleanupCloudResourcesAnnotation),
			fmt.Sprintf("%s=true", hyperv1.SkipReleaseImageValidation),
		},
		SkipAPIBudgetVerification: o.SkipAPIBudgetVerification,
		EtcdStorageClass:          o.configurableClusterOptions.EtcdStorageClass,
	}

	// Arch is only currently valid for aws platform
	if o.Platform == hyperv1.AWSPlatform {
		createOption.Arch = "amd64"
	}

	createOption.AWSPlatform.AdditionalTags = append(createOption.AWSPlatform.AdditionalTags, o.additionalTags...)
	if len(o.configurableClusterOptions.Zone) == 0 {
		// align with default for e2e.aws-region flag
		createOption.AWSPlatform.Zones = []string{"us-east-1a"}
	} else {
		// For AWS, select a single zone for InfrastructureAvailabilityPolicy: SingleReplica guest cluster.
		// This option is currently not configurable through flags and not set manually
		// in any test, so we know InfrastructureAvailabilityPolicy is SingleReplica.
		// If any test changes this in the future, we need to add logic here to make the
		// guest cluster multi-zone in that case.
		zones := strings.Split(o.configurableClusterOptions.Zone.String(), ",")
		awsGuestZone := zones[nextAWSZoneIndex]
		nextAWSZoneIndex = (nextAWSZoneIndex + 1) % len(zones)
		createOption.AWSPlatform.Zones = []string{awsGuestZone}

		// Assign all Azure zones to guest cluster
		createOption.AzurePlatform.AvailabilityZones = zones
	}

	if o.configurableClusterOptions.SSHKeyFile == "" {
		createOption.GenerateSSH = true
	} else {
		createOption.SSHKeyFile = o.configurableClusterOptions.SSHKeyFile
	}

	return createOption
}

// Complete is intended to be called after flags have been bound and sets
// up additional contextual defaulting.
func (o *options) Complete() error {
	if len(o.LatestReleaseImage) == 0 {
		defaultVersion, err := version.LookupDefaultOCPVersion("")
		if err != nil {
			return fmt.Errorf("couldn't look up default OCP version: %w", err)
		}
		o.LatestReleaseImage = defaultVersion.PullSpec
	}
	// TODO: This is actually basically a required field right now. Maybe the input
	// to tests should be a small API spec that describes the tests and their
	// inputs to avoid having to make every test input required. Or extract
	// e2e test suites into subcommands with their own distinct flags to make
	// selectively running them easier?
	if len(o.PreviousReleaseImage) == 0 {
		o.PreviousReleaseImage = o.LatestReleaseImage
	}

	o.IsRunningInCI = os.Getenv("OPENSHIFT_CI") == "true"

	if o.IsRunningInCI {
		if len(o.ArtifactDir) == 0 {
			o.ArtifactDir = os.Getenv("ARTIFACT_DIR")
		}
		if len(o.configurableClusterOptions.BaseDomain) == 0 && o.Platform != hyperv1.KubevirtPlatform {
			// TODO: make this an envvar with change to openshift/release, then change here
			o.configurableClusterOptions.BaseDomain = "origin-ci-int-aws.dev.rhcloud.com"
		}
	}

	return nil
}

// Validate is intended to be called after Complete and validates the options
// are usable by tests.
func (o *options) Validate() error {
	var errs []error

	if len(o.LatestReleaseImage) == 0 {
		errs = append(errs, fmt.Errorf("latest release image is required"))
	}

	if len(o.configurableClusterOptions.BaseDomain) == 0 {
		// The KubeVirt e2e tests don't require a base domain right now.
		//
		// For KubeVirt, the e2e tests generate a base domain within the *.apps domain
		// of the ocp cluster. So, the guest cluster's base domain is a
		// subdomain of the hypershift infra/mgmt cluster's base domain.
		//
		// Example:
		//   Infra/Mgmt cluster's DNS
		//     Base: example.com
		//     Cluster: mgmt-cluster.example.com
		//     Apps:    *.apps.mgmt-cluster.example.com
		//   KubeVirt Guest cluster's DNS
		//     Base: apps.mgmt-cluster.example.com
		//     Cluster: guest.apps.mgmt-cluster.example.com
		//     Apps: *.apps.guest.apps.mgmt-cluster.example.com
		//
		// This is possible using OCP wildcard routes
		if o.Platform != hyperv1.KubevirtPlatform {
			errs = append(errs, fmt.Errorf("base domain is required"))
		}
	}

	return apierr.NewAggregate(errs)
}

var _ flag.Value = &stringSliceVar{}

// stringSliceVar mimicks github.com/spf13/pflag.StringSliceVar in a stdlib-compatible way
type stringSliceVar []string

func (s *stringSliceVar) String() string     { return strings.Join(*s, ",") }
func (s *stringSliceVar) Set(v string) error { *s = append(*s, strings.Split(v, ",")...); return nil }
