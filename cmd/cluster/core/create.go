package core

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	"golang.org/x/crypto/ssh"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/wait"
	kubeclient "k8s.io/client-go/kubernetes"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/go-logr/logr"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/cmd/version"
	apifixtures "github.com/openshift/hypershift/examples/fixtures"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/globalconfig"
	"github.com/openshift/hypershift/support/infraid"
	hyperutil "github.com/openshift/hypershift/support/util"
)

// ApplyPlatformSpecifics can be used to create platform specific values as well as enriching the fixture with additional values
type ApplyPlatformSpecifics = func(ctx context.Context, fixture *apifixtures.ExampleOptions, options *CreateOptions) error

type CreateOptions struct {
	AdditionalTrustBundle            string
	Annotations                      []string
	AutoRepair                       bool
	ControlPlaneAvailabilityPolicy   string
	ControlPlaneOperatorImage        string
	EtcdStorageClass                 string
	FIPS                             bool
	GenerateSSH                      bool
	ImageContentSources              string
	InfrastructureAvailabilityPolicy string
	InfrastructureJSON               string
	InfraID                          string
	Name                             string
	Namespace                        string
	BaseDomain                       string
	BaseDomainPrefix                 string
	NetworkType                      string
	NodePoolReplicas                 int32
	NodeDrainTimeout                 time.Duration
	PullSecretFile                   string
	ReleaseImage                     string
	ReleaseStream                    string
	Render                           bool
	SSHKeyFile                       string
	ServiceCIDR                      []string
	ClusterCIDR                      []string
	DefaultDual                      bool
	ExternalDNSDomain                string
	Arch                             string
	NodeSelector                     map[string]string
	NonePlatform                     NonePlatformCreateOptions
	KubevirtPlatform                 KubevirtPlatformCreateOptions
	AWSPlatform                      AWSPlatformOptions
	AgentPlatform                    AgentPlatformCreateOptions
	AzurePlatform                    AzurePlatformOptions
	PowerVSPlatform                  PowerVSPlatformOptions
	Wait                             bool
	Timeout                          time.Duration
	Log                              logr.Logger
	SkipAPIBudgetVerification        bool
	CredentialSecretName             string
	NodeUpgradeType                  hyperv1.UpgradeType
	PausedUntil                      string
	OLMCatalogPlacement              hyperv1.OLMCatalogPlacement
	OLMDisableDefaultSources         bool

	// BeforeApply is called immediately before resources are applied to the
	// server, giving the user an opportunity to inspect or mutate the resources.
	// This is intended primarily for e2e testing and should be used with care.
	BeforeApply func(crclient.Object) `json:"-"`
}

type PowerVSPlatformOptions struct {
	// ResourceGroup to use in IBM Cloud
	ResourceGroup string
	// Region to use in PowerVS service in IBM Cloud
	Region string
	// Zone to use in PowerVS service in IBM Cloud
	Zone string
	// CloudInstanceID of the existing PowerVS service instance
	// Set this field when reusing existing resources from IBM Cloud
	CloudInstanceID string
	// CloudConnection is name of the existing cloud connection
	// Set this field when reusing existing resources from IBM Cloud
	CloudConnection string
	// VPCRegion to use in IBM Cloud
	// Set this field when reusing existing resources from IBM Cloud
	VPCRegion string
	// VPC is name of the existing VPC instance
	VPC string
	// Debug flag is to enable debug logs in powervs client
	Debug bool
	// RecreateSecrets flag is to delete the existing secrets created in IBM Cloud and recreate new secrets
	// This is required since cannot recover the secret once its created
	// Can be used during rerun
	RecreateSecrets bool
	// PER flag is to choose Power Edge Router via Transit Gateway instead of using cloud connections to connect VPC
	PER bool
	// TransitGatewayLocation to use in Transit gateway service in IBM Cloud
	TransitGatewayLocation string
	// TransitGateway is name of the existing Transit gateway instance
	// Set this field when reusing existing resources from IBM Cloud
	TransitGateway string

	// nodepool related options
	// SysType of the worker node in PowerVS service
	SysType string
	// ProcType of the worker node in PowerVS service
	ProcType hyperv1.PowerVSNodePoolProcType
	// Processors count of the worker node in PowerVS service
	Processors string
	// Memory of the worker node in PowerVS service
	Memory int32
}

