package core

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	configv1 "github.com/openshift/api/config/v1"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/ipnet"
	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/cmd/version"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/globalconfig"
	"github.com/openshift/hypershift/support/infraid"
	"github.com/openshift/hypershift/support/releaseinfo/registryclient"
	hyperutil "github.com/openshift/hypershift/support/util"

	"github.com/go-logr/logr"
	"github.com/spf13/pflag"
	"golang.org/x/crypto/ssh"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/wait"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

func DefaultOptions() *RawCreateOptions {
	return &RawCreateOptions{
		Namespace:                      "clusters",
		Name:                           "example",
		ControlPlaneAvailabilityPolicy: string(hyperv1.SingleReplica),
		ServiceCIDR:                    []string{globalconfig.DefaultIPv4ServiceCIDR},
		ClusterCIDR:                    []string{globalconfig.DefaultIPv4ClusterCIDR},
		Log:                            log.Log,
		Arch:                           "amd64",
		OLMCatalogPlacement:            hyperv1.ManagementOLMCatalogPlacement,
		NetworkType:                    string(hyperv1.OVNKubernetes),
	}
}

// BindOptions binds options that should always be exposed to users in all CLIs
func BindOptions(opts *RawCreateOptions, flags *pflag.FlagSet) {
	bindCoreOptions(opts, flags)
}

