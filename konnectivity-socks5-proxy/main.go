package konnectivitysocks5proxy

import (
	"fmt"
	"os"

	"github.com/openshift/hypershift/support/konnectivityproxy"
	"github.com/openshift/hypershift/support/supportedversion"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/armon/go-socks5"
	"github.com/spf13/cobra"
	"go.uber.org/zap/zapcore"
)

func NewStartCommand() *cobra.Command {
	l := log.Log.WithName("konnectivity-socks5-proxy")
	log.SetLogger(zap.New(zap.JSONEncoder(func(o *zapcore.EncoderConfig) {
		o.EncodeTime = zapcore.RFC3339TimeEncoder
	})))
	cmd := &cobra.Command{
		Use:   "konnectivity-socks5-proxy",
		Short: "Runs the konnectivity socks5 proxy server.",
		Long: ` Runs the konnectivity socks5 proxy server.
		This proxy accepts request and tunnels them through the designated Konnectivity Server.
		When resolving hostnames, the proxy will attempt to derive the Cluster IP Address from
		a Kubernetes Service using the provided KubeConfig. If the IP address
		cannot be resolved from a service, the system DNS is used to resolve hostnames.
		`,
	}

	opts := konnectivityproxy.Options{}

	var servingPort uint32

	cmd.Flags().StringVar(&opts.KonnectivityHost, "konnectivity-hostname", "konnectivity-server-local", "The hostname of the konnectivity service.")
	cmd.Flags().Uint32Var(&opts.KonnectivityPort, "konnectivity-port", 8090, "The konnectivity port that socks5 proxy should connect to.")
	cmd.Flags().Uint32Var(&servingPort, "serving-port", 8090, "The port that socks5 proxy should serve on.")
	cmd.Flags().BoolVar(&opts.ConnectDirectlyToCloudAPIs, "connect-directly-to-cloud-apis", false, "If true, traffic destined for AWS or Azure APIs should be sent there directly rather than going through konnectivity. If enabled, proxy env vars from the mgmt cluster must be propagated to this container")
	cmd.Flags().BoolVar(&opts.ResolveFromGuestClusterDNS, "resolve-from-guest-cluster-dns", false, "If DNS resolving should use the guest clusters cluster-dns")
	cmd.Flags().BoolVar(&opts.ResolveFromManagementClusterDNS, "resolve-from-management-cluster-dns", false, "If guest cluster's dns fails, fallback to the management cluster's dns")
	cmd.Flags().BoolVar(&opts.DisableResolver, "disable-resolver", false, "If true, DNS resolving is disabled. Takes precedence over resolve-from-guest-cluster-dns and resolve-from-management-cluster-dns")

	cmd.Flags().StringVar(&opts.CAFile, "ca-cert-path", "/etc/konnectivity/proxy-ca/ca.crt", "The path to the konnectivity client's ca-cert.")
	cmd.Flags().StringVar(&opts.ClientCertFile, "tls-cert-path", "/etc/konnectivity/proxy-client/tls.crt", "The path to the konnectivity client's tls certificate.")
	cmd.Flags().StringVar(&opts.ClientKeyFile, "tls-key-path", "/etc/konnectivity/proxy-client/tls.key", "The path to the konnectivity client's private key.")

	cmd.Run = func(cmd *cobra.Command, args []string) {
		l.Info("Starting proxy", "version", supportedversion.String())
		client, err := client.New(ctrl.GetConfigOrDie(), client.Options{})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: cannot get client: %v", err)
			os.Exit(1)
		}

		opts.Client = client
		opts.Log = l

		dialer, err := konnectivityproxy.NewKonnectivityDialer(opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: cannot initialize konnectivity dialer: %v", err)
			os.Exit(1)
		}

		conf := &socks5.Config{
			Dial:     dialer.DialContext,
			Resolver: dialer,
		}
		server, err := socks5.New(conf)
		if err != nil {
			panic(err)
		}

		if err := server.ListenAndServe("tcp", fmt.Sprintf(":%d", servingPort)); err != nil {
			panic(err)
		}
	}

	return cmd
}