type AgentPlatformCreateOptions struct {
	APIServerAddress   string
	AgentNamespace     string
	AgentLabelSelector string
}

type NonePlatformCreateOptions struct {
	APIServerAddress          string
	ExposeThroughLoadBalancer bool
}

type KubevirtPlatformCreateOptions struct {
	ServicePublishingStrategy        string
	APIServerAddress                 string
	Memory                           string
	Cores                            uint32
	ContainerDiskImage               string
	RootVolumeSize                   uint32
	RootVolumeStorageClass           string
	RootVolumeAccessModes            string
	RootVolumeVolumeMode             string
	InfraKubeConfigFile              string
	InfraNamespace                   string
	CacheStrategyType                string
	InfraStorageClassMappings        []string
	InfraVolumeSnapshotClassMappings []string
	NetworkInterfaceMultiQueue       string
	QoSClass                         string
	AdditionalNetworks               []string
	AttachDefaultNetwork             *bool
	VmNodeSelector                   map[string]string
}

type AWSPlatformOptions struct {
	AWSCredentialsOpts      awsutil.AWSCredentialsOptions
	AdditionalTags          []string
	IAMJSON                 string
	InstanceType            string
	IssuerURL               string
	PrivateZoneID           string
	PublicZoneID            string
	Region                  string
	RootVolumeIOPS          int64
	RootVolumeSize          int64
	RootVolumeType          string
	RootVolumeEncryptionKey string
	EndpointAccess          string
	Zones                   []string
	EtcdKMSKeyARN           string
	EnableProxy             bool
	SingleNATGateway        bool
	MultiArch               bool
}

type AzurePlatformOptions struct {
	CredentialsFile        string
	Location               string
	EncryptionKeyID        string
	InstanceType           string
	DiskSizeGB             int32
	AvailabilityZones      []string
	ResourceGroupName      string
	VnetID                 string
	DiskEncryptionSetID    string
	NetworkSecurityGroupID string
	EnableEphemeralOSDisk  bool
	DiskStorageAccountType string
	ResourceGroupTags      map[string]string
	SubnetID               string
}