func bindCoreOptions(opts *RawCreateOptions, flags *pflag.FlagSet) {
	flags.StringVar(&opts.Namespace, "namespace", opts.Namespace, "A namespace to contain the generated resources")
	flags.StringVar(&opts.Name, "name", opts.Name, "A name for the cluster")
	flags.StringVar(&opts.BaseDomain, "base-domain", opts.BaseDomain, "The ingress base domain for the cluster")
	flags.StringVar(&opts.BaseDomainPrefix, "base-domain-prefix", opts.BaseDomainPrefix, "The ingress base domain prefix for the cluster, defaults to cluster name. Use 'none' for an empty prefix")
	flags.StringVar(&opts.ExternalDNSDomain, "external-dns-domain", opts.ExternalDNSDomain, "Sets hostname to opinionated values in the specificed domain for services with publishing type LoadBalancer or Route.")
	flags.StringVar(&opts.NetworkType, "network-type", opts.NetworkType, "Enum specifying the cluster SDN provider. Supports either Calico, OVNKubernetes, OpenShiftSDN or Other.")
	flags.StringVar(&opts.ReleaseImage, "release-image", opts.ReleaseImage, "The OCP release image for the cluster")
	flags.StringVar(&opts.PullSecretFile, "pull-secret", opts.PullSecretFile, "File path to a pull secret.")
	flags.StringVar(&opts.ControlPlaneAvailabilityPolicy, "control-plane-availability-policy", opts.ControlPlaneAvailabilityPolicy, "Availability policy for hosted cluster components. Supported options: SingleReplica, HighlyAvailable")
	flags.BoolVar(&opts.Render, "render", opts.Render, "Render output as YAML to stdout instead of applying")
	flags.StringVar(&opts.RenderInto, "render-into", opts.RenderInto, "Render output as YAML into this file instead of applying. If unset, YAML will be output to stdout.")
	flags.BoolVar(&opts.RenderSensitive, "render-sensitive", opts.RenderSensitive, "enables rendering of sensitive information in the output")
	flags.StringVar(&opts.SSHKeyFile, "ssh-key", opts.SSHKeyFile, "Path to an SSH key file")
	flags.StringVar(&opts.AdditionalTrustBundle, "additional-trust-bundle", opts.AdditionalTrustBundle, "Path to a file with user CA bundle")
	flags.StringVar(&opts.ImageContentSources, "image-content-sources", opts.ImageContentSources, "Path to a file with image content sources")
	flags.Int32Var(&opts.NodePoolReplicas, "node-pool-replicas", opts.NodePoolReplicas, "If 0 or greater, creates a nodepool with that many replicas; else if less than 0, does not create a nodepool.")
	flags.DurationVar(&opts.NodeDrainTimeout, "node-drain-timeout", opts.NodeDrainTimeout, "The NodeDrainTimeout on any created NodePools")
	flags.DurationVar(&opts.NodeVolumeDetachTimeout, "node-volume-detach-timeout", opts.NodeVolumeDetachTimeout, "The NodeVolumeDetachTimeout on any created NodePools")
	flags.StringArrayVar(&opts.Annotations, "annotations", opts.Annotations, "Annotations to apply to the hostedcluster (key=value). Can be specified multiple times.")
	flags.StringArrayVar(&opts.Labels, "labels", opts.Labels, "Labels to apply to the hostedcluster (key=value). Can be specified multiple times.")
	flags.BoolVar(&opts.FIPS, "fips", opts.FIPS, "Enables FIPS mode for nodes in the cluster")
	flags.BoolVar(&opts.AutoRepair, "auto-repair", opts.AutoRepair, "Enables machine autorepair with machine health checks")
	flags.StringVar(&opts.InfrastructureAvailabilityPolicy, "infra-availability-policy", opts.InfrastructureAvailabilityPolicy, "Availability policy for infrastructure services in guest cluster. Supported options: SingleReplica, HighlyAvailable")
	flags.BoolVar(&opts.GenerateSSH, "generate-ssh", opts.GenerateSSH, "If true, generate SSH keys")
	flags.StringVar(&opts.EtcdStorageClass, "etcd-storage-class", opts.EtcdStorageClass, "The persistent volume storage class for etcd data volumes")
	flags.StringVar(&opts.InfraID, "infra-id", opts.InfraID, "Infrastructure ID to use for hosted cluster resources.")
	flags.StringArrayVar(&opts.ServiceCIDR, "service-cidr", opts.ServiceCIDR, "The CIDR of the service network. Can be specified multiple times.")
	flags.StringArrayVar(&opts.ClusterCIDR, "cluster-cidr", opts.ClusterCIDR, "The CIDR of the cluster network. Can be specified multiple times.")
	flags.BoolVar(&opts.DefaultDual, "default-dual", opts.DefaultDual, "Defines the Service and Cluster CIDRs as dual-stack default values. Cannot be defined with service-cidr or cluster-cidr flag.")
	flags.StringToStringVar(&opts.NodeSelector, "node-selector", opts.NodeSelector, "A comma separated list of key=value to use as node selector for the Hosted Control Plane pods to stick to. E.g. role=cp,disk=fast")
	flags.StringArrayVar(&opts.Tolerations, "toleration", opts.Tolerations, "A comma separated list of options for a toleration that will be applied to the hcp pods. Valid options are, key, value, operator, effect, tolerationSeconds. E.g. key=node-role.kubernetes.io/master,operator=Exists,effect=NoSchedule. Can be specified multiple times to add multiple tolerations")
	flags.BoolVar(&opts.Wait, "wait", opts.Wait, "If the create command should block until the cluster is up. Requires at least one node.")
	flags.DurationVar(&opts.Timeout, "timeout", opts.Timeout, "If the --wait flag is set, set the optional timeout to limit the waiting duration. The format is duration; e.g. 30s or 1h30m45s; 0 means no timeout; default = 0")
	flags.Var(&opts.NodeUpgradeType, "node-upgrade-type", "The NodePool upgrade strategy for how nodes should behave when upgraded. Supported options: Replace, InPlace")
	flags.Var(&opts.OLMCatalogPlacement, "olm-catalog-placement", "The OLM Catalog Placement for the HostedCluster. Supported options: Management, Guest")
	flags.BoolVar(&opts.OLMDisableDefaultSources, "olm-disable-default-sources", opts.OLMDisableDefaultSources, "Disables the OLM default catalog sources for the HostedCluster.")
	flags.StringVar(&opts.Arch, "arch", opts.Arch, "The default processor architecture for the NodePool (e.g. arm64, amd64)")
	flags.StringVar(&opts.PausedUntil, "pausedUntil", opts.PausedUntil, "If a date is provided in RFC3339 format, HostedCluster creation is paused until that date. If the boolean true is provided, HostedCluster creation is paused until the field is removed.")
	flags.StringVar(&opts.ReleaseStream, "release-stream", opts.ReleaseStream, "The OCP release stream for the cluster (e.g. 4-stable-multi), this flag is ignored if release-image is set")

}

// BindDeveloperOptions binds options that should only be exposed to developers in the `hypershift` CLI
func BindDeveloperOptions(opts *RawCreateOptions, flags *pflag.FlagSet) {
	bindCoreOptions(opts, flags)

	flags.StringVar(&opts.ControlPlaneOperatorImage, "control-plane-operator-image", opts.ControlPlaneOperatorImage, "Override the default image used to deploy the control plane operator")

	flags.StringVar(&opts.InfrastructureJSON, "infra-json", opts.InfrastructureJSON, "Path to file containing infrastructure information for the cluster. If not specified, infrastructure will be created")
}

