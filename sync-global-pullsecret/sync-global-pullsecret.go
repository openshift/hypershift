package syncglobalpullsecret

// sync-global-pullsecret syncs the pull secret from the user provided pull secret in DataPlane and appends it to the HostedCluster PullSecret to be deployed in the nodes of the HostedCluster.

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	hyperapi "github.com/openshift/hypershift/support/api"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
	checkInterval         time.Duration
}

const (
	defaultKubeletConfigJsonPath     = "/var/lib/kubelet/config.json"
	defaultGlobalPSSecretName        = "global-pull-secret"
	defaultCheckInterval             = 30 * time.Second
	defaultGlobalPullSecretNamespace = "kube-system"
	dbusRestartUnitMode              = "replace"
	kubeletServiceUnit               = "kubelet.service"
)

// systemd job completion state as documented in go-systemd/dbus
const (
	systemdJobDone = "done" // Job completed successfully
)

//go:generate ../hack/tools/bin/mockgen -destination=sync-global-pullsecret_mock.go -package=syncglobalpullsecret . dbusConn
type dbusConn interface {
	RestartUnit(name string, mode string, ch chan<- string) (int, error)
	Close()
}

// writeFileFunc is a variable that holds the function used to write files.
// This allows tests to inject custom write functions for testing rollback scenarios.
var writeFileFunc = os.WriteFile

// GlobalPullSecretReconciler reconciles a Secret object
type GlobalPullSecretReconciler struct {
	client.Client
	Scheme                *runtime.Scheme
	kubeletConfigJsonPath string
	globalPSSecretName    string
	globalPSSecretNS      string
}

// NewRunCommand creates a new cobra.Command for the sync-global-pullsecret command
func NewRunCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync-global-pullsecret",
		Short: "Syncs a mixture between the user provided pull secret in DataPlane and the HostedCluster PullSecret to be deployed in the nodes of the HostedCluster",
		Long:  `Syncs a mixture between the user provided pull secret in DataPlane and the HostedCluster PullSecret to be deployed in the nodes of the HostedCluster. The resulting pull secret is deployed in a DaemonSet in the DataPlane that updates the kubelet.config.json file with the new pull secret. If there are conflicting entries in the resulting global pull secret, the user provided pull secret will prevail.`,
	}

	opts := syncGlobalPullSecretOptions{
		kubeletConfigJsonPath: defaultKubeletConfigJsonPath,
		checkInterval:         defaultCheckInterval,
	}
	cmd.Flags().DurationVar(&opts.checkInterval, "check-interval", opts.checkInterval, "The interval at which the file is checked for changes.")
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
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: hyperapi.Scheme,
	})
	if err != nil {
		return fmt.Errorf("failed to create manager: %w", err)
	}

	// Create reconciler
	r := &GlobalPullSecretReconciler{
		Client:                mgr.GetClient(),
		Scheme:                mgr.GetScheme(),
		kubeletConfigJsonPath: o.kubeletConfigJsonPath,
		globalPSSecretName:    o.globalPSSecretName,
		globalPSSecretNS:      defaultGlobalPullSecretNamespace,
	}

	// Get the global pull secret
	globalPullSecret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: defaultGlobalPullSecretNamespace,
		Name:      o.globalPSSecretName,
	}, globalPullSecret); err != nil {
		return fmt.Errorf("failed to get global pull secret: %w", err)
	}

	// Create controller
	if err := ctrl.NewControllerManagedBy(mgr).
		For(globalPullSecret).
		WithEventFilter(predicate.Funcs{
			// Adding filters to avoid processing events that are not relevant
			CreateFunc: func(e event.CreateEvent) bool {
				return false
			},
			UpdateFunc: func(e event.UpdateEvent) bool {
				return o.isTargetSecret(e.ObjectNew)
			},
			DeleteFunc: func(e event.DeleteEvent) bool {
				return false
			},
			GenericFunc: func(e event.GenericEvent) bool {
				return false
			},
		}).
		Complete(r); err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}

	// Initial reconciliation
	if _, err := r.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{
			Namespace: defaultGlobalPullSecretNamespace,
			Name:      o.globalPSSecretName,
		},
	}); err != nil {
		return fmt.Errorf("initial reconciliation failed: %w", err)
	}

	// Start manager
	if err := mgr.Start(ctx); err != nil {
		return fmt.Errorf("failed to start manager: %w", err)
	}

	return nil
}

// isTargetSecret checks if the given object is the target secret we want to watch
func (o *syncGlobalPullSecretOptions) isTargetSecret(obj client.Object) bool {
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

	// Get the global pull secret
	globalPullSecret := &corev1.Secret{}
	if err := r.Get(ctx, req.NamespacedName, globalPullSecret); err != nil {
		log.Error(err, "Failed to get global pull secret")
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	// Check and fix the file
	if err := r.checkAndFixFile(ctx, globalPullSecret); err != nil {
		log.Error(err, "Failed to check and fix file")
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

// checkAndFixFile reads the current file content and updates it if it differs from the desired content.
// Have in mind the logic which do the merge of the pull secret is in the globalpullsecret package under the HCCO.
func (r *GlobalPullSecretReconciler) checkAndFixFile(ctx context.Context, globalPullSecret *corev1.Secret) error {
	log := ctrl.LoggerFrom(ctx)

	// Read existing content if file exists
	existingContent, err := os.ReadFile(r.kubeletConfigJsonPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read existing file: %w", err)
	}

	// Get bytes of the global pull secret
	globalPullSecretBytes := globalPullSecret.Data[corev1.DockerConfigJsonKey]

	// If file content is different, write the desired content
	if string(existingContent) != string(globalPullSecretBytes) {
		// Save original content for potential rollback
		originalContent := existingContent

		// Write the new content
		if err := writeFileFunc(r.kubeletConfigJsonPath, globalPullSecretBytes, 0600); err != nil {
			return fmt.Errorf("failed to write file: %w", err)
		}
		log.Info("Pull secret updated", "file", r.kubeletConfigJsonPath)

		// Attempt to restart Kubelet with retries
		maxRetries := 3
		var lastErr error
		for attempt := 1; attempt <= maxRetries; attempt++ {
			if err := signalKubeletToRestartProcess(); err != nil {
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
func signalKubeletToRestartProcess() error {
	log.Println("Signaling Kubelet to reload config")
	conn, err := dbus.New()
	if err != nil {
		return fmt.Errorf("failed to connect to dbus: %w", err)
	}
	defer conn.Close()

	return restartKubelet(conn)
}

func restartKubelet(conn dbusConn) error {
	ch := make(chan string)
	if _, err := conn.RestartUnit(kubeletServiceUnit, dbusRestartUnitMode, ch); err != nil {
		return fmt.Errorf("failed to restart kubelet: %w", err)
	}

	// Wait for the result of the restart
	result := <-ch
	if result != systemdJobDone {
		return fmt.Errorf("failed to restart kubelet, result: %s", result)
	}

	log.Printf("Successfully signaled Kubelet to reload config")
	return nil
}
