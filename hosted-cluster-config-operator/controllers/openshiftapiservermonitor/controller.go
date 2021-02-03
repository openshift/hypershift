package openshiftapiservermonitor

import (
	"context"
	"github.com/go-logr/logr"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeclient "k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	roleBindingRestrictionsCRD   = "rolebindingrestrictions.authorization.openshift.io"
	crdPresentAnnotation         = "ibm-roks.openshift.io/rolebindingrestrictions-present"
	openshiftAPIServerDeployment = "openshift-apiserver"
)

type OpenshiftAPIServerMonitor struct {
	KubeClient kubeclient.Interface
	Namespace  string
	Log        logr.Logger
}

func (m *OpenshiftAPIServerMonitor) Reconcile(_ context.Context, req ctrl.Request) (ctrl.Result, error) {
	if req.Name != roleBindingRestrictionsCRD {
		return ctrl.Result{}, nil
	}
	l := m.Log.WithValues("crd", req.Name)
	l.Info("Start reconciling")
	ctx := context.Background()
	deployment, err := m.KubeClient.AppsV1().Deployments(m.Namespace).Get(ctx, openshiftAPIServerDeployment, metav1.GetOptions{})
	if err != nil {
		return ctrl.Result{}, err
	}
	if value, present := deployment.Spec.Template.ObjectMeta.Annotations[crdPresentAnnotation]; present && value == "true" {
		l.Info("Openshift API server deployment already contains annotation, nothing to do")
		return ctrl.Result{}, nil
	}
	l.Info("Adding annotation to openshift API server deployment")
	if deployment.Spec.Template.ObjectMeta.Annotations == nil {
		deployment.Spec.Template.ObjectMeta.Annotations = map[string]string{}
	}
	deployment.Spec.Template.ObjectMeta.Annotations[crdPresentAnnotation] = "true"
	_, err = m.KubeClient.AppsV1().Deployments(m.Namespace).Update(ctx, deployment, metav1.UpdateOptions{})
	return ctrl.Result{}, err
}