type RawCreateOptions struct {
	AdditionalTrustBundle            string
	Annotations                      []string
	Labels                           []string
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
	NodeVolumeDetachTimeout          time.Duration
	PullSecretFile                   string
	ReleaseImage                     string
	ReleaseStream                    string
	Render                           bool
	RenderInto                       string
	RenderSensitive                  bool
	SSHKeyFile                       string
	ServiceCIDR                      []string
	ClusterCIDR                      []string
	DefaultDual                      bool
	ExternalDNSDomain                string
	Arch                             string
	NodeSelector                     map[string]string
	Tolerations                      []string
	Wait                             bool
	Timeout                          time.Duration
	Log                              logr.Logger
	SkipAPIBudgetVerification        bool
	NodeUpgradeType                  hyperv1.UpgradeType
	PausedUntil                      string
	OLMCatalogPlacement              hyperv1.OLMCatalogPlacement
	OLMDisableDefaultSources         bool

	// BeforeApply is called immediately before resources are applied to the
	// server, giving the user an opportunity to inspect or mutate the resources.
	// This is intended primarily for e2e testing and should be used with care.
	BeforeApply func(crclient.Object) `json:"-"`

	// These fields are reverse-completed by the aws CLI since we support a flag that projects
	// them back up here
	PublicKey, PrivateKey, PullSecret []byte
}

type resources struct {
	AdditionalTrustBundle *corev1.ConfigMap
	Namespace             *corev1.Namespace
	PullSecret            *corev1.Secret
	Resources             []crclient.Object
	SSHKey                *corev1.Secret
	Cluster               *hyperv1.HostedCluster
	NodePools             []*hyperv1.NodePool
}

func (r *resources) asObjects() []crclient.Object {
	var objects []crclient.Object

	if object := r.AdditionalTrustBundle; object != nil {
		objects = append(objects, object)
	}
	if object := r.Namespace; object != nil {
		objects = append(objects, object)
	}
	if object := r.PullSecret; object != nil {
		objects = append(objects, object)
	}
	if object := r.SSHKey; object != nil {
		objects = append(objects, object)
	}
	if object := r.Cluster; object != nil {
		objects = append(objects, object)
	}

	// there's no way to check that the objects in `r.Resources` are not nil, as we can have
	// a non-nil controllerruntime.Object interface vtable but a nil object that it points to
	objects = append(objects, r.Resources...)

	for _, object := range r.NodePools {
		if object != nil {
			objects = append(objects, object)
		}
	}
	return objects
}

