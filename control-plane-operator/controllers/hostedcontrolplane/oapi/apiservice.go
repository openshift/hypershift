package oapi

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/pki"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/util"
)

func ReconcileWorkerAPIService(cm *corev1.ConfigMap, ownerRef config.OwnerRef, svc *corev1.Service, ca *corev1.Secret, group string) error {
	apiService := manifests.OpenShiftAPIServerAPIService(group)
	if err := reconcileClusterAPIService(apiService, svc, ca, group); err != nil {
		return err
	}
	ownerRef.ApplyTo(cm)
	util.ReconcileWorkerManifest(cm, apiService)
	return nil
}

func reconcileClusterAPIService(apiService *apiregistrationv1.APIService, svc *corev1.Service, ca *corev1.Secret, group string) error {
	groupName := fmt.Sprintf("%s.openshift.io", group)
	caBundle := ca.Data[pki.CASignerCertMapKey]
	apiService.Spec = apiregistrationv1.APIServiceSpec{
		CABundle:             caBundle,
		Group:                groupName,
		Version:              "v1",
		GroupPriorityMinimum: 9900,
		Service: &apiregistrationv1.ServiceReference{
			Name:      svc.Name,
			Namespace: svc.Namespace,
		},
		VersionPriority: 15,
	}
	return nil
}
