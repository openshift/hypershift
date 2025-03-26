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
	"syscall"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	awsinfra "github.com/openshift/hypershift/cmd/infra/aws"
	"github.com/openshift/hypershift/test/e2e/podtimingcontroller"
	"github.com/openshift/hypershift/test/e2e/util"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	ctrl "sigs.k8s.io/controller-runtime"
	crzap "sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/oidc"
)

var (
	// opts are global options for the test suite bound in TestMain.
	globalOpts = &e2eutil.Options{}

	// testContext should be used as the parent context for any test code, and will
	// be cancelled if a SIGINT or SIGTERM is received. It's set up in TestMain.
	testContext context.Context

	log = crzap.New(crzap.UseDevMode(true), crzap.JSONEncoder(func(o *zapcore.EncoderConfig) {
		o.EncodeTime = zapcore.RFC3339TimeEncoder
	}))
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

// TestMain deals with global options and setting up a signal-bound context
// for all tests to use.
func TestMain(m *testing.M) {
	ctrl.SetLogger(log)

	// Platform-agnostic flags
	flag.BoolVar(&globalOpts.DisablePKIReconciliation, "e2e.disable-pki-reconciliation", false, "If set, TestUpgradeControlPlane will upgrade the control plane without reconciling the pki components")
	flag.BoolVar(&globalOpts.RequestServingIsolation, "e2e.test-request-serving-isolation", false, "If set, TestCreate creates a cluster with request serving isolation topology")
	flag.IntVar(&globalOpts.ConfigurableClusterOptions.NodePoolReplicas, "e2e.node-pool-replicas", 2, "the number of replicas for each node pool in the cluster")
	flag.StringVar(&globalOpts.ArtifactDir, "e2e.artifact-dir", "", "The directory where cluster resources and logs should be dumped. If empty, nothing is dumped")
	flag.StringVar(&globalOpts.ManagementClusterName, "e2e.management-cluster-name", "", "Name of the management cluster's HostedCluster (required to test request serving isolation)")
	flag.StringVar(&globalOpts.ManagementClusterNamespace, "e2e.management-cluster-namespace", "", "Namespace of the management cluster's HostedCluster (required to test request serving isolation)")
	flag.StringVar(&globalOpts.ManagementParentKubeconfig, "e2e.management-parent-kubeconfig", "", "Kubeconfig of the management cluster's parent cluster (required to test request serving isolation)")
	flag.StringVar(&globalOpts.LatestReleaseImage, "e2e.latest-release-image", "", "The latest OCP release image for use by tests")
	flag.StringVar(&globalOpts.PreviousReleaseImage, "e2e.previous-release-image", "", "The previous OCP release image relative to the latest")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.BaseDomain, "e2e.base-domain", "", "The ingress base domain for the cluster")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.ControlPlaneOperatorImage, "e2e.control-plane-operator-image", "", "The image to use for the control plane operator. If none specified, the default is used.")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.EtcdStorageClass, "e2e.etcd-storage-class", "", "The persistent volume storage class for etcd data volumes")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.ExternalDNSDomain, "e2e.external-dns-domain", "", "domain that external-dns will use to create DNS records for HCP endpoints")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.NetworkType, "network-type", string(hyperv1.OVNKubernetes), "The network type to use. If unset, will default based on the OCP version.")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.PullSecretFile, "e2e.pull-secret-file", "", "path to pull secret")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.SSHKeyFile, "e2e.ssh-key-file", "", "Path to a ssh public key")
	flag.StringVar(&globalOpts.N1MinorReleaseImage, "e2e.n1-minor-release-image", "", "The n-1 minor OCP release image relative to the latest")
	flag.StringVar(&globalOpts.N2MinorReleaseImage, "e2e.n2-minor-release-image", "", "The n-2 minor OCP release image relative to the latest")
	flag.StringVar(&globalOpts.PlatformRaw, "e2e.platform", string(hyperv1.AWSPlatform), "The platform to use for the tests")
	flag.Var(&globalOpts.ConfigurableClusterOptions.Annotations, "e2e.annotations", "Annotations to apply to the HostedCluster (key=value). Can be specified multiple times")
	flag.Var(&globalOpts.ConfigurableClusterOptions.ClusterCIDR, "e2e.cluster-cidr", "The CIDR of the cluster network. Can be specified multiple times.")
	flag.Var(&globalOpts.ConfigurableClusterOptions.ServiceCIDR, "e2e.service-cidr", "The CIDR of the service network. Can be specified multiple times.")
	flag.Var(&globalOpts.ConfigurableClusterOptions.Zone, "e2e.availability-zones", "Availability zones for clusters")
	flag.StringVar(&globalOpts.HyperShiftOperatorLatestImage, "e2e.hypershift-operator-latest-image", "quay.io/hypershift/hypershift-operator:latest", "The latest HyperShift Operator image to deploy. If e2e.hypershift-operator-initial-image is set (e.g. to run an upgrade test), this image will be considered the latest HyperShift Operator image to upgrade to.")
	flag.StringVar(&globalOpts.HOInstallationOptions.PrivatePlatform, "e2e.private-platform", "None", "Platform on which private clusters are supported by the HyperShift Operator (supports \"AWS\" or \"None\"). This is a HyperShift Operator installation option")
	flag.StringVar(&globalOpts.HOInstallationOptions.AWSPrivateCredentialsFile, "e2e.aws-private-credentials-file", "/etc/hypershift-pool-aws-credentials/credentials", "path to AWS private credentials. This is a HyperShift Operator installation option")
	flag.StringVar(&globalOpts.HOInstallationOptions.AWSPrivateRegion, "e2e.aws-private-region", "us-east-1", "AWS region where private clusters are supported by the HyperShift Operator. This is a HyperShift Operator installation option")
	flag.StringVar(&globalOpts.HOInstallationOptions.AWSOidcS3Credentials, "e2e.aws-oidc-s3-credentials", "/etc/hypershift-pool-aws-credentials/credentials", "AWS S3 credentials for the setup of the OIDC provider. This is a HyperShift Operator installation option")
	flag.StringVar(&globalOpts.HOInstallationOptions.AWSOidcS3Region, "e2e.aws-oidc-s3-region", "us-east-1", "AWS S3 region for the setup of the OIDC provider. This is a HyperShift Operator installation option")
	flag.StringVar(&globalOpts.HOInstallationOptions.ExternalDNSProvider, "e2e.external-dns-provider", "aws", "Provider to use for managing DNS records using external-dns. This is a HyperShift Operator installation option")
	flag.StringVar(&globalOpts.HOInstallationOptions.ExternalDNSDomainFilter, "e2e.external-dns-domain-filter", "service.ci.hypershift.devcluster.openshift.com", "restrict external-dns to changes within the specified domain. This is a HyperShift Operator installation option")
	flag.StringVar(&globalOpts.HOInstallationOptions.ExternalDNSCredentials, "e2e.external-dns-credentials", "/etc/hypershift-pool-aws-credentials/credentials", "path to credentials file to use for managing DNS records using external-dns. This is a HyperShift Operator installation option")
	flag.BoolVar(&globalOpts.HOInstallationOptions.EnableCIDebugOutput, "e2e.ho-enable-ci-debug-output", false, "Install the HyperShift Operator with extra CI debug output enabled. This is a HyperShift Operator installation option")
	flag.StringVar(&globalOpts.HOInstallationOptions.PlatformMonitoring, "e2e.platform-monitoring", "All", "The option for enabling platform cluster monitoring when installing the HyperShift Operator. Valid values are: None, OperatorOnly, All. This is a HyperShift Operator installation option")
	flag.BoolVar(&globalOpts.RunUpgradeTest, "upgrade.run-tests", false, "Run HyperShift Operator upgrade test")

	// AWS specific flags
	flag.BoolVar(&globalOpts.ConfigurableClusterOptions.AWSMultiArch, "e2e.aws-multi-arch", false, "Enable multi arch for aws clusters")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.AWSCredentialsFile, "e2e.aws-credentials-file", "", "path to AWS credentials")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.AWSEndpointAccess, "e2e.aws-endpoint-access", "", "endpoint access profile for the cluster")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.AWSKmsKeyAlias, "e2e.aws-kms-key-alias", "", "AWS KMS Key Alias to use when creating encrypted nodepools, when empty the default EBS KMS Key will be used")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.AWSOidcS3BucketName, "e2e.aws-oidc-s3-bucket-name", "", "AWS S3 Bucket Name to setup the OIDC provider in")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.Region, "e2e.aws-region", "us-east-1", "AWS region for clusters")
	flag.Var(&globalOpts.AdditionalTags, "e2e.additional-tags", "Additional tags to set on AWS resources")
	flag.Var(&globalOpts.ConfigurableClusterOptions.Zone, "e2e.aws-zones", "Deprecated, use -e2e.availability-zones instead")

	// Azure specific flags
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.AzureCredentialsFile, "e2e.azure-credentials-file", "", "Path to an Azure credentials file")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.AzureLocation, "e2e.azure-location", "eastus", "The location to use for Azure")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.AzureManagedIdentitiesFile, "e2e.azure-managed-identities-file", "", "Path to an Azure managed identities file")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.AzureMarketplaceOffer, "e2e.azure-marketplace-offer", "", "The location to use for Azure")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.AzureMarketplacePublisher, "e2e.azure-marketplace-publisher", "", "The marketplace publisher to use for Azure")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.AzureMarketplaceSKU, "e2e.azure-marketplace-sku", "", "The marketplace SKU to use for Azure")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.AzureMarketplaceVersion, "e2e.azure-marketplace-version", "", "The marketplace version to use for Azure")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.AzureIssuerURL, "e2e.oidc-issuer-url", "", "The OIDC provider issuer URL")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.AzureServiceAccountTokenIssuerKeyPath, "e2e.sa-token-issuer-private-key-path", "", "The file to the private key for the service account token issuer")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.AzureDataPlaneIdentities, "e2e.azure-data-plane-identities-file", "", "Path to a file containing the client IDs of the managed identities associated with the data plane")

	// Kubevirt specific flags
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.KubeVirtContainerDiskImage, "e2e.kubevirt-container-disk-image", "", "DEPRECATED (ignored will be removed soon)")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.KubeVirtInfraKubeconfigFile, "e2e.kubevirt-infra-kubeconfig", "", "path to the kubeconfig file of the external infra cluster")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.KubeVirtInfraNamespace, "e2e.kubevirt-infra-namespace", "", "the namespace on the infra cluster the workers will be created on")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.KubeVirtNodeMemory, "e2e.kubevirt-node-memory", "8Gi", "the amount of memory to provide to each workload node")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.KubeVirtRootVolumeVolumeMode, "e2e.kubevirt-root-volume-volume-mode", "Filesystem", "The root pvc volume mode")
	flag.UintVar(&globalOpts.ConfigurableClusterOptions.KubeVirtNodeCores, "e2e.kubevirt-node-cores", 2, "The number of cores provided to each workload node")
	flag.UintVar(&globalOpts.ConfigurableClusterOptions.KubeVirtRootVolumeSize, "e2e.kubevirt-root-volume-size", 32, "The root volume size in Gi")

	// OpenStack specific flags
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.OpenStackCACertFile, "e2e.openstack-ca-cert-file", "", "Path to the OpenStack CA certificate file")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.OpenStackCredentialsFile, "e2e.openstack-credentials-file", "", "Path to the OpenStack credentials file")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.OpenStackExternalNetworkID, "e2e.openstack-external-network-id", "", "ID of the OpenStack external network")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.OpenStackNodeAvailabilityZone, "e2e.openstack-node-availability-zone", "", "The availability zone to use for OpenStack nodes")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.OpenStackNodeFlavor, "e2e.openstack-node-flavor", "", "The flavor to use for OpenStack nodes")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.OpenStackNodeImageName, "e2e.openstack-node-image-name", "", "The image name to use for OpenStack nodes")
	flag.Var(&globalOpts.ConfigurableClusterOptions.OpenStackDNSNameservers, "e2e.openstack-dns-nameservers", "List of DNS nameservers to use for the cluster")

	// PowerVS specific flags
	flag.BoolVar(&globalOpts.ConfigurableClusterOptions.PowerVSPER, "e2e-powervs-power-edge-router", false, "Enabling this flag will utilize Power Edge Router solution via transit gateway instead of cloud connection to create a connection between PowerVS and VPC")
	flag.IntVar(&globalOpts.ConfigurableClusterOptions.PowerVSMemory, "e2e.powervs-memory", 32, "Amount of memory allocated (in GB). Default is 32")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.PowerVSCloudConnection, "e2e-powervs-cloud-connection", "", "Cloud Connection in given zone. Use this flag to reuse an existing Cloud Connection resource for cluster's infra")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.PowerVSCloudInstanceID, "e2e-powervs-cloud-instance-id", "", "IBM Cloud PowerVS Service Instance ID. Use this flag to reuse an existing PowerVS Service Instance resource for cluster's infra")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.PowerVSProcessors, "e2e.powervs-processors", "0.5", "Number of processors allocated. Default is 0.5")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.PowerVSRegion, "e2e.powervs-region", "us-south", "IBM Cloud region. Default is us-south")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.PowerVSResourceGroup, "e2e.powervs-resource-group", "", "IBM Cloud Resource group")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.PowerVSSysType, "e2e.powervs-sys-type", "s922", "System type used to host the instance(e.g: s922, e980, e880). Default is s922")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.PowerVSTransitGateway, "e2e-powervs-transit-gateway", "", "Transit gateway name. Use this flag to reuse an existing transit gateway resource for cluster's infra")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.PowerVSTransitGatewayLocation, "e2e-powervs-transit-gateway-location", "", "IBM Cloud Transit Gateway location")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.PowerVSVPC, "e2e-powervs-vpc", "", "IBM Cloud VPC Name. Use this flag to reuse an existing VPC resource for cluster's infra")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.PowerVSVpcRegion, "e2e.powervs-vpc-region", "us-south", "IBM Cloud VPC Region for VPC resources. Default is us-south")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.PowerVSZone, "e2e.powervs-zone", "us-south", "IBM Cloud zone. Default is us-sout")
	flag.Var(&globalOpts.ConfigurableClusterOptions.PowerVSProcType, "e2e.powervs-proc-type", "Processor type (dedicated, shared, capped). Default is shared")

	flag.Parse()

	globalOpts.Platform = hyperv1.PlatformType(globalOpts.PlatformRaw)

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
		if err := setupSharedOIDCProvider(globalOpts.ArtifactDir); err != nil {
			log.Error(err, "failed to setup shared OIDC provider")
			return -1
		}
		defer cleanupSharedOIDCProvider()
	}

	// set the semantic version of the latest release image for version gating tests
	err := util.SetReleaseImageVersion(testContext, globalOpts.LatestReleaseImage, globalOpts.ConfigurableClusterOptions.PullSecretFile)
	if err != nil {
		log.Error(err, "failed to set release image version")
		return -1
	}

	// Everything's okay to run tests
	log.Info("executing e2e tests", "options", globalOpts)
	return m.Run()
}