func createCommonFixture(ctx context.Context, opts *CreateOptions) (*apifixtures.ExampleOptions, error) {
	// allow client side defaulting when release image is empty but release stream is set.
	if len(opts.ReleaseImage) == 0 && len(opts.ReleaseStream) != 0 {
		defaultVersion, err := version.LookupDefaultOCPVersion(opts.ReleaseStream)
		if err != nil {
			return nil, fmt.Errorf("release image is required when unable to lookup default OCP version: %w", err)
		}
		opts.ReleaseImage = defaultVersion.PullSpec
	}

	annotations := map[string]string{}
	for _, s := range opts.Annotations {
		pair := strings.SplitN(s, "=", 2)
		if len(pair) != 2 {
			return nil, fmt.Errorf("invalid annotation: %s", s)
		}
		k, v := pair[0], pair[1]
		annotations[k] = v
	}

	if len(opts.ControlPlaneOperatorImage) > 0 {
		annotations[hyperv1.ControlPlaneOperatorImageAnnotation] = opts.ControlPlaneOperatorImage
	}

	var pullSecret []byte
	var err error
	if len(opts.CredentialSecretName) > 0 {
		pullSecret, err = util.GetPullSecret(opts.CredentialSecretName, opts.Namespace)
		if err != nil {
			return nil, err
		}
	}
	// overrides if pullSecretFile is set
	if len(opts.PullSecretFile) > 0 {
		pullSecret, err = os.ReadFile(opts.PullSecretFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read pull secret file: %w", err)
		}
	}
	var sshKey, sshPrivateKey []byte
	if len(opts.CredentialSecretName) > 0 {
		var secret *corev1.Secret
		secret, err = util.GetSecret(opts.CredentialSecretName, opts.Namespace)
		if err != nil {
			return nil, err
		}
		sshKey = secret.Data["ssh-publickey"]
		if len(sshKey) == 0 {
			return nil, fmt.Errorf("the ssh-publickey is invalid {namespace: %s, secret: %s}", opts.Namespace, opts.CredentialSecretName)
		}
		sshPrivateKey = secret.Data["ssh-privatekey"]
		if len(sshPrivateKey) == 0 {
			return nil, fmt.Errorf("the ssh-privatekey is invalid {namespace: %s, secret: %s}", opts.Namespace, opts.CredentialSecretName)
		}
	}
	// overrides secret if SSHKeyFile is set
	if len(opts.SSHKeyFile) > 0 {
		if opts.GenerateSSH {
			return nil, fmt.Errorf("--generate-ssh and --ssh-key cannot be specified together")
		}
		key, err := os.ReadFile(opts.SSHKeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read ssh key file: %w", err)
		}
		sshKey = key
	} else if opts.GenerateSSH {
		sshKey, sshPrivateKey, err = generateSSHKeys()
		if err != nil {
			return nil, fmt.Errorf("failed to generate ssh keys: %w", err)
		}
	}

	if opts.DefaultDual {
		// Using this AgentNamespace field because I cannot infer the Provider we are using at this point
		// TODO (jparrill): Refactor this to use generic validations as same as we use the ApplyPlatformSpecificsValues in a follow up PR
		if len(opts.AgentPlatform.AgentNamespace) <= 0 {
			return nil, fmt.Errorf("--default-dual is only supported on Agent platform")
		}
		opts.ClusterCIDR = []string{globalconfig.DefaultIPv4ClusterCIDR, globalconfig.DefaultIPv6ClusterCIDR}
		opts.ServiceCIDR = []string{globalconfig.DefaultIPv4ServiceCIDR, globalconfig.DefaultIPv6ServiceCIDR}
	}

	var userCABundle []byte
	if len(opts.AdditionalTrustBundle) > 0 {
		userCABundle, err = os.ReadFile(opts.AdditionalTrustBundle)
		if err != nil {
			return nil, fmt.Errorf("failed to read additional trust bundle file: %w", err)
		}
	}

	var imageContentSources []hyperv1.ImageContentSource
	if len(opts.ImageContentSources) > 0 {
		icspFileBytes, err := os.ReadFile(opts.ImageContentSources)
		if err != nil {
			return nil, fmt.Errorf("failed to read image content sources file: %w", err)
		}

		err = yaml.Unmarshal(icspFileBytes, &imageContentSources)
		if err != nil {
			return nil, fmt.Errorf("unable to deserialize image content sources file: %w", err)
		}

	}

	// validate pausedUntil value
	// valid values are either "true" or RFC3339 format date
	if len(opts.PausedUntil) > 0 && opts.PausedUntil != "true" {
		_, err := time.Parse(time.RFC3339, opts.PausedUntil)
		if err != nil {
			return nil, fmt.Errorf("invalid pausedUntil value, should be \"true\" or a valid RFC3339 date format: %w", err)
		}
	}

	var operatorHub *configv1.OperatorHubSpec
	if opts.OLMDisableDefaultSources {
		operatorHub = &configv1.OperatorHubSpec{
			DisableAllDefaultSources: true,
		}
	}

	if len(opts.InfraID) == 0 {
		opts.InfraID = infraid.New(opts.Name)
	}

	return &apifixtures.ExampleOptions{
		AdditionalTrustBundle:            string(userCABundle),
		ImageContentSources:              imageContentSources,
		InfraID:                          opts.InfraID,
		Annotations:                      annotations,
		AutoRepair:                       opts.AutoRepair,
		ControlPlaneAvailabilityPolicy:   hyperv1.AvailabilityPolicy(opts.ControlPlaneAvailabilityPolicy),
		FIPS:                             opts.FIPS,
		InfrastructureAvailabilityPolicy: hyperv1.AvailabilityPolicy(opts.InfrastructureAvailabilityPolicy),
		Namespace:                        opts.Namespace,
		Name:                             opts.Name,
		NetworkType:                      hyperv1.NetworkType(opts.NetworkType),
		NodePoolReplicas:                 opts.NodePoolReplicas,
		NodeDrainTimeout:                 opts.NodeDrainTimeout,
		PullSecret:                       pullSecret,
		ReleaseImage:                     opts.ReleaseImage,
		SSHPrivateKey:                    sshPrivateKey,
		SSHPublicKey:                     sshKey,
		EtcdStorageClass:                 opts.EtcdStorageClass,
		ServiceCIDR:                      opts.ServiceCIDR,
		ClusterCIDR:                      opts.ClusterCIDR,
		Arch:                             opts.Arch,
		NodeSelector:                     opts.NodeSelector,
		UpgradeType:                      opts.NodeUpgradeType,
		PausedUntil:                      opts.PausedUntil,
		OLMCatalogPlacement:              opts.OLMCatalogPlacement,
		OperatorHub:                      operatorHub,
	}, nil
}

