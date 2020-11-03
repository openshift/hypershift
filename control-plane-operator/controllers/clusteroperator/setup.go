package clusteroperator

import (
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"openshift.io/hypershift/control-plane-operator/operator"
)

func Setup(cfg *operator.ControlPlaneOperatorConfig) error {
	clusterOperators := cfg.TargetConfigInformers().Config().V1().ClusterOperators()
	reconciler := &ControlPlaneClusterOperatorSyncer{
		Versions: cfg.Versions(),
		Client:   cfg.TargetConfigClient(),
		Lister:   clusterOperators.Lister(),
		Log:      cfg.Logger().WithName("ControlPlaneClusterOperatorSyncer"),
	}
	c, err := controller.New("control-plane-operator-syncer", cfg.Manager(), controller.Options{Reconciler: reconciler})
	if err != nil {
		return err
	}
	if err := c.Watch(&source.Informer{Informer: clusterOperators.Informer()}, &handler.EnqueueRequestForObject{}); err != nil {
		return err
	}
	return nil
}
