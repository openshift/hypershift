package openstack

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/ipnet"
	"github.com/openshift/hypershift/cmd/cluster/core"
	openstacknodepool "github.com/openshift/hypershift/cmd/nodepool/openstack"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"gopkg.in/yaml.v3"
)

const (
	credentialCloudName = "openstack"
)

func DefaultOptions() *RawCreateOptions {
	return &RawCreateOptions{
		OpenStackImageRetentionPolicy: hyperv1.PruneRetentionPolicy,
		NodePoolOpts:                  openstacknodepool.DefaultOptions(),
	}
}

func BindOptions(opts *RawCreateOptions, flags *pflag.FlagSet) {
	bindCoreOptions(opts, flags)
	openstacknodepool.BindOptions(opts.NodePoolOpts, flags)
}

func bindCoreOptions(opts *RawCreateOptions, flags *pflag.FlagSet) {
	// TODO(stephenfin): This is unnecessary given the information should already be in clouds.yaml. We should deprecate and remove it.
	flags.StringVar(&opts.OpenStackCredentialsFile, "openstack-credentials-file", opts.OpenStackCredentialsFile, "Path to the OpenStack credentials file (optional)")
	flags.StringVar(&opts.OpenStackCloud, "openstack-cloud", opts.OpenStackCloud, "Name of the cloud in clouds.yaml (optional) (default: 'openstack')")
	flags.StringVar(&opts.OpenStackCACertFile, "openstack-ca-cert-file", opts.OpenStackCACertFile, "Path to the OpenStack CA certificate file (optional)")
	flags.StringVar(&opts.OpenStackExternalNetworkID, "openstack-external-network-id", opts.OpenStackExternalNetworkID, "ID of the OpenStack external network (optional)")
	flags.StringVar(&opts.OpenStackIngressFloatingIP, "openstack-ingress-floating-ip", opts.OpenStackIngressFloatingIP, "An available floating IP in your OpenStack cluster that will be associated with the OpenShift ingress port (optional)")
	flags.StringSliceVar(&opts.OpenStackDNSNameservers, "openstack-dns-nameservers", opts.OpenStackDNSNameservers, "List of DNS nameservers to use for the cluster (optional)")
	flags.Var(&opts.OpenStackImageRetentionPolicy, "openstack-image-retention-policy", "OpenStack Glance Image retention policy. Valid values are 'Orphan' and 'Prune'. By default images are orphaned. (optional)")
}

type RawCreateOptions struct {
	OpenStackCredentialsFile      string
	OpenStackCloud                string
	OpenStackCACertFile           string
	OpenStackExternalNetworkID    string
	OpenStackIngressFloatingIP    string
	OpenStackDNSNameservers       []string
	OpenStackImageRetentionPolicy hyperv1.RetentionPolicy

	NodePoolOpts *openstacknodepool.RawOpenStackPlatformCreateOptions
}

// validatedCreateOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedCreateOptions struct {
	*RawCreateOptions

	*openstacknodepool.ValidatedOpenStackPlatformCreateOptions
}

type ValidatedCreateOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedCreateOptions
}

// completedCreateOptions is a private wrapper that enforces a call of Complete() before cluster creation can be invoked.
type completedCreateOptions struct {
	*ValidatedCreateOptions

	CompletedNodePoolOpts *openstacknodepool.OpenStackPlatformCreateOptions

	externalDNSDomain string
	name, namespace   string
}

type CreateOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedCreateOptions
}

func (o *ValidatedCreateOptions) Complete(ctx context.Context, opts *core.CreateOptions) (core.Platform, error) {
	output := &CreateOptions{
		completedCreateOptions: &completedCreateOptions{
			ValidatedCreateOptions: o,
			externalDNSDomain:      opts.ExternalDNSDomain,
			name:                   opts.Name,
			namespace:              opts.Namespace,
		},
	}

	completed, err := o.ValidatedOpenStackPlatformCreateOptions.Complete()
	output.CompletedNodePoolOpts = completed
	return output, err
}

