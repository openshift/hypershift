package integration

import (
	"context"
	"fmt"

	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/kubeadminpassword"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	hccomanifests "github.com/openshift/hypershift/pkg/manifests/hcco"
	pkgoauth "github.com/openshift/hypershift/pkg/oauth"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// reconcileKubeadminPasswordHashSecret projects the hash of the control-plane kubeadmin password
// into the hosted cluster and records it on the control-plane secret, the only control-plane write.
func reconcileKubeadminPasswordHashSecret(ctx context.Context, hosted HostedCluster, controlPlane ControlPlane, namespace string) error {
	kubeadminPasswordSecret := hccomanifests.KubeadminPasswordSecret(namespace)
	if err := controlPlane.get(ctx, client.ObjectKeyFromObject(kubeadminPasswordSecret), kubeadminPasswordSecret); err != nil {
		if apierrors.IsNotFound(err) {
			// kubeAdminPasswordHash should not exist when a user specifies an explicit oauth config
			// delete kubeAdminPasswordHash if it exist
			return deleteKubeadminPasswordHashSecret(ctx, hosted)
		}
		return fmt.Errorf("failed to get kubeadmin password secret: %w", err)
	}

	kubeadminPasswordHashSecret := manifests.KubeadminPasswordHashSecret()
	if _, err := hosted.upsert(ctx, kubeadminPasswordHashSecret, func() error {
		return kubeadminpassword.ReconcileKubeadminPasswordHashSecret(kubeadminPasswordHashSecret, kubeadminPasswordSecret)
	}); err != nil {
		return err
	}

	if _, err := controlPlane.upsert(ctx, kubeadminPasswordSecret, func() error {
		if kubeadminPasswordSecret.Annotations == nil {
			kubeadminPasswordSecret.Annotations = map[string]string{}
		}
		kubeadminPasswordSecret.Annotations[pkgoauth.KubeadminSecretHashAnnotation] = string(kubeadminPasswordHashSecret.Data["kubeadmin"])
		return nil
	}); err != nil {
		return fmt.Errorf("failed to annotate kubeadmin-password secret in hcp namespace: %w", err)
	}

	return nil
}

func deleteKubeadminPasswordHashSecret(ctx context.Context, hosted HostedCluster) error {
	kubeadminPasswordHashSecret := manifests.KubeadminPasswordHashSecret()
	if err := hosted.get(ctx, client.ObjectKeyFromObject(kubeadminPasswordHashSecret), kubeadminPasswordHashSecret); err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
	} else {
		if err := hosted.delete(ctx, kubeadminPasswordHashSecret); err != nil {
			return err
		}
	}

	return nil
}
