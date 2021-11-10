package resources

import (
	"context"
	"fmt"

	imageregistryv1 "github.com/openshift/api/imageregistry/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/hosted-cluster-config-operator/controllers/resources/manifests"
	"github.com/openshift/hypershift/hosted-cluster-config-operator/controllers/resources/registry"
	"github.com/openshift/hypershift/hosted-cluster-config-operator/operator"
	"github.com/openshift/hypershift/support/upsert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const ControllerName = "resources"

type reconciler struct {
	client crclient.Client
	upsert.CreateOrUpdateProvider
	platformType    hyperv1.PlatformType
	clusterSignerCA string
}

// eventHandler is the handler used throughout. As this controller reconciles all kind of different resources
// it uses an empty request but always reconciles everything.
func eventHandler() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(
		func(crclient.Object) []reconcile.Request {
			return []reconcile.Request{{}}
		})
}

func Setup(opts *operator.HostedClusterConfigOperatorConfig) error {
	if err := imageregistryv1.AddToScheme(opts.Manager().GetScheme()); err != nil {
		return fmt.Errorf("failed to add to scheme: %w", err)
	}
	c, err := controller.New(ControllerName, opts.Manager(), controller.Options{Reconciler: &reconciler{
		client:                 opts.Manager().GetClient(),
		CreateOrUpdateProvider: opts.TargetCreateOrUpdateProvider,
		platformType:           opts.PlatformType,
		clusterSignerCA:        opts.ClusterSignerCA(),
	}})
	if err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}
	resourcesToWatch := []crclient.Object{
		&imageregistryv1.Config{},
		&corev1.ConfigMap{},
		&corev1.Secret{},
	}
	for _, r := range resourcesToWatch {
		if err := c.Watch(&source.Kind{Type: r}, eventHandler()); err != nil {
			return fmt.Errorf("failed to watch %T: %w", r, err)
		}
	}

	return nil
}

func (r *reconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	return ctrl.Result{}, r.reconcile(ctx)
}

func (r *reconciler) reconcile(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling")

	registryConfig := manifests.Registry()
	if _, err := r.CreateOrUpdate(ctx, r.client, registryConfig, func() error {
		registry.ReconcileRegistryConfig(registryConfig, r.platformType == hyperv1.NonePlatform)
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile imageregistry config: %w", err)
	}

	kubeControlPlaneSignerSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "openshift-kube-apiserver-operator",
			Name:      "kube-control-plane-signer",
		},
	}
	if _, err := r.CreateOrUpdate(ctx, r.client, kubeControlPlaneSignerSecret, func() error {
		kubeControlPlaneSignerSecret.Data = map[string][]byte{corev1.TLSCertKey: []byte(r.clusterSignerCA)}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile the %s Secret: %w", crclient.ObjectKeyFromObject(kubeControlPlaneSignerSecret), err)
	}

	kubeletServingCAConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "openshift-config-managed",
			Name:      "kubelet-serving-ca",
		},
	}
	if _, err := r.CreateOrUpdate(ctx, r.client, kubeletServingCAConfigMap, func() error {
		kubeletServingCAConfigMap.Data = map[string]string{"ca-bundle.crt": r.clusterSignerCA}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile the %s ConfigMap: %w", crclient.ObjectKeyFromObject(kubeletServingCAConfigMap), err)
	}

	return nil
}
