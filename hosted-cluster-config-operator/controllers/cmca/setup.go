package cmca

import (
	"context"

	"k8s.io/client-go/informers"
	kubeclient "k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/openshift/hypershift/hosted-cluster-config-operator/controllers"
	"github.com/openshift/hypershift/hosted-cluster-config-operator/operator"
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
	informerFactory := informers.NewSharedInformerFactoryWithOptions(targetKubeClient, controllers.DefaultResync, informers.WithNamespace(ManagedConfigNamespace))
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
	if err := c.Watch(&source.Informer{Informer: configMaps.Informer()}, controllers.NamedResourceHandler(RouterCAConfigMap, ServiceCAConfigMap)); err != nil {
		return err
	}
	return nil
}