func (o *RawCreateOptions) Validate(ctx context.Context, opts *core.CreateOptions) (core.PlatformCompleter, error) {
	// Check that the OpenStack credentials file arg is set and that the file exists with the correct cloud
	if o.OpenStackCredentialsFile != "" {
		if _, err := os.Stat(o.OpenStackCredentialsFile); err != nil {
			return nil, fmt.Errorf("OpenStack credentials file does not exist: %w", err)
		}
	} else {
		credentialsFile, err := findOpenStackCredentialsFile()
		if err != nil {
			return nil, fmt.Errorf("failed to find clouds.yaml file: %w", err)
		}
		if credentialsFile == "" {
			return nil, fmt.Errorf("failed to find clouds.yaml file")
		}
		o.OpenStackCredentialsFile = credentialsFile
	}

	if o.OpenStackCloud == "" {
		cloud := os.Getenv("OS_CLOUD")
		if cloud == "" {
			cloud = credentialCloudName
		}
		o.OpenStackCloud = cloud
	}

	_, _, err := extractCloud(o.OpenStackCredentialsFile, o.OpenStackCACertFile, o.OpenStackCloud)
	if err != nil {
		return nil, fmt.Errorf("failed to read OpenStack credentials file: %w", err)
	}

	if err := util.ValidateRequiredOption("pull-secret", opts.PullSecretFile); err != nil {
		return nil, err
	}

	if err := validateRetentionPolicy(o.OpenStackImageRetentionPolicy); err != nil {
		return nil, err
	}

	if opts.ExternalDNSDomain != "" {
		err := fmt.Errorf("--external-dns-domain is not supported on OpenStack")
		opts.Log.Error(err, "Failed to create cluster")
		return nil, err
	}

	validOpts := &ValidatedCreateOptions{
		validatedCreateOptions: &validatedCreateOptions{
			RawCreateOptions: o,
		},
	}

	validOpts.ValidatedOpenStackPlatformCreateOptions, err = o.NodePoolOpts.Validate()

	return validOpts, err
}

func (o *RawCreateOptions) ApplyPlatformSpecifics(cluster *hyperv1.HostedCluster) error {
	cluster.Spec.Platform = hyperv1.PlatformSpec{
		Type:      hyperv1.OpenStackPlatform,
		OpenStack: &hyperv1.OpenStackPlatformSpec{},
	}

	cluster.Spec.Platform.OpenStack.IdentityRef = hyperv1.OpenStackIdentityReference{
		Name:      credentialsSecret(cluster.Namespace, cluster.Name).Name,
		CloudName: credentialCloudName,
	}

	if o.OpenStackExternalNetworkID != "" {
		cluster.Spec.Platform.OpenStack.ExternalNetwork = &hyperv1.NetworkParam{
			ID: &o.OpenStackExternalNetworkID,
		}
	}

	if o.OpenStackIngressFloatingIP != "" {
		cluster.Spec.Platform.OpenStack.IngressFloatingIP = o.OpenStackIngressFloatingIP
	}

	// If the user has specified DNS nameservers, it'll be used when creating the managed subnet(s).
	// In the case where the user wants to override the other fields of the managed subnets, they can
	// first render the cluster spec and then provide the needed configuration as API allows (for
	// example the allocation pools).
	if len(o.OpenStackDNSNameservers) > 0 {
		if len(cluster.Spec.Platform.OpenStack.ManagedSubnets) == 0 {
			cluster.Spec.Platform.OpenStack.ManagedSubnets = make([]hyperv1.SubnetSpec, 1)
		}
		for i := range cluster.Spec.Platform.OpenStack.ManagedSubnets {
			cluster.Spec.Platform.OpenStack.ManagedSubnets[i].DNSNameservers = o.OpenStackDNSNameservers
		}
	}

	cluster.Spec.Services = core.GetIngressServicePublishingStrategyMapping(cluster.Spec.Networking.NetworkType, false)

	// MachineNetwork has no default in Hypershift, but it's convenient to have one for OpenStack:
	// * To specify the subnet that CAPO will manage.
	// * To inform CCM the preferred subnet for kubelet's NodeIPs.
	if len(cluster.Spec.Networking.MachineNetwork) == 0 {
		cluster.Spec.Networking.MachineNetwork = []hyperv1.MachineNetworkEntry{{CIDR: *ipnet.MustParseCIDR(config.DefaultMachineNetwork)}}
	}

	cluster.Spec.Platform.OpenStack.ImageRetentionPolicy = hyperv1.RetentionPolicy(o.OpenStackImageRetentionPolicy)

	return nil
}

func (o *CreateOptions) GenerateNodePools(constructor core.DefaultNodePoolConstructor) []*hyperv1.NodePool {
	nodePool := constructor(hyperv1.OpenStackPlatform, "")
	if nodePool.Spec.Management.UpgradeType == "" {
		nodePool.Spec.Management.UpgradeType = hyperv1.UpgradeTypeReplace
	}
	nodePool.Spec.Platform.OpenStack = o.CompletedNodePoolOpts.NodePoolPlatform()
	return []*hyperv1.NodePool{nodePool}
}

