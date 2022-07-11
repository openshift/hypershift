package cmca

import (
	"context"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/informers"
	kubeclient "k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/operator"
)

const (
	ManagedConfigNamespace                 = "openshift-config-managed"
	ControllerManagerAdditionalCAConfigMap = "controller-manager-additional-ca"
)

func Setup(cfg *operator.HostedClusterConfigOperatorConfig) error {
	if err := setupConfigMapObserver(cfg); err != nil {
		return err
	}
	return nil
}

func setupConfigMapObserver(cfg *operator.HostedClusterConfigOperatorConfig) error {
	targetKubeClient, err := kubeclient.NewForConfig(cfg.TargetConfig)
	if err != nil {
		return err
	}
	informerFactory := informers.NewSharedInformerFactoryWithOptions(targetKubeClient, 10*time.Hour, informers.WithNamespace(ManagedConfigNamespace))
	cfg.Manager.Add(manager.RunnableFunc(func(ctx context.Context) error {
		informerFactory.Start(ctx.Done())
		return nil
	}))

	configMaps := informerFactory.Core().V1().ConfigMaps()
	reconciler := &ManagedCAObserver{
		InitialCA:    cfg.InitialCA,
		Client:       cfg.KubeClient(),
		TargetClient: targetKubeClient,
		Namespace:    cfg.Namespace,
		Log:          cfg.Logger.WithName("ManagedCAObserver"),
	}
	c, err := controller.New("ca-configmap-observer", cfg.Manager, controller.Options{Reconciler: reconciler})
	if err != nil {
		return err
	}
	if err := c.Watch(
		&source.Informer{Informer: configMaps.Informer()},
		&handler.EnqueueRequestForObject{},
		predicateForNames(RouterCAConfigMap, ServiceCAConfigMap),
	); err != nil {
		return err
	}
	if err := c.Watch(
		source.NewKindWithCache(&appsv1.Deployment{}, cfg.CPCluster.GetCache()),
		handler.EnqueueRequestsFromMapFunc(func(client.Object) []reconcile.Request {
			return []reconcile.Request{
				{NamespacedName: types.NamespacedName{Namespace: ManagedConfigNamespace, Name: RouterCAConfigMap}},
				{NamespacedName: types.NamespacedName{Namespace: ManagedConfigNamespace, Name: ServiceCAConfigMap}},
			}
		}),
		predicateForNames(reconciler.managedDeployments()...),
	); err != nil {
		return err
	}
	return nil
}

func predicateForNames(names ...string) predicate.Predicate {
	set := sets.NewString(names...)
	return predicate.NewPredicateFuncs(func(o client.Object) bool {
		return set.Has(o.GetName())
	})
}
