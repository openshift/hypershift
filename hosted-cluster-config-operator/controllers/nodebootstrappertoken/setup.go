package nodebootstrappertoken

import (
	"time"

	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/openshift/hypershift/hosted-cluster-config-operator/controllers"
	"github.com/openshift/hypershift/hosted-cluster-config-operator/operator"
)

const (
	NodeBootstrapperTokenNamespace     = "openshift-infra"
	NodeBootstrapperServiceAccountName = "node-bootstrapper"
	syncInterval                       = 10 * time.Minute
)

func Setup(cfg *operator.HostedClusterConfigOperatorConfig) error {
	if err := setupNodeBootstrapperTokenObserver(cfg); err != nil {
		return err
	}
	return nil
}

func setupNodeBootstrapperTokenObserver(cfg *operator.HostedClusterConfigOperatorConfig) error {
	informerFactory := cfg.TargetKubeInformersForNamespace(NodeBootstrapperTokenNamespace)
	serviceAccounts := informerFactory.Core().V1().ServiceAccounts()
	reconciler := &NodeBootstrapperTokenObserver{
		Client:       cfg.KubeClient(),
		TargetClient: cfg.TargetKubeClient(),
		Namespace:    cfg.Namespace(),
		Log:          cfg.Logger().WithName("NodeBootstrapperTokenObserver"),
	}
	c, err := controller.New("node-bootstrapper-token-observer", cfg.Manager(), controller.Options{Reconciler: reconciler})
	if err != nil {
		return err
	}
	if err := c.Watch(&source.Informer{Informer: serviceAccounts.Informer()}, controllers.NamedResourceHandler(NodeBootstrapperServiceAccountName)); err != nil {
		return err
	}
	return nil
}
