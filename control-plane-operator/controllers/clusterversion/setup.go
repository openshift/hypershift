package clusterversion

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/source"

	configclient "github.com/openshift/client-go/config/clientset/versioned"
	configinformers "github.com/openshift/client-go/config/informers/externalversions"

	"openshift.io/hypershift/control-plane-operator/controllers"
	"openshift.io/hypershift/control-plane-operator/operator"
)

func Setup(cfg *operator.ControlPlaneOperatorConfig) error {
	openshiftClient, err := configclient.NewForConfig(cfg.TargetConfig())
	if err != nil {
		return err
	}
	informerFactory := configinformers.NewSharedInformerFactory(openshiftClient, controllers.DefaultResync)
	cfg.Manager().Add(manager.RunnableFunc(func(ctx context.Context) error {
		informerFactory.Start(ctx.Done())
		return nil
	}))
	clusterVersions := informerFactory.Config().V1().ClusterVersions()
	reconciler := &ClusterVersionReconciler{
		Client: openshiftClient,
		Lister: clusterVersions.Lister(),
		Log:    cfg.Logger().WithName("ClusterVersion"),
	}
	c, err := controller.New("cluster-version", cfg.Manager(), controller.Options{Reconciler: reconciler})
	if err != nil {
		return err
	}
	if err := c.Watch(&source.Informer{Informer: clusterVersions.Informer()}, &handler.EnqueueRequestForObject{}); err != nil {
		return err
	}
	return nil
}
