package controllers

import (
	"context"
	"fmt"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/support/releaseinfo"
	cpoutil "github.com/openshift/hypershift/support/util"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"time"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	DefaultResync = 10 * time.Hour
)

func nameMapper(names []string) handler.MapFunc {
	nameSet := sets.NewString(names...)
	return func(obj client.Object) []reconcile.Request {
		if !nameSet.Has(obj.GetName()) {
			return nil
		}
		return []reconcile.Request{
			{
				NamespacedName: types.NamespacedName{
					Namespace: obj.GetNamespace(),
					Name:      obj.GetName(),
				},
			},
		}
	}
}

func NamedResourceHandler(names ...string) handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(nameMapper(names))
}

// GetExpectedOperatorImage retrieves the expected operator image based off the hostedControlPlane data.
func GetExpectedOperatorImage(ctx context.Context, kubeClient client.Client, releaseProvider releaseinfo.Provider, hostedControlPlane *hyperv1.HostedControlPlane) (string, error) {
	// Determine if active control plane operator image matches the expected release image payload. If it doesn't
	// need to no-op until the operator is recreated and at the matching version
	if _, ok := hostedControlPlane.Annotations[hyperv1.ActiveHypershiftOperatorImage]; !ok {
		return "", fmt.Errorf("active hypershift operator image annotation not present")
	}
	pullSecret := common.PullSecret(hostedControlPlane.Namespace)
	if err := kubeClient.Get(ctx, client.ObjectKeyFromObject(pullSecret), pullSecret); err != nil {
		return "", fmt.Errorf("failed to lookup pull secret: %w", err)
	}
	expectedImage, err := cpoutil.GetHypershiftComponentImage(ctx, hostedControlPlane.Annotations, hostedControlPlane.Spec.ReleaseImage, releaseProvider, hostedControlPlane.Annotations[hyperv1.ActiveHypershiftOperatorImage], pullSecret.Data[corev1.DockerConfigJsonKey])
	if err != nil {
		return "", fmt.Errorf("failed to lookup expected image: %w", err)
	}
	return expectedImage, nil
}
