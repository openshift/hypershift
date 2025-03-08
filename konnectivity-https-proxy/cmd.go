package konnectivityhttpsproxy

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"

	"github.com/openshift/hypershift/pkg/version"
	"github.com/openshift/hypershift/support/konnectivityproxy"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/elazarl/goproxy"
	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	"go.uber.org/zap/zapcore"
	"golang.org/x/net/http/httpproxy"
)

func NewStartCommand() *cobra.Command {
	zLogger := zap.New(
		zap.JSONEncoder(func(o *zapcore.EncoderConfig) {
			o.EncodeTime = zapcore.RFC3339TimeEncoder
		}),
	)
	log.SetLogger(zLogger)
	l := log.Log.WithName("konnectivity-https-proxy")
	cmd := &cobra.Command{
		Use:   "konnectivity-https-proxy",
		Short: "Runs the konnectivity https proxy server.",
		Long: ` Runs the konnectivity https proxy server.
		This proxy accepts request and tunnels them through the designated Konnectivity Server.`,
	}

	opts := konnectivityproxy.Options{
		ResolveBeforeDial:          true,
		ResolveFromGuestClusterDNS: true,
	}

	var servingPort uint32
	var httpProxyURL string
	var httpsProxyURL string
	var noProxy string

	cmd.Flags().StringVar(&opts.KonnectivityHost, "konnectivity-hostname", "konnectivity-server-local", "The hostname of the konnectivity service.")
	cmd.Flags().Uint32Var(&opts.KonnectivityPort, "konnectivity-port", 8090, "The konnectivity port that https proxy should connect to.")
	cmd.Flags().Uint32Var(&servingPort, "serving-port", 8090, "The port that https proxy should serve on.")

	cmd.Flags().StringVar(&opts.CAFile, "ca-cert-path", "/etc/konnectivity/proxy-ca/ca.crt", "The path to the konnectivity client's ca-cert.")
	cmd.Flags().StringVar(&opts.ClientCertFile, "tls-cert-path", "/etc/konnectivity/proxy-client/tls.crt", "The path to the konnectivity client's tls certificate.")
	cmd.Flags().StringVar(&opts.ClientKeyFile, "tls-key-path", "/etc/konnectivity/proxy-client/tls.key", "The path to the konnectivity client's private key.")

	cmd.Flags().StringVar(&httpProxyURL, "http-proxy", "", "HTTP proxy to use on hosted cluster requests")
	cmd.Flags().StringVar(&httpsProxyURL, "https-proxy", "", "HTTPS proxy to use on hosted cluster requests")
	cmd.Flags().StringVar(&noProxy, "no-proxy", "", "URLs that should not use the provided http-proxy and https-proxy")

	cmd.Flags().BoolVar(&opts.ConnectDirectlyToCloudAPIs, "connect-directly-to-cloud-apis", false, "If true, bypass konnectivity to connect to cloud APIs while still honoring management proxy config")

	cmd.Run = func(cmd *cobra.Command, args []string) {
		l.Info("Starting proxy", "version", version.String())
		c, err := client.New(ctrl.GetConfigOrDie(), client.Options{})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: failed to get kubernetes client: %v", err)
			os.Exit(1)
		}
		opts.Client = c
		opts.Log = l

		var proxyTLS *tls.Config
		var proxyURLHostPort *string
		proxyHostNames := sets.New[string]()

		if len(httpsProxyURL) > 0 {
			u, err := url.Parse(httpsProxyURL)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: failed to parse HTTPS proxy URL: %v", err)
				os.Exit(1)
			}
			hostName, _, err := net.SplitHostPort(u.Host)
			if err == nil {
				proxyHostNames.Insert(hostName)
			}
			l.V(4).Info("Data plane HTTPS proxy is set", "hostname", hostName, "url", u.String())
			proxyURLHostPort = ptr.To(u.Host)
		}
		if len(httpProxyURL) > 0 {
			u, err := url.Parse(httpProxyURL)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: failed to parse HTTP proxy URL: %v", err)
				os.Exit(1)
			}
			hostName, _, err := net.SplitHostPort(u.Host)
			if err == nil {
				proxyHostNames.Insert(hostName)
			}
			l.V(4).Info("Data plane HTTP proxy is set", "hostname", hostName, "url", u.String())
			if proxyURLHostPort == nil {
				proxyURLHostPort = ptr.To(u.Host)
			}
		}
		l.V(4).Info("Excluding API hosts from isCloudAPI check", "hosts", sets.List(proxyHostNames))
		opts.ExcludeCloudAPIHosts = sets.List(proxyHostNames)

		konnectivityDialer, err := konnectivityproxy.NewKonnectivityDialer(opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: failed to initialize konnectivity dialer: %v", err)
			os.Exit(1)
		}

		userProxyConfig := &httpproxy.Config{
			HTTPProxy:  httpProxyURL,
			HTTPSProxy: httpsProxyURL,
			NoProxy:    noProxy,
		}
		userProxyFunc := userProxyConfig.ProxyFunc()

		httpProxy := goproxy.NewProxyHttpServer()
		httpProxy.Verbose = true

		if proxyURLHostPort != nil {
			host, _, err := net.SplitHostPort(*proxyURLHostPort)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: failed to split proxy URL host port (%s): %v", *proxyURLHostPort, err)
			}
			proxyTLS = &tls.Config{
				MinVersion: tls.VersionTLS12,
				ServerName: host,
			}
		}
		httpProxy.Tr = &http.Transport{
			TLSClientConfig: proxyTLS,
			Proxy: func(req *http.Request) (*url.URL, error) {
				l.V(4).Info("Determining whether request should be proxied", "url", req.URL)
				u, err := userProxyFunc(req.URL)
				if err != nil {
					l.V(4).Error(err, "failed to determine whether request should be proxied")
					return nil, err
				}
				l.V(4).Info("Should proxy", "url", u)
				return u, nil
			},
			Dial: konnectivityDialer.Dial,
		}
		if httpsProxyURL != "" {
			httpProxy.ConnectDialWithReq = connectDialFunc(l, httpProxy, httpsProxyURL, opts.ConnectDirectlyToCloudAPIs, konnectivityDialer.IsCloudAPI, userProxyFunc)
		} else {
			httpProxy.ConnectDial = nil
			httpProxy.ConnectDialWithReq = nil
		}
		err = http.ListenAndServe(fmt.Sprintf(":%d", servingPort), httpProxy)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v", err)
			os.Exit(1)
		}
	}

	return cmd
}

