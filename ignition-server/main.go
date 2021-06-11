package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

// We only match /ignition/$NodePoolName
var ignPathPattern = regexp.MustCompile("^/ignition/[^/ ]*$")

// This is the simplest http server that enable us to satisfy
// 1 - 1 relation between clusters and ign endpoints.
// At the moment this acts as purely a proxy towards MCS clusterIP type Services that
// are named machine-config-server-$nodePoolName by convention and
// are created on demand by NodePools,
// this might / might not change in the future.
// TODO (alberto): Metrics.

func main() {
	cmd := &cobra.Command{
		Use: "ignition-server",
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

type Options struct {
	Addr      string
	CertFile  string
	KeyFile   string
	TokenFile string
}

func NewStartCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Starts the ignition server",
	}

	opts := Options{
		Addr:      "0.0.0.0:9090",
		CertFile:  "/var/run/secrets/ignition/tls.crt",
		KeyFile:   "/var/run/secrets/ignition/tls.key",
		TokenFile: "/var/run/secrets/ignition/token",
	}

	cmd.Flags().StringVar(&opts.Addr, "addr", opts.Addr, "Listen address")
	cmd.Flags().StringVar(&opts.CertFile, "cert-file", opts.CertFile, "Path to the serving cert")
	cmd.Flags().StringVar(&opts.KeyFile, "key-file", opts.KeyFile, "Path to the serving key")
	cmd.Flags().StringVar(&opts.TokenFile, "token-file", opts.TokenFile, "Path to the auth token")

	cmd.Run = func(cmd *cobra.Command, args []string) {
		ctx, cancel := context.WithCancel(context.Background())
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT)
		go func() {
			<-sigs
			cancel()
		}()

		// TODO: Add an fsnotify watcher to cancel the context and trigger a restart
		// if any of the secret data has changed.
		if err := run(ctx, opts); err != nil {
			log.Fatal(err)
		}
	}

	return cmd
}

func run(ctx context.Context, opts Options) error {
	token, err := ioutil.ReadFile(opts.TokenFile)
	if err != nil {
		return fmt.Errorf("failed to read token file: %w", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("User Agent: %s. Requested: %s", r.Header.Get("User-Agent"), r.URL.Path)

		if !ignPathPattern.MatchString(r.URL.Path) {
			// No pattern matched; send 404 response.
			log.Printf("Path not found: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}

		// Get the nodePool name to build the MCS Service targetURL
		// by convention machine-config-server-$nodePoolName/config/master.
		urlPathSplit := strings.SplitAfter(r.URL.Path, "/")
		if len(urlPathSplit) != 3 {
			// This can only happen if our regExp is not working as expected.
			log.Printf("Unexpected URL path: %s", r.URL.Path)
			http.Error(w, fmt.Sprintf("Bad request, path %q is not supported", r.URL.Path), http.StatusBadRequest)
			return
		}
		nodePoolName := urlPathSplit[2]
		targetURL := fmt.Sprintf("http://machine-config-server-%s/config/master", nodePoolName)

		// Authorize the request against the token
		const bearerPrefix = "Bearer "
		auth := r.Header.Get("Authorization")
		n := len(bearerPrefix)
		if len(auth) < n || auth[:n] != bearerPrefix {
			log.Printf("Invalid Authorization header value prefix")
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		encodedToken := auth[n:]
		decodedToken, err := base64.StdEncoding.DecodeString(encodedToken)
		if err != nil {
			log.Printf("Invalid token value")
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		if !bytes.Equal(decodedToken, token) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// Build proxy request.
		proxyReq, err := http.NewRequest("GET", targetURL, nil)
		if err != nil {
			// Send 500 response.
			log.Printf("Server internal error: %v", err)
			http.Error(w, fmt.Sprintf("Server internal error: %v", err), http.StatusInternalServerError)
		}

		// Copy original Headers into proxy request.
		for k, vv := range r.Header {
			for _, v := range vv {
				proxyReq.Header.Add(k, v)
			}
		}

		// Send proxy request.
		client := &http.Client{
			Timeout: 5 * time.Second,
		}
		resp, err := client.Do(proxyReq)
		if err != nil {
			// Send 503 response.
			log.Printf("Service unavailable: %v", err)
			http.Error(w, fmt.Sprintf("Service unavailable: %v", err), http.StatusServiceUnavailable)
			return
		}
		defer resp.Body.Close()

		// Copy proxy response headers into the responseWriter.
		for k, vv := range resp.Header {
			for _, v := range vv {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)

		if _, err := io.Copy(w, resp.Body); err != nil {
			log.Printf("error copying body: %v", err)
			http.Error(w, "Request can't be handled", http.StatusInternalServerError)
			return
		}
	})

	server := http.Server{
		Addr:         opts.Addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("error shutting down server: %s", err)
		}
	}()

	log.Printf("Listening on %s", opts.Addr)
	if err := server.ListenAndServeTLS(opts.CertFile, opts.KeyFile); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
