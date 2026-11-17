package configuration

import (
	"context"
	"fmt"

	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/support/globalconfig"

	corev1 "k8s.io/api/core/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func ReconcileProxyTrustedCA(ctx context.Context, hosted HostedCluster, controlPlane ControlPlane, params ProxyParams) error {
	log := ctrl.LoggerFrom(ctx)

	configMapRef := params.TrustedCA

	proxy := globalconfig.ProxyConfig()
	if err := hosted.get(ctx, client.ObjectKeyFromObject(proxy), proxy); err != nil {
		return err
	}

	currentConfigMapRef := proxy.Spec.TrustedCA.Name
	if currentConfigMapRef != "" && currentConfigMapRef != configMapRef {
		cm := &corev1.ConfigMap{}
		cm.Name = currentConfigMapRef

		cm.Namespace = params.Namespace
		if err := controlPlane.delete(ctx, cm); err != nil {
			log.Error(err, "failed to delete configmap", "name", cm.Name, "namespace", cm.Namespace)
		}

		cm.Namespace = manifests.ProxyTrustedCAConfigMap("").Namespace
		if err := hosted.delete(ctx, cm); err != nil {
			log.Error(err, "failed to delete configmap in hosted cluster", "name", cm.Name, "namespace", cm.Namespace)
		}
	}

	if configMapRef == "" {
		return nil
	}

	sourceCM := &corev1.ConfigMap{}
	if err := controlPlane.get(ctx, client.ObjectKey{Namespace: params.Namespace, Name: configMapRef}, sourceCM); err != nil {
		return fmt.Errorf("failed to get referenced TrustedCA configmap %s/%s: %w", params.Namespace, configMapRef, err)
	}

	destCM := manifests.ProxyTrustedCAConfigMap(sourceCM.Name)
	if _, err := hosted.upsert(ctx, destCM, func() error {
		destCM.Data = sourceCM.Data
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile referenced TrustedCA config map %s/%s: %w", destCM.Namespace, destCM.Name, err)
	}

	return nil
}

func ReconcileProxyCABundle(ctx context.Context, hosted HostedCluster, controlPlane ControlPlane, params ProxyParams) error {
	proxyCADestination := manifests.OpenShiftUserCABundle()
	if params.TrustedCA != "" {
		cpProxyCA := &corev1.ConfigMap{}
		cpProxyCA.Namespace = params.Namespace
		cpProxyCA.Name = params.TrustedCA
		if err := controlPlane.get(ctx, client.ObjectKeyFromObject(cpProxyCA), cpProxyCA); err != nil {
			return fmt.Errorf("cannot get proxy CA bundle ConfigMap: %w", err)
		}
		if _, err := hosted.upsert(ctx, proxyCADestination, func() error {
			proxyCADestination.Data = cpProxyCA.Data
			return nil
		}); err != nil {
			return fmt.Errorf("failed to reconcile the proxy CA bundle ConfigMap: %w", err)
		}
	} else {
		if _, err := hosted.deleteIfNeeded(ctx, proxyCADestination); err != nil {
			return err
		}
	}
	return nil
}
