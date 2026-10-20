package azure

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestInitializeAPIJournalCollector(t *testing.T) {
	t.Run("When API setup returns successfully, it should leave cleanup to the caller", func(t *testing.T) {
		cleaned := false
		collector, cleanup, err := initializeAPIJournalCollector(t.Context(), func(context.Context) (JournalCollector, func(), error) {
			return apiJournalCollectorForNodes(nil, ""), func() { cleaned = true }, nil
		})
		if err != nil || collector == nil || cleaned {
			t.Fatalf("unexpected setup outcome: %v, cleaned=%v", err, cleaned)
		}
		cleanup()
		if !cleaned {
			t.Fatal("caller could not clean up API access")
		}
	})
	t.Run("When setup is blocked past cancellation, it should return and clean up a late result", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		release := make(chan struct{})
		cleaned := make(chan struct{})
		_, _, err := initializeAPIJournalCollector(ctx, func(context.Context) (JournalCollector, func(), error) {
			cancel()
			<-release
			return nil, func() { close(cleaned) }, nil
		})
		close(release)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want cancellation", err)
		}
		select {
		case <-cleaned:
		case <-time.After(time.Second):
			t.Fatal("late API setup leaked its resources")
		}
	})
}

func TestAPIJournalCollectorForNodes(t *testing.T) {
	t.Run("When provider IDs differ in casing, it should collect the matching Node and requested unit", func(t *testing.T) {
		dir := t.TempDir()
		argsFile := filepath.Join(dir, "oc-args")
		t.Setenv("OC_ARGS_FILE", argsFile)
		script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$OC_ARGS_FILE\"\nprintf 'kubelet journal output\\n'\n"
		if err := os.WriteFile(filepath.Join(dir, "oc"), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
		collector := apiJournalCollectorForNodes([]corev1.Node{{ObjectMeta: metav1.ObjectMeta{Name: "azure-worker-0"}, Spec: corev1.NodeSpec{ProviderID: strings.ToUpper(testProviderID)}}}, "/tmp/journal-kubeconfig")
		var output bytes.Buffer
		if err := collector(t.Context(), azureMachineWithProviderID("worker-0", testProviderID), "kubelet", &output); err != nil {
			t.Fatal(err)
		}
		args, err := os.ReadFile(argsFile)
		if err != nil {
			t.Fatal(err)
		}
		got := strings.Split(strings.TrimSpace(string(args)), "\n")
		for _, arg := range []string{"azure-worker-0", "--kubeconfig=/tmp/journal-kubeconfig", "--unit=kubelet", "--boot=0", "--tail=100000", "--raw"} {
			if !slices.Contains(got, arg) {
				t.Fatalf("missing %s in %v", arg, got)
			}
		}
		if output.String() != "kubelet journal output\n" {
			t.Fatalf("wrong stdout: %q", output.String())
		}
	})
	t.Run("When a VM has no matching Node, it should report unavailable API access", func(t *testing.T) {
		collector := apiJournalCollectorForNodes(nil, "")
		err := collector(t.Context(), azureMachineWithProviderID("worker-0", testProviderID), "journal", io.Discard)
		if !errors.Is(err, errNoHostedNode) {
			t.Fatalf("got %v", err)
		}
	})
}

func TestNewAPIJournalCollector(t *testing.T) {
	for _, tt := range []struct {
		name       string
		secret     *corev1.Secret
		useForward bool
	}{
		{name: "When the hosted kubeconfig secret is missing, it should report unavailable access"},
		{name: "When the hosted kubeconfig is invalid, it should return a sanitized error", secret: &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "hc-kubeconfig", Namespace: "clusters"}, Data: map[string][]byte{"kubeconfig": []byte("secret invalid kubeconfig")}}},
		{name: "When a private cluster has no localhost kubeconfig, it should report unavailable access", useForward: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := corev1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			builder := fake.NewClientBuilder().WithScheme(scheme)
			if tt.secret != nil {
				builder = builder.WithObjects(tt.secret)
			}
			hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "hc", Namespace: "clusters"}, Status: hyperv1.HostedClusterStatus{KubeConfig: &corev1.LocalObjectReference{Name: "hc-kubeconfig"}}}
			var config *rest.Config
			if tt.useForward {
				config = &rest.Config{}
			}
			collector, cleanup, err := newAPIJournalCollector(t.Context(), t.Context(), builder.Build(), hc, config)
			if err == nil || collector != nil || cleanup != nil || strings.Contains(err.Error(), "secret invalid") {
				t.Fatalf("unexpected or unsanitized setup result: %v", err)
			}
		})
	}
}
