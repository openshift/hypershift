package ignitionserver

import (
	"fmt"
	"net"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"
	"github.com/openshift/hypershift/support/certs"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	routev1 "github.com/openshift/api/route/v1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Reconcile a root CA for ignition serving certificates.
// We only create this and don't update it for now.
func adaptCACertSecret(cpContext component.WorkloadContext, secret *corev1.Secret) error {
	secret.Type = corev1.SecretTypeTLS
	return certs.ReconcileSelfSignedCA(secret, "ignition-root-ca", "openshift", func(o *certs.CAOpts) {
		o.CASignerCertMapKey = corev1.TLSCertKey
		o.CASignerKeyMapKey = corev1.TLSPrivateKeyKey
	})
}

// Reconcile an ignition serving certificate issued by the generated root CA.
// We only create this and don't update it for now.
func adaptServingCertSecret(cpContext component.WorkloadContext, secret *corev1.Secret) error {
	caCertSecret := ignitionserver.IgnitionCACertSecret(cpContext.HCP.Namespace)
	if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(caCertSecret), caCertSecret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get ignition ca-cert secret: %v", err)
	}

	serviceStrategy := util.ServicePublishingStrategyByTypeForHCP(cpContext.HCP, hyperv1.Ignition)
	if serviceStrategy == nil {
		return fmt.Errorf("ignition service strategy not specified")
	}

	var ignitionServerAddress string
	switch serviceStrategy.Type {
	case hyperv1.Route:
		ignitionServerRoute := &routev1.Route{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: cpContext.HCP.Namespace,
				Name:      ComponentName,
			},
		}

		if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(ignitionServerRoute), ignitionServerRoute); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("failed to get ignition route: %v", err)
		}
		// The route must be admitted and assigned a host before we can generate certs
		if len(ignitionServerRoute.Status.Ingress) == 0 || len(ignitionServerRoute.Status.Ingress[0].Host) == 0 {
			return nil
		}
		ignitionServerAddress = ignitionServerRoute.Status.Ingress[0].Host
	case hyperv1.NodePort:
		if serviceStrategy.NodePort == nil {
			return fmt.Errorf("nodeport metadata not specified for ignition service")
		}
		ignitionServerAddress = serviceStrategy.NodePort.Address
	default:
		return fmt.Errorf("unknown service strategy type for ignition service: %s", serviceStrategy.Type)
	}

	var dnsNames, ipAddresses []string
	numericIP := net.ParseIP(ignitionServerAddress)
	if numericIP == nil {
		dnsNames = []string{ignitionServerAddress}
	} else {
		ipAddresses = []string{ignitionServerAddress}
	}

	secret.Type = corev1.SecretTypeTLS
	return certs.ReconcileSignedCert(
		secret,
		caCertSecret,
		"ignition-server",
		[]string{"openshift"},
		nil,
		corev1.TLSCertKey,
		corev1.TLSPrivateKeyKey,
		"",
		dnsNames,
		ipAddresses,
		func(o *certs.CAOpts) {
			o.CASignerCertMapKey = corev1.TLSCertKey
			o.CASignerKeyMapKey = corev1.TLSPrivateKeyKey
		},
	)
}
