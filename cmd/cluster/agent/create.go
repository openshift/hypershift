package agent

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/globalconfig"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/util"
)

type CreateOptions struct {
	APIServerAddress   string
	AgentNamespace     string
	AgentLabelSelector string
}

func (o *CreateOptions) Validate(ctx context.Context, options *core.CreateOptions) error {
	return nil
}

func (o *CreateOptions) Complete(ctx context.Context, opts *core.CreateOptions) error {
	var err error
	if o.APIServerAddress == "" {
		o.APIServerAddress, err = core.GetAPIServerAddressByNode(ctx, opts.Log)
	}
	if opts.DefaultDual {
		// Using this AgentNamespace field because I cannot infer the Provider we are using at this point
		// TODO (jparrill): Refactor this to use a 'forward' instead of a 'backward' logic flow
		if len(o.AgentNamespace) <= 0 {
			return fmt.Errorf("--default-dual is only supported on Agent platform")
		}
		opts.ClusterCIDR = []string{globalconfig.DefaultIPv4ClusterCIDR, globalconfig.DefaultIPv6ClusterCIDR}
		opts.ServiceCIDR = []string{globalconfig.DefaultIPv4ServiceCIDR, globalconfig.DefaultIPv6ServiceCIDR}
	}
	return err
}

func (o *CreateOptions) ApplyPlatformSpecifics(cluster *hyperv1.HostedCluster) error {
	if cluster.Spec.DNS.BaseDomain == "" {
		cluster.Spec.DNS.BaseDomain = "example.com"
	}
	cluster.Spec.Platform = hyperv1.PlatformSpec{
		Type: hyperv1.AgentPlatform,
		Agent: &hyperv1.AgentPlatformSpec{
			AgentNamespace: o.AgentNamespace,
		},
	}
	cluster.Spec.Services = core.GetServicePublishingStrategyMappingByAPIServerAddress(o.APIServerAddress, cluster.Spec.Networking.NetworkType)
	return nil
}

func (o *CreateOptions) GenerateNodePools(defaultNodePool core.DefaultNodePoolConstructor) []*hyperv1.NodePool {
	nodePool := defaultNodePool(hyperv1.AgentPlatform, "")
	nodePool.Spec.Platform.Agent = &hyperv1.AgentNodePoolPlatform{}
	if nodePool.Spec.Management.UpgradeType == "" {
		nodePool.Spec.Management.UpgradeType = hyperv1.UpgradeTypeInPlace
	}
	if o.AgentLabelSelector != "" {
		agentSelector, err := metav1.ParseToLabelSelector(o.AgentLabelSelector)
		if err != nil {
			panic(fmt.Sprintf("Failed to parse AgentLabelSelector: %s", err))
		}
		nodePool.Spec.Platform.Agent.AgentLabelSelector = agentSelector
	}
	return []*hyperv1.NodePool{nodePool}
}

func (o *CreateOptions) GenerateResources() ([]crclient.Object, error) {
	return []crclient.Object{
		&rbacv1.Role{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Role",
				APIVersion: rbacv1.SchemeGroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Namespace: o.AgentNamespace,
				Name:      "capi-provider-role",
			},
			Rules: []rbacv1.PolicyRule{
				{
					APIGroups: []string{"agent-install.openshift.io"},
					Resources: []string{"agents"},
					Verbs:     []string{"*"},
				},
			},
		},
	}, nil
}

var _ core.Platform = (*CreateOptions)(nil)

func BindOptions(opts *CreateOptions, flags *pflag.FlagSet) {
	flags.StringVar(&opts.APIServerAddress, "api-server-address", opts.APIServerAddress, "The IP address to be used for the hosted cluster's Kubernetes API communication. Requires management cluster connectivity if left unset.")
	flags.StringVar(&opts.AgentNamespace, "agent-namespace", opts.AgentNamespace, "The namespace in which to search for Agents")
	flags.StringVar(&opts.AgentLabelSelector, "agentLabelSelector", opts.AgentLabelSelector, "A LabelSelector for selecting Agents according to their labels, e.g., 'size=large,zone notin (az1,az2)'")
}

func NewCreateCommand(opts *core.CreateOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "agent",
		Short:        "Creates basic functional HostedCluster resources on Agent",
		SilenceUsage: true,
	}

	agentOpts := &CreateOptions{}
	BindOptions(agentOpts, cmd.Flags())
	_ = cmd.MarkFlagRequired("agent-namespace")
	_ = cmd.MarkPersistentFlagRequired("pull-secret")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if opts.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
			defer cancel()
		}

		if err := CreateCluster(ctx, opts, agentOpts); err != nil {
			opts.Log.Error(err, "Failed to create cluster")
			return err
		}
		return nil
	}

	return cmd
}

func CreateCluster(ctx context.Context, opts *core.CreateOptions, agentOpts *CreateOptions) error {
	// Validate that the agent namespace exists
	agentNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: agentOpts.AgentNamespace,
		},
	}
	client, err := util.GetClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	if err := client.Get(ctx, crclient.ObjectKeyFromObject(agentNamespace), agentNamespace); err != nil {
		return fmt.Errorf("failed to get agent namespace: %w", err)
	}

	return core.CreateCluster(ctx, opts, agentOpts)
}
