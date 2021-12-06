package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"

	socks5 "github.com/armon/go-socks5"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/apiserver-network-proxy/pkg/util"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func main() {
	cmd := &cobra.Command{
		Use: "konnectivity-socks5-proxy",
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Help()
			os.Exit(1)
		},
	}
	cmd.AddCommand(NewStartCommand())

	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func NewStartCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Runs the konnectivity socks5 proxy server.",
		Long: ` Runs the konnectivity socks5 proxy server.
		This proxy accepts request and tunnels them through the designated Konnectivity Server.
		When resolving hostnames, the proxy will attempt to derive the Cluster IP Address from
		a Kubernetes Service using the provided KubeConfig. If the IP address
		cannot be resolved from a service, the system DNS is used to resolve hostnames.
		`,
	}

	var proxyHostname string
	var proxyPort int
	var servingPort int
	var caCertPath string
	var clientCertPath string
	var clientKeyPath string

	cmd.Flags().StringVar(&proxyHostname, "konnectivity-hostname", "konnectivity-server-local", "The hostname of the konnectivity service.")
	cmd.Flags().IntVar(&proxyPort, "konnectivity-port", 8090, "The konnectivity port that socks5 proxy should connect to.")
	cmd.Flags().IntVar(&servingPort, "serving-port", 8090, "The port that socks5 proxy should serve on.")

	cmd.Flags().StringVar(&caCertPath, "ca-cert-path", "/etc/konnectivity-proxy-tls/ca.crt", "The path to the konnectivity client's ca-cert.")
	cmd.Flags().StringVar(&clientCertPath, "tls-cert-path", "/etc/konnectivity-proxy-tls/tls.crt", "The path to the konnectivity client's tls certificate.")
	cmd.Flags().StringVar(&clientKeyPath, "tls-key-path", "/etc/konnectivity-proxy-tls/tls.key", "The path to the konnectivity client's private key.")

	cmd.Run = func(cmd *cobra.Command, args []string) {
		fmt.Println("Starting proxy...")
		client, err := client.New(ctrl.GetConfigOrDie(), client.Options{})
		if err != nil {
			panic(err)
		}

		conf := &socks5.Config{
			Dial: dialKonnectivityFunc(caCertPath, clientCertPath, clientKeyPath, proxyHostname, proxyPort),
			Resolver: K8sServiceResolver{
				client: client,
			},
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

func dialKonnectivityFunc(caCertPath string, clientCertPath string, clientKeyPath string, proxyHostname string, proxyPort int) func(ctx context.Context, network string, addr string) (net.Conn, error) {
	return func(ctx context.Context, network string, addr string) (net.Conn, error) {
		caCert := caCertPath
		tlsConfig, err := util.GetClientTLSConfig(caCert, clientCertPath, clientKeyPath, proxyHostname, nil)
		if err != nil {
			return nil, err
		}
		var proxyConn net.Conn

		proxyAddress := fmt.Sprintf("%s:%d", proxyHostname, proxyPort)
		requestAddress := addr

		proxyConn, err = tls.Dial("tcp", proxyAddress, tlsConfig)
		if err != nil {
			return nil, fmt.Errorf("dialing proxy %q failed: %v", proxyAddress, err)
		}
		fmt.Fprintf(proxyConn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", requestAddress, "127.0.0.1")
		br := bufio.NewReader(proxyConn)
		res, err := http.ReadResponse(br, nil)
		if err != nil {
			return nil, fmt.Errorf("reading HTTP response from CONNECT to %s via proxy %s failed: %v",
				requestAddress, proxyAddress, err)
		}
		if res.StatusCode != 200 {
			return nil, fmt.Errorf("proxy error from %s while dialing %s: %v", proxyAddress, requestAddress, res.Status)
		}

		// It's safe to discard the bufio.Reader here and return the
		// original TCP conn directly because we only use this for
		// TLS, and in TLS the client speaks first, so we know there's
		// no unbuffered data. But we can double-check.
		if br.Buffered() > 0 {
			return nil, fmt.Errorf("unexpected %d bytes of buffered data from CONNECT proxy %q",
				br.Buffered(), proxyAddress)
		}
		return proxyConn, nil
	}
}

// K8sServiceResolver attempts to resolve the hostname by matching it to a Kubernetes Service, but will fallback to the system DNS if an error is encountered.
type K8sServiceResolver struct {
	client client.Client
}

func (d K8sServiceResolver) Resolve(ctx context.Context, name string) (context.Context, net.IP, error) {
	_, ip, err := d.ResolveK8sService(ctx, name)
	if err != nil {
		fmt.Printf("Error resolving k8s service %v\n", err)
		return socks5.DNSResolver{}.Resolve(ctx, name)
	}

	return ctx, ip, nil
}

func (d K8sServiceResolver) ResolveK8sService(ctx context.Context, name string) (context.Context, net.IP, error) {
	namespaceNamedService := strings.Split(name, ".")
	if len(namespaceNamedService) < 2 {
		return nil, nil, fmt.Errorf("unable to derive namespacedName from %v", name)
	}
	namespacedName := types.NamespacedName{
		Namespace: namespaceNamedService[1],
		Name:      namespaceNamedService[0],
	}

	service := &corev1.Service{}
	err := d.client.Get(ctx, namespacedName, service)
	if err != nil {
		return nil, nil, err
	}

	// Convert service name to ip address...
	ip := net.ParseIP(service.Spec.ClusterIP)
	if ip == nil {
		return nil, nil, fmt.Errorf("unable to parse IP %v", ip)
	}

	fmt.Printf("%s resolved to %v\n", name, ip)

	return ctx, ip, nil
}
