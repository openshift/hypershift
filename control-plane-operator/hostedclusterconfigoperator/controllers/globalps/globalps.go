package globalps

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	resourceglobalps "github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/globalps"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/support/upsert"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	kubeclient "k8s.io/client-go/kubernetes"

	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	crreconcile "sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	ControllerName = "globalps"
)

var (
	recoverBeforeShutdown = true
)

type Reconciler struct {
	cpClient               crclient.Client
	hcKubeClient           kubeclient.Interface
	kubeSystemSecretClient crclient.Client
	hcUncachedClient       crclient.Client
	hcpNamespace           string
	upsert.CreateOrUpdateProvider
}

func (r *Reconciler) Reconcile(ctx context.Context, req crreconcile.Request) (crreconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("reconciling global pull secret")

	// Reconcile GlobalPullSecret
	hccoImage := os.Getenv("HOSTED_CLUSTER_CONFIG_OPERATOR_IMAGE")
	if hccoImage == "" {
		return ctrl.Result{}, fmt.Errorf("HOSTED_CLUSTER_CONFIG_OPERATOR_IMAGE is not set")
	}

	if err := r.reconcileGlobalPullSecret(ctx, r.hcpNamespace, hccoImage); err != nil {
		if !strings.Contains(err.Error(), "global pull secret syncer signaled to shutdown") {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	return ctrl.Result{}, nil
}

// reconcileGlobalPullSecret reconciles the original pull secret given by HCP and merges it with a new pull secret provided by the user.
// The new pull secret is only stored in the DataPlane side so, it's not exposed in the API. It lives in the kube-system namespace of the DataPlane.
// If that PS exists, the HCCO deploys a DaemonSet which mounts the whole Root FS of the node, and merges the new PS with the original one.
// If the PS doesn't exist, the HCCO doesn't do anything.
func (r *Reconciler) reconcileGlobalPullSecret(ctx context.Context, hcpNamespace string, cpoImage string) error {
	var (
		userProvidedPullSecretBytes []byte
		originalPullSecretBytes     []byte
		globalPullSecretBytes       []byte
		err                         error
	)
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling global pull secret")

	// Get the user provided pull secret
	exists, additionalPullSecret, err := resourceglobalps.AdditionalPullSecretExists(ctx, r.kubeSystemSecretClient)
	if err != nil {
		return fmt.Errorf("failed to check if user provided pull secret exists: %w", err)
	}

	// Reconcile the RBAC for the Global Pull Secret
	if err := resourceglobalps.ReconcileGlobalPullSecretRBAC(ctx, r.hcUncachedClient, r.kubeSystemSecretClient, r.CreateOrUpdate, hcpNamespace); err != nil {
		return fmt.Errorf("failed to reconcile global pull secret RBAC: %w", err)
	}

	if !exists {
		// Early cleanup
		if recoverBeforeShutdown {
			// Recover the original pull secret
			log.Info("Recovering original pull secret")
			originalPullSecret := manifests.PullSecret(hcpNamespace)
			if err := r.cpClient.Get(ctx, crclient.ObjectKeyFromObject(originalPullSecret), originalPullSecret); err != nil {
				return fmt.Errorf("failed to get original pull secret: %w", err)
			}
			originalPullSecretBytes = originalPullSecret.Data[corev1.DockerConfigJsonKey]

			// Create secret in the DataPlane
			secret := manifests.GlobalPullSecret()
			if _, err := r.CreateOrUpdate(ctx, r.kubeSystemSecretClient, secret, func() error {
				secret.Data = map[string][]byte{
					corev1.DockerConfigJsonKey: originalPullSecretBytes,
				}
				return nil
			}); err != nil {
				return fmt.Errorf("failed to create global pull secret: %w", err)
			}
			log.Info("Original pull secret recovered, global pull secret syncer signaled to shutdown")
			recoverBeforeShutdown = false

			return fmt.Errorf("original pull secret recovered, global pull secret syncer signaled to shutdown")
		}

		// Delete the global pull secret and the daemon set
		secret := manifests.GlobalPullSecret()
		if err := r.kubeSystemSecretClient.Delete(ctx, secret); err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to delete global pull secret: %w", err)
			}
		}

		daemonSet := manifests.GlobalPullSecretDaemonSet()
		if err := r.hcUncachedClient.Delete(ctx, daemonSet); err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to delete global pull secret daemon set: %w", err)
			}
		}

		log.Info("Skipping global pull secret reconciliation")
		return nil
	}

	// If the PS doesn't exist, the HCCO doesn't do anything.
	if additionalPullSecret.Data == nil {
		return nil
	}

	if userProvidedPullSecretBytes, err = resourceglobalps.ValidateAdditionalPullSecret(additionalPullSecret); err != nil {
		return fmt.Errorf("failed to validate user provided pull secret: %w", err)
	}

	log.Info("Valid additional pull secret found in the DataPlane, reconciling global pull secret")
	recoverBeforeShutdown = true

	// Get the original pull secret
	originalPullSecret := manifests.PullSecret(hcpNamespace)
	if err := r.cpClient.Get(ctx, crclient.ObjectKeyFromObject(originalPullSecret), originalPullSecret); err != nil {
		return fmt.Errorf("failed to get original pull secret: %w", err)
	}

	// Asumming hcp pull secret is valid
	originalPullSecretBytes = originalPullSecret.Data[corev1.DockerConfigJsonKey]

	// Merge the additional pull secret with the original pull secret
	if globalPullSecretBytes, err = resourceglobalps.MergePullSecrets(ctx, originalPullSecretBytes, userProvidedPullSecretBytes); err != nil {
		return fmt.Errorf("failed to merge pull secrets: %w", err)
	}

	// Create secret in the DataPlane
	secret := manifests.GlobalPullSecret()
	if _, err := r.CreateOrUpdate(ctx, r.kubeSystemSecretClient, secret, func() error {
		secret.Data = map[string][]byte{
			corev1.DockerConfigJsonKey: globalPullSecretBytes,
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to create global pull secret: %w", err)
	}

	// Use the Global Pull Secret to deploy the DaemonSet in the DataPlane.
	daemonSet := manifests.GlobalPullSecretDaemonSet()
	if err := resourceglobalps.ReconcileDaemonSet(ctx, daemonSet, secret.Name, r.hcUncachedClient, r.CreateOrUpdate, cpoImage); err != nil {
		return fmt.Errorf("failed to reconcile global pull secret daemon set: %w", err)
	}

	return nil
}