func generateSSHKeys() ([]byte, []byte, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, nil, err
	}
	privateDER := x509.MarshalPKCS1PrivateKey(privateKey)
	privatePEMBlock := pem.Block{
		Type:    "RSA PRIVATE KEY",
		Headers: nil,
		Bytes:   privateDER,
	}
	privatePEM := pem.EncodeToMemory(&privatePEMBlock)

	publicRSAKey, err := ssh.NewPublicKey(&privateKey.PublicKey)
	if err != nil {
		return nil, nil, err
	}
	publicBytes := ssh.MarshalAuthorizedKey(publicRSAKey)

	return publicBytes, privatePEM, nil
}

func apply(ctx context.Context, l logr.Logger, exampleOptions *apifixtures.ExampleOptions, waitForRollout bool, mutate func(crclient.Object)) error {
	exampleObjects := exampleOptions.Resources().AsObjects()

	client, err := util.GetClient()
	if err != nil {
		return err
	}
	if mutate != nil {
		for _, object := range exampleObjects {
			mutate(object)
		}
	}
	var hostedCluster *hyperv1.HostedCluster
	for _, object := range exampleObjects {
		key := crclient.ObjectKeyFromObject(object)

		labels := object.GetLabels()
		if labels == nil {
			labels = make(map[string]string)
		}
		labels[util.AutoInfraLabelName] = exampleOptions.InfraID
		object.SetLabels(labels)

		var err error
		if object.GetObjectKind().GroupVersionKind().Kind == "HostedCluster" {
			hostedCluster = &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Namespace: object.GetNamespace(), Name: object.GetName()}}
			err = client.Create(ctx, object)
		} else {
			err = client.Patch(ctx, object, crclient.Apply, crclient.ForceOwnership, crclient.FieldOwner("hypershift-cli"))
		}
		if err != nil {
			return fmt.Errorf("failed to apply object %q: %w", key, err)
		}
		l.Info("Applied Kube resource", "kind", object.GetObjectKind().GroupVersionKind().Kind, "namespace", key.Namespace, "name", key.Name)
	}

	if waitForRollout {
		l.Info("Waiting for cluster rollout")
		return wait.PollInfiniteWithContext(ctx, 30*time.Second, func(ctx context.Context) (bool, error) {
			hostedCluster := hostedCluster.DeepCopy()
			if err := client.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster); err != nil {
				return false, fmt.Errorf("failed to get hostedcluster %s: %w", crclient.ObjectKeyFromObject(hostedCluster), err)
			}
			rolledOut := hostedCluster.Status.Version != nil && len(hostedCluster.Status.Version.History) > 0 && hostedCluster.Status.Version.History[0].CompletionTime != nil
			if !rolledOut {
				l.Info("Cluster rollout not finished yet, checking again in 30 seconds...")
			}
			return rolledOut, nil
		})
	}

	return nil
}