func prototypeResources(opts *CreateOptions) (*resources, error) {
	prototype := &resources{}
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

	labels := map[string]string{}
	for _, s := range opts.Labels {
		pair := strings.SplitN(s, "=", 2)
		if len(pair) != 2 {
			return nil, fmt.Errorf("invalid label: %s", s)
		}
		k, v := pair[0], pair[1]
		labels[k] = v
	}

	if len(opts.ControlPlaneOperatorImage) > 0 {
		annotations[hyperv1.ControlPlaneOperatorImageAnnotation] = opts.ControlPlaneOperatorImage
	}

	pullSecret := opts.PullSecret
	var err error
	// overrides if pullSecretFile is set
	if len(opts.PullSecretFile) > 0 {
		pullSecret, err = os.ReadFile(opts.PullSecretFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read pull secret file: %w", err)
		}
	}

	prototype.Namespace = &corev1.Namespace{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Namespace",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: opts.Namespace,
		},
	}

	prototype.PullSecret = &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: prototype.Namespace.Name,
			Name:      opts.Name + "-pull-secret",
			Labels:    map[string]string{util.DeleteWithClusterLabelName: "true"},
		},
		Data: map[string][]byte{
			".dockerconfigjson": pullSecret,
		},
	}

	prototype.Cluster = &hyperv1.HostedCluster{
		TypeMeta: metav1.TypeMeta{
			Kind:       "HostedCluster",
			APIVersion: hyperv1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   prototype.Namespace.Name,
			Name:        opts.Name,
			Annotations: annotations,
			Labels:      labels,
		},
		Spec: hyperv1.HostedClusterSpec{
			Release: hyperv1.Release{
				Image: opts.ReleaseImage,
			},
			Etcd: hyperv1.EtcdSpec{
				ManagementType: hyperv1.Managed,
				Managed: &hyperv1.ManagedEtcdSpec{
					Storage: hyperv1.ManagedEtcdStorageSpec{
						Type: hyperv1.PersistentVolumeEtcdStorage,
						PersistentVolume: &hyperv1.PersistentVolumeEtcdStorageSpec{
							Size: &hyperv1.DefaultPersistentVolumeEtcdStorageSize,
						},
					},
				},
			},
			Networking: hyperv1.ClusterNetworking{
				NetworkType: hyperv1.NetworkType(opts.NetworkType),
			},
			InfraID:    opts.InfraID,
			PullSecret: corev1.LocalObjectReference{Name: prototype.PullSecret.Name},
			FIPS:       opts.FIPS,
			DNS: hyperv1.DNSSpec{
				BaseDomain: opts.BaseDomain,
			},
			ControllerAvailabilityPolicy:     hyperv1.AvailabilityPolicy(opts.ControlPlaneAvailabilityPolicy),
			InfrastructureAvailabilityPolicy: hyperv1.AvailabilityPolicy(opts.InfrastructureAvailabilityPolicy),
			Configuration:                    &hyperv1.ClusterConfiguration{},
		},
	}

	if opts.EtcdStorageClass != "" {
		prototype.Cluster.Spec.Etcd.Managed.Storage.PersistentVolume.StorageClassName = ptr.To(opts.EtcdStorageClass)
	}

	sshKey, sshPrivateKey := opts.PublicKey, opts.PrivateKey
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
	if len(sshKey) > 0 {
		prototype.SSHKey = &corev1.Secret{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Secret",
				APIVersion: corev1.SchemeGroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Namespace: prototype.Namespace.Name,
				Name:      opts.Name + "-ssh-key",
				Labels:    map[string]string{util.DeleteWithClusterLabelName: "true"},
			},
			Data: map[string][]byte{
				"id_rsa.pub": sshKey,
			},
		}
		if len(sshPrivateKey) > 0 {
			prototype.SSHKey.Data["id_rsa"] = sshPrivateKey
		}
		prototype.Cluster.Spec.SSHKey = corev1.LocalObjectReference{Name: prototype.SSHKey.Name}
	}

	// validate pausedUntil value
	// valid values are either "true" or RFC3339 format date
	if len(opts.PausedUntil) > 0 && opts.PausedUntil != "true" {
		_, err := time.Parse(time.RFC3339, opts.PausedUntil)
		if err != nil {
			return nil, fmt.Errorf("invalid pausedUntil value, should be \"true\" or a valid RFC3339 date format: %w", err)
		}
		prototype.Cluster.Spec.PausedUntil = &opts.PausedUntil
	}

	if opts.OLMDisableDefaultSources {
		prototype.Cluster.Spec.Configuration.OperatorHub = &configv1.OperatorHubSpec{
			DisableAllDefaultSources: true,
		}
	}

	if len(opts.OLMCatalogPlacement) > 0 {
		prototype.Cluster.Spec.OLMCatalogPlacement = opts.OLMCatalogPlacement
	}

	var clusterNetworkEntries []hyperv1.ClusterNetworkEntry
	for _, cidr := range opts.ClusterCIDR {
		parsedCIDR, err := ipnet.ParseCIDR(cidr)
		if err != nil {
			return nil, fmt.Errorf("parsing ClusterCIDR (%s): %w", cidr, err)
		}
		clusterNetworkEntries = append(clusterNetworkEntries, hyperv1.ClusterNetworkEntry{CIDR: *parsedCIDR})
	}
	prototype.Cluster.Spec.Networking.ClusterNetwork = clusterNetworkEntries

	var serviceNetworkEntries []hyperv1.ServiceNetworkEntry
	for _, cidr := range opts.ServiceCIDR {
		parsedCIDR, err := ipnet.ParseCIDR(cidr)
		if err != nil {
			return nil, fmt.Errorf("parsing ServiceCIDR (%s): %w", cidr, err)
		}
		serviceNetworkEntries = append(serviceNetworkEntries, hyperv1.ServiceNetworkEntry{CIDR: *parsedCIDR})
	}
	prototype.Cluster.Spec.Networking.ServiceNetwork = serviceNetworkEntries

	if opts.NodeSelector != nil {
		prototype.Cluster.Spec.NodeSelector = opts.NodeSelector
	}

	for _, tStr := range opts.Tolerations {
		toleration, err := parseTolerationString(tStr)
		if err != nil {
			return nil, err
		}
		prototype.Cluster.Spec.Tolerations = append(prototype.Cluster.Spec.Tolerations, *toleration)
	}

	if len(opts.AdditionalTrustBundle) > 0 {
		userCABundle, err := os.ReadFile(opts.AdditionalTrustBundle)
		if err != nil {
			return nil, fmt.Errorf("failed to read additional trust bundle file: %w", err)
		}
		prototype.AdditionalTrustBundle = &corev1.ConfigMap{
			TypeMeta: metav1.TypeMeta{
				Kind:       "ConfigMap",
				APIVersion: corev1.SchemeGroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "user-ca-bundle",
				Namespace: prototype.Namespace.Name,
			},
			Data: map[string]string{
				"ca-bundle.crt": string(userCABundle),
			},
		}
		prototype.Cluster.Spec.AdditionalTrustBundle = &corev1.LocalObjectReference{Name: prototype.AdditionalTrustBundle.Name}
	}

	if len(opts.ImageContentSources) > 0 {
		icspFileBytes, err := os.ReadFile(opts.ImageContentSources)
		if err != nil {
			return nil, fmt.Errorf("failed to read image content sources file: %w", err)
		}

		var imageContentSources []hyperv1.ImageContentSource
		err = yaml.Unmarshal(icspFileBytes, &imageContentSources)
		if err != nil {
			return nil, fmt.Errorf("unable to deserialize image content sources file: %w", err)
		}
		prototype.Cluster.Spec.ImageContentSources = imageContentSources
	}

	return prototype, nil
}

