package omoperator

import (
	"net"
	"net/http"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	genericapiserver "k8s.io/apiserver/pkg/server"
	genericoptions "k8s.io/apiserver/pkg/server/options"
	"k8s.io/client-go/rest"
	utilversion "k8s.io/component-base/version"
)

type simpleHTTPProxy struct {
	server                  *genericapiserver.GenericAPIServer
	namespace               string
	hostedControlPlaneName  string
	managementClusterConfig *rest.Config
	guestClusterConfig      *rest.Config
}

func newSimpleHTTPProxy(opts *httpProxy2Options) (*simpleHTTPProxy, error) {
	scheme := runtime.NewScheme()
	codecs := serializer.NewCodecFactory(scheme)

	config := genericapiserver.NewConfig(codecs)
	config.PublicAddress = net.ParseIP("127.0.0.1")
	config.EnableIndex = false // disable default "/" handler so we can register our own
	config.BuildHandlerChainFunc = func(_ http.Handler, _ *genericapiserver.Config) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("hello from generic apiserver\n"))
		})
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

	return &simpleHTTPProxy{
		server:                  server,
		namespace:               opts.Namespace,
		hostedControlPlaneName:  opts.HostedControlPlaneName,
		managementClusterConfig: opts.ManagementClusterConfig,
		guestClusterConfig:      opts.GuestClusterConfig,
	}, nil
}

func (p *simpleHTTPProxy) Run(stopCh <-chan struct{}) error {
	return p.server.PrepareRun().Run(stopCh)
}
