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
	"github.com/go-logr/zapr"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hypershiftaws "github.com/openshift/hypershift/cmd/cluster/aws"
	"github.com/openshift/hypershift/cmd/cluster/azure"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/cluster/kubevirt"
	"github.com/openshift/hypershift/cmd/cluster/none"
	hypershiftopenstack "github.com/openshift/hypershift/cmd/cluster/openstack"
	"github.com/openshift/hypershift/cmd/cluster/powervs"
	awsinfra "github.com/openshift/hypershift/cmd/infra/aws"
	awscmdutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	azurenodepool "github.com/openshift/hypershift/cmd/nodepool/azure"
	kubevirtnodepool "github.com/openshift/hypershift/cmd/nodepool/kubevirt"
	openstacknodepool "github.com/openshift/hypershift/cmd/nodepool/openstack"
	"github.com/openshift/hypershift/cmd/version"
	controlplaneoperatoroverrides "github.com/openshift/hypershift/hypershift-operator/controlplaneoperator-overrides"
	"github.com/openshift/hypershift/test/e2e/podtimingcontroller"
	"github.com/openshift/hypershift/test/e2e/util"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	apierr "k8s.io/apimachinery/pkg/util/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	crzap "sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/oidc"
)

var (
	// opts are global options for the test suite bound in TestMain.
	globalOpts = &options{}

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

	flag.StringVar(&globalOpts.configurableClusterOptions.AWSCredentialsFile, "e2e.aws-credentials-file", "", "path to AWS credentials")
	flag.StringVar(&globalOpts.configurableClusterOptions.Region, "e2e.aws-region", "us-east-1", "AWS region for clusters")
	flag.Var(&globalOpts.configurableClusterOptions.Zone, "e2e.aws-zones", "Deprecated, use -e2e.availability-zones instead")
	flag.Var(&globalOpts.configurableClusterOptions.Zone, "e2e.availability-zones", "Availability zones for clusters")
	flag.BoolVar(&globalOpts.configurableClusterOptions.AWSMultiArch, "e2e.aws-multi-arch", false, "Enable multi arch for aws clusters")
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
	flag.StringVar(&globalOpts.configurableClusterOptions.OpenStackCredentialsFile, "e2e.openstack-credentials-file", "", "Path to the OpenStack credentials file")
	flag.StringVar(&globalOpts.configurableClusterOptions.OpenStackCACertFile, "e2e.openstack-ca-cert-file", "", "Path to the OpenStack CA certificate file")
	flag.StringVar(&globalOpts.configurableClusterOptions.OpenStackExternalNetworkID, "e2e.openstack-external-network-id", "", "ID of the OpenStack external network")
	flag.StringVar(&globalOpts.configurableClusterOptions.OpenStackNodeFlavor, "e2e.openstack-node-flavor", "", "The flavor to use for OpenStack nodes")
	flag.StringVar(&globalOpts.configurableClusterOptions.OpenStackNodeImageName, "e2e.openstack-node-image-name", "", "The image name to use for OpenStack nodes")
	flag.StringVar(&globalOpts.configurableClusterOptions.OpenStackNodeAvailabilityZone, "e2e.openstack-node-availability-zone", "", "The availability zone to use for OpenStack nodes")
	flag.StringVar(&globalOpts.configurableClusterOptions.AzureCredentialsFile, "e2e.azure-credentials-file", "", "Path to an Azure credentials file")
	flag.StringVar(&globalOpts.configurableClusterOptions.AzureManagedIdentitiesFile, "e2e.azure-managed-identities-file", "", "Path to an Azure managed identities file")
	flag.StringVar(&globalOpts.configurableClusterOptions.ManagementKeyVaultName, "e2e.management-key-vault-name", "", "Name of the Azure Key Vault to use for Certificates")
	flag.StringVar(&globalOpts.configurableClusterOptions.ManagementKeyVaultTenantId, "e2e.management-key-vault-tenant-id", "", "Tenant ID of the Azure Key Vault to use for Certificates")
	flag.StringVar(&globalOpts.configurableClusterOptions.AzureLocation, "e2e.azure-location", "eastus", "The location to use for Azure")
	flag.StringVar(&globalOpts.configurableClusterOptions.SSHKeyFile, "e2e.ssh-key-file", "", "Path to a ssh public key")
	flag.StringVar(&globalOpts.platformRaw, "e2e.platform", string(hyperv1.AWSPlatform), "The platform to use for the tests")
	flag.StringVar(&globalOpts.configurableClusterOptions.NetworkType, "network-type", string(hyperv1.OVNKubernetes), "The network type to use. If unset, will default based on the OCP version.")
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
	flag.BoolVar(&globalOpts.configurableClusterOptions.PowerVSPER, "e2e-powervs-power-edge-router", false, "Enabling this flag will utilize Power Edge Router solution via transit gateway instead of cloud connection to create a connection between PowerVS and VPC")
	flag.StringVar(&globalOpts.configurableClusterOptions.PowerVSTransitGatewayLocation, "e2e-powervs-transit-gateway-location", "", "IBM Cloud Transit Gateway location")
	flag.StringVar(&globalOpts.configurableClusterOptions.PowerVSTransitGateway, "e2e-powervs-transit-gateway", "", "Transit gateway name. Use this flag to reuse an existing transit gateway resource for cluster's infra")
	flag.StringVar(&globalOpts.configurableClusterOptions.EtcdStorageClass, "e2e.etcd-storage-class", "", "The persistent volume storage class for etcd data volumes")
	flag.BoolVar(&globalOpts.RequestServingIsolation, "e2e.test-request-serving-isolation", false, "If set, TestCreate creates a cluster with request serving isolation topology")
	flag.StringVar(&globalOpts.ManagementParentKubeconfig, "e2e.management-parent-kubeconfig", "", "Kubeconfig of the management cluster's parent cluster (required to test request serving isolation)")
	flag.StringVar(&globalOpts.ManagementClusterNamespace, "e2e.management-cluster-namespace", "", "Namespace of the management cluster's HostedCluster (required to test request serving isolation)")
	flag.StringVar(&globalOpts.ManagementClusterName, "e2e.management-cluster-name", "", "Name of the management cluster's HostedCluster (required to test request serving isolation)")
	flag.BoolVar(&globalOpts.DisablePKIReconciliation, "e2e.disable-pki-reconciliation", false, "If set, TestUpgradeControlPlane will upgrade the control plane without reconciling the pki components")
	flag.Var(&globalOpts.configurableClusterOptions.Annotations, "e2e.annotations", "Annotations to apply to the HostedCluster (key=value). Can be specified multiple times")
	flag.Var(&globalOpts.configurableClusterOptions.ServiceCIDR, "e2e.service-cidr", "The CIDR of the service network. Can be specified multiple times.")
	flag.Var(&globalOpts.configurableClusterOptions.ClusterCIDR, "e2e.cluster-cidr", "The CIDR of the cluster network. Can be specified multiple times.")
	flag.StringVar(&globalOpts.n1MinorReleaseImage, "e2e.n1-minor-release-image", "", "The n-1 minor OCP release image relative to the latest")
	flag.StringVar(&globalOpts.n2MinorReleaseImage, "e2e.n2-minor-release-image", "", "The n-2 minor OCP release image relative to the latest")

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
		if err := setupSharedOIDCProvider(globalOpts.ArtifactDir); err != nil {
			log.Error(err, "failed to setup shared OIDC provider")
			return -1
		}
		defer cleanupSharedOIDCProvider()
	}

	// set the semantic version of the latest release image for version gating tests
	err := util.SetReleaseImageVersion(testContext, globalOpts.LatestReleaseImage, globalOpts.configurableClusterOptions.PullSecretFile)
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
	iamClient := e2eutil.GetIAMClient(globalOpts.configurableClusterOptions.AWSCredentialsFile, globalOpts.configurableClusterOptions.Region)
	s3Client := e2eutil.GetS3Client(globalOpts.configurableClusterOptions.AWSCredentialsFile, globalOpts.configurableClusterOptions.Region)

	e2eutil.DestroyOIDCProvider(log, iamClient, globalOpts.IssuerURL)
	e2eutil.CleanupOIDCBucketObjects(log, s3Client, globalOpts.configurableClusterOptions.AWSOidcS3BucketName, globalOpts.IssuerURL)
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