func (o *CreateOptions) GenerateResources() ([]client.Object, error) {
	resources := []client.Object{}
	cloudsYAML, caCert, err := extractCloud(o.OpenStackCredentialsFile, o.OpenStackCACertFile, o.OpenStackCloud)
	if err != nil {
		return nil, fmt.Errorf("failed to read OpenStack credentials file: %w", err)
	}

	credentialsSecret := credentialsSecret(o.namespace, o.name)
	credentialsSecret.Data = map[string][]byte{
		"clouds.yaml": cloudsYAML,
	}

	if caCert != nil {
		credentialsSecret.Data["cacert"] = caCert
	}

	resources = append(resources, credentialsSecret)

	return resources, nil
}

func credentialsSecret(namespace, name string) *corev1.Secret {
	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "-cloud-credentials",
			Namespace: namespace,
			Labels:    map[string]string{util.DeleteWithClusterLabelName: "true"},
		},
		Type: corev1.SecretTypeOpaque,
	}
}

var _ core.Platform = (*CreateOptions)(nil)

func NewCreateCommand(opts *core.RawCreateOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "openstack",
		Short:        "Creates basic functional HostedCluster resources on OpenStack platform",
		SilenceUsage: true,
	}

	openstackOpts := DefaultOptions()
	BindOptions(openstackOpts, cmd.Flags())
	_ = cmd.MarkPersistentFlagRequired("pull-secret")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if opts.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
			defer cancel()
		}

		if err := core.CreateCluster(ctx, opts, openstackOpts); err != nil {
			opts.Log.Error(err, "Failed to create cluster")
			return err
		}
		return nil
	}

	return cmd
}

// findOpenStackCredentialsFile searches for a clouds.yaml in the standard locations,
// returning the first match found else the empty string
func findOpenStackCredentialsFile() (string, error) {
	currDir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	paths := []string{
		filepath.Join(currDir, "clouds.yaml"),
		filepath.Join(homeDir, ".config", "openstack", "clouds.yaml"),
		"/etc/openstack/clouds.yaml",
	}

	if os.Getenv("OS_CLIENT_CONFIG_FILE") != "" {
		paths = append([]string{os.Getenv("OS_CLIENT_CONFIG_FILE")}, paths...)
	}

	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}

	return "", nil
}

// extractCloud extracts the relevant cloud from a provided clouds.yaml and return a new clouds.yaml
// with only that cloud in it and using a well-known cloud name
func extractCloud(cloudsYAMLPath, caCertPath, cloudName string) ([]byte, []byte, error) {
	cloudsFile, err := os.ReadFile(cloudsYAMLPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read OpenStack credentials file: %w", err)
	}

	clouds := make(map[string]interface{})
	if err := yaml.Unmarshal(cloudsFile, &clouds); err != nil {
		return nil, nil, fmt.Errorf("failed to parse OpenStack credentials file: %w", err)
	}

	_, ok := clouds["clouds"]
	if !ok {
		return nil, nil, fmt.Errorf("'clouds' key not found in credentials file")
	}

	clouds = clouds["clouds"].(map[string]any)
	if _, ok := clouds[cloudName]; !ok {
		return nil, nil, fmt.Errorf("'%s' cloud not found in credentials file", cloudName)
	}

	cloud := clouds[cloudName].(map[string]any)
	if _, ok := cloud["cacert"]; ok {
		if caCertPath == "" {
			caCertPath = cloud["cacert"].(string)
		}
		// Always unset this key if present since it's not used and can therefore be confusing. We
		// set '[Global] ca-file' in the cloud provider and CSI configs, which means takes priority
		// over configuration sourced from clouds.yaml
		// https://github.com/kubernetes/cloud-provider-openstack/blob/v1.31.0/pkg/client/client.go#L228
		delete(cloud, "cacert")
	}

	var caCert []byte
	if caCertPath != "" {
		caCert, err = os.ReadFile(caCertPath)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read CA cert: %w", err)
		}
	}

	cloudsYAML, err := yaml.Marshal(map[string]any{
		"clouds": map[string]any{credentialCloudName: cloud},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshall OpenStack credentials file: %w", err)
	}

	return cloudsYAML, caCert, nil
}

// validateRetentionPolicy validates the retention policy is valid
func validateRetentionPolicy(policy hyperv1.RetentionPolicy) error {
	if policy == "" {
		return nil
	}

	switch policy {
	case hyperv1.OrphanRetentionPolicy, hyperv1.PruneRetentionPolicy:
		return nil
	default:
		return fmt.Errorf("invalid retention policy: %s", policy)
	}
}
