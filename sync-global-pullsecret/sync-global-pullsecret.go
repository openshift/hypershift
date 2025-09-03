package syncglobalpullsecret

// sync-global-pullsecret syncs the pull secret from the user provided pull secret in DataPlane and appends it to the HostedCluster PullSecret to be deployed in the nodes of the HostedCluster.

import (
	"context"
	"fmt"
	"os"
	"time"

	hyperapi "github.com/openshift/hypershift/support/api"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/coreos/go-systemd/dbus"
	"github.com/spf13/cobra"
)

// syncGlobalPullSecretOptions contains the configuration options for the sync-global-pullsecret command
type syncGlobalPullSecretOptions struct {
	kubeletConfigJsonPath string
	globalPSSecretName    string
}

//go:generate ../hack/tools/bin/mockgen -destination=sync-global-pullsecret_mock.go -package=syncglobalpullsecret . dbusConn
type dbusConn interface {
	RestartUnit(name string, mode string, ch chan<- string) (int, error)
	Close()
}

// GlobalPullSecretReconciler reconciles a Secret object
type GlobalPullSecretReconciler struct {
	cachedClient          crclient.Client
	uncachedClient        crclient.Client
	Scheme                *runtime.Scheme
	kubeletConfigJsonPath string
	globalPSSecretName    string
	globalPSSecretNS      string
}

const (
	defaultKubeletConfigJsonPath     = "/var/lib/kubelet/config.json"
	defaultGlobalPSSecretName        = "global-pull-secret"
	defaultGlobalPullSecretNamespace = "kube-system"
	dbusRestartUnitMode              = "replace"
	kubeletServiceUnit               = "kubelet.service"

	// Mounted secret file paths
	originalPullSecretFilePath = "/etc/original-pull-secret/.dockerconfigjson"
	globalPullSecretFilePath   = "/etc/global-pull-secret/.dockerconfigjson"

	// systemd job completion state as documented in go-systemd/dbus
	systemdJobDone = "done" // Job completed successfully
)

var (
	// writeFileFunc is a variable that holds the function used to write files.
	// This allows tests to inject custom write functions for testing rollback scenarios.
	writeFileFunc = os.WriteFile

	// readFileFunc is a variable that holds the function used to read files.
	// This allows tests to inject custom read functions for testing.
	readFileFunc = os.ReadFile
)

// NewRunCommand creates a new cobra.Command for the sync-global-pullsecret command
func NewRunCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync-global-pullsecret",
		Short: "Syncs a mixture between the user provided pull secret in DataPlane and the HostedCluster PullSecret to be deployed in the nodes of the HostedCluster",
		Long:  `Syncs a mixture between the user provided pull secret in DataPlane and the HostedCluster PullSecret to be deployed in the nodes of the HostedCluster. The resulting pull secret is deployed in a DaemonSet in the DataPlane that updates the kubelet.config.json file with the new pull secret. If there are conflicting entries in the resulting global pull secret, the user provided pull secret will prevail.`,
	}

	opts := syncGlobalPullSecretOptions{
		kubeletConfigJsonPath: defaultKubeletConfigJsonPath,
	}
	cmd.Flags().StringVar(&opts.globalPSSecretName, "global-pull-secret-name", defaultGlobalPSSecretName, "The name of the global pullSecret secret in the DataPlane.")
	cmd.Run = func(cmd *cobra.Command, args []string) {
		setupLog := ctrl.Log.WithName("global-pullsecret")
		zapOpts := zap.Options{
			Development: true,
		}
		ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zapOpts)))
		ctx := ctrl.SetupSignalHandler()
		if err := opts.run(ctx); err != nil {
			setupLog.Error(err, "unable to start manager")
			os.Exit(1)
		}
	}

	return cmd
}

// run executes the main logic of the sync-global-pullsecret command
func (o *syncGlobalPullSecretOptions) run(ctx context.Context) error {
	// Create manager
	// TODO: Review if we really need a controller here with the new way of work
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: hyperapi.Scheme,
		Cache: cache.Options{
			DefaultNamespaces: map[string]cache.Config{defaultGlobalPullSecretNamespace: {}},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create manager: %w", err)
	}

	uncachedClientRestConfig := mgr.GetConfig()
	uncachedClientRestConfig.WarningHandler = rest.NoWarnings{}
	uncachedClient, err := crclient.New(uncachedClientRestConfig, crclient.Options{
		Scheme: mgr.GetScheme(),
		Mapper: mgr.GetRESTMapper(),
	})
	if err != nil {
		return fmt.Errorf("failed to create uncached client: %w", err)
	}

	// Create reconciler
	r := &GlobalPullSecretReconciler{
		cachedClient:          mgr.GetClient(),
		uncachedClient:        uncachedClient,
		Scheme:                mgr.GetScheme(),
		kubeletConfigJsonPath: o.kubeletConfigJsonPath,
		globalPSSecretName:    o.globalPSSecretName,
		globalPSSecretNS:      defaultGlobalPullSecretNamespace,
	}

	// Create controller
	if err := ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Secret{}).
		WithEventFilter(predicate.Funcs{
			// Adding filters to avoid processing events that are not relevant
			CreateFunc: func(e event.CreateEvent) bool {
				return o.isTargetSecret(e.Object)
			},
			UpdateFunc: func(e event.UpdateEvent) bool {
				return o.isTargetSecret(e.ObjectNew)
			},
			DeleteFunc: func(e event.DeleteEvent) bool {
				return o.isTargetSecret(e.Object)
			},
			GenericFunc: func(e event.GenericEvent) bool {
				return false
			},
		}).
		Complete(r); err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}

	// Start manager
	if err := mgr.Start(ctx); err != nil {
		return fmt.Errorf("failed to start manager: %w", err)
	}

	return nil
}