func connectDialFunc(log logr.Logger, httpProxy *goproxy.ProxyHttpServer, proxyURL string, connectDirectlyToCloudAPIs bool, isCloudAPI func(string) bool, userProxyFunc func(*url.URL) (*url.URL, error)) func(req *http.Request, network, addr string) (net.Conn, error) {
	defaultDial := httpProxy.NewConnectDialToProxy(proxyURL)
	return func(req *http.Request, network, addr string) (net.Conn, error) {
		log.V(4).Info("Connect dial called", "network", network, "address", addr, "URL", req.URL)
		requestURL := *req.URL
		// Ensure the request URL scheme is set. This function is only called
		// for requests to https endpoints.
		requestURL.Scheme = "https"
		proxyURL, err := userProxyFunc(&requestURL)
		if err != nil {
			return nil, err
		}
		log.V(4).Info("Determined proxy URL", "url", proxyURL)
		host, _, err := net.SplitHostPort(requestURL.Host)
		if err != nil {
			return nil, err
		}
		// If the URL is a cloud API or it should not be proxied, then
		// send it through the dialer directly.
		if (connectDirectlyToCloudAPIs && isCloudAPI(host)) || proxyURL == nil {
			log.V(4).Info("Host is cloud API or should not use a proxy with it, dialing directly through konnectivity")
			return httpProxy.Tr.Dial(network, addr)
		}
		log.V(4).Info("Using proxy to dial", "proxy", proxyURL)
		return defaultDial(network, addr)
	}
}
