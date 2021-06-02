package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
)

// We only match /ignition/$NodePoolName
var ignPathPattern = regexp.MustCompile("^/ignition/[^/ ]*$")

// TODO (alberto): Parameterize listening port and URL to proxy.
var addr = "0.0.0.0:9090"

// This is the simplest http server that enable us to satisfy
// 1 - 1 relation between clusters and ign endpoints.
// At the moment this acts as purely a proxy towards MCS clusterIP type Services that
// are named machine-config-server-$nodePoolName by convention and
// are created on demand by NodePools,
// this might / might not change in the future.
// TODO (alberto): Support only https.
// TODO (alberto): Support only token authenticated requests.
// TODO (alberto): Metrics.
func main() {
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
		client := &http.Client{}
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

		io.Copy(w, resp.Body)
	})

	ctx, cancel := context.WithCancel(context.Background())
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT)
	go func() {
		<-sigs
		cancel()
	}()

	server := http.Server{Addr: addr, Handler: mux}

	go func() {
		<-ctx.Done()
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("error shutting down server: %s", err)
		}
	}()

	log.Printf("Listening on %s", addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
