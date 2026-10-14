package kubernetesdefaultproxy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/openshift/hypershift/support/supportedversion"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
)

func NewStartCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "kubernetes-default-proxy",
		Short: "A small tcp proxy that will use HTTP_CONNECT to establish a connection if the HTTP_PROXY env var is set",
	}

	s := &server{
		log: ctrl.Log.WithName("kubernetes-default-proxy"),
	}

	cmd.Flags().StringVar(&s.listenAddr, "listen-addr", "", "TCP host:port to listen on (IPv6: [address]:port)")
	cmd.Flags().StringVar(&s.proxyAddr, "proxy-addr", "", "TCP host:port of the proxy server (IPv6: [address]:port)")
	cmd.Flags().StringVar(&s.apiServerAddr, "apiserver-addr", "", "TCP host:port of the apiserver (IPv6: [address]:port)")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := s.validate(cmd.Context()); err != nil {
			return err
		}
		return s.run(cmd.Context())
	}

	return cmd
}

type server struct {
	listenAddr    string
	proxyAddr     string
	apiServerAddr string
	log           logr.Logger
}

func (s *server) validate(ctx context.Context) error {
	var errs []error
	addresses := []struct {
		flag  string
		value string
	}{
		{flag: "listen-addr", value: s.listenAddr},
		{flag: "proxy-addr", value: s.proxyAddr},
		{flag: "apiserver-addr", value: s.apiServerAddr},
	}
	for index, address := range addresses {
		if address.value == "" {
			errs = append(errs, fmt.Errorf("--%s is mandatory", address.flag))
			continue
		}
		normalized, err := normalizeTCPAddress(ctx, address.value)
		if err != nil {
			errs = append(errs, fmt.Errorf("--%s: %w", address.flag, err))
			continue
		}
		addresses[index].value = normalized
	}
	if err := utilerrors.NewAggregate(errs); err != nil {
		return err
	}
	s.listenAddr, s.proxyAddr, s.apiServerAddr = addresses[0].value, addresses[1].value, addresses[2].value
	return nil
}

func normalizeTCPAddress(ctx context.Context, address string) (string, error) {
	invalid := errors.New("expected TCP host:port with an explicit port (IPv6: [address]:port); URLs are not supported")
	if strings.ContainsAny(address, "/\\@?#") || strings.IndexFunc(address, func(character rune) bool {
		return unicode.IsSpace(character) || unicode.IsControl(character)
	}) >= 0 {
		return "", invalid
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		if strings.ContainsAny(address, "[]") {
			return "", invalid
		}
		separator := strings.LastIndexByte(address, ':')
		if separator < 0 {
			return "", invalid
		}
		host, port = address[:separator], address[separator+1:]
		parsed, err := netip.ParseAddr(host)
		if err != nil || !parsed.Is6() {
			return "", invalid
		}
		if _, err := strconv.ParseUint(port, 10, 16); err != nil {
			return "", invalid
		}
		return net.JoinHostPort(host, port), nil
	}
	if strings.ContainsAny(host, "[]") || port == "" {
		return "", invalid
	}
	if strings.Contains(host, ":") {
		parsed, err := netip.ParseAddr(host)
		if err != nil || !parsed.Is6() {
			return "", invalid
		}
	}
	if _, err := net.DefaultResolver.LookupPort(ctx, "tcp", port); err != nil {
		return "", invalid
	}
	return address, nil
}

func (s *server) run(ctx context.Context) error {
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", s.listenAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on tcp:%s: %w", s.listenAddr, err)
	}
	s.log.Info("Starting to listen", "listen-address", s.listenAddr, "version", supportedversion.String())

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		conn, err := listener.Accept()
		if err != nil {
			s.log.Error(err, "accepting connection failed")
			continue
		}

		go s.handleConnection(ctx, conn)
	}
}

func (s *server) handleConnection(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	backendConn, err := (&net.Dialer{}).DialContext(ctx, "tcp", s.proxyAddr)
	if err != nil {
		s.log.Error(err, "failed diaing backend", "proxyAddr", s.proxyAddr)
		return
	}
	defer backendConn.Close()

	req := &http.Request{
		Method:     http.MethodConnect,
		URL:        &url.URL{Host: s.apiServerAddr},
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
	}
	if err := req.Write(backendConn); err != nil {
		s.log.Error(err, "failed to write connect request")
		return
	}

	response, err := http.ReadResponse(bufio.NewReader(backendConn), req)
	if err != nil {
		s.log.Error(err, "failed to read response to connect request")
		return
	}
	if response.StatusCode != http.StatusOK {
		s.log.Error(fmt.Errorf("got unexpected statuscode %d to CONNECT request", response.StatusCode), "failed to establish a connection through http connect")
		return
	}

	closer := make(chan struct{}, 2)
	go s.copy(closer, backendConn, conn)
	go s.copy(closer, conn, backendConn)
	<-closer

	s.log.Info("Connection completed")
}

func (s *server) copy(closer chan struct{}, dst io.Writer, src io.Reader) {
	_, err := io.Copy(dst, src)
	if err != nil && !strings.Contains(err.Error(), "use of closed network connection") {
		s.log.Error(err, "io.Copy failed")
	}
	closer <- struct{}{} // connection is closed, send signal to stop proxy
}
