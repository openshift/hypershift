package konnectivityproxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/armon/go-socks5"
	"github.com/go-logr/logr"
	"golang.org/x/net/proxy"
)

// The ProxyDialer is the dialer used to connect via a Konnectivity proxy
// It implements the ContextDialer and Dialer interfaces as well as a
// the socks5.NameResolver interface to look up names through the konnectivity
// tunnel if necessary.
type ProxyDialer interface {
	proxy.ContextDialer
	proxy.Dialer
	socks5.NameResolver
	IsCloudAPI(string) bool
}

// Options specifies the inputs for creating a Konnectivity dialer.
type Options struct {
	// CAFile or CABytes specifies the CA bundle that should be used to verify
	// connections to the Konnectivity server. One or the other can be specified,
	// not both. REQUIRED.
	CAFile  string
	CABytes []byte

	// ClientCertFile or ClientCertBytes specifies the client certificate to be used
	// to authenticate to the Konnectivity server (via mTLS). One or the other can
	// be specified, not both. REQUIRED.
	ClientCertFile  string
	ClientCertBytes []byte

	// ClientKeyFile or ClientKeyBytes specifies the client key to be used to
	// authenticate to the Konnectivity server (via mTLS). One or the other can be
	// specified, not both. REQUIRED.
	ClientKeyFile  string
	ClientKeyBytes []byte

	// KonnectivityHost is the host name of the Konnectivity server proxy. REQUIRED.
	KonnectivityHost string

	// KonnectivityPort is the port of the Konnectivity server proxy. REQUIRED.
	KonnectivityPort uint32

	// ConnectDirectlyToCloudAPIs specifies whether cloud APIs should be bypassed
	// by the proxy. This is used by the ingress operator to be able to create DNS records
	// before worker nodes are present in the cluster.
	// See https://github.com/openshift/hypershift/pull/1601
	ConnectDirectlyToCloudAPIs bool

	// ExcludeCloudAPIHosts is a list of hostnames to exclude when determining if a particular
	// hostname is a CloudAPI hostname.
	// This is needed in the case when we use an internal proxy whose hostname ends in
	// one of the cloud API suffixes we check. We should not need to use the management cluster
	// proxy to get to the endpoint.
	ExcludeCloudAPIHosts []string

	// ResolveFromManagementClusterDNS tells the dialer to fallback to the management
	// cluster's DNS (and direct dialer) initially until the konnectivity tunnel is available.
	// Once the konnectivity tunnel is available, it no longer falls back on the management
	// cluster. This is used by the OAuth server to allow quicker initialization of identity
	// providers while worker nodes have not joined.
	// See https://github.com/openshift/hypershift/pull/2261
	ResolveFromManagementClusterDNS bool

	// ResolveFromGuestClusterDNS tells the dialer to resolve names using the guest
	// cluster's coreDNS service. Used by oauth and ingress operator.
	ResolveFromGuestClusterDNS bool

	// ResolveBeforeDial tells the dialer to resolve names before creating a TCP connection
	// through the Konnectivity server. This is needed by the HTTPS konnectivity proxy since the
	// hostname to be proxied needs to be resolved before being sent to the user's proxy.
	ResolveBeforeDial bool

	// DisableResolver disables any name resolution by the resolver. This is used by the CNO.
	// See https://github.com/openshift/hypershift/pull/3986
	DisableResolver bool

	// Client for the hosted cluster. This is used by the resolver to resolve names either via
	// service name or via coredns. REQUIRED (unless DisableResolver is specified)
	Client client.Client

	// Log is the logger to use for the dialer. No log output is generated if not specified.
	Log logr.Logger
}

