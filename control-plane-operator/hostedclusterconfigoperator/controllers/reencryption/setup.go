package reencryption

import (
	"context"
	"fmt"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/operator"

	"github.com/openshift/library-go/pkg/operator/encryption/controllers/migrators"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
	kubemigratorclient "sigs.k8s.io/kube-storage-version-migrator/pkg/clients/clientset"
	migrationv1alpha1informer "sigs.k8s.io/kube-storage-version-migrator/pkg/clients/informer"
)

const ControllerName = "reencryption"

func Setup(ctx context.Context, opts *operator.HostedClusterConfigOperatorConfig) error {
	// Create the kube-storage-version-migrator client for the guest cluster.
	svmClient, err := kubemigratorclient.NewForConfig(opts.TargetConfig)
	if err != nil {
		return fmt.Errorf("failed to create storage-version-migrator client: %w", err)
	}

	// Create a discovery client for the guest cluster to resolve preferred versions.
	guestKubeClient, err := kubeclient.NewForConfig(opts.TargetConfig)
	if err != nil {
		return fmt.Errorf("failed to create guest kube client: %w", err)
	}

	svmInformerFactory := migrationv1alpha1informer.NewSharedInformerFactory(svmClient, 10*time.Minute)
	svmInformer := svmInformerFactory.Migration().V1alpha1()

	migrator := migrators.NewKubeStorageVersionMigrator(
		svmClient,
		svmInformer,
		guestKubeClient.Discovery(),
	)

	r := &Reconciler{
		cpClient:     opts.CPCluster.GetClient(),
		guestClient:  opts.Manager.GetClient(),
		hcpName:      opts.HCPName,
		hcpNamespace: opts.Namespace,
		migrator:     migrator,
		now:          time.Now,
	}

	c, err := controller.New(ControllerName, opts.Manager, controller.Options{Reconciler: r})
	if err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}

	// Watch the HostedControlPlane in the CP cluster.
	if err := c.Watch(source.Kind(opts.CPCluster.GetCache(), &hyperv1.HostedControlPlane{},
		&handler.TypedEnqueueRequestForObject[*hyperv1.HostedControlPlane]{})); err != nil {
		return fmt.Errorf("failed to watch HostedControlPlane: %w", err)
	}

	hcpRequest := []reconcile.Request{{NamespacedName: types.NamespacedName{
		Namespace: opts.Namespace,
		Name:      opts.HCPName,
	}}}

	// Watch KAS Deployment in the CP cluster for convergence detection.
	if err := c.Watch(source.Kind[crclient.Object](opts.CPCluster.GetCache(), &appsv1.Deployment{},
		handler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj crclient.Object) []reconcile.Request {
			if obj.GetNamespace() == opts.Namespace && obj.GetName() == manifests.KASDeployment("").Name {
				return hcpRequest
			}
			return nil
		}))); err != nil {
		return fmt.Errorf("failed to watch KAS Deployment: %w", err)
	}

	// Watch the encryption config Secret in the CP cluster namespace.
	if err := c.Watch(source.Kind[crclient.Object](opts.CPCluster.GetCache(), &corev1.Secret{},
		handler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj crclient.Object) []reconcile.Request {
			if obj.GetNamespace() == opts.Namespace && obj.GetName() == manifests.KASSecretEncryptionConfigFile("").Name {
				return hcpRequest
			}
			return nil
		}))); err != nil {
		return fmt.Errorf("failed to watch Secrets: %w", err)
	}

	// Register an event handler on the SVM informer. This call is required
	// because AddEventHandler initializes the migrator's internal cacheSynced
	// function; without it, HasSynced() will panic. The handler itself is a
	// no-op because the controller reconciles on a requeue interval while
	// migrations are in progress.
	if _, err := migrator.AddEventHandler(cache.ResourceEventHandlerFuncs{}); err != nil {
		return fmt.Errorf("failed to add SVM event handler: %w", err)
	}
	svmInformerFactory.Start(ctx.Done())

	return nil
}
