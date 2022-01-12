package core

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io/ioutil"
	"strings"
	"time"

	apifixtures "github.com/openshift/hypershift/api/fixtures"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	nodepoolcore "github.com/openshift/hypershift/cmd/nodepool/core"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/cmd/version"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	kubeclient "k8s.io/client-go/kubernetes"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type CreateClusterPlatformOptions interface {
	// ApplyPlatformSpecifics can be used to create platform specific values as well as enriching the fixure with additional values
	ApplyPlatformSpecifics(ctx context.Context, fixture *apifixtures.ExampleOptions, name, infraID, baseDomain string) error
	// Validate checks if the platform options configured as expected, otherwise return error
	Validate() error
	// NodePoolPlatformOptions returns the options for the specific platform NodePool creation
	NodePoolPlatformOptions() nodepoolcore.PlatformOptions
}

type CreateOptions struct {
	Annotations                      []string
	ControlPlaneAvailabilityPolicy   string
	ControlPlaneOperatorImage        string
	EtcdStorageClass                 string
	FIPS                             bool
	GenerateSSH                      bool
	InfrastructureAvailabilityPolicy string
	InfraID                          string
	Name                             string
	Namespace                        string
	BaseDomain                       string
	NetworkType                      string
	PullSecretFile                   string
	ReleaseImage                     string
	Render                           bool
	SSHKeyFile                       string
	ServiceCIDR                      string
	PodCIDR                          string
	Wait                             bool
	Timeout                          time.Duration
	CreateNodePoolOptions            *nodepoolcore.CreateNodePoolOptions
}

func (o *CreateOptions) createCommonFixture() (*apifixtures.ExampleOptions, error) {
	if len(o.ReleaseImage) == 0 {
		defaultVersion, err := version.LookupDefaultOCPVersion()
		if err != nil {
			return nil, fmt.Errorf("release image is required when unable to lookup default OCP version: %w", err)
		}
		o.ReleaseImage = defaultVersion.PullSpec
	}

	annotations := map[string]string{}
	for _, s := range o.Annotations {
		pair := strings.SplitN(s, "=", 2)
		if len(pair) != 2 {
			return nil, fmt.Errorf("invalid annotation: %s", s)
		}
		k, v := pair[0], pair[1]
		annotations[k] = v
	}

	if len(o.ControlPlaneOperatorImage) > 0 {
		annotations[hyperv1.ControlPlaneOperatorImageAnnotation] = o.ControlPlaneOperatorImage
	}

	pullSecret, err := ioutil.ReadFile(o.PullSecretFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read pull secret file: %w", err)
	}
	var sshKey, sshPrivateKey []byte
	if len(o.SSHKeyFile) > 0 {
		if o.GenerateSSH {
			return nil, fmt.Errorf("--generate-ssh and --ssh-key cannot be specified together")
		}
		key, err := ioutil.ReadFile(o.SSHKeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read ssh key file: %w", err)
		}
		sshKey = key
	} else if o.GenerateSSH {
		sshKey, sshPrivateKey, err = generateSSHKeys()
		if err != nil {
			return nil, fmt.Errorf("failed to generate ssh keys: %w", err)
		}
	}

	return &apifixtures.ExampleOptions{
		InfraID:                          o.InfraID,
		Annotations:                      annotations,
		ControlPlaneAvailabilityPolicy:   hyperv1.AvailabilityPolicy(o.ControlPlaneAvailabilityPolicy),
		FIPS:                             o.FIPS,
		InfrastructureAvailabilityPolicy: hyperv1.AvailabilityPolicy(o.InfrastructureAvailabilityPolicy),
		Namespace:                        o.Namespace,
		Name:                             o.Name,
		NetworkType:                      hyperv1.NetworkType(o.NetworkType),
		PullSecret:                       pullSecret,
		ReleaseImage:                     o.ReleaseImage,
		SSHPrivateKey:                    sshPrivateKey,
		SSHPublicKey:                     sshKey,
		EtcdStorageClass:                 o.EtcdStorageClass,
		ServiceCIDR:                      o.ServiceCIDR,
		PodCIDR:                          o.PodCIDR,
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

func waitForRollout(ctx context.Context, clusterName, clusterNamespace string, client crclient.Client) error {
	log.Info("Waiting for cluster rollout")
	hostedCluster := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: clusterNamespace,
			Name:      clusterName,
		},
	}
	return wait.PollInfiniteWithContext(ctx, 30*time.Second, func(ctx context.Context) (bool, error) {
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster); err != nil {
			return false, fmt.Errorf("failed to get hostedcluster %s: %w", crclient.ObjectKeyFromObject(hostedCluster), err)
		}
		rolledOut := len(hostedCluster.Status.Version.History) > 0 && hostedCluster.Status.Version.History[0].CompletionTime != nil
		if !rolledOut {
			log.Info("Cluster rollout not finished yet, checking again in 30 seconds...")
		}
		return rolledOut, nil
	})
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
	log.Info(fmt.Sprintf("detected %q from node %q as external-api-server-address", apiServerAddress, nodes.Items[0].Name))
	return apiServerAddress, nil
}

func (o *CreateOptions) validate(ctx context.Context, platformOpts CreateClusterPlatformOptions) error {
	if !o.Render {
		client := util.GetClientOrDie()
		// Validate HostedCluster with this name doesn't exists in the namespace
		cluster := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Namespace: o.Namespace, Name: o.Name}}
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(cluster), cluster); err == nil {
			return fmt.Errorf("hostedcluster %s already exists", crclient.ObjectKeyFromObject(cluster))
		} else if !apierrors.IsNotFound(err) {
			return fmt.Errorf("hostedcluster doesn't exist validation failed with error: %w", err)
		}
	}
	if err := platformOpts.Validate(); err != nil {
		return err
	}

	return nil
}

func (o *CreateOptions) CreateExecFunc(platformOpts CreateClusterPlatformOptions) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if o.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, o.Timeout)
			defer cancel()
		}

		if err := o.CreateCluster(ctx, platformOpts); err != nil {
			log.Error(err, "Failed to create cluster")
			return err
		}
		return nil
	}
}

func (o *CreateOptions) CreateCluster(ctx context.Context, platformOpts CreateClusterPlatformOptions) error {
	o.CreateNodePoolOptions.Render = o.Render
	if err := o.validate(ctx, platformOpts); err != nil {
		return err
	}
	if o.Wait && o.CreateNodePoolOptions.NodeCount < 1 {
		return errors.New("--wait requires --node-count > 0")
	}
	exampleOptions, err := o.createCommonFixture()
	if err != nil {
		return err
	}

	// Apply platform specific options and create platform specific resources
	if err := platformOpts.ApplyPlatformSpecifics(ctx, exampleOptions, o.Name, o.InfraID, o.BaseDomain); err != nil {
		return err
	}

	client := util.GetClientOrDie()

	exampleResources := exampleOptions.Resources()
	exampleObjects := exampleResources.AsObjects()
	nodePoolObject, err := o.CreateNodePoolOptions.GenerateNodePoolObject(ctx, platformOpts.NodePoolPlatformOptions(), exampleResources.Cluster, client)
	if err != nil {
		return err
	}
	if nodePoolObject != nil {
		exampleObjects = append(exampleObjects, nodePoolObject)
	}

	if err := util.ApplyObjects(ctx, exampleObjects, o.Render, exampleOptions.InfraID); err != nil {
		return err
	}
	if !o.Render && o.Wait {
		if err := waitForRollout(ctx, exampleOptions.Name, exampleOptions.Namespace, client); err != nil {
			return err
		}
	}

	return nil
}