// options are global test options applicable to all scenarios.
type options struct {
	LatestReleaseImage   string
	PreviousReleaseImage string
	n2MinorReleaseImage  string
	n1MinorReleaseImage  string
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

	// If set, the CreateCluster test will create a cluster with request serving
	// isolation topology.
	RequestServingIsolation bool

	// If testing request serving isolation topology, we need a kubeconfig to the
	// parent of the management cluster, name and namespace of the management cluster
	// so we can create additional nodepools for it.
	ManagementParentKubeconfig string
	ManagementClusterNamespace string
	ManagementClusterName      string
	// If set, the UpgradeControlPlane test will upgrade control plane without
	// reconciling PKI.
	DisablePKIReconciliation bool
}

type configurableClusterOptions struct {
	AWSCredentialsFile            string
	AWSMultiArch                  bool
	AzureCredentialsFile          string
	ManagementKeyVaultName        string
	ManagementKeyVaultTenantId    string
	AzureManagedIdentitiesFile    string
	OpenStackCredentialsFile      string
	OpenStackCACertFile           string
	AzureLocation                 string
	Region                        string
	Zone                          stringSliceVar
	PullSecretFile                string
	BaseDomain                    string
	ControlPlaneOperatorImage     string
	AWSEndpointAccess             string
	AWSOidcS3BucketName           string
	AWSKmsKeyAlias                string
	ExternalDNSDomain             string
	KubeVirtContainerDiskImage    string
	KubeVirtNodeMemory            string
	KubeVirtRootVolumeSize        uint
	KubeVirtRootVolumeVolumeMode  string
	KubeVirtNodeCores             uint
	KubeVirtInfraKubeconfigFile   string
	KubeVirtInfraNamespace        string
	NodePoolReplicas              int
	SSHKeyFile                    string
	NetworkType                   string
	OpenStackExternalNetworkID    string
	OpenStackNodeFlavor           string
	OpenStackNodeImageName        string
	OpenStackNodeAvailabilityZone string
	PowerVSResourceGroup          string
	PowerVSRegion                 string
	PowerVSZone                   string
	PowerVSVpcRegion              string
	PowerVSSysType                string
	PowerVSProcType               hyperv1.PowerVSNodePoolProcType
	PowerVSProcessors             string
	PowerVSMemory                 int
	PowerVSCloudInstanceID        string
	PowerVSCloudConnection        string
	PowerVSVPC                    string
	PowerVSPER                    bool
	PowerVSTransitGatewayLocation string
	PowerVSTransitGateway         string
	EtcdStorageClass              string
	Annotations                   stringMapVar
	ServiceCIDR                   stringSliceVar
	ClusterCIDR                   stringSliceVar
}

