package cmca

import (
	"context"
	"time"

	"k8s.io/client-go/informers"
	kubeclient "k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/operator"
	"github.com/openshift/hypershift/support/upsert"
)

const (
	ManagedConfigNamespace                 = "openshift-config-managed"
	ControllerManagerAdditionalCAConfigMap = "controller-manager-additional-ca"
)

func Setup(ctx context.Context, cfg *operator.HostedClusterConfigOperatorConfig) error {
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
	err = cfg.Manager.Add(manager.RunnableFunc(func(ctx context.Context) error {
		informerFactory.Start(ctx.Done())
		return nil
	}))
	if err != nil {
		return err
	}

	configMaps := informerFactory.Core().V1().ConfigMaps()
	reconciler := &ManagedCAObserver{
		cpClient:       cfg.CPCluster.GetClient(),
		cmLister:       configMaps.Lister(),
		namespace:      cfg.Namespace,
		hcpName:        cfg.HCPName,
		log:            cfg.Logger.WithName("ManagedCAObserver"),
		createOrUpdate: upsert.New(cfg.EnableCIDebugOutput).CreateOrUpdate,
	}
	c, err := controller.New("ca-configmap-observer", cfg.Manager, controller.Options{Reconciler: reconciler})
	if err != nil {
		return err
	}
	if err := c.Watch(
		&source.Informer{
			Informer: configMaps.Informer(),
			Handler:  &handler.EnqueueRequestForObject{},
		},
	); err != nil {
		return err
	}
	return nil
}
