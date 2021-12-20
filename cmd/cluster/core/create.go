package core

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io/ioutil"
	"os"
	"strings"

	apifixtures "github.com/openshift/hypershift/api/fixtures"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/cmd/version"
	hyperapi "github.com/openshift/hypershift/support/api"
	"golang.org/x/crypto/ssh"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeclient "k8s.io/client-go/kubernetes"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// ApplyPlatformSpecifics can be used to create platform specific values as well as enriching the fixure with additional values
type ApplyPlatformSpecifics = func(ctx context.Context, fixture *apifixtures.ExampleOptions, options *CreateOptions) error

type CreateOptions struct {
	Annotations                      []string
	AutoRepair                       bool
	ControlPlaneAvailabilityPolicy   string
	ControlPlaneOperatorImage        string
	EtcdStorageClass                 string
	FIPS                             bool
	GenerateSSH                      bool
	InfrastructureAvailabilityPolicy string
	InfrastructureJSON               string
	InfraID                          string
	Name                             string
	Namespace                        string
	NetworkType                      string
	NodePoolReplicas                 int32
	PullSecretFile                   string
	ReleaseImage                     string
	Render                           bool
	SSHKeyFile                       string
	ServiceCIDR                      string
	PodCIDR                          string
	NonePlatform                     NonePlatformCreateOptions
	AWSPlatform                      AWSPlatformOptions
	AgentPlatform                    AgentPlatformCreateOptions
}

type AgentPlatformCreateOptions struct {
	APIServerAddress string
}

type NonePlatformCreateOptions struct {
	APIServerAddress string
}

type AWSPlatformOptions struct {
	AWSCredentialsFile string
	AdditionalTags     []string
	BaseDomain         string
	IAMJSON            string
	InstanceType       string
	IssuerURL          string
	PrivateZoneID      string
	PublicZoneID       string
	Region             string
	RootVolumeIOPS     int64
	RootVolumeSize     int64
	RootVolumeType     string
	EndpointAccess     string
}

func createCommonFixture(opts *CreateOptions) (*apifixtures.ExampleOptions, error) {
	if len(opts.ReleaseImage) == 0 {
		defaultVersion, err := version.LookupDefaultOCPVersion()
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

	pullSecret, err := ioutil.ReadFile(opts.PullSecretFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read pull secret file: %w", err)
	}
	var sshKey, sshPrivateKey []byte
	if len(opts.SSHKeyFile) > 0 {
		if opts.GenerateSSH {
			return nil, fmt.Errorf("--generate-ssh and --ssh-key cannot be specified together")
		}
		key, err := ioutil.ReadFile(opts.SSHKeyFile)
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

	return &apifixtures.ExampleOptions{
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
		PullSecret:                       pullSecret,
		ReleaseImage:                     opts.ReleaseImage,
		SSHPrivateKey:                    sshPrivateKey,
		SSHPublicKey:                     sshKey,
		EtcdStorageClass:                 opts.EtcdStorageClass,
		ServiceCIDR:                      opts.ServiceCIDR,
		PodCIDR:                          opts.PodCIDR,
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

func apply(ctx context.Context, exampleOptions *apifixtures.ExampleOptions, render bool) error {

	exampleObjects := exampleOptions.Resources().AsObjects()
	switch {
	case render:
		for _, object := range exampleObjects {
			err := hyperapi.YamlSerializer.Encode(object, os.Stdout)
			if err != nil {
				return fmt.Errorf("failed to encode objects: %w", err)
			}
			fmt.Println("---")
		}
	default:
		client := util.GetClientOrDie()
		for _, object := range exampleObjects {
			key := crclient.ObjectKeyFromObject(object)
			object.SetLabels(map[string]string{util.AutoInfraLabelName: exampleOptions.InfraID})
			if err := client.Patch(ctx, object, crclient.Apply, crclient.ForceOwnership, crclient.FieldOwner("hypershift-cli")); err != nil {
				return fmt.Errorf("failed to apply object %q: %w", key, err)
			}
			log.Info("Applied Kube resource", "kind", object.GetObjectKind().GroupVersionKind().Kind, "namespace", key.Namespace, "name", key.Name)
		}
		return nil
	}
	return nil
}

func GetAPIServerAddressByNode(ctx context.Context) (string, error) {
	// Fetch a single node and determine possible DNS or IP entries to use
	// for external node-port communication.
	// Possible values are considered with the following priority based on the address type:
	// - NodeExternalDNS
	// - NodeExternalIP
	// - NodeInternalIP
	apiServerAddress := ""
	kubeClient := kubeclient.NewForConfigOrDie(util.GetConfigOrDie())
	nodes, err := kubeClient.CoreV1().Nodes().List(ctx, v1.ListOptions{Limit: 1})
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
	log.Info(fmt.Sprintf("detected %q from node %q as external-api-server-address", apiServerAddress, nodes.Items[0].Name))
	return apiServerAddress, nil
}

func CreateCluster(ctx context.Context, opts *CreateOptions, platformSpecificApply ApplyPlatformSpecifics) error {
	exampleOptions, err := createCommonFixture(opts)
	if err != nil {
		return err
	}

	// Apply platform specific options and create platform specific resources
	if err := platformSpecificApply(ctx, exampleOptions, opts); err != nil {
		return err
	}

	return apply(ctx, exampleOptions, opts.Render)
}
