package omoperator

import (
	"fmt"

	"github.com/spf13/cobra"

	genericapiserver "k8s.io/apiserver/pkg/server"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/component-base/logs"
	logsapi "k8s.io/component-base/logs/api/v1"
)

func NewHTTPProxy2Command() *cobra.Command {
	proxyFlags := &httpProxy2Flags{
		Logs: logs.NewOptions(),
	}
	cmd := &cobra.Command{
		Use:   "proxy2",
		Short: "Runs a minimal HTTP proxy backed by the generic API server library",
		RunE: func(cmd *cobra.Command, args []string) error {
			defer logs.FlushLogs()

			if err := proxyFlags.Validate(); err != nil {
				return err
			}

			options, err := proxyFlags.ToOptions()
			if err != nil {
				return err
			}
			proxy, err := newSimpleHTTPProxy(options)
			if err != nil {
				return err
			}
			stopCh := genericapiserver.SetupSignalHandler()
			return proxy.Run(stopCh)
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

type httpProxy2Flags struct {
	Logs *logs.Options

	ManagementClusterKubeconfigPath string
	GuestClusterKubeconfigPath      string
	HostedControlPlaneName          string
	Namespace                       string
}

func (f *httpProxy2Flags) Validate() error {
	if len(f.ManagementClusterKubeconfigPath) == 0 {
		return fmt.Errorf("--management-cluster-kubeconfig is required")
	}
	return logsapi.ValidateAndApply(f.Logs, nil)
}

func (f *httpProxy2Flags) ToOptions() (*httpProxy2Options, error) {
	managementClusterConfig, err := clientcmd.BuildConfigFromFlags("", f.ManagementClusterKubeconfigPath)
	if err != nil {
		return nil, err
	}

	var guestClusterConfig *rest.Config
	if len(f.GuestClusterKubeconfigPath) > 0 {
		guestClusterConfig, err = clientcmd.BuildConfigFromFlags("", f.GuestClusterKubeconfigPath)
		if err != nil {
			return nil, err
		}
	}

	return &httpProxy2Options{
		ManagementClusterConfig: managementClusterConfig,
		GuestClusterConfig:      guestClusterConfig,
		HostedControlPlaneName:  f.HostedControlPlaneName,
		Namespace:               f.Namespace,
	}, nil
}

type httpProxy2Options struct {
	ManagementClusterConfig *rest.Config
	GuestClusterConfig      *rest.Config
	HostedControlPlaneName  string
	Namespace               string
}
