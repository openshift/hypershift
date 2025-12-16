package omoperator

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	genericapiserver "k8s.io/apiserver/pkg/server"
	genericoptions "k8s.io/apiserver/pkg/server/options"
	"k8s.io/client-go/rest"
	utilversion "k8s.io/component-base/version"
)

type simpleHTTPProxy struct {
	server                  *genericapiserver.GenericAPIServer
	proxy                   *httputil.ReverseProxy
	namespace               string
	hostedControlPlaneName  string
	managementClusterConfig *rest.Config
	guestClusterConfig      *rest.Config
}

func newSimpleHTTPProxy(opts *httpProxy2Options) (*simpleHTTPProxy, error) {
	if opts.ManagementClusterConfig == nil {
		return nil, fmt.Errorf("management cluster config must be provided")
	}

	proxy := &simpleHTTPProxy{
		namespace:               opts.Namespace,
		hostedControlPlaneName:  opts.HostedControlPlaneName,
		managementClusterConfig: opts.ManagementClusterConfig,
		guestClusterConfig:      opts.GuestClusterConfig,
	}

	reverseProxy, err := newManagementClusterReverseProxy(opts.ManagementClusterConfig)
	if err != nil {
		return nil, err
	}
	proxy.proxy = reverseProxy

	scheme := runtime.NewScheme()
	codecs := serializer.NewCodecFactory(scheme)

	config := genericapiserver.NewConfig(codecs)
	config.PublicAddress = net.ParseIP("127.0.0.1")
	config.EnableIndex = false // disable default "/" handler so we can register our own
	config.BuildHandlerChainFunc = func(_ http.Handler, _ *genericapiserver.Config) http.Handler {
		return http.HandlerFunc(proxy.ServeHTTP)
	}

	secureServing := genericoptions.NewSecureServingOptions()
	secureServing.BindAddress = net.ParseIP("127.0.0.1")
	secureServing.BindPort = 9443
	secureServing.ServerCert.CertDirectory = ""

	if err := secureServing.MaybeDefaultWithSelfSignedCerts("localhost", nil, []net.IP{net.ParseIP("127.0.0.1")}); err != nil {
		return nil, err
	}
	if err := secureServing.WithLoopback().ApplyTo(&config.SecureServing, &config.LoopbackClientConfig); err != nil {
		return nil, err
	}
	config.EffectiveVersion = utilversion.DefaultKubeEffectiveVersion()

	completedConfig := config.Complete(nil)
	server, err := completedConfig.New("om-http-proxy2", genericapiserver.NewEmptyDelegate())
	if err != nil {
		return nil, err
	}

	proxy.server = server
	return proxy, nil
}

func (p *simpleHTTPProxy) Run(stopCh <-chan struct{}) error {
	return p.server.PrepareRun().Run(stopCh)
}

func newManagementClusterReverseProxy(cfg *rest.Config) (*httputil.ReverseProxy, error) {
	targetHost := cfg.Host
	if !strings.HasPrefix(targetHost, "http://") && !strings.HasPrefix(targetHost, "https://") {
		targetHost = "https://" + targetHost
	}
	targetURL, err := url.Parse(targetHost)
	if err != nil {
		return nil, fmt.Errorf("failed to parse management cluster host %q: %w", cfg.Host, err)
	}

	transportConfig := rest.CopyConfig(cfg)
	transport, err := rest.TransportFor(transportConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to build transport for management cluster: %w", err)
	}

	reverseProxy := httputil.NewSingleHostReverseProxy(targetURL)
	reverseProxy.Transport = transport
	return reverseProxy, nil
}

func (p *simpleHTTPProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if p.proxy == nil {
		http.Error(w, "proxy not initialized", http.StatusInternalServerError)
		return
	}
	p.proxy.ServeHTTP(w, r)
}
