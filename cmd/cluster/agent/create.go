package agent

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	apifixtures "github.com/openshift/hypershift/api/fixtures"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	kubeclient "k8s.io/client-go/kubernetes"
)

func NewCreateCommand(opts *core.CreateOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "agent",
		Short:        "Creates basic functional HostedCluster resources on Agent",
		SilenceUsage: true,
	}

	opts.AgentPlatform = core.AgentPlatformCreateOptions{}

	cmd.Run = func(cmd *cobra.Command, args []string) {
		ctx, cancel := context.WithCancel(context.Background())
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT)
		go func() {
			<-sigs
			cancel()
		}()

		if err := CreateCluster(ctx, opts); err != nil {
			log.Error(err, "Failed to create cluster")
			os.Exit(1)
		}
	}

	return cmd
}

func CreateCluster(ctx context.Context, opts *core.CreateOptions) error {
	return core.CreateCluster(ctx, opts, applyPlatformSpecificsValues)
}

func applyPlatformSpecificsValues(ctx context.Context, exampleOptions *apifixtures.ExampleOptions, opts *core.CreateOptions) (err error) {
	// Fetch a single node and determine possible DNS or IP entries to use
	// for external node-port communication.
	// Possible values are considered with the following priority based on the address type:
	// - NodeExternalDNS
	// - NodeExternalIP
	// - NodeInternalIP
	if opts.AgentPlatform.APIServerAddress == "" {
		kubeClient := kubeclient.NewForConfigOrDie(util.GetConfigOrDie())
		nodes, err := kubeClient.CoreV1().Nodes().List(ctx, v1.ListOptions{Limit: 1})
		if err != nil {
			return fmt.Errorf("unable to fetch node objects: %w", err)
		}
		if len(nodes.Items) < 1 {
			return fmt.Errorf("no node objects found: %w", err)
		}
		addresses := map[corev1.NodeAddressType]string{}
		for _, address := range nodes.Items[0].Status.Addresses {
			addresses[address.Type] = address.Address
		}
		for _, addrType := range []corev1.NodeAddressType{corev1.NodeExternalDNS, corev1.NodeExternalIP, corev1.NodeInternalIP} {
			if address, exists := addresses[addrType]; exists {
				opts.AgentPlatform.APIServerAddress = address
				break
			}
		}
		if opts.AgentPlatform.APIServerAddress == "" {
			return fmt.Errorf("node %q does not expose any IP addresses, this should not be possible", nodes.Items[0].Name)
		}
		log.Info(fmt.Sprintf("detected %q from node %q as external-api-server-address", opts.AgentPlatform.APIServerAddress, nodes.Items[0].Name))
	}

	infraID := opts.InfraID
	if len(infraID) == 0 {
		infraID = fmt.Sprintf("%s-%s", opts.Name, utilrand.String(5))
	}
	exampleOptions.InfraID = infraID
	exampleOptions.BaseDomain = "example.com"

	exampleOptions.Agent = &apifixtures.ExampleAgentOptions{
		APIServerAddress: opts.AgentPlatform.APIServerAddress,
	}
	return nil
}
