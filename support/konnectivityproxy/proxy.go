package konnectivityproxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"golang.org/x/net/proxy"

	"sigs.k8s.io/apiserver-network-proxy/pkg/util"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Proxy interface {
	Dialer
	Resolver
}

type Dialer interface {
	Dial(ctx context.Context, network string, requestAddress string) (net.Conn, error)
}

type Resolver interface {
	Resolve(ctx context.Context, name string) (context.Context, net.IP, error)
}

type KonnectivityProxyOpts struct {
	CAFile                          string
	ClientCertFile                  string
	ClientKeyFile                   string
	KonnectivityHost                string
	KonnectivityPort                uint32
	ConnectDirectlyToCloudAPIs      bool
	ResolveFromManagementClusterDNS bool
	ResolveFromGuestClusterDNS      bool
	ResolveBeforeDial               bool
	DisableResolver                 bool
	Client                          client.Client
	Log                             logr.Logger
}

func NewKonnectivityProxy(opts KonnectivityProxyOpts) Proxy {
	proxy := konnectivityProxy{
		caFile:                          opts.CAFile,
		clientCertFile:                  opts.ClientCertFile,
		clientKeyFile:                   opts.ClientKeyFile,
		konnectivityHost:                opts.KonnectivityHost,
		konnectivityPort:                opts.KonnectivityPort,
		connectDirectlyToCloudAPIs:      opts.ConnectDirectlyToCloudAPIs,
		resolveFromManagementClusterDNS: opts.ResolveFromManagementClusterDNS,
		resolveBeforeDial:               opts.ResolveBeforeDial,
	}
	proxy.proxyResolver = proxyResolver{
		client:                       opts.Client,
		disableResolver:              opts.DisableResolver,
		resolveFromGuestCluster:      opts.ResolveFromGuestClusterDNS,
		resolveFromManagementCluster: opts.ResolveFromManagementClusterDNS,
		dnsFallback:                  &proxy.fallbackToMCDNS,
		log:                          opts.Log,
	}
	proxy.proxyResolver.guestClusterResolver = &guestClusterResolver{
		client:               opts.Client,
		konnectivityDialFunc: proxy.Dial,
		log:                  opts.Log,
	}
	return &proxy
}

type konnectivityProxy struct {
	caFile                          string
	clientCertFile                  string
	clientKeyFile                   string
	konnectivityHost                string
	konnectivityPort                uint32
	connectDirectlyToCloudAPIs      bool
	resolveFromManagementClusterDNS bool
	resolveBeforeDial               bool

	proxyResolver

	fallbackToMCDNS syncBool
}

// dialFunc returns the appropriate dial function based on user and proxy setting configurations
func (p *konnectivityProxy) Dial(ctx context.Context, network string, requestAddress string) (net.Conn, error) {
	// return a dial direct function which respects any proxy environment settings
	if p.connectDirectlyToCloudAPIs && isCloudAPI(strings.Split(requestAddress, ":")[0]) {
		return p.dialDirectWithProxy(ctx, network, requestAddress)
	}

	// return a dial direct function ignoring any proxy environment settings
	shouldDNSFallback := p.fallbackToMCDNS.get()
	if shouldDNSFallback && p.resolveFromManagementClusterDNS {
		return p.dialDirectWithoutProxy(ctx, network, requestAddress)
	}

	// get a TLS config based on x509 certs
	tlsConfig, err := util.GetClientTLSConfig(p.caFile, p.clientCertFile, p.clientKeyFile, p.konnectivityHost, nil)
	if err != nil {
		return nil, err
	}

	// connect to the proxy address and get a TLS connection
	proxyAddress := fmt.Sprintf("%s:%d", p.konnectivityHost, p.konnectivityPort)
	proxyConn, err := tls.Dial("tcp", proxyAddress, tlsConfig)
	if err != nil {
		return nil, fmt.Errorf("dialing proxy %q failed: %v", proxyAddress, err)
	}

	requestHost, requestPort, err := net.SplitHostPort(requestAddress)
	if err != nil {
		return nil, fmt.Errorf("cannot parse request address %s: %w", requestAddress, err)
	}
	if p.resolveBeforeDial && !p.disableResolver && !isIP(requestHost) {
		_, ip, err := p.Resolve(ctx, requestHost)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve name %s: %w", requestHost, err)
		}
		requestAddress = net.JoinHostPort(ip.String(), requestPort)
	}

	connectString := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", requestAddress, requestHost)
	_, err = fmt.Fprintf(proxyConn, "%s", connectString)
	if err != nil {
		return nil, err
	}

	// read HTTP response and return the connection
	br := bufio.NewReader(proxyConn)
	res, err := http.ReadResponse(br, nil)
	if err != nil {
		return nil, fmt.Errorf("reading HTTP response from CONNECT to %s via proxy %s failed: %v",
			requestAddress, proxyAddress, err)
	}
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("proxy error from %s while dialing %s: %v", proxyAddress, requestAddress, res.Status)
	}
	// It's safe to discard the bufio.Reader here and return the original TCP conn directly because we only use this
	// for TLS. In TLS, the client speaks first, so we know there's no unbuffered data, but we can double-check.
	if br.Buffered() > 0 {
		return nil, fmt.Errorf("unexpected %d bytes of buffered data from CONNECT proxy %q",
			br.Buffered(), proxyAddress)
	}
	return proxyConn, nil
}

// dialDirectWithoutProxy directly connect to the target, ignoring any local proxy settings from the environment
func (p *konnectivityProxy) dialDirectWithoutProxy(ctx context.Context, network, addr string) (net.Conn, error) {
	var d = net.Dialer{
		Timeout: 2 * time.Minute,
	}
	connection, err := d.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	p.fallbackToMCDNS.set(false)
	return connection, nil
}

// dialDirectWithProxy directly connect to the target, respecting any local proxy settings from the environment
func (p *konnectivityProxy) dialDirectWithProxy(ctx context.Context, network, addr string) (net.Conn, error) {
	return proxy.Dial(ctx, network, addr)
}

type syncBool struct {
	value bool
	mutex sync.RWMutex
}

func (b *syncBool) get() bool {
	b.mutex.RLock()
	defer b.mutex.RUnlock()
	return b.value
}

func (f *syncBool) set(valueToSet bool) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.value = valueToSet
}

// isCloudAPI is a hardcoded list of domains that should not be routed through Konnectivity but be reached
// through the management cluster. This is needed to support management clusters with a proxy configuration,
// as the components themselves already have proxy env vars pointing to the socks proxy (this binary). If we then
// actually end up proxying or not depends on the env for this binary.
// DNS domains. The API list can be found below:
// AWS: https://docs.aws.amazon.com/general/latest/gr/rande.html#regional-endpoints
// AZURE: https://docs.microsoft.com/en-us/rest/api/azure/#how-to-call-azure-rest-apis-with-curl
// IBMCLOUD: https://cloud.ibm.com/apidocs/iam-identity-token-api#endpoints
func isCloudAPI(host string) bool {
	return strings.HasSuffix(host, ".amazonaws.com") ||
		strings.HasSuffix(host, ".microsoftonline.com") ||
		strings.HasSuffix(host, "azure.com") ||
		strings.HasSuffix(host, "cloud.ibm.com")
}

func isIP(address string) bool {
	return net.ParseIP(address) != nil
}