// isTargetSecret checks if the given object is the target secret we want to watch
func (o *syncGlobalPullSecretOptions) isTargetSecret(obj crclient.Object) bool {
	// Check if it's a Secret and has the correct name and namespace
	if secret, ok := obj.(*corev1.Secret); ok {
		return secret.GetNamespace() == defaultGlobalPullSecretNamespace &&
			secret.GetName() == o.globalPSSecretName
	}
	return false
}

// Reconcile handles the reconciliation logic for the GlobalPullSecret
func (r *GlobalPullSecretReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling GlobalPullSecret")

	// Try to read the global pull secret from mounted file first
	globalPullSecretBytes, err := readPullSecretFromFile(globalPullSecretFilePath)
	if err != nil {
		// If global pull secret file doesn't exist, fall back to original pull secret
		log.Info("Global pull secret file not found, using original pull secret", "error", err)
		originalPullSecretBytes, err := readPullSecretFromFile(originalPullSecretFilePath)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("failed to read original pull secret from file: %w", err)
		}
		globalPullSecretBytes = originalPullSecretBytes
	} else {
		log.Info("Global pull secret found, using it")
	}

	// Create a temporary secret object for compatibility with existing checkAndFixFile logic
	chosenPullSecret := &corev1.Secret{
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: globalPullSecretBytes,
		},
	}

	// Normal reconciliation
	if err := r.checkAndFixFile(ctx, chosenPullSecret); err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to check and fix file: %w", err)
	}

	return reconcile.Result{}, nil
}

// checkAndFixFile reads the current file content and updates it if it differs from the desired content (global pull secret content).
// Have in mind the logic which do the merge of the pull secret is in the globalpullsecret package under the HCCO.
func (r *GlobalPullSecretReconciler) checkAndFixFile(ctx context.Context, pullSecret *corev1.Secret) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Checking and fixing file")

	// Validate pullSecret is not nil
	if pullSecret == nil {
		return fmt.Errorf("pullSecret cannot be nil")
	}

	// Validate pullSecret.Data is not nil
	if pullSecret.Data == nil {
		return fmt.Errorf("pullSecret.Data cannot be nil")
	}

	log.Info("DEBUG: Pass Data check")
	// Validate the required key exists
	pullSecretBytes, exists := pullSecret.Data[corev1.DockerConfigJsonKey]
	if !exists {
		return fmt.Errorf("pullSecret does not contain required key: %s", corev1.DockerConfigJsonKey)
	}

	// Read existing content if file exists
	existingContent, err := os.ReadFile(r.kubeletConfigJsonPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read existing file: %w", err)
	}

	// If file content is different, write the desired content
	if string(existingContent) != string(pullSecretBytes) {
		log.Info("file content is different, updating it")
		// Save original content for potential rollback
		originalContent := existingContent

		// Write the new content
		if err := writeFileFunc(r.kubeletConfigJsonPath, pullSecretBytes, 0600); err != nil {
			return fmt.Errorf("failed to write file: %w", err)
		}
		log.Info("Pull secret updated", "file", r.kubeletConfigJsonPath)

		// Attempt to restart Kubelet with retries
		maxRetries := 3
		var lastErr error
		for attempt := 1; attempt <= maxRetries; attempt++ {
			if err := signalKubeletToRestartProcess(ctx); err != nil {
				lastErr = err
				if attempt < maxRetries {
					log.Info(fmt.Sprintf("Attempt %d failed, retrying...: %v", attempt, err))
					time.Sleep(time.Duration(attempt) * time.Second)
					continue
				}
			} else {
				log.Info("Successfully restarted Kubelet", "attempt", attempt)
				return nil
			}
		}

		// If we reach this point, all retries failed - perform rollback
		log.Info("Failed to restart Kubelet after some attempts, executing rollback", "maxRetries", maxRetries, "lastErr", lastErr)
		if err := writeFileFunc(r.kubeletConfigJsonPath, originalContent, 0600); err != nil {
			return fmt.Errorf("2 errors happened: the kubelet restart failed after %d attempts and it failed to rollback the file: %w", maxRetries, err)
		}
		return fmt.Errorf("failed to restart kubelet after %d attempts, rolled back changes: %w", maxRetries, lastErr)
	}

	return nil
}

// signalKubeletToRestartProcess signals Kubelet to reload the config by restarting the kubelet.service.
// This is done by sending a signal to systemd via dbus.
func signalKubeletToRestartProcess(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Signaling Kubelet to reload config")
	conn, err := dbus.New()
	if err != nil {
		return fmt.Errorf("failed to connect to dbus: %w", err)
	}
	defer conn.Close()

	return restartKubelet(ctx, conn)
}

func restartKubelet(ctx context.Context, conn dbusConn) error {
	log := ctrl.LoggerFrom(ctx)
	ch := make(chan string)
	if _, err := conn.RestartUnit(kubeletServiceUnit, dbusRestartUnitMode, ch); err != nil {
		return fmt.Errorf("failed to restart kubelet: %w", err)
	}

	// Wait for the result of the restart
	result := <-ch
	if result != systemdJobDone {
		return fmt.Errorf("failed to restart kubelet, result: %s", result)
	}

	log.Info("Successfully signaled Kubelet to reload config")
	return nil
}

// readPullSecretFromFile reads a pull secret from a mounted file path
func readPullSecretFromFile(filePath string) ([]byte, error) {
	content, err := readFileFunc(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read pull secret from file %s: %w", filePath, err)
	}
	return content, nil
}
