package powervs

import (
	"context"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/cmd/nodepool/core"
	"github.com/openshift/hypershift/cmd/util"

	"k8s.io/apimachinery/pkg/util/intstr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/spf13/cobra"
)

type PowerVSPlatformCreateOptions struct {
	SysType    string
	ProcType   hyperv1.PowerVSNodePoolProcType
	Processors string
	Memory     int32
}

func NewCreateCommand(coreOpts *core.CreateNodePoolOptions, clientProviders ...*util.ClientProvider) *cobra.Command {
	clientProvider := util.ResolveClientProvider(clientProviders...)
	cmd := &cobra.Command{
		Use:          "powervs",
		Short:        "Creates an PowerVS nodepool",
		SilenceUsage: true,
	}
	opts := &PowerVSPlatformCreateOptions{
		SysType:    "s922",
		ProcType:   "shared",
		Processors: "0.5",
		Memory:     32,
	}

	cmd.Flags().StringVar(&opts.SysType, "sys-type", opts.SysType, "System type used to host the instance(e.g: s922, e980, e880). Default is s922")
	cmd.Flags().Var(&opts.ProcType, "proc-type", "Processor type (dedicated, shared, capped). Default is shared")
	cmd.Flags().StringVar(&opts.Processors, "processors", opts.Processors, "Number of processors allocated. Default is 0.5")
	cmd.Flags().Int32Var(&opts.Memory, "memory", opts.Memory, "Amount of memory allocated (in GB). Default is 32")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		client, err := clientProvider.ControllerRuntimeClientFor("")
		if err != nil {
			return err
		}
		if err := coreOpts.CreateNodePool(cmd.Context(), opts, client); err != nil {
			log.Log.Error(err, "Failed to create nodepool")
			return err
		}
		return nil
	}

	return cmd
}

func (o *PowerVSPlatformCreateOptions) UpdateNodePool(ctx context.Context, nodePool *hyperv1.NodePool, hcluster *hyperv1.HostedCluster, client crclient.Client) error {
	nodePool.Spec.Platform.Type = hyperv1.PowerVSPlatform
	nodePool.Spec.Platform.PowerVS = &hyperv1.PowerVSNodePoolPlatform{
		SystemType:    o.SysType,
		Processors:    intstr.FromString(o.Processors),
		ProcessorType: o.ProcType,
		MemoryGiB:     o.Memory,
	}
	return nil
}

func (o PowerVSPlatformCreateOptions) Type() hyperv1.PlatformType {
	return hyperv1.PowerVSPlatform
}
