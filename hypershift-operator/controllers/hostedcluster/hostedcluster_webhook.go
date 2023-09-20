package hostedcluster

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/support/supportedversion"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type hostedClusterDefaulter struct {
}

type nodePoolDefaulter struct {
	client client.Client
}

func (defaulter *hostedClusterDefaulter) Default(ctx context.Context, obj runtime.Object) error {
	hcluster, ok := obj.(*hyperv1.HostedCluster)
	if !ok {
		return apierrors.NewBadRequest(fmt.Sprintf("expected a HostedCluster but got a %T", obj))
	}

	if hcluster.Spec.Release.Image != "" {
		return nil
	}

	pullSpec, err := supportedversion.LookupLatestSupportedRelease(ctx, hcluster)
	if err != nil {
		return fmt.Errorf("unable to find default release image: %w", err)
	}
	hcluster.Spec.Release.Image = pullSpec

	return nil
}

func (defaulter *nodePoolDefaulter) Default(ctx context.Context, obj runtime.Object) error {
	np, ok := obj.(*hyperv1.NodePool)
	if !ok {
		return apierrors.NewBadRequest(fmt.Sprintf("expected a NodePool but got a %T", obj))
	}

	if np.Spec.Release.Image != "" {
		return nil
	} else if np.Spec.ClusterName == "" {
		return fmt.Errorf("nodePool.Spec.ClusterName is a required field")
	}

	hc := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      np.Spec.ClusterName,
			Namespace: np.Namespace,
		},
	}

	err := defaulter.client.Get(ctx, client.ObjectKeyFromObject(hc), hc)
	if err != nil {
		return fmt.Errorf("error retrieving HostedCluster named [%s], %v", np.Spec.ClusterName, err)
	}
	np.Spec.Release.Image = hc.Spec.Release.Image

	return nil
}

// SetupWebhookWithManager sets up HostedCluster webhooks.
func SetupWebhookWithManager(mgr ctrl.Manager) error {

	err := ctrl.NewWebhookManagedBy(mgr).
		For(&hyperv1.HostedCluster{}).
		WithDefaulter(&hostedClusterDefaulter{}).
		Complete()
	if err != nil {
		return fmt.Errorf("unable to register hostedcluster webhook: %w", err)
	}
	err = ctrl.NewWebhookManagedBy(mgr).
		For(&hyperv1.NodePool{}).
		WithDefaulter(&nodePoolDefaulter{client: mgr.GetClient()}).
		Complete()
	if err != nil {
		return fmt.Errorf("unable to register nodepool webhook: %w", err)
	}
	err = ctrl.NewWebhookManagedBy(mgr).
		For(&hyperv1.HostedControlPlane{}).
		Complete()
	if err != nil {
		return fmt.Errorf("unable to register hostedcontrolplane webhook: %w", err)
	}
	return nil

}
