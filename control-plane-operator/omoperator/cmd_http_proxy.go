package omoperator

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"k8s.io/apimachinery/pkg/util/sets"
	k8sserver "k8s.io/apiserver/pkg/server"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/component-base/logs"
	logsapi "k8s.io/component-base/logs/api/v1"
	"k8s.io/klog/v2"
)

func NewHTTPProxyCommand() *cobra.Command {
	proxyFlags := &httpProxyFlags{
		Logs: logs.NewOptions(),
	}
	cmd := &cobra.Command{
		Use:   "proxy",
		Short: "",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			defer logs.FlushLogs()

			if err := proxyFlags.Validate(); err != nil {
				return err
			}

			proxyOptions, err := proxyFlags.ToOptions()
			if err != nil {
				return err
			}
			if err := proxyOptions.Run(ctx); err != nil {
				klog.Error(err, "Error running Openshift Manager HTTP Proxy")
				os.Exit(1)
			}
			return nil
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&proxyFlags.Namespace, "namespace", proxyFlags.Namespace, "the namespace for control plane components on management cluster.")
	flags.StringVar(&proxyFlags.HostedControlPlaneName, "hosted-control-plane", proxyFlags.HostedControlPlaneName, "Name of the hosted control plane that owns this operator.")
	flags.StringVar(&proxyFlags.ManagementClusterKubeconfigPath, "management-cluster-kubeconfig", proxyFlags.ManagementClusterKubeconfigPath, "path to kubeconfig file for the management cluster.")
	flags.StringVar(&proxyFlags.GuestClusterKubeconfigPath, "guest-cluster-kubeconfig", proxyFlags.GuestClusterKubeconfigPath, "path to kubeconfig file for the guest cluster.")
	logsapi.AddFlags(proxyFlags.Logs, flags)

	return cmd
}

type httpProxyOptions struct {
	s *server
}

func (o *httpProxyOptions) Run(ctx context.Context) error {
	return o.s.Start(ctx)
}

type httpProxyFlags struct {
	Logs *logs.Options

	ManagementClusterKubeconfigPath string
	GuestClusterKubeconfigPath      string
	HostedControlPlaneName          string
	Namespace                       string
}

func (f *httpProxyFlags) Validate() error {
	if len(f.ManagementClusterKubeconfigPath) == 0 {
		return fmt.Errorf("--management-cluster-kubeconfig is required")
	}
	return logsapi.ValidateAndApply(f.Logs, nil)
}

func (f *httpProxyFlags) ToOptions() (*httpProxyOptions, error) {
	managementClusterConfig, err := clientcmd.BuildConfigFromFlags("", f.ManagementClusterKubeconfigPath)
	if err != nil {
		return nil, err
	}

	requestResolver := k8sserver.NewRequestInfoResolver(&k8sserver.Config{
		LegacyAPIGroupPrefixes: sets.NewString(k8sserver.DefaultLegacyAPIPrefix),
	})
	s, err := newServer(requestResolver, managementClusterConfig, f.Namespace)
	if err != nil {
		return nil, err
	}
	return &httpProxyOptions{
		s: s,
	}, nil
}