var nextAWSZoneIndex = 0

func (o *options) DefaultClusterOptions(t *testing.T) e2eutil.PlatformAgnosticOptions {
	createOption := e2eutil.PlatformAgnosticOptions{
		RawCreateOptions: core.RawCreateOptions{
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
			ServiceCIDR:                      []string{"172.31.0.0/16"},
			ClusterCIDR:                      []string{"10.132.0.0/14"},
			BeforeApply:                      o.BeforeApply,
			Log:                              util.NewLogr(t),
			Annotations: []string{
				fmt.Sprintf("%s=true", hyperv1.CleanupCloudResourcesAnnotation),
				fmt.Sprintf("%s=true", hyperv1.SkipReleaseImageValidation),
			},
			EtcdStorageClass: o.configurableClusterOptions.EtcdStorageClass,
		},
		NonePlatform:      o.DefaultNoneOptions(),
		AWSPlatform:       o.DefaultAWSOptions(),
		KubevirtPlatform:  o.DefaultKubeVirtOptions(),
		AzurePlatform:     o.DefaultAzureOptions(),
		PowerVSPlatform:   o.DefaultPowerVSOptions(),
		OpenStackPlatform: o.DefaultOpenStackOptions(),
	}

	switch o.Platform {
	case hyperv1.AWSPlatform, hyperv1.AzurePlatform, hyperv1.NonePlatform, hyperv1.KubevirtPlatform, hyperv1.OpenStackPlatform:
		createOption.Arch = hyperv1.ArchitectureAMD64
	case hyperv1.PowerVSPlatform:
		createOption.Arch = hyperv1.ArchitecturePPC64LE
	}

	if o.configurableClusterOptions.SSHKeyFile == "" {
		createOption.GenerateSSH = true
	} else {
		createOption.SSHKeyFile = o.configurableClusterOptions.SSHKeyFile
	}

	if o.configurableClusterOptions.Annotations != nil {
		for k, v := range o.configurableClusterOptions.Annotations {
			createOption.Annotations = append(createOption.Annotations, fmt.Sprintf("%s=%s", k, v))
		}
	}

	if len(o.configurableClusterOptions.ServiceCIDR) != 0 {
		createOption.ServiceCIDR = o.configurableClusterOptions.ServiceCIDR
	}

	if len(o.configurableClusterOptions.ClusterCIDR) != 0 {
		createOption.ClusterCIDR = o.configurableClusterOptions.ClusterCIDR
	}

	return createOption
}