func generateSSHKeys() ([]byte, []byte, error) {
	privateKey, err := rsa.GenerateKey(certs.Reader(), 4096)
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

func apply(ctx context.Context, l logr.Logger, infraID string, objects []crclient.Object, waitForRollout bool, mutate func(crclient.Object)) error {
	client, err := util.GetClient()
	if err != nil {
		return err
	}
	if mutate != nil {
		for _, object := range objects {
			mutate(object)
		}
	}
	var hostedCluster *hyperv1.HostedCluster
	for _, object := range objects {
		key := crclient.ObjectKeyFromObject(object)

		labels := object.GetLabels()
		if labels == nil {
			labels = make(map[string]string)
		}
		labels[util.AutoInfraLabelName] = infraID
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

// validatedCreateOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedCreateOptions struct {
	*RawCreateOptions
}

type ValidatedCreateOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedCreateOptions
}

func (opts *RawCreateOptions) Validate(ctx context.Context) (*ValidatedCreateOptions, error) {
	if opts.Wait && opts.NodePoolReplicas < 1 {
		return nil, errors.New("--wait requires --node-pool-replicas > 0")
	}

	// Validate HostedCluster name follows RFC1123 standard
	// https://kubernetes.io/docs/concepts/overview/working-with-objects/names/#dns-label-names
	errs := validation.IsDNS1123Label(opts.Name)
	if len(errs) > 0 {
		return nil, fmt.Errorf("HostedCluster name failed RFC1123 validation: %s", strings.Join(errs[:], " "))
	}

	if !opts.Render {
		client, err := util.GetClient()
		if err != nil {
			return nil, err
		}
		// Validate HostedCluster with this name doesn't exist in the namespace
		cluster := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Namespace: opts.Namespace, Name: opts.Name}}
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(cluster), cluster); err == nil {
			return nil, fmt.Errorf("hostedcluster %s already exists", crclient.ObjectKeyFromObject(cluster))
		} else if !apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("hostedcluster doesn't exist validation failed with error: %w", err)
		}

		// Validate multi-arch aspects
		kc, err := hyperutil.GetKubeClientSet()
		if err != nil {
			return nil, err
		}
		if err = validateMgmtClusterAndNodePoolCPUArchitectures(ctx, opts, kc); err != nil {
			return nil, err
		}
	}

	// Validate arch is only hyperv1.ArchitectureAMD64 or hyperv1.ArchitectureARM64 or hyperv1.ArchitecturePPC64LE
	arch := strings.ToLower(opts.Arch)
	switch arch {
	case hyperv1.ArchitectureAMD64:
	case hyperv1.ArchitectureARM64:
	case hyperv1.ArchitecturePPC64LE:
	default:
		return nil, fmt.Errorf("specified arch %q is not supported", opts.Arch)
	}

	return &ValidatedCreateOptions{
		validatedCreateOptions: &validatedCreateOptions{
			RawCreateOptions: opts,
		},
	}, nil
}

// completedCreateOptions is a private wrapper that enforces a call of Complete() before CreateCluster() can be invoked.
type completedCreateOptions struct {
	*ValidatedCreateOptions
}

type CreateOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedCreateOptions
}

func (opts *ValidatedCreateOptions) Complete() (*CreateOptions, error) {
	if len(opts.InfraID) == 0 {
		opts.InfraID = infraid.New(opts.Name)
	}

	if opts.RenderInto != "" {
		opts.Render = true
	}

	return &CreateOptions{
		completedCreateOptions: &completedCreateOptions{
			ValidatedCreateOptions: opts,
		},
	}, nil
}

// PlatformValidator knows how to validate platform options.
type PlatformValidator interface {
	// Validate allows the platform-specific logic to validate inputs.
	Validate(context.Context, *CreateOptions) (PlatformCompleter, error)
}

// PlatformCompleter knows how to absorb configuration.
type PlatformCompleter interface {
	// Complete allows the platform-specific logic to default values from the agnostic set.
	Complete(context.Context, *CreateOptions) (Platform, error)
}

// Platform closes over the work that each platform does when creating a HostedCluster and the associated
// resources. The Platform knows how to decorate the HostedClusterSpec itself as well as how to add new
// resources to the list that needs to be applied to the cluster for a functional guest cluster.
type Platform interface {
	// ApplyPlatformSpecifics decorates the HostedCluster prototype created from common options with platform-
	// specific values and cAonfigurations.
	ApplyPlatformSpecifics(*hyperv1.HostedCluster) error

	// GenerateNodePools generates the NodePools that need to exist for this guest cluster to be functional.
	GenerateNodePools(DefaultNodePoolConstructor) []*hyperv1.NodePool

	// GenerateResources generates the additional resources that need to exist for this guest cluster to be functional.
	GenerateResources() ([]crclient.Object, error)
}

func CreateCluster(ctx context.Context, rawOpts *RawCreateOptions, rawPlatform PlatformValidator) error {
	validatedOpts, err := rawOpts.Validate(ctx)
	if err != nil {
		return err
	}

	opts, err := validatedOpts.Complete()
	if err != nil {
		return err
	}

	completer, err := rawPlatform.Validate(ctx, opts)
	if err != nil {
		return err
	}

	platform, err := completer.Complete(ctx, opts)
	if err != nil {
		return fmt.Errorf("could not complete platform specific options: %w", err)
	}

	resources, err := prototypeResources(opts)
	if err != nil {
		return err
	}

	if err := platform.ApplyPlatformSpecifics(resources.Cluster); err != nil {
		return fmt.Errorf("failed to apply platform specifics: %w", err)
	}

	if opts.NodePoolReplicas > -1 {
		nodePools := platform.GenerateNodePools(defaultNodePool(opts))
		if len(opts.PausedUntil) > 0 {
			for _, nodePool := range nodePools {
				nodePool.Spec.PausedUntil = &opts.PausedUntil
			}
		}
		resources.NodePools = nodePools
	}

	additional, err := platform.GenerateResources()
	if err != nil {
		return fmt.Errorf("could not generate additional resources: %w", err)
	}
	resources.Resources = append(resources.Resources, additional...)

	postProcess(resources, opts)

	// In render mode, print the objects and return early
	if opts.Render {
		output := os.Stdout
		if opts.RenderInto != "" {
			var err error
			output, err = os.Create(opts.RenderInto)
			if err != nil {
				return fmt.Errorf("failed to create file for rendering output: %w", err)
			}
			defer func() {
				if err := output.Close(); err != nil {
					fmt.Printf("failed to close file for rendering output: %v\n", err)
				}
			}()
		}
		for _, object := range resources.asObjects() {
			if !opts.RenderSensitive {
				if _, ok := object.(*corev1.Secret); ok {
					continue
				}
			}

			err := hyperapi.YamlSerializer.Encode(object, output)
			if err != nil {
				return fmt.Errorf("failed to encode objects: %w", err)
			}
			if _, err := fmt.Fprintln(output, "---"); err != nil {
				return fmt.Errorf("failed to write object separator: %w", err)
			}
		}
		return nil
	}

	// Otherwise, apply the objects
	return apply(ctx, opts.Log, resources.Cluster.Spec.InfraID, resources.asObjects(), opts.Wait, opts.BeforeApply)
}

type DefaultNodePoolConstructor func(platformType hyperv1.PlatformType, suffix string) *hyperv1.NodePool

