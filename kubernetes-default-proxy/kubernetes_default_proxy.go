package kubernetesdefaultproxy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

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

	cmd.Flags().StringVar(&s.listenAddr, "listen-addr", "", "Address to listen on")
	cmd.Flags().StringVar(&s.proxyAddr, "proxy-addr", "", "Address of the proxy server")
	cmd.Flags().StringVar(&s.apiServerAddr, "apiserver-addr", "", "Address of the apiserver")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := s.validate(); err != nil {
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

func (s *server) validate() error {
	var errs []error
	if s.listenAddr == "" {
		errs = append(errs, errors.New("--listen-addr is mandatory"))
	}
	if s.proxyAddr == "" {
		errs = append(errs, errors.New("--proxy-addr is mandatory"))
	}
	if s.apiServerAddr == "" {
		errs = append(errs, errors.New("--apiserver-addr is mandatory"))
	}
	return utilerrors.NewAggregate(errs)
}

func (s *server) run(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.listenAddr)
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

		go func() {
			defer conn.Close()

			backendConn, err := net.Dial("tcp", s.proxyAddr)
			if err != nil {
				s.log.Error(err, "failed diaing backend", "proxyAddr", s.proxyAddr)
				return
			}
			defer backendConn.Close()

			req := &http.Request{
				Method:     "CONNECT",
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
			if response.StatusCode != 200 {
				s.log.Error(fmt.Errorf("got unexpected statuscode %d to CONNECT request", response.StatusCode), "failed to establish a connection through http connect")
				return
			}

			closer := make(chan struct{}, 2)
			go s.copy(closer, backendConn, conn)
			go s.copy(closer, conn, backendConn)
			<-closer

			s.log.Info("Connection completed")
		}()
	}
}

func (s *server) copy(closer chan struct{}, dst io.Writer, src io.Reader) {
	_, err := io.Copy(dst, src)
	if err != nil && !strings.Contains(err.Error(), "use of closed network connection") {
		s.log.Error(err, "io.Copy failed")
	}
	closer <- struct{}{} // connection is closed, send signal to stop proxy
}
