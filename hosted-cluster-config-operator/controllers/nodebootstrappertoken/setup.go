package nodebootstrappertoken

import (
	"github.com/openshift/hypershift/api"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/openshift/hypershift/hosted-cluster-config-operator/controllers"
	"github.com/openshift/hypershift/hosted-cluster-config-operator/operator"
)

const (
	nodeBootstrapperTokenNamespace     = "openshift-infra"
	nodeBootstrapperServiceAccountName = "node-bootstrapper"
)

func Setup(cfg *operator.HostedClusterConfigOperatorConfig) error {
	if err := setupNodeBootstrapperTokenObserver(cfg); err != nil {
		return err
	}
	return nil
}

func setupNodeBootstrapperTokenObserver(cfg *operator.HostedClusterConfigOperatorConfig) error {
	informerFactory := cfg.TargetKubeInformersForNamespace(nodeBootstrapperTokenNamespace)
	serviceAccounts := informerFactory.Core().V1().ServiceAccounts()
	crdClient, err := client.New(cfg.Config(), client.Options{Scheme: api.Scheme})
	if err != nil {
		return err
	}
	reconciler := &NodeBootstrapperTokenObserver{
		Client:       crdClient,
		TargetClient: cfg.TargetKubeClient(),
		Namespace:    cfg.Namespace(),
		Log:          cfg.Logger().WithName("NodeBootstrapperTokenObserver"),
	}
	c, err := controller.New("node-bootstrapper-token-observer", cfg.Manager(), controller.Options{Reconciler: reconciler})
	if err != nil {
		return err
	}
	if err := c.Watch(&source.Informer{Informer: serviceAccounts.Informer()}, controllers.NamedResourceHandler(nodeBootstrapperServiceAccountName)); err != nil {
		return err
	}
	return nil
}
