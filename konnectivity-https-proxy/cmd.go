package konnectivityhttpsproxy

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"

	"github.com/elazarl/goproxy"
	"github.com/openshift/hypershift/pkg/version"
	"github.com/openshift/hypershift/support/konnectivityproxy"
	"github.com/spf13/cobra"
	"go.uber.org/zap/zapcore"
	"golang.org/x/net/http/httpproxy"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func NewStartCommand() *cobra.Command {
	l := log.Log.WithName("konnectivity-https-proxy")
	log.SetLogger(zap.New(zap.UseDevMode(true), zap.JSONEncoder(func(o *zapcore.EncoderConfig) {
		o.EncodeTime = zapcore.RFC3339TimeEncoder
	})))
	cmd := &cobra.Command{
		Use:   "konnectivity-https-proxy",
		Short: "Runs the konnectivity https proxy server.",
		Long: ` Runs the konnectivity https proxy server.
		This proxy accepts request and tunnels them through the designated Konnectivity Server.`,
	}

	opts := konnectivityproxy.Options{
		ResolveBeforeDial: true,
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

	cmd.Run = func(cmd *cobra.Command, args []string) {
		l.Info("Starting proxy", "version", version.String())
		c, err := client.New(ctrl.GetConfigOrDie(), client.Options{})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: failed to get kubernetes client: %v", err)
			os.Exit(1)
		}
		opts.Client = c
		opts.Log = l

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

		var proxyTLS *tls.Config
		var proxyURLHostPort *string

		if len(httpsProxyURL) > 0 {
			u, err := url.Parse(httpsProxyURL)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: failed to parse HTTPS proxy URL: %v", err)
				os.Exit(1)
			}
			proxyURLHostPort = ptr.To(u.Host)
		} else if len(httpProxyURL) > 0 {
			u, err := url.Parse(httpProxyURL)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: failed to parse HTTP proxy URL: %v", err)
				os.Exit(1)
			}
			proxyURLHostPort = ptr.To(u.Host)
		}
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
				return userProxyFunc(req.URL)
			},
			Dial: konnectivityDialer.Dial,
		}
		if httpsProxyURL != "" {
			httpProxy.ConnectDial = httpProxy.NewConnectDialToProxy(httpsProxyURL)
		} else {
			httpProxy.ConnectDial = nil
		}
		err = http.ListenAndServe(fmt.Sprintf(":%d", servingPort), httpProxy)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v", err)
			os.Exit(1)
		}
	}

	return cmd
}
