package cmca

import (
	"time"

	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/openshift/hypershift/hosted-cluster-config-operator/controllers"
	"github.com/openshift/hypershift/hosted-cluster-config-operator/operator"
)

const (
	ManagedConfigNamespace                 = "openshift-config-managed"
	ControllerManagerAdditionalCAConfigMap = "controller-manager-additional-ca"
	syncInterval                           = 10 * time.Minute
)

func Setup(cfg *operator.HostedClusterConfigOperatorConfig) error {
	if err := setupConfigMapObserver(cfg); err != nil {
		return err
	}
	return nil
}

func setupConfigMapObserver(cfg *operator.HostedClusterConfigOperatorConfig) error {
	informerFactory := cfg.TargetKubeInformersForNamespace(ManagedConfigNamespace)
	configMaps := informerFactory.Core().V1().ConfigMaps()
	reconciler := &ManagedCAObserver{
		InitialCA:    cfg.InitialCA(),
		Client:       cfg.KubeClient(),
		TargetClient: cfg.TargetKubeClient(),
		Namespace:    cfg.Namespace(),
		Log:          cfg.Logger().WithName("ManagedCAObserver"),
	}
	c, err := controller.New("ca-configmap-observer", cfg.Manager(), controller.Options{Reconciler: reconciler})
	if err != nil {
		return err
	}
	if err := c.Watch(&source.Informer{Informer: configMaps.Informer()}, controllers.NamedResourceHandler(RouterCAConfigMap, ServiceCAConfigMap)); err != nil {
		return err
	}
	return nil
}