func (o *Options) Validate() error {
	var errs []error
	if len(o.CAFile) > 0 && len(o.CABytes) > 0 {
		errs = append(errs, fmt.Errorf("cannot specify both CAFile and CABytes"))
	}
	if len(o.CAFile) == 0 && len(o.CABytes) == 0 {
		errs = append(errs, fmt.Errorf("CAFile or CABytes is required"))
	}
	if len(o.ClientCertFile) > 0 && len(o.ClientCertBytes) > 0 {
		errs = append(errs, fmt.Errorf("cannot specify both ClientCertFile and ClientCertBytes"))
	}
	if len(o.ClientCertFile) == 0 && len(o.ClientCertBytes) == 0 {
		errs = append(errs, fmt.Errorf("ClientCertFile or ClientCertBytes is required"))
	}
	if len(o.ClientKeyFile) > 0 && len(o.ClientKeyBytes) > 0 {
		errs = append(errs, fmt.Errorf("cannot specify both ClientKeyFile and ClientKeyBytes"))
	}
	if len(o.ClientKeyFile) == 0 && len(o.ClientKeyBytes) == 0 {
		errs = append(errs, fmt.Errorf("ClientKeyFile or ClientKeyBytes is required"))
	}

	if len(o.KonnectivityHost) == 0 {
		errs = append(errs, fmt.Errorf("KonnectivityHost is required"))
	}
	if o.KonnectivityPort == 0 {
		errs = append(errs, fmt.Errorf("KonnectivityPort is required"))
	}

	if !o.DisableResolver && o.Client == nil {
		errs = append(errs, fmt.Errorf("client is required when resolving names"))
	}

	return errors.NewAggregate(errs)
}

func readFileOrBytes(name string, b []byte) ([]byte, error) {
	if len(b) > 0 {
		return b, nil
	}
	result, err := os.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", name, err)
	}
	return result, nil
}

// NewKonnectivityDialer creates a dialer that uses a konnectivity server as a
// tunnel to obtain a TCP connection to the target address. The dialer also includes
// a resolver that optionally uses the same konnectivity server to resolve names
// via the CoreDNS service in a hosted cluster.
func NewKonnectivityDialer(opts Options) (ProxyDialer, error) {
	if err := opts.Validate(); err != nil {
		return nil, fmt.Errorf("failed validation: %w", err)
	}

	var caBytes, clientCertBytes, clientKeyBytes []byte
	var err error

	caBytes, err = readFileOrBytes(opts.CAFile, opts.CABytes)
	if err != nil {
		return nil, err
	}
	clientCertBytes, err = readFileOrBytes(opts.ClientCertFile, opts.ClientCertBytes)
	if err != nil {
		return nil, err
	}
	clientKeyBytes, err = readFileOrBytes(opts.ClientKeyFile, opts.ClientKeyBytes)
	if err != nil {
		return nil, err
	}

	proxy := &konnectivityProxy{
		ca:                              caBytes,
		clientCert:                      clientCertBytes,
		clientKey:                       clientKeyBytes,
		konnectivityHost:                opts.KonnectivityHost,
		konnectivityPort:                opts.KonnectivityPort,
		connectDirectlyToCloudAPIs:      opts.ConnectDirectlyToCloudAPIs,
		resolveFromManagementClusterDNS: opts.ResolveFromManagementClusterDNS,
		resolveBeforeDial:               opts.ResolveBeforeDial,
		excludeCloudHosts:               sets.New(opts.ExcludeCloudAPIHosts...),
	}
	proxy.proxyResolver = proxyResolver{
		client:                       opts.Client,
		disableResolver:              opts.DisableResolver,
		resolveFromGuestCluster:      opts.ResolveFromGuestClusterDNS,
		resolveFromManagementCluster: opts.ResolveFromManagementClusterDNS,
		mustResolve:                  opts.ResolveBeforeDial,
		dnsFallback:                  &proxy.fallbackToMCDNS,
		log:                          opts.Log,
		isCloudAPI:                   proxy.IsCloudAPI,
	}
	proxy.guestClusterResolver = &guestClusterResolver{
		client:               opts.Client,
		konnectivityDialFunc: proxy.DialContext,
		log:                  opts.Log,
	}
	return proxy, nil
}

