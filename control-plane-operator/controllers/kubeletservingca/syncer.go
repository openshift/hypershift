package kubeletservingca

import (
	"context"
	"time"

	"github.com/go-logr/logr"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeclient "k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
)

// syncInterval is the amount of time to use between checks
var syncInterval = 20 * time.Minute

// controlPlaneOperatorConfig is the name of the source configmap on the management cluster
const controlPlaneOperatorConfig = "control-plane-operator"

type KubeletServingCASyncer struct {
	TargetClient kubeclient.Interface
	Log          logr.Logger
	InitialCA    string
}

func (s *KubeletServingCASyncer) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	targetConfigMap, err := s.TargetClient.CoreV1().ConfigMaps("openshift-config-managed").Get(ctx, "kubelet-serving-ca", metav1.GetOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return result(err)
	}
	expectedConfigMap := s.expectedConfigMap()
	if err != nil {
		s.Log.Info("target configmap not found, creating it")
		_, err = s.TargetClient.CoreV1().ConfigMaps("openshift-config-managed").Create(ctx, expectedConfigMap, metav1.CreateOptions{})
		return result(err)
	}
	if targetConfigMap.Data["ca-bundle.crt"] != expectedConfigMap.Data["ca-bundle.crt"] {
		targetConfigMap.Data["ca-bundle.crt"] = expectedConfigMap.Data["ca-bundle.crt"]
		_, err = s.TargetClient.CoreV1().ConfigMaps("openshift-config-managed").Update(ctx, targetConfigMap, metav1.UpdateOptions{})
		return result(err)
	}
	return result(nil)
}

func result(err error) (ctrl.Result, error) {
	if err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: syncInterval}, nil
}

func (s *KubeletServingCASyncer) expectedConfigMap() *corev1.ConfigMap {
	cm := &corev1.ConfigMap{}
	cm.Name = "kubelet-serving-ca"
	cm.Namespace = "openshift-config-managed"
	cm.Data = map[string]string{
		"ca-bundle.crt": s.InitialCA,
	}
	return cm
}
