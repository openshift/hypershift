package openstack

import (
	"context"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/openshift/hypershift/cmd/util"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	openstacknodepool "github.com/openshift/hypershift/cmd/nodepool/openstack"
	corev1 "k8s.io/api/core/v1"
)

func DefaultOptions() *RawCreateOptions {
	return &RawCreateOptions{NodePoolOpts: openstacknodepool.DefaultOptions()}
}

func BindOptions(opts *RawCreateOptions, flags *pflag.FlagSet) {
	bindCoreOptions(opts, flags)
	openstacknodepool.BindOptions(opts.NodePoolOpts, flags)
}

func bindCoreOptions(opts *RawCreateOptions, flags *pflag.FlagSet) {
	flags.StringVar(&opts.OpenStackCredentialsFile, "openstack-credentials-file", opts.OpenStackCredentialsFile, "Path to the OpenStack credentials file (required)")
	flags.StringVar(&opts.OpenStackCACertFile, "openstack-ca-cert-file", opts.OpenStackCACertFile, "Path to the OpenStack CA certificate file (optional)")
	flags.StringVar(&opts.OpenStackExternalNetworkID, "openstack-external-network-id", opts.OpenStackExternalNetworkID, "ID of the OpenStack external network (optional)")
}

type RawCreateOptions struct {
	OpenStackCredentialsFile   string
	OpenStackCACertFile        string
	OpenStackExternalNetworkID string

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
	// Check that the OpenStack credentials file arg is set and that the file exists with the "openstack" cloud
	if o.OpenStackCredentialsFile == "" {
		return nil, fmt.Errorf("OpenStack credentials file is required")
	}

	if _, err := os.Stat(o.OpenStackCredentialsFile); err != nil {
		return nil, fmt.Errorf("OpenStack credentials file does not exist: %w", err)
	}

	cloudsFile, err := os.ReadFile(o.OpenStackCredentialsFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read OpenStack credentials file: %w", err)
	}

	clouds := make(map[string]interface{})
	if err := yaml.Unmarshal(cloudsFile, &clouds); err != nil {
		return nil, fmt.Errorf("failed to parse OpenStack credentials file: %w", err)
	}

	_, ok := clouds["clouds"]
	if !ok {
		return nil, fmt.Errorf("'clouds' key not found in credentials file")
	}

	clouds = clouds["clouds"].(map[string]interface{})
	if _, ok := clouds["openstack"]; !ok {
		return nil, fmt.Errorf("'openstack' cloud not found in credentials file")
	}

	if err := util.ValidateRequiredOption("pull-secret", opts.PullSecretFile); err != nil {
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
		CloudName: "openstack", // TODO: make this configurable or at least check the clouds.yaml file
	}

	if o.OpenStackExternalNetworkID != "" {
		cluster.Spec.Platform.OpenStack.ExternalNetwork = &hyperv1.NetworkParam{
			ID: &o.OpenStackExternalNetworkID,
		}
	}

	cluster.Spec.Services = core.GetIngressServicePublishingStrategyMapping(cluster.Spec.Networking.NetworkType, false)

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
	credentialsContents, err := os.ReadFile(o.OpenStackCredentialsFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read OpenStack credentials file: %w", err)
	}
	credentialsSecret := credentialsSecret(o.namespace, o.name)
	credentialsSecret.Data = map[string][]byte{
		"clouds.yaml": credentialsContents,
	}

	if o.OpenStackCACertFile != "" {
		caCertContents, err := os.ReadFile(o.OpenStackCACertFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read OpenStack CA certificate file: %w", err)
		}
		credentialsSecret.Data["cacert"] = caCertContents
	}

	return []client.Object{credentialsSecret}, nil
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
	cmd.MarkPersistentFlagRequired("pull-secret")

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