// konnectivityProxy is the implementation of the ProxyDialer interface
type konnectivityProxy struct {
	ca                              []byte
	clientCert                      []byte
	clientKey                       []byte
	konnectivityHost                string
	konnectivityPort                uint32
	connectDirectlyToCloudAPIs      bool
	resolveFromManagementClusterDNS bool
	resolveBeforeDial               bool

	proxyResolver

	// fallbackToMCDNS is a synced boolean that keeps track
	// of whether to fallback to the management cluster's DNS
	// (and dial directly).
	// It is initially false, but if lookup through the guest
	// fails, then it's set to true.
	fallbackToMCDNS syncBool

	tlsConfigOnce sync.Once
	tlsConfig     *tls.Config

	httpDialerOnce sync.Once
	httpDialer     proxy.Dialer

	excludeCloudHosts sets.Set[string]
}

func (p *konnectivityProxy) Dial(network, address string) (net.Conn, error) {
	return p.DialContext(context.Background(), network, address)
}

func (p *konnectivityProxy) getTLSConfig() *tls.Config {
	p.tlsConfigOnce.Do(func() {
		certPool := x509.NewCertPool()
		if !certPool.AppendCertsFromPEM(p.ca) {
			panic("cannot load client CA")
		}
		p.tlsConfig = &tls.Config{
			RootCAs:    certPool,
			MinVersion: tls.VersionTLS12,
		}
		cert, err := tls.X509KeyPair(p.clientCert, p.clientKey)
		if err != nil {
			panic(fmt.Sprintf("cannot load client certs: %v", err))
		}
		p.tlsConfig.ServerName = p.konnectivityHost
		p.tlsConfig.Certificates = []tls.Certificate{cert}
	})
	return p.tlsConfig
}

// DialContext dials the specified address using the specified context. It implements the upstream
// proxy.Dialer interface.
func (p *konnectivityProxy) DialContext(ctx context.Context, network string, requestAddress string) (net.Conn, error) {
	log := p.log.WithName("konnectivityProxy.DialContext")
	log.V(4).Info("Dial called", "network", network, "requestAddress", requestAddress)
	requestHost, requestPort, err := net.SplitHostPort(requestAddress)
	if err != nil {
		return nil, fmt.Errorf("invalid address (%s): %w", requestAddress, err)
	}
	log.V(4).Info("Host and port determined", "requestHost", requestHost, "requestPort", requestPort)
	// return a dial direct function which respects any proxy environment settings
	if p.IsCloudAPI(requestHost) {
		p.log.V(4).Info("Host name is cloud API, dialing through mgmt cluster proxy if present")
		return p.dialDirectWithProxy(network, requestAddress)
	}

	// return a dial direct function ignoring any proxy environment settings
	shouldDNSFallback := p.fallbackToMCDNS.get()
	if shouldDNSFallback && p.resolveFromManagementClusterDNS {
		log.V(4).Info("Should DNS fallback is set to true and resolve from management cluster DNS is true, dialing direct")
		return p.dialDirectWithoutProxy(ctx, network, requestAddress)
	}

	// get a TLS config based on x509 certs
	tlsConfig := p.getTLSConfig()

	// connect to the konnectivity server address and get a TLS connection
	konnectivityServerAddress := net.JoinHostPort(p.konnectivityHost, fmt.Sprintf("%d", p.konnectivityPort))
	log.V(4).Info("Dialing konnectivity server", "address", konnectivityServerAddress)
	konnectivityConnection, err := tls.Dial("tcp", konnectivityServerAddress, tlsConfig)
	if err != nil {
		return nil, fmt.Errorf("dialing proxy %q failed: %v", konnectivityServerAddress, err)
	}

	if p.resolveBeforeDial && !p.disableResolver && !isIP(requestHost) {
		log.V(4).Info("Host name must be resolved before dialing", "host", requestHost)
		_, ip, err := p.Resolve(ctx, requestHost)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve name %s: %w", requestHost, err)
		}
		p.log.V(4).Info("Host name resolved", "ip", ip.String())
		requestAddress = net.JoinHostPort(ip.String(), requestPort)
	}

	// The CONNECT command sent to the Konnectivity server opens a TCP connection
	// to the request host via the konnectivity tunnel.
	connectString := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", requestAddress, requestHost)
	log.V(4).Info("Sending connect string to konnectivity server", "connectString", connectString)
	_, err = fmt.Fprintf(konnectivityConnection, "%s", connectString)
	if err != nil {
		log.V(4).Error(err, "Failed to write string to konnectivity server connection")
		return nil, err
	}

	// read HTTP response and return the connection
	br := bufio.NewReader(konnectivityConnection)
	p.log.V(4).Info("Reading response from konnectivity server")
	res, err := http.ReadResponse(br, nil)
	if err != nil {
		return nil, fmt.Errorf("reading HTTP response from CONNECT to %s via proxy %s failed: %v",
			requestAddress, konnectivityServerAddress, err)
	}
	if res.StatusCode != 200 {
		log.V(4).Info("Status code was not 200", "statusCode", res.StatusCode)
		return nil, fmt.Errorf("proxy error from %s while dialing %s: %v", konnectivityServerAddress, requestAddress, res.Status)
	}
	// It's safe to discard the bufio.Reader here and return the original TCP conn directly because we only use this
	// for TLS. In TLS, the client speaks first, so we know there's no unbuffered data, but we can double-check.
	if br.Buffered() > 0 {
		log.V(4).Info("The response contained buffered data, none expected")
		return nil, fmt.Errorf("unexpected %d bytes of buffered data from CONNECT proxy %q",
			br.Buffered(), konnectivityServerAddress)
	}
	log.V(4).Info("Successfully created connection through konnectivity")
	return konnectivityConnection, nil
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
func (p *konnectivityProxy) dialDirectWithProxy(network, addr string) (net.Conn, error) {
	p.httpDialerOnce.Do(func() {
		if proxyURLStr := os.Getenv("HTTPS_PROXY"); proxyURLStr != "" {
			proxyURL, err := url.Parse(proxyURLStr)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to parse HTTPS_PROXY(%s): %v", proxyURLStr, err)
			} else {
				p.httpDialer = newHTTPDialer(proxyURL)
			}
		}
		if p.httpDialer == nil {
			p.httpDialer = proxy.Direct
		}
	})
	return p.httpDialer.Dial(network, addr)
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

