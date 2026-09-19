// Package gcplbserviceannotations implements a mutating admission webhook that injects
// the cloud.google.com/load-balancer-resource-labels annotation on every
// Service{type: LoadBalancer} admitted to a GCP hosted cluster.
//
// The webhook runs as a sidecar container alongside the kube-apiserver in the
// hosted control plane namespace on the management cluster. It listens on
// 127.0.0.1 (loopback) so that the KAS admission call reaches it directly.
// HCCO registers a MutatingWebhookConfiguration in the hosted cluster pointing
// to that loopback URL with the management cluster root CA as the CA bundle.
package gcplbserviceannotations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/openshift/hypershift/support/gcputil"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	"github.com/spf13/cobra"
)

var (
	scheme = runtime.NewScheme()
	codecs = serializer.NewCodecFactory(scheme)
)

const (
	// maxAdmissionReviewSize permits the kube-apiserver's maximum request body plus
	// the AdmissionReview envelope sent to this webhook.
	maxAdmissionReviewSize = 4 * 1024 * 1024

	readHeaderTimeout = 10 * time.Second
	readTimeout       = 30 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 120 * time.Second
)

func init() {
	utilruntime.Must(admissionv1.AddToScheme(scheme))
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

// Run starts the HTTPS webhook server. It blocks until the server exits.
func (o *Options) Run(ctx context.Context) error {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	mux := http.NewServeMux()
	mux.HandleFunc("/mutate", o.handleMutate)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	addr := fmt.Sprintf("127.0.0.1:%d", o.Port)
	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServeTLS(o.TLSCertFile, o.TLSKeyFile)
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shut down webhook server: %w", err)
		}
		if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve webhook: %w", err)
		}
		return nil
	}
}

// handleMutate is the admission webhook handler.
func (o *Options) handleMutate(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxAdmissionReviewSize))
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to read request body: %v", err), http.StatusBadRequest)
		return
	}

	review := &admissionv1.AdmissionReview{}
	if _, _, err := codecs.UniversalDeserializer().Decode(body, nil, review); err != nil {
		http.Error(w, fmt.Sprintf("failed to decode admission review: %v", err), http.StatusBadRequest)
		return
	}

	response := o.mutate(review.Request)
	response.UID = review.Request.UID
	review.Response = response

	respBytes, err := json.Marshal(review)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to marshal response: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(respBytes)
}

// mutate decides whether and how to mutate the incoming Service.
func (o *Options) mutate(req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
	// Only act on Services.
	if req.Kind.Kind != "Service" {
		return &admissionv1.AdmissionResponse{Allowed: true}
	}

	svc := &corev1.Service{}
	if err := json.Unmarshal(req.Object.Raw, svc); err != nil {
		return &admissionv1.AdmissionResponse{
			Allowed: false,
			Result: &metav1.Status{
				Message: fmt.Sprintf("failed to unmarshal Service: %v", err),
			},
		}
	}

	// Only mutate LoadBalancer services.
	if svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
		return &admissionv1.AdmissionResponse{Allowed: true}
	}

	currentValue, annotationExists := svc.Annotations[gcputil.LBResourceLabelsAnnotation]
	if (!annotationExists && o.Labels == "") || (annotationExists && o.Labels != "" && currentValue == o.Labels) {
		return &admissionv1.AdmissionResponse{Allowed: true}
	}

	patch, err := buildAnnotationPatch(svc, gcputil.LBResourceLabelsAnnotation, o.Labels)
	if err != nil {
		return &admissionv1.AdmissionResponse{
			Allowed: false,
			Result: &metav1.Status{
				Message: fmt.Sprintf("failed to build patch: %v", err),
			},
		}
	}

	patchType := admissionv1.PatchTypeJSONPatch
	return &admissionv1.AdmissionResponse{
		Allowed:   true,
		Patch:     patch,
		PatchType: &patchType,
	}
}

// buildAnnotationPatch returns a JSON patch that makes the annotation match value.
// An empty value removes a currently set annotation.
func buildAnnotationPatch(svc *corev1.Service, key, value string) ([]byte, error) {
	escapedKey := jsonPatchEscapeKey(key)
	if value == "" {
		return json.Marshal([]map[string]any{{
			"op":   "remove",
			"path": "/metadata/annotations/" + escapedKey,
		}})
	}

	if len(svc.Annotations) == 0 {
		return json.Marshal([]map[string]any{{
			"op":    "add",
			"path":  "/metadata/annotations",
			"value": map[string]string{key: value},
		}})
	}

	op := "add"
	if _, exists := svc.Annotations[key]; exists {
		op = "replace"
	}
	return json.Marshal([]map[string]any{{
		"op":    op,
		"path":  "/metadata/annotations/" + escapedKey,
		"value": value,
	}})
}

// jsonPatchEscapeKey escapes a JSON Pointer token per RFC 6901:
// '~' → '~0', '/' → '~1'.
func jsonPatchEscapeKey(key string) string {
	out := make([]byte, 0, len(key))
	for i := 0; i < len(key); i++ {
		switch key[i] {
		case '~':
			out = append(out, '~', '0')
		case '/':
			out = append(out, '~', '1')
		default:
			out = append(out, key[i])
		}
	}
	return string(out)
}