// setup a shared OIDC provider to be used by all HostedClusters
func setupSharedOIDCProvider(artifactDir string) error {
	if globalOpts.ConfigurableClusterOptions.AWSOidcS3BucketName == "" {
		return errors.New("please supply a public S3 bucket name with --e2e.aws-oidc-s3-bucket-name")
	}

	iamClient := e2eutil.GetIAMClient(globalOpts.ConfigurableClusterOptions.AWSCredentialsFile, globalOpts.ConfigurableClusterOptions.Region)
	s3Client := e2eutil.GetS3Client(globalOpts.ConfigurableClusterOptions.AWSCredentialsFile, globalOpts.ConfigurableClusterOptions.Region)

	providerID := e2eutil.SimpleNameGenerator.GenerateName("e2e-oidc-provider-")
	issuerURL := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", globalOpts.ConfigurableClusterOptions.AWSOidcS3BucketName, globalOpts.ConfigurableClusterOptions.Region, providerID)

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
			Bucket: aws.String(globalOpts.ConfigurableClusterOptions.AWSOidcS3BucketName),
			Key:    aws.String(providerID + path),
		})
		if err != nil {
			wrapped := fmt.Errorf("failed to upload %s to the %s s3 bucket", path, globalOpts.ConfigurableClusterOptions.AWSOidcS3BucketName)
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
		AdditionalTags: globalOpts.AdditionalTags,
	}
	iamOptions.ParseAdditionalTags()

	createLogFile := filepath.Join(artifactDir, "create-oidc-provider.log")
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

	if _, err := iamOptions.CreateOIDCProvider(iamClient, zapr.NewLogger(createLogger)); err != nil {
		return fmt.Errorf("failed to create OIDC provider: %w", err)
	}

	globalOpts.IssuerURL = issuerURL
	globalOpts.ServiceAccountSigningKey = keyBytes

	return nil
}

func cleanupSharedOIDCProvider() {
	iamClient := e2eutil.GetIAMClient(globalOpts.ConfigurableClusterOptions.AWSCredentialsFile, globalOpts.ConfigurableClusterOptions.Region)
	s3Client := e2eutil.GetS3Client(globalOpts.ConfigurableClusterOptions.AWSCredentialsFile, globalOpts.ConfigurableClusterOptions.Region)

	e2eutil.DestroyOIDCProvider(log, iamClient, globalOpts.IssuerURL)
	e2eutil.CleanupOIDCBucketObjects(log, s3Client, globalOpts.ConfigurableClusterOptions.AWSOidcS3BucketName, globalOpts.IssuerURL)
}

// alertSLOs creates alert for our SLO/SLIs and log when firing.
// TODO(alberto): have a global t.Run which runs all tests first then TestAlertSLOs.
func alertSLOs(ctx context.Context) error {
	if globalOpts.Platform == hyperv1.AzurePlatform {
		return fmt.Errorf("Alerting SLOs is not supported on Azure")
	}

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
	mgr, err := ctrl.NewManager(config, manager.Options{
		Metrics: metricsserver.Options{
			BindAddress: "0",
		},
	})
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
