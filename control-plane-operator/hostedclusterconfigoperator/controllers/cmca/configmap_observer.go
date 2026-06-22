package cmca

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	resourcemanifests "github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/support/capabilities"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/upsert"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	corev1listers "k8s.io/client-go/listers/core/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-logr/logr"
)

const (
	ServiceCAConfigMap        = "service-ca"
	DefaultIngressCAConfigMap = "default-ingress-cert"
)

type syncDesc struct {
	destination *corev1.ConfigMap
	sourceKey   string
	destKey     string
}

func configMapsToSync(ns string, hcp *hyperv1.HostedControlPlane) map[string]syncDesc {
	m := map[string]syncDesc{
		"service-ca": {
			destination: manifests.ServiceServingCA(ns),
			sourceKey:   "ca-bundle.crt",
			destKey:     "service-ca.crt",
		},
	}
	if capabilities.IsIngressCapabilityEnabled(hcp.Spec.Capabilities) {
		m["default-ingress-cert"] = syncDesc{
			destination: manifests.IngressObservedDefaultIngressCertCA(ns),
			sourceKey:   "ca-bundle.crt",
			destKey:     "ca.crt",
		}
	}
	return m
}

// ManagedCAObserver watches 2 CA configmaps in the target cluster:
// - openshift-managed-config/router-ca
// - openshift-managed-config/service-ca
// It populates a configmap on the management cluster with their content.
// A separate controller uses that content to adjust the configmap for
// the Kube controller manager CA.
type ManagedCAObserver struct {
	createOrUpdate upsert.CreateOrUpdateFN

	// cpClient is a client that allows access to the management cluster
	cpClient client.Client

	// cmLister is a lister of configmaps on the guest cluster
	cmLister corev1listers.ConfigMapLister

	// namespace is the namespace where the control plane of the cluster
	// lives on the management server
	namespace string

	// hcpName is the name of the hostedcontrolplane resource in the
	// control plane namespace
	hcpName string

	// log is the logger for this controller
	log logr.Logger
}

// Reconcile periodically watches configmaps in the guest cluster and syncs them to the control plane side
func (r *ManagedCAObserver) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {

	if req.Namespace != ManagedConfigNamespace {
		return ctrl.Result{}, nil
	}

	hcp := resourcemanifests.HostedControlPlane(r.namespace, r.hcpName)
	if err := r.cpClient.Get(ctx, client.ObjectKeyFromObject(hcp), hcp); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get hosted control plane %s/%s: %w", r.namespace, r.hcpName, err)
	}

	configMaps := configMapsToSync(r.namespace, hcp)
	if _, found := configMaps[req.Name]; !found {
		return ctrl.Result{}, nil
	}

	log := r.log.WithValues("configmap", req.NamespacedName)
	log.Info("syncing configmap")

	ownerRef := config.OwnerRefFrom(hcp)

	sourceCM, err := r.cmLister.ConfigMaps(req.Namespace).Get(req.Name)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to get source ConfigMap %s: %w", req.Name, err)
		}
		sourceCM = nil
	}
	desc := configMaps[req.Name]
	destinationCM := desc.destination
	if sourceCM != nil {
		if _, err := r.createOrUpdate(ctx, r.cpClient, destinationCM, func() error {
			ownerRef.ApplyTo(destinationCM)
			if destinationCM.Data == nil {
				destinationCM.Data = map[string]string{}
			}
			destinationCM.Data[desc.destKey] = sourceCM.Data[desc.sourceKey]
			return nil
		}); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update destination ConfigMap %s: %w", destinationCM.Name, err)
		}
	} else {
		if err := r.cpClient.Delete(ctx, destinationCM); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to delete destination ConfigMap %s: %w", destinationCM.Name, err)
		}
	}
	return ctrl.Result{}, nil
}
