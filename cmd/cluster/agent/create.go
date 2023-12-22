package agent

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/hypershift/api/fixtures"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/util"
)

func NewCreateCommand(opts *core.CreateOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "agent",
		Short:        "Creates basic functional HostedCluster resources on Agent",
		SilenceUsage: true,
	}

	opts.AgentPlatform = core.AgentPlatformCreateOptions{
		APIServerAddress: "",
		AgentNamespace:   "",
	}

	cmd.Flags().StringVar(&opts.AgentPlatform.APIServerAddress, "api-server-address", opts.AgentPlatform.APIServerAddress, "The IP address to be used for the hosted cluster's Kubernetes API communication. Requires management cluster connectivity if left unset.")
	cmd.Flags().StringVar(&opts.AgentPlatform.AgentNamespace, "agent-namespace", opts.AgentPlatform.AgentNamespace, "The namespace in which to search for Agents")
	_ = cmd.MarkFlagRequired("agent-namespace")
	_ = cmd.MarkPersistentFlagRequired("pull-secret")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if opts.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
			defer cancel()
		}

		if err := createCluster(ctx, opts); err != nil {
			opts.Log.Error(err, "Failed to create cluster")
			return err
		}
		return nil
	}

	return cmd
}

func createCluster(ctx context.Context, opts *core.CreateOptions) error {
	return core.CreateCluster(ctx, opts, ApplyPlatformSpecificsValues)
}

func ApplyPlatformSpecificsValues(ctx context.Context, exampleOptions *fixtures.ExampleOptions, opts *core.CreateOptions) (err error) {
	if opts.AgentPlatform.APIServerAddress == "" {
		opts.AgentPlatform.APIServerAddress, err = core.GetAPIServerAddressByNode(ctx, opts.Log)
		if err != nil {
			return err
		}
	}

	infraID := opts.InfraID
	exampleOptions.InfraID = infraID
	exampleOptions.BaseDomain = opts.BaseDomain
	if exampleOptions.BaseDomain == "" {
		exampleOptions.BaseDomain = "example.com"
	}

	exampleOptions.Agent = &fixtures.ExampleAgentOptions{
		APIServerAddress: opts.AgentPlatform.APIServerAddress,
		AgentNamespace:   opts.AgentPlatform.AgentNamespace,
	}

	// Validate that the agent namespace exists
	agentNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: opts.AgentPlatform.AgentNamespace,
		},
	}
	client, err := util.GetClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	if err := client.Get(ctx, crclient.ObjectKeyFromObject(agentNamespace), agentNamespace); err != nil {
		return fmt.Errorf("failed to get agent namespace: %w", err)
	}

	return nil
}
