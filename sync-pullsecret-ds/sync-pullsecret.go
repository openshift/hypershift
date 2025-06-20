package syncpullsecret

// sync-pullsecret-ds syncs the pull secret from the user provided pull secret in DataPlane and appends it to the HostedCluster PullSecret
// to be deployed in the nodes of the HostedCluster.

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	cmdutil "github.com/openshift/hypershift/cmd/util"

	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"
)

// syncGlobalPullSecretOptions contains the configuration options for the sync-pullsecret command
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
)

// NewRunCommand creates a new cobra.Command for the sync-pullsecret command
func NewRunCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync-global-pullsecret",
		Short: "Syncs a mixture between the user provided pull secret in DataPlane and the HostedCluster PullSecret to be deployed in the nodes of the HostedCluster",
		Long:  `Syncs a mixture between the user provided pull secret in DataPlane and the HostedCluster PullSecret to be deployed in the nodes of the HostedCluster. The resulting pull secret is deployed in a DaemonSet in the DataPlane that updates the kubelet.config.json file with the new pull secret. If there are conflicting entries in the resulting global pull secret, the user provided pull secret will prevail.`,
	}

	opts := syncGlobalPullSecretOptions{
		kubeletConfigJsonPath: defaultKubeletConfigJsonPath,
		globalPSSecretName:    defaultGlobalPSSecretName,
		checkInterval:         defaultCheckInterval,
	}
	cmd.Flags().DurationVar(&opts.checkInterval, "check-interval", opts.checkInterval, "The interval at which the file is checked for changes.")
	cmd.Run = func(cmd *cobra.Command, args []string) {
		ctx, cancel := context.WithCancel(context.Background())
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT)
		go func() {
			<-sigs
			cancel()
		}()

		if err := opts.run(ctx); err != nil {
			log.Fatal(err)
			os.Exit(1)
		}
	}

	return cmd
}

// run executes the main logic of the sync-pullsecret command
func (o *syncGlobalPullSecretOptions) run(ctx context.Context) error {
	var err error

	c, err := cmdutil.GetClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	// Create a new watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create file watcher: %w", err)
	}
	defer watcher.Close()

	// Start watching the file
	if err := watcher.Add(o.kubeletConfigJsonPath); err != nil {
		return fmt.Errorf("failed to add file to watcher: %w", err)
	}

	// Initial check and fix
	if err := o.checkAndFixFile(ctx, c); err != nil {
		log.Printf("Initial file check failed: %v", err)
	}

	// Periodic check ticker
	ticker := time.NewTicker(o.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case event := <-watcher.Events:
			if event.Has(fsnotify.Write) {
				log.Printf("File change detected: %s", event.String())
				if err := o.checkAndFixFile(ctx, c); err != nil {
					log.Printf("Failed to fix file: %v", err)
				}
			}
		case err := <-watcher.Errors:
			log.Printf("Watcher error: %v", err)
		case <-ticker.C:
			if err := o.checkAndFixFile(ctx, c); err != nil {
				log.Printf("Periodic check failed: %v", err)
			}
		case <-ctx.Done():
			return nil
		}
	}
}

// checkAndFixFile reads the current file content and updates it if it differs from the desired content.
// Have in mind the logic which do the merge of the pull secret is in the globalpullsecret package under the HCCO.
func (o *syncGlobalPullSecretOptions) checkAndFixFile(ctx context.Context, c client.Client) error {
	// TODO (jparrill):
	// 	- Validate the Kubelet flags does not contain a different path for the pull secret.

	// Read existing content if file exists
	existingContent, err := os.ReadFile(o.kubeletConfigJsonPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read existing file: %w", err)
	}

	// Get the global pull secret from the DataPlane
	globalPullSecret := &corev1.Secret{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: defaultGlobalPullSecretNamespace, Name: o.globalPSSecretName}, globalPullSecret); err != nil {
		return fmt.Errorf("failed to get global pull secret: %w", err)
	}

	// Get bytes of the global pull secret
	globalPullSecretBytes := globalPullSecret.Data[corev1.DockerConfigJsonKey]

	// If file content is different, write the desired content
	if string(existingContent) != string(globalPullSecretBytes) {
		if err := os.WriteFile(o.kubeletConfigJsonPath, globalPullSecretBytes, 0600); err != nil {
			return fmt.Errorf("failed to write file: %w", err)
		}
		log.Printf("Updated %s with desired content", o.kubeletConfigJsonPath)
		// TODO (jparrill):
		//  - Signal Kubelet to reload the config.
	}

	return nil
}