func (o *options) DefaultNoneOptions() none.RawCreateOptions {
	return none.RawCreateOptions{
		APIServerAddress:          "",
		ExposeThroughLoadBalancer: true,
	}
}

func (p *options) DefaultOpenStackOptions() hypershiftopenstack.RawCreateOptions {
	opts := hypershiftopenstack.RawCreateOptions{
		OpenStackCredentialsFile:   p.configurableClusterOptions.OpenStackCredentialsFile,
		OpenStackCACertFile:        p.configurableClusterOptions.OpenStackCACertFile,
		OpenStackExternalNetworkID: p.configurableClusterOptions.OpenStackExternalNetworkID,
		NodePoolOpts: &openstacknodepool.RawOpenStackPlatformCreateOptions{
			OpenStackPlatformOptions: &openstacknodepool.OpenStackPlatformOptions{
				Flavor:         p.configurableClusterOptions.OpenStackNodeFlavor,
				ImageName:      p.configurableClusterOptions.OpenStackNodeImageName,
				AvailabityZone: p.configurableClusterOptions.OpenStackNodeAvailabilityZone,
			},
		},
	}

	return opts
}

func (o *options) DefaultAWSOptions() hypershiftaws.RawCreateOptions {
	opts := hypershiftaws.RawCreateOptions{
		RootVolumeSize: 64,
		RootVolumeType: "gp3",
		Credentials: awscmdutil.AWSCredentialsOptions{
			AWSCredentialsFile: o.configurableClusterOptions.AWSCredentialsFile,
		},
		Region:         o.configurableClusterOptions.Region,
		EndpointAccess: o.configurableClusterOptions.AWSEndpointAccess,
		IssuerURL:      o.IssuerURL,
		MultiArch:      o.configurableClusterOptions.AWSMultiArch,
	}
	opts.AdditionalTags = append(opts.AdditionalTags, o.additionalTags...)
	if len(o.configurableClusterOptions.Zone) == 0 {
		// align with default for e2e.aws-region flag
		opts.Zones = []string{"us-east-1a"}
	} else {
		// For AWS, select a single zone for InfrastructureAvailabilityPolicy: SingleReplica guest cluster.
		// This option is currently not configurable through flags and not set manually
		// in any test, so we know InfrastructureAvailabilityPolicy is SingleReplica.
		// If any test changes this in the future, we need to add logic here to make the
		// guest cluster multi-zone in that case.
		zones := strings.Split(o.configurableClusterOptions.Zone.String(), ",")
		awsGuestZone := zones[nextAWSZoneIndex]
		nextAWSZoneIndex = (nextAWSZoneIndex + 1) % len(zones)
		opts.Zones = []string{awsGuestZone}
	}

	return opts
}

func (o *options) DefaultKubeVirtOptions() kubevirt.RawCreateOptions {
	return kubevirt.RawCreateOptions{
		ServicePublishingStrategy: kubevirt.IngressServicePublishingStrategy,
		InfraKubeConfigFile:       o.configurableClusterOptions.KubeVirtInfraKubeconfigFile,
		InfraNamespace:            o.configurableClusterOptions.KubeVirtInfraNamespace,
		NodePoolOpts: &kubevirtnodepool.RawKubevirtPlatformCreateOptions{
			KubevirtPlatformOptions: &kubevirtnodepool.KubevirtPlatformOptions{
				Cores:                uint32(o.configurableClusterOptions.KubeVirtNodeCores),
				Memory:               o.configurableClusterOptions.KubeVirtNodeMemory,
				RootVolumeSize:       uint32(o.configurableClusterOptions.KubeVirtRootVolumeSize),
				RootVolumeVolumeMode: o.configurableClusterOptions.KubeVirtRootVolumeVolumeMode,
			},
		},
	}
}

