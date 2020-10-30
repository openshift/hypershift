package infrastatus

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"

	configv1 "github.com/openshift/api/config/v1"

	"openshift.io/hypershift/control-plane-operator/operator"
)

const (
	infrastructureConfigMap = "user-manifest-cluster-infrastructure-02-config"
)

var (
	configScheme = runtime.NewScheme()
	configCodecs = serializer.NewCodecFactory(configScheme)
)

func init() {
	if err := configv1.AddToScheme(configScheme); err != nil {
		panic(err)
	}
}

func Setup(cfg *operator.ControlPlaneOperatorConfig) error {
	infrastructures := cfg.TargetConfigInformers().Config().V1().Infrastructures()
	sourceInfraConfigMap, err := cfg.KubeClient().CoreV1().ConfigMaps(cfg.Namespace()).Get(context.TODO(), infrastructureConfigMap, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("cannot get infrastructure configmap: %v", err)
	}
	infrastructureRaw := sourceInfraConfigMap.Data["data"]
	infrastructureObj, err := runtime.Decode(configCodecs.UniversalDecoder(configv1.SchemeGroupVersion), []byte(infrastructureRaw))
	if err != nil {
		return fmt.Errorf("cannot decode source infrastructure: %v", err)
	}
	sourceInfrastructure := infrastructureObj.(*configv1.Infrastructure)

	reconciler := &InfraStatusReconciler{
		Source:     sourceInfrastructure,
		Client:     cfg.TargetConfigClient(),
		KubeClient: cfg.TargetKubeClient(),
		Lister:     infrastructures.Lister(),
		Log:        cfg.Logger().WithName("InfraStatus"),
	}
	c, err := controller.New("infra-status", cfg.Manager(), controller.Options{Reconciler: reconciler})
	if err != nil {
		return err
	}
	if err := c.Watch(&source.Informer{Informer: infrastructures.Informer()}, &handler.EnqueueRequestForObject{}); err != nil {
		return err
	}
	return nil
}