// IsCloudAPI is a hardcoded list of domains that should not be routed through Konnectivity but be reached
// through the management cluster. This is needed to support management clusters with a proxy configuration,
// as the components themselves already have proxy env vars pointing to the socks proxy (this binary). If we then
// actually end up proxying or not depends on the env for this binary.
// DNS domains. The API list can be found below:
// AWS: https://docs.aws.amazon.com/general/latest/gr/rande.html#regional-endpoints
// AZURE: https://docs.microsoft.com/en-us/rest/api/azure/#how-to-call-azure-rest-apis-with-curl
// IBMCLOUD: https://cloud.ibm.com/apidocs/iam-identity-token-api#endpoints
func (p *konnectivityProxy) IsCloudAPI(host string) bool {
	log := p.log.WithName("konnectivityProxy.IsCloudAPI")
	if !p.connectDirectlyToCloudAPIs {
		// If not connecting directly to cloud APIs, we should not
		// make any distinction between regular hostnames and cloud hostnames.
		// This is used by both the dialer and the resolver in determining how
		// to access cloud api hostnames when connectDirectlyToCloudAPIs is set
		// to true.
		return false
	}
	log.V(4).Info("Determining whether host is cloud API", "host", host)
	if p.excludeCloudHosts.Has(host) {
		log.V(4).Info("Host is in the list of exclude hosts, returning false")
		return false
	}
	if strings.HasSuffix(host, ".amazonaws.com") ||
		strings.HasSuffix(host, ".microsoftonline.com") ||
		strings.HasSuffix(host, "azure.com") ||
		strings.HasSuffix(host, "cloud.ibm.com") {
		log.V(4).Info("Host has one of the cloud API suffixes, returning true")
		return true
	}
	return false
}

func isIP(address string) bool {
	return net.ParseIP(address) != nil
}