func (o *options) DefaultAzureOptions() azure.RawCreateOptions {
	opts := azure.RawCreateOptions{
		CredentialsFile: o.configurableClusterOptions.AzureCredentialsFile,
		Location:        o.configurableClusterOptions.AzureLocation,

		NodePoolOpts: azurenodepool.DefaultOptions(),
	}
	if len(o.configurableClusterOptions.Zone) != 0 {
		zones := strings.Split(o.configurableClusterOptions.Zone.String(), ",")
		// Assign all Azure zones to guest cluster
		opts.AvailabilityZones = zones
	}

	if o.configurableClusterOptions.ManagementKeyVaultName != "" {
		opts.KeyVaultInfo.KeyVaultName = o.configurableClusterOptions.ManagementKeyVaultName
	}

	if o.configurableClusterOptions.ManagementKeyVaultTenantId != "" {
		opts.KeyVaultInfo.KeyVaultTenantID = o.configurableClusterOptions.ManagementKeyVaultTenantId
	}

	if o.configurableClusterOptions.AzureManagedIdentitiesFile != "" {
		opts.ManagedIdentitiesFile = o.configurableClusterOptions.AzureManagedIdentitiesFile
	}

	if (opts.KeyVaultInfo.KeyVaultName != "" && opts.KeyVaultInfo.KeyVaultTenantID != "") || opts.ManagedIdentitiesFile != "" {
		opts.TechPreviewEnabled = true
	}

	return opts
}

func (o *options) DefaultPowerVSOptions() powervs.RawCreateOptions {
	return powervs.RawCreateOptions{
		ResourceGroup:          o.configurableClusterOptions.PowerVSResourceGroup,
		Region:                 o.configurableClusterOptions.PowerVSRegion,
		Zone:                   o.configurableClusterOptions.PowerVSZone,
		VPCRegion:              o.configurableClusterOptions.PowerVSVpcRegion,
		SysType:                o.configurableClusterOptions.PowerVSSysType,
		ProcType:               o.configurableClusterOptions.PowerVSProcType,
		Processors:             o.configurableClusterOptions.PowerVSProcessors,
		Memory:                 int32(o.configurableClusterOptions.PowerVSMemory),
		CloudInstanceID:        o.configurableClusterOptions.PowerVSCloudInstanceID,
		CloudConnection:        o.configurableClusterOptions.PowerVSCloudConnection,
		VPC:                    o.configurableClusterOptions.PowerVSVPC,
		PER:                    o.configurableClusterOptions.PowerVSPER,
		TransitGatewayLocation: o.configurableClusterOptions.PowerVSTransitGatewayLocation,
		TransitGateway:         o.configurableClusterOptions.PowerVSTransitGateway,
	}
}

// Complete is intended to be called after flags have been bound and sets
// up additional contextual defaulting.
func (o *options) Complete() error {

	if shouldTestCPOOverride() {
		o.LatestReleaseImage, o.PreviousReleaseImage = controlplaneoperatoroverrides.LatestOverrideTestReleases()
	}

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

	if o.RequestServingIsolation {
		if o.ManagementClusterName == "" || o.ManagementClusterNamespace == "" || o.ManagementParentKubeconfig == "" {
			errs = append(errs, fmt.Errorf("management cluster name, namespace, and parent kubeconfig are required to test request serving isolation"))
		}
	}

	return apierr.NewAggregate(errs)
}

var _ flag.Value = &stringSliceVar{}

// stringSliceVar mimicks github.com/spf13/pflag.StringSliceVar in a stdlib-compatible way
type stringSliceVar []string

func (s *stringSliceVar) String() string     { return strings.Join(*s, ",") }
func (s *stringSliceVar) Set(v string) error { *s = append(*s, strings.Split(v, ",")...); return nil }

type stringMapVar map[string]string

func (s *stringMapVar) String() string {
	if *s == nil {
		return ""
	}
	return fmt.Sprintf("%v", *s)
}

func (s *stringMapVar) Set(value string) error {
	split := strings.Split(value, "=")
	if len(split) != 2 {
		return fmt.Errorf("invalid argument: %s", value)
	}
	if *s == nil {
		*s = map[string]string{}
	}
	map[string]string(*s)[split[0]] = split[1]
	return nil
}

func shouldTestCPOOverride() bool {
	return os.Getenv("TEST_CPO_OVERRIDE") == "1"
}
