package hostedcluster

import (
	"context"
	"errors"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	platformkubevirt "github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/internal/platform/kubevirt"
	"github.com/openshift/hypershift/pkg/manifests"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (r *HostedClusterReconciler) validateKubevirtCredentials(ctx context.Context, hc *hyperv1.HostedCluster) error {
	if hc.Spec.Platform.Type != hyperv1.KubevirtPlatform {
		return nil
	}
	p := platformkubevirt.New(nil)
	namespace := manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name)
	var err error
	if hc.DeletionTimestamp.IsZero() {
		_, err = p.ValidateCredentials(ctx, r.Client, hc, namespace)
	} else {
		// Non-consuming teardown does not require a source, but unsafe persisted
		// credentials must not remain available to deleting infrastructure clients.
		var result platformkubevirt.DeletionCredentialsResult
		result, err = p.ReconcileDeletionCredentials(ctx, r.Client, hc, namespace)
		if err == nil {
			if result.CredentialError != nil {
				if r.KubevirtInfraClients != nil {
					r.KubevirtInfraClients.Delete(hc.Spec.InfraID)
				}
				// Report rejection, but allow teardown which does not need an infra client.
				return r.patchPlatformCredentialsCondition(ctx, hc, result.CredentialError)
			}
			if result.Published {
				return r.patchPlatformCredentialsCondition(ctx, hc, nil)
			}
			return nil
		}
	}
	if err == nil {
		// Do not mark credentials found until they have actually been published.
		return nil
	}
	if r.KubevirtInfraClients != nil {
		r.KubevirtInfraClients.Delete(hc.Spec.InfraID)
	}
	return errors.Join(err, r.patchPlatformCredentialsCondition(ctx, hc, err))
}

func (r *HostedClusterReconciler) patchPlatformCredentialsCondition(ctx context.Context, hc *hyperv1.HostedCluster, credentialErr error) error {
	return r.patchHostedClusterCondition(ctx, hc, hc.Generation, func(target *hyperv1.HostedCluster) metav1.Condition {
		condition := metav1.Condition{
			Type:               string(hyperv1.PlatformCredentialsFound),
			Status:             metav1.ConditionTrue,
			Reason:             hyperv1.AsExpectedReason,
			ObservedGeneration: target.Generation,
			Message:            "Required platform credentials are found",
		}
		if credentialErr != nil {
			condition.Status = metav1.ConditionFalse
			condition.Reason = hyperv1.PlatformCredentialsNotFoundReason
			condition.Message = credentialErr.Error()
		}
		return condition
	})
}