func defaultNodePool(opts *CreateOptions) func(platformType hyperv1.PlatformType, suffix string) *hyperv1.NodePool {
	return func(platformType hyperv1.PlatformType, suffix string) *hyperv1.NodePool {
		name := opts.Name
		if suffix != "" {
			name = fmt.Sprintf("%s-%s", opts.Name, suffix)
		}
		return &hyperv1.NodePool{
			TypeMeta: metav1.TypeMeta{
				Kind:       "NodePool",
				APIVersion: hyperv1.GroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Namespace: opts.Namespace,
				Name:      name,
			},
			Spec: hyperv1.NodePoolSpec{
				Management: hyperv1.NodePoolManagement{
					AutoRepair:  opts.AutoRepair,
					UpgradeType: opts.NodeUpgradeType,
				},
				Replicas:    &opts.NodePoolReplicas,
				ClusterName: opts.Name,
				Release: hyperv1.Release{
					Image: opts.ReleaseImage,
				},
				Platform: hyperv1.NodePoolPlatform{
					Type: platformType,
				},
				Arch:                    opts.Arch,
				NodeDrainTimeout:        &metav1.Duration{Duration: opts.NodeDrainTimeout},
				NodeVolumeDetachTimeout: &metav1.Duration{Duration: opts.NodeVolumeDetachTimeout},
			},
		}
	}
}

func GetIngressServicePublishingStrategyMapping(netType hyperv1.NetworkType, usesExternalDNS bool) []hyperv1.ServicePublishingStrategyMapping {
	// TODO (Alberto): Default KAS to Route if endpointAccess is Private.
	apiServiceStrategy := hyperv1.LoadBalancer
	if usesExternalDNS {
		apiServiceStrategy = hyperv1.Route
	}
	services := map[hyperv1.ServiceType]hyperv1.PublishingStrategyType{
		hyperv1.APIServer:    apiServiceStrategy,
		hyperv1.OAuthServer:  hyperv1.Route,
		hyperv1.Konnectivity: hyperv1.Route,
		hyperv1.Ignition:     hyperv1.Route,
	}
	var ret []hyperv1.ServicePublishingStrategyMapping
	for service, strategy := range services {
		ret = append(ret, hyperv1.ServicePublishingStrategyMapping{
			Service: service,
			ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
				Type: strategy,
			},
		})
	}
	sort.Slice(ret, func(i, j int) bool {
		return ret[i].Service < ret[j].Service
	})
	return ret
}

func GetServicePublishingStrategyMappingByAPIServerAddress(APIServerAddress string, netType hyperv1.NetworkType) []hyperv1.ServicePublishingStrategyMapping {
	services := []hyperv1.ServiceType{
		hyperv1.APIServer,
		hyperv1.OAuthServer,
		hyperv1.OIDC,
		hyperv1.Konnectivity,
		hyperv1.Ignition,
	}
	var ret []hyperv1.ServicePublishingStrategyMapping
	for _, service := range services {
		ret = append(ret, hyperv1.ServicePublishingStrategyMapping{
			Service: service,
			ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
				Type:     hyperv1.NodePort,
				NodePort: &hyperv1.NodePortPublishingStrategy{Address: APIServerAddress},
			},
		})
	}
	sort.Slice(ret, func(i, j int) bool {
		return ret[i].Service < ret[j].Service
	})
	return ret
}

func postProcess(r *resources, opts *CreateOptions) {
	// If secret encryption was not specified, default to AESCBC
	if r.Cluster.Spec.SecretEncryption == nil {
		encryptionSecret := etcdEncryptionKeySecret(opts)
		r.Resources = append(r.Resources, encryptionSecret)
		r.Cluster.Spec.SecretEncryption = &hyperv1.SecretEncryptionSpec{
			Type: hyperv1.AESCBC,
			AESCBC: &hyperv1.AESCBCSpec{
				ActiveKey: corev1.LocalObjectReference{
					Name: encryptionSecret.Name,
				},
			},
		}
	}
}

func etcdEncryptionKeySecret(opts *CreateOptions) *corev1.Secret {
	generatedKey := make([]byte, 32)
	_, err := io.ReadFull(certs.Reader(), generatedKey)
	if err != nil {
		panic(fmt.Sprintf("failed to generate random etcd key: %v", err))
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      opts.Name + "-etcd-encryption-key",
			Namespace: opts.Namespace,
			Labels:    map[string]string{util.DeleteWithClusterLabelName: "true"},
		},
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		Data: map[string][]byte{
			hyperv1.AESCBCKeySecretKey: generatedKey,
		},
		Type: corev1.SecretTypeOpaque,
	}
}

