package konnectivitysocks5proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"

	socks5 "github.com/armon/go-socks5"
	"github.com/openshift/hypershift/pkg/version"
	"github.com/spf13/cobra"
	"go.uber.org/zap/zapcore"
	"golang.org/x/net/proxy"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/apiserver-network-proxy/pkg/util"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func NewStartCommand() *cobra.Command {
	l := log.Log.WithName("konnectivity-socks5-proxy")
	log.SetLogger(zap.New(zap.UseDevMode(true), zap.JSONEncoder(func(o *zapcore.EncoderConfig) {
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

	var proxyHostname string
	var proxyPort int
	var servingPort int
	var caCertPath string
	var clientCertPath string
	var clientKeyPath string
	var connectDirectlyToCloudAPIs bool
	var resolveFromGuestClusterDNS bool

	cmd.Flags().StringVar(&proxyHostname, "konnectivity-hostname", "konnectivity-server-local", "The hostname of the konnectivity service.")
	cmd.Flags().IntVar(&proxyPort, "konnectivity-port", 8090, "The konnectivity port that socks5 proxy should connect to.")
	cmd.Flags().IntVar(&servingPort, "serving-port", 8090, "The port that socks5 proxy should serve on.")
	cmd.Flags().BoolVar(&connectDirectlyToCloudAPIs, "connect-directly-to-cloud-apis", false, "If true, traffic destined for AWS or Azure APIs should be sent there directly rather than going through konnectivity. If enabled, proxy env vars from the mgmt cluster must be propagated to this container")
	cmd.Flags().BoolVar(&resolveFromGuestClusterDNS, "resolve-from-guest-cluster-dns", false, "If DNS resolving should use the guest clusters cluster-dns")

	cmd.Flags().StringVar(&caCertPath, "ca-cert-path", "/etc/konnectivity-proxy-tls/ca.crt", "The path to the konnectivity client's ca-cert.")
	cmd.Flags().StringVar(&clientCertPath, "tls-cert-path", "/etc/konnectivity-proxy-tls/tls.crt", "The path to the konnectivity client's tls certificate.")
	cmd.Flags().StringVar(&clientKeyPath, "tls-key-path", "/etc/konnectivity-proxy-tls/tls.key", "The path to the konnectivity client's private key.")

	cmd.Run = func(cmd *cobra.Command, args []string) {
		l.Info("Starting proxy", "version", version.String())
		client, err := client.New(ctrl.GetConfigOrDie(), client.Options{})
		if err != nil {
			panic(err)
		}

		dialFunc := dialFunc(caCertPath, clientCertPath, clientKeyPath, proxyHostname, proxyPort, connectDirectlyToCloudAPIs)
		conf := &socks5.Config{
			Dial: dialFunc,
			Resolver: proxyResolver{
				client:                  client,
				resolveFromGuestCluster: resolveFromGuestClusterDNS,
				guestClusterResolver: &guestClusterResolver{
					log:                  l,
					client:               client,
					konnektivityDialFunc: dialFunc,
				},
				log: l,
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

func dialFunc(caCertPath string, clientCertPath string, clientKeyPath string, proxyHostname string, proxyPort int, connectDirectlyToCloudApis bool) func(ctx context.Context, network string, addr string) (net.Conn, error) {
	return func(ctx context.Context, network string, addr string) (net.Conn, error) {
		if connectDirectlyToCloudApis && isCloudAPI(strings.Split(addr, ":")[0]) {
			return dialDirect(ctx, network, addr)
		}
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

// dialDirect directly connect directly to the target, respecting any local proxy settings from the environment
func dialDirect(ctx context.Context, network, addr string) (net.Conn, error) {
	return proxy.Dial(ctx, network, addr)
}

type guestClusterResolver struct {
	log                  logr.Logger
	client               client.Client
	konnektivityDialFunc func(ctx context.Context, network string, addr string) (net.Conn, error)
	resolver             *net.Resolver
	resolverLock         sync.Mutex
}

func (gr *guestClusterResolver) getResolver(ctx context.Context) (*net.Resolver, error) {
	gr.resolverLock.Lock()
	defer gr.resolverLock.Unlock()
	if gr.resolver != nil {
		return gr.resolver, nil
	}
	dnsService := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: "openshift-dns", Name: "dns-default"}}
	if err := gr.client.Get(ctx, client.ObjectKeyFromObject(dnsService), dnsService); err != nil {
		return nil, fmt.Errorf("failed to get dns service from guest cluster: %w", err)
	}
	clusterDNSAddress := dnsService.Spec.ClusterIP + ":53"
	gr.resolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			return gr.konnektivityDialFunc(ctx, "tcp", clusterDNSAddress)
		},
	}

	return gr.resolver, nil
}

func (gr *guestClusterResolver) resolve(ctx context.Context, name string) (net.IP, error) {
	resolver, err := gr.getResolver(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get resolver: %w", err)

	}
	addresses, err := resolver.LookupHost(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve %q: %w", name, err)
	}
	if len(addresses) == 0 {
		return nil, errors.New("no addresses found")
	}
	address := net.ParseIP(addresses[0])
	if address == nil {
		return nil, fmt.Errorf("failed to parse address %q as IP", addresses[0])
	}
	return address, nil
}

// proxyResolver tries to resolve addresses using the following steps in order:
// 1. Not at all for cloud provider apis, as we do not want to tunnel them through Konnektivity
// 2. If the address is a valid Kubernetes service and that service exists in the guest cluster, it's clusterIP is returned
// 2. If --resolve-from-guest-cluster-dns is set, it uses the guest clusters dns. If that fails, an error is returned
// 4. Lastly, golangs default resolver is used
type proxyResolver struct {
	client                  client.Client
	resolveFromGuestCluster bool
	guestClusterResolver    *guestClusterResolver
	log                     logr.Logger
}

func (d proxyResolver) Resolve(ctx context.Context, name string) (context.Context, net.IP, error) {
	// Preserve the host so we can recognize it
	if isCloudAPI(name) {
		return ctx, nil, nil
	}
	l := d.log.WithValues("name", name)
	_, ip, err := d.ResolveK8sService(ctx, l, name)
	if err != nil {

		l.Info("failed to resolve address from Kubernetes service", "err", err.Error())
		if !d.resolveFromGuestCluster {
			return socks5.DNSResolver{}.Resolve(ctx, name)
		}

		l.Info("looking up address from guest cluster cluster-dns")
		address, err := d.guestClusterResolver.resolve(ctx, name)
		if err != nil {
			l.Error(err, "failed to look up address from guest cluster")
			return ctx, nil, fmt.Errorf("failed to look up name %s from guest cluster cluster-dns: %w", name, err)
		}
		l.WithValues("address", address.String()).Info("Successfully looked up address from guest cluster")
		return ctx, address, nil

	}

	return ctx, ip, nil
}

func (d proxyResolver) ResolveK8sService(ctx context.Context, l logr.Logger, name string) (context.Context, net.IP, error) {
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

	l.Info("resolved address from Kubernetes service", "ip", ip.String())

	return ctx, ip, nil
}

// isCloudAPI is a hardcoded list of domains that should not be routed through konnektivity but be reached
// through the management cluster. This is needed to support management clusters with a proxy configuration,
// as the components themselves already have proxy env vars pointing to the socks proxy (this binary). If we then
// actually end up proxying or not depends on the env for this binary.
// DNS domains. The API list can be found below:
// AWS: https://docs.aws.amazon.com/general/latest/gr/rande.html#regional-endpoints
// AZURE: https://docs.microsoft.com/en-us/rest/api/azure/#how-to-call-azure-rest-apis-with-curl
func isCloudAPI(host string) bool {
	return strings.HasSuffix(host, ".amazonaws.com") || strings.HasSuffix(host, ".microsoftonline.com") || strings.HasSuffix(host, "azure.com")
}
