package autoapprover

import (
	"context"

	"k8s.io/client-go/informers"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"openshift.io/hypershift/control-plane-operator/controllers"
	"openshift.io/hypershift/control-plane-operator/operator"
)

func Setup(cfg *operator.ControlPlaneOperatorConfig) error {
	informerFactory := informers.NewSharedInformerFactory(cfg.TargetKubeClient(), controllers.DefaultResync)
	cfg.Manager().Add(manager.RunnableFunc(func(ctx context.Context) error {
		informerFactory.Start(ctx.Done())
		return nil
	}))
	csrs := informerFactory.Certificates().V1beta1().CertificateSigningRequests()
	reconciler := &AutoApprover{
		Lister:     csrs.Lister(),
		KubeClient: cfg.TargetKubeClient(),
		Log:        cfg.Logger().WithName("AutoApprover"),
	}
	c, err := controller.New("auto-approver", cfg.Manager(), controller.Options{Reconciler: reconciler})
	if err != nil {
		return err
	}
	if err := c.Watch(&source.Informer{Informer: csrs.Informer()}, &handler.EnqueueRequestForObject{}); err != nil {
		return err
	}
	return nil
}
