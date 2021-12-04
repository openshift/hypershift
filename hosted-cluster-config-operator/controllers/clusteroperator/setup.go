package clusteroperator

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/source"

	configclient "github.com/openshift/client-go/config/clientset/versioned"
	configinformers "github.com/openshift/client-go/config/informers/externalversions"
	"github.com/openshift/hypershift/hosted-cluster-config-operator/controllers"
	"github.com/openshift/hypershift/hosted-cluster-config-operator/operator"
)

func Setup(cfg *operator.HostedClusterConfigOperatorConfig) error {
	configClient, err := configclient.NewForConfig(cfg.TargetConfig)
	if err != nil {
		return err
	}
	informerFactory := configinformers.NewSharedInformerFactory(configClient, controllers.DefaultResync)
	cfg.Manager.Add(manager.RunnableFunc(func(ctx context.Context) error {
		informerFactory.Start(ctx.Done())
		return nil
	}))
	clusterOperators := informerFactory.Config().V1().ClusterOperators()
	reconciler := &ControlPlaneClusterOperatorSyncer{
		Versions: cfg.Versions,
		Client:   configClient,
		Lister:   clusterOperators.Lister(),
		Log:      cfg.Logger.WithName("HostedClusterConfigOperatorSyncer"),
	}
	c, err := controller.New("hosted-cluster-config-operator-syncer", cfg.Manager, controller.Options{Reconciler: reconciler})
	if err != nil {
		return err
	}
	if err := c.Watch(&source.Informer{Informer: clusterOperators.Informer()}, &handler.EnqueueRequestForObject{}); err != nil {
		return err
	}
	return nil
}