func parseTolerationString(str string) (*corev1.Toleration, error) {
	keyVals := strings.Split(str, ",")
	if len(keyVals) == 0 {
		return nil, nil
	}

	var toleration corev1.Toleration
	for _, kv := range keyVals {
		split := strings.Split(kv, "=")
		if len(split) != 2 {
			return nil, fmt.Errorf("invalid toleration cli argument. [%s]", str)
		}

		split[0] = strings.TrimSpace(split[0])
		split[1] = strings.TrimSpace(split[1])

		// The cli arg ignores case, and normalizes values to their enum equivalent. This
		// prevents confusing validation feedback like "unknown effect type [noSchedule]"
		// just because someone didn't set NoSchedule with a capital 'N'. It's an easy mistake.
		switch strings.ToLower(split[0]) {
		case "key":
			toleration.Key = split[1]
		case "value":
			toleration.Value = split[1]
		case "operator":
			switch strings.ToLower(split[1]) {
			case strings.ToLower(string(corev1.TolerationOpExists)):
				toleration.Operator = corev1.TolerationOpExists
			case strings.ToLower(string(corev1.TolerationOpEqual)):
				toleration.Operator = corev1.TolerationOpEqual
			case "":
			default:
				return nil, fmt.Errorf("invalid toleration cli argument. unknown operator type [%s]", split[1])
			}
		case "effect":
			switch strings.ToLower(split[1]) {
			case strings.ToLower(string(corev1.TaintEffectNoSchedule)):
				toleration.Effect = corev1.TaintEffectNoSchedule
			case strings.ToLower(string(corev1.TaintEffectPreferNoSchedule)):
				toleration.Effect = corev1.TaintEffectPreferNoSchedule
			case strings.ToLower(string(corev1.TaintEffectNoExecute)):
				toleration.Effect = corev1.TaintEffectNoExecute
			case "":
			default:
				return nil, fmt.Errorf("invalid toleration cli argument. unknown effect type [%s]", split[1])
			}

		case "tolerationseconds":
			seconds, err := strconv.Atoi(split[1])
			if err != nil {
				return nil, fmt.Errorf("invalid toleration cli argument. failed to parse tolerationSeconds [%s]", split[1])
			}
			i64 := int64(seconds)
			toleration.TolerationSeconds = &i64
		default:
			return nil, fmt.Errorf("invalid toleration cli argument. unknown field [%s]", split[0])
		}
	}

	return &toleration, nil
}

// validateMgmtClusterAndNodePoolCPUArchitectures checks if a multi-arch release image or release stream was provided.
// If none were provided, checks to make sure the NodePool CPU arch and the management cluster CPU arch match; if they
// do not, the CLI will return an error since the NodePool will fail to complete during runtime.
func validateMgmtClusterAndNodePoolCPUArchitectures(ctx context.Context, opts *RawCreateOptions, kc kubeclient.Interface) error {
	validMultiArchImage := false

	// Check if the release image is multi-arch
	if len(opts.ReleaseImage) > 0 && len(opts.PullSecretFile) > 0 {
		pullSecret, err := os.ReadFile(opts.PullSecretFile)
		if err != nil {
			return fmt.Errorf("failed to read pull secret file: %w", err)
		}

		validMultiArchImage, err = registryclient.IsMultiArchManifestList(ctx, opts.ReleaseImage, pullSecret)
		if err != nil {
			return err
		}
	}

	// If not release image was provided, check if a release stream was provided instead and its multi-arch
	if opts.ReleaseImage == "" && len(opts.ReleaseStream) > 0 && strings.Contains(opts.ReleaseStream, "multi") {
		validMultiArchImage = true
	}

	// If a release image/stream is not multi-arch, check the mgmt & NodePool CPU architectures match
	if !validMultiArchImage {
		mgmtClusterCPUArch, err := hyperutil.GetMgmtClusterCPUArch(kc)
		if err != nil {
			return fmt.Errorf("failed to check mgmt cluster CPU arch: %v", err)
		}

		if !strings.EqualFold(mgmtClusterCPUArch, opts.Arch) {
			return fmt.Errorf("multi-arch hosted cluster is not enabled and "+
				"management cluster and nodepool cpu architectures do not match; "+
				"please use a multi-arch release image or a multi-arch release stream - management cluster cpu arch: %s, nodepool cpu arch: %s", mgmtClusterCPUArch, opts.Arch)
		}
	}

	return nil
}
