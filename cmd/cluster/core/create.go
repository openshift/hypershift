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

	"golang.org/x/crypto/ssh"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/wait"
	kubeclient "k8s.io/client-go/kubernetes"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/blang/semver"
	"github.com/go-logr/logr"
	apifixtures "github.com/openshift/hypershift/api/fixtures"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/cmd/version"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/releaseinfo"
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

	// BeforeApply is called immediately before resources are applied to the
	// server, giving the user an opportunity to inspect or mutate the resources.
	// This is intended primarily for e2e testing and should be used with care.
	BeforeApply func(crclient.Object) `json:"-"`
}

type PowerVSPlatformOptions struct {
	ResourceGroup   string
	Region          string
	Zone            string
	CloudInstanceID string
	CloudConnection string
	VPCRegion       string
	VPC             string
	VPCSubnet       string
	Debug           bool
	RecreateSecrets bool

	// nodepool related options
	SysType    string
	ProcType   hyperv1.PowerVSNodePoolProcType
	Processors string
	Memory     int32
}

type AgentPlatformCreateOptions struct {
	APIServerAddress string
	AgentNamespace   string
}

type NonePlatformCreateOptions struct {
	APIServerAddress          string
	ExposeThroughLoadBalancer bool
}

type KubevirtPlatformCreateOptions struct {
	ServicePublishingStrategy  string
	APIServerAddress           string
	Memory                     string
	Cores                      uint32
	ContainerDiskImage         string
	RootVolumeSize             uint32
	RootVolumeStorageClass     string
	RootVolumeAccessModes      string
	RootVolumeVolumeMode       string
	InfraKubeConfigFile        string
	InfraNamespace             string
	CacheStrategyType          string
	InfraStorageClassMappings  []string
	NetworkInterfaceMultiQueue string
	QoSClass                   string
}

type AWSPlatformOptions struct {
	AWSCredentialsFile      string
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
}

type AzurePlatformOptions struct {
	CredentialsFile   string
	Location          string
	InstanceType      string
	DiskSizeGB        int32
	AvailabilityZones []string
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

	if err := defaultNetworkType(ctx, opts, &releaseinfo.RegistryClientProvider{}, os.ReadFile); err != nil {
		return nil, fmt.Errorf("failed to default network: %w", err)
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
		object.SetLabels(map[string]string{util.AutoInfraLabelName: exampleOptions.InfraID})
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
		// Validate HostedCluster with this name doesn't exists in the namespace
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

func defaultNetworkType(ctx context.Context, opts *CreateOptions, releaseProvider releaseinfo.Provider, readFile func(string) ([]byte, error)) error {
	if opts.NetworkType != "" {
		return nil
	} else if opts.ReleaseImage == "" {
		opts.NetworkType = string(hyperv1.OVNKubernetes)
		return nil
	}

	version, err := getReleaseSemanticVersion(ctx, opts, releaseProvider, readFile)
	if err != nil {
		return fmt.Errorf("failed to get version for release image %s: %w", opts.ReleaseImage, err)
	}
	if version.Minor > 10 {
		opts.NetworkType = string(hyperv1.OVNKubernetes)
	} else {
		opts.NetworkType = string(hyperv1.OpenShiftSDN)
	}

	return nil
}

func getReleaseSemanticVersion(ctx context.Context, opts *CreateOptions, provider releaseinfo.Provider, readFile func(string) ([]byte, error)) (*semver.Version, error) {
	var pullSecretBytes []byte
	var err error
	if len(opts.CredentialSecretName) > 0 {
		pullSecretBytes, err = util.GetPullSecret(opts.CredentialSecretName, opts.Namespace)
		if err != nil {
			return nil, err
		}
	}
	// overrides secret if set
	if len(opts.PullSecretFile) > 0 {
		pullSecretBytes, err = readFile(opts.PullSecretFile)
		if err != nil {
			return nil, fmt.Errorf("cannot read pull secret file %s: %w", opts.PullSecretFile, err)
		}

	}

	releaseImage, err := provider.Lookup(ctx, opts.ReleaseImage, pullSecretBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to get version information from %s: %w", opts.ReleaseImage, err)
	}
	semanticVersion, err := semver.Parse(releaseImage.Version())
	if err != nil {
		return nil, err
	}
	return &semanticVersion, nil
}
