// Package gcplbserviceannotations implements a mutating admission webhook that injects
// the cloud.google.com/load-balancer-resource-labels annotation on every
// Service{type: LoadBalancer} admitted to a GCP hosted cluster.
//
// The webhook runs as a sidecar container alongside the kube-apiserver in the
// hosted control plane namespace on the management cluster. Its admission
// listener binds to 127.0.0.1 (loopback) so that the KAS admission call reaches
// it directly. A separate pod-reachable HTTP listener serves health probes.
// HCCO registers a MutatingWebhookConfiguration in the hosted cluster pointing
// to the loopback admission URL with the management cluster root CA as the CA bundle.
package gcplbserviceannotations

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/openshift/hypershift/support/gcputil"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/spf13/cobra"
)

var (
	scheme = runtime.NewScheme()
)

const (
	healthProbePort = 8082

	readHeaderTimeout = 10 * time.Second
	readTimeout       = 30 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 120 * time.Second
)

func init() {
	utilruntime.Must(admissionv1.AddToScheme(scheme))
	utilruntime.Must(corev1.AddToScheme(scheme))
}

// Options holds the webhook server configuration.
type Options struct {
	// Labels is the set of GCP resource labels to inject, serialized as
	// "key=value,key=value" (same format as LBResourceLabelsAnnotation).
	Labels string
	// TLSCertFile is the path to the TLS certificate file.
	TLSCertFile string
	// TLSKeyFile is the path to the TLS private key file.
	TLSKeyFile string
	// Port is the TCP port to listen on (default 8443).
	Port int
}

// NewStartCommand returns the cobra.Command for the gcp-lb-service-annotations-webhook subcommand.
func NewStartCommand() *cobra.Command {
	opts := &Options{
		Port: 8443,
	}

	cmd := &cobra.Command{
		Use:   "gcp-lb-service-annotations-webhook",
		Short: "Start the GCP load-balancer resource-labels admission webhook",
		Long: `Runs a mutating admission webhook that injects the
cloud.google.com/load-balancer-resource-labels annotation on every
Service{type: LoadBalancer} created in the hosted cluster, so that the
GCP cloud-controller-manager applies the specified resource labels to the
GCP forwarding rules it creates.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.Run(cmd.Context())
		},
	}

	cmd.Flags().StringVar(&opts.Labels, "labels", "", "Comma-separated GCP resource labels to inject (key=value,…)")
	cmd.Flags().StringVar(&opts.TLSCertFile, "tls-cert", "/var/run/app/certs/tls.crt", "Path to the TLS certificate file")
	cmd.Flags().StringVar(&opts.TLSKeyFile, "tls-key", "/var/run/app/certs/tls.key", "Path to the TLS private key file")
	cmd.Flags().IntVar(&opts.Port, "port", 8443, "Port to listen on")

	return cmd
}

// Run starts the private HTTPS webhook server and the pod-reachable health server.
// It blocks until a server exits or the context is canceled.
func (o *Options) Run(ctx context.Context) error {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	admissionMux := http.NewServeMux()
	admissionMux.Handle("/mutate", &admission.Webhook{
		Handler: newServiceAnnotationHandler(o.Labels),
	})

	addr := fmt.Sprintf("127.0.0.1:%d", o.Port)
	certificate, err := tls.LoadX509KeyPair(o.TLSCertFile, o.TLSKeyFile)
	if err != nil {
		return fmt.Errorf("load webhook serving certificate: %w", err)
	}
	admissionListener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen for admission requests: %w", err)
	}

	admissionServer := &http.Server{
		Handler:           admissionMux,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}
	admissionReady := &atomic.Bool{}
	healthServer := &http.Server{
		Addr:              fmt.Sprintf(":%d", healthProbePort),
		Handler:           newHealthHandler(admissionReady),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	errCh := make(chan error, 2)
	go func() {
		errCh <- admissionServer.Serve(tls.NewListener(admissionListener, &tls.Config{
			Certificates: []tls.Certificate{certificate},
		}))
	}()
	admissionReady.Store(true)
	go func() {
		errCh <- healthServer.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		admissionReady.Store(false)
		shutdownErr := shutdownServers(admissionServer, healthServer)
		if errors.Is(err, http.ErrServerClosed) {
			return shutdownErr
		}
		return errors.Join(err, shutdownErr)
	case <-ctx.Done():
		admissionReady.Store(false)
		return shutdownServers(admissionServer, healthServer)
	}
}

func newHealthHandler(admissionReady *atomic.Bool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if !admissionReady.Load() {
			http.Error(w, "admission listener is not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

func shutdownServers(servers ...*http.Server) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var errs []error
	for _, server := range servers {
		if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

type serviceAnnotationHandler struct {
	labels  string
	decoder admission.Decoder
}

func newServiceAnnotationHandler(labels string) *serviceAnnotationHandler {
	return &serviceAnnotationHandler{
		labels:  labels,
		decoder: admission.NewDecoder(scheme),
	}
}

// Handle decides whether and how to mutate an incoming Service.
func (h *serviceAnnotationHandler) Handle(_ context.Context, req admission.Request) admission.Response {
	if req.UID == "" {
		return admission.Errored(http.StatusBadRequest, errors.New("admission request UID is required"))
	}

	// Only act on Services.
	if req.Kind.Kind != "Service" {
		return admission.Allowed("")
	}

	svc := &corev1.Service{}
	if err := h.decoder.Decode(req, svc); err != nil {
		return admission.Errored(http.StatusBadRequest, fmt.Errorf("decode Service: %w", err))
	}

	// Only mutate LoadBalancer services.
	if svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
		return admission.Allowed("")
	}

	currentValue, annotationExists := svc.Annotations[gcputil.LBResourceLabelsAnnotation]
	if (!annotationExists && h.labels == "") || (annotationExists && h.labels != "" && currentValue == h.labels) {
		return admission.Allowed("")
	}

	mutated := svc.DeepCopy()
	if h.labels == "" {
		delete(mutated.Annotations, gcputil.LBResourceLabelsAnnotation)
	} else {
		if mutated.Annotations == nil {
			mutated.Annotations = map[string]string{}
		}
		mutated.Annotations[gcputil.LBResourceLabelsAnnotation] = h.labels
	}

	mutatedRaw, err := json.Marshal(mutated)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, fmt.Errorf("marshal mutated Service: %w", err))
	}

	return admission.PatchResponseFromRaw(req.Object.Raw, mutatedRaw)
}