func GetAPIServerAddressByNode(ctx context.Context, l logr.Logger) (string, error) {
	// Fetch a single node and determine possible DNS or IP entries to use
	// for external node-port communication.
	// Possible values are considered with the following priority based on the address type:
	// - NodeExternalDNS
	// - NodeExternalIP
	// - NodeInternalIP
	apiServerAddress := ""
	config, err := util.GetConfig()
	if err != nil {
		return "", err
	}
	kubeClient, err := kubeclient.NewForConfig(config)
	if err != nil {
		return "", err
	}
	nodes, err := kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{Limit: 1})
	if err != nil {
		return "", fmt.Errorf("unable to fetch node objects: %w", err)
	}
	if len(nodes.Items) < 1 {
		return "", fmt.Errorf("no node objects found: %w", err)
	}
	addresses := map[corev1.NodeAddressType]string{}
	for _, address := range nodes.Items[0].Status.Addresses {
		addresses[address.Type] = address.Address
	}
	for _, addrType := range []corev1.NodeAddressType{corev1.NodeExternalDNS, corev1.NodeExternalIP, corev1.NodeInternalIP} {
		if address, exists := addresses[addrType]; exists {
			apiServerAddress = address
			break
		}
	}
	if apiServerAddress == "" {
		return "", fmt.Errorf("node %q does not expose any IP addresses, this should not be possible", nodes.Items[0].Name)
	}
	l.Info(fmt.Sprintf("detected %q from node %q as external-api-server-address", apiServerAddress, nodes.Items[0].Name))
	return apiServerAddress, nil
}

func Validate(ctx context.Context, opts *CreateOptions) error {
	if !opts.Render {
		client, err := util.GetClient()
		if err != nil {
			return err
		}
		// Validate HostedCluster with this name doesn't exist in the namespace
		cluster := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Namespace: opts.Namespace, Name: opts.Name}}
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(cluster), cluster); err == nil {
			return fmt.Errorf("hostedcluster %s already exists", crclient.ObjectKeyFromObject(cluster))
		} else if !apierrors.IsNotFound(err) {
			return fmt.Errorf("hostedcluster doesn't exist validation failed with error: %w", err)
		}
	}

	// Validate HostedCluster name follows RFC1123 standard
	// https://kubernetes.io/docs/concepts/overview/working-with-objects/names/#dns-label-names
	errs := validation.IsDNS1123Label(opts.Name)
	if len(errs) > 0 {
		return fmt.Errorf("HostedCluster name failed RFC1123 validation: %s", strings.Join(errs[:], " "))
	}

	// Validate if mgmt cluster and NodePool CPU arches don't match, a multi-arch release image or stream was used
	// Exception for ppc64le arch since management cluster would be in x86 and node pools are going to be in ppc64le arch
	if !opts.AWSPlatform.MultiArch && !opts.Render && opts.Arch != hyperv1.ArchitecturePPC64LE {
		mgmtClusterCPUArch, err := hyperutil.GetMgmtClusterCPUArch(ctx)
		if err != nil {
			return err
		}

		if err = hyperutil.DoesMgmtClusterAndNodePoolCPUArchMatch(mgmtClusterCPUArch, opts.Arch); err != nil {
			opts.Log.Info(fmt.Sprintf("WARNING: %v", err))
		}
	}

	// Validate arch is only hyperv1.ArchitectureAMD64 or hyperv1.ArchitectureARM64 or hyperv1.ArchitecturePPC64LE
	arch := strings.ToLower(opts.Arch)
	switch arch {
	case hyperv1.ArchitectureAMD64:
	case hyperv1.ArchitectureARM64:
	case hyperv1.ArchitecturePPC64LE:
	default:
		return fmt.Errorf("specified arch is not supported: %s", opts.Arch)
	}

	return nil
}

func CreateCluster(ctx context.Context, opts *CreateOptions, platformSpecificApply ApplyPlatformSpecifics) error {
	if opts.Wait && opts.NodePoolReplicas < 1 {
		return errors.New("--wait requires --node-pool-replicas > 0")
	}

	exampleOptions, err := createCommonFixture(ctx, opts)
	if err != nil {
		return err
	}

	// Apply platform specific options and create platform specific resources
	if err := platformSpecificApply(ctx, exampleOptions, opts); err != nil {
		return err
	}

	// In render mode, print the objects and return early
	if opts.Render {
		for _, object := range exampleOptions.Resources().AsObjects() {
			err := hyperapi.YamlSerializer.Encode(object, os.Stdout)
			if err != nil {
				return fmt.Errorf("failed to encode objects: %w", err)
			}
			fmt.Println("---")
		}
		return nil
	}

	// Otherwise, apply the objects
	return apply(ctx, opts.Log, exampleOptions, opts.Wait, opts.BeforeApply)
}
