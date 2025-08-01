package sharedingressconfiggenerator

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/util"

	routev1 "github.com/openshift/api/route/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

type SharedIngressConfigReconciler struct {
	Client client.Client

	ConfigPath               string
	HAProxyRuntimeSocketPath string

	// lastReloadedHash stores the hash of the last successfully reloaded configuration
	lastReloadedHash []byte
}

func (r *SharedIngressConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Client = mgr.GetClient()

	// A channel is used to generate an initial sync event.
	// Afterwards, the controller syncs on HostedClusters.
	initialSync := make(chan event.GenericEvent)

	go func() {
		initialSync <- event.GenericEvent{Object: &hyperv1.HostedCluster{}}
	}()

	return ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.HostedCluster{}).
		WatchesRawSource(source.Channel(initialSync, &handler.EnqueueRequestForObject{})).
		Watches(
			&routev1.Route{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
				if _, hasHCPLabel := obj.GetLabels()[util.HCPRouteLabel]; !hasHCPLabel {
					return nil
				}
				return []ctrl.Request{{NamespacedName: client.ObjectKey{
					Name:      obj.GetName(),
					Namespace: obj.GetNamespace(),
				}}}
			}),
		).
		Named("SharedIngressConfigGenerator").
		Complete(r)
}

const (
	reloadTimeout = 5 * time.Second
)

func (r *SharedIngressConfigReconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	logger := ctrl.LoggerFrom(ctx)

	currentHash, err := r.currentConfigHash()
	if err != nil {
		return ctrl.Result{}, err
	}

	// Create a temporary file for the new config
	dir := filepath.Dir(r.ConfigPath)
	tmpFile, err := os.CreateTemp(dir, ".haproxy.*.cfg.tmp")
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create temporary config file: %w", err)
	}
	defer tmpFile.Close()
	defer os.Remove(tmpFile.Name())

	// Use MultiWriter to write to both hash and file simultaneously
	h := sha256.New()
	writer := io.MultiWriter(tmpFile, h)

	if err := generateRouterConfig(ctx, r.Client, writer); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to generate config: %w", err)
	}
	newHash := h.Sum(nil)

	// Update the config file if it doesn't exist or has changed
	if currentHash == nil || !bytes.Equal(currentHash, newHash) {
		logger.Info("HAProxy configuration change detected!")

		// Ensure the file is synced to disk before we rename it
		if err := tmpFile.Sync(); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to sync temporary file: %w", err)
		}
		tmpFile.Close()

		if err := os.Rename(tmpFile.Name(), r.ConfigPath); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update config file: %w", err)
		}

		logger.Info("HAProxy configuration file updated", "hash", fmt.Sprintf("%x", newHash))
	}

	// Reload HAProxy if current config differs from last successfully reloaded config
	if !bytes.Equal(r.lastReloadedHash, newHash) {
		logger.Info("Reloading HAProxy configuration")
		if response, err := sendHAProxyCommand(r.HAProxyRuntimeSocketPath, "reload"); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reload HAProxy: response: %s, err: %w", response, err)
		}

		// Update the last reloaded hash only after successful reload
		r.lastReloadedHash = make([]byte, len(newHash))
		copy(r.lastReloadedHash, newHash)
		logger.Info("HAProxy configuration reloaded successfully", "hash", fmt.Sprintf("%x", newHash))
	}

	return ctrl.Result{}, nil
}

func (r *SharedIngressConfigReconciler) currentConfigHash() ([]byte, error) {
	file, err := os.Open(r.ConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // File does not exist, return nil
		}
		return nil, fmt.Errorf("failed to open config file: %w", err)
	}
	defer file.Close()

	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return nil, fmt.Errorf("failed to read current config: %w", err)
	}

	return h.Sum(nil), nil
}

// sendHAProxyCommand connects to the specified Unix socket, sends a command,
// and returns the response from HAProxy.
func sendHAProxyCommand(socketPath, command string) (string, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return "", fmt.Errorf("failed to connect to socket %s: %w", socketPath, err)
	}
	defer conn.Close()

	// Set a deadline for reads/writes to avoid blocking forever.
	if err := conn.SetDeadline(time.Now().Add(reloadTimeout)); err != nil {
		return "", fmt.Errorf("failed to set deadline on socket: %w", err)
	}

	// A newline character is required to terminate the command.
	_, err = conn.Write([]byte(command + "\n"))
	if err != nil {
		return "", fmt.Errorf("failed to send command to socket: %w", err)
	}

	response, err := io.ReadAll(conn)
	if err != nil {
		return "", fmt.Errorf("failed to read response from socket: %w", err)
	}

	return string(response), nil
}
