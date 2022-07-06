package hostedcluster

import (
	"context"
	"fmt"
	"reflect"

	corev1 "k8s.io/api/core/v1"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

// Webhook implements a validating webhook for HostedCluster.
type Webhook struct{}

// SetupWebhookWithManager sets up HostedCluster webhooks.
func SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(&hyperv1.HostedCluster{}).
		WithValidator(&Webhook{}).
		Complete()
}

var _ webhook.CustomValidator = &Webhook{}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type.
func (webhook *Webhook) ValidateCreate(ctx context.Context, obj runtime.Object) error {
	return nil
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type.
func (webhook *Webhook) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) error {
	newHC, ok := newObj.(*hyperv1.HostedCluster)
	if !ok {
		return apierrors.NewBadRequest(fmt.Sprintf("expected a HostedCluster but got a %T", newObj))
	}

	oldHC, ok := oldObj.(*hyperv1.HostedCluster)
	if !ok {
		return apierrors.NewBadRequest(fmt.Sprintf("expected a HostedCluster but got a %T", oldObj))
	}

	return validateHostedClusterUpdate(newHC, oldHC)
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type.
func (webhook *Webhook) ValidateDelete(_ context.Context, obj runtime.Object) error {
	return nil
}

func filterMutableHostedClusterSpecFields(spec *hyperv1.HostedClusterSpec) {
	spec.Release.Image = ""
	spec.ClusterID = ""
	spec.InfraID = ""
	spec.Configuration = nil
	spec.AdditionalTrustBundle = nil
	spec.SecretEncryption = nil
	spec.PausedUntil = nil
	for i, svc := range spec.Services {
		if svc.Type == hyperv1.NodePort && svc.NodePort != nil {
			spec.Services[i].NodePort.Address = ""
			spec.Services[i].NodePort.Port = 0
		}
	}
	if spec.Platform.Type == hyperv1.AWSPlatform && spec.Platform.AWS != nil {
		spec.Platform.AWS.ResourceTags = nil
		// This is to enable reconcileDeprecatedAWSRoles.
		spec.Platform.AWS.RolesRef = hyperv1.AWSRolesRef{}
		spec.Platform.AWS.Roles = []hyperv1.AWSRoleCredentials{}
		spec.Platform.AWS.NodePoolManagementCreds = corev1.LocalObjectReference{}
		spec.Platform.AWS.ControlPlaneOperatorCreds = corev1.LocalObjectReference{}
		spec.Platform.AWS.KubeCloudControllerCreds = corev1.LocalObjectReference{}
	}
}

func validateHostedClusterUpdate(new *hyperv1.HostedCluster, old *hyperv1.HostedCluster) error {
	filterMutableHostedClusterSpecFields(&new.Spec)
	filterMutableHostedClusterSpecFields(&old.Spec)

	// We default the port in Azure management cluster, so we allow setting it from being unset, but no updates.
	if new.Spec.Networking.APIServer != nil && (old.Spec.Networking.APIServer == nil || old.Spec.Networking.APIServer.Port == nil) {
		if old.Spec.Networking.APIServer == nil {
			old.Spec.Networking.APIServer = &hyperv1.APIServerNetworking{}
		}
		old.Spec.Networking.APIServer.Port = new.Spec.Networking.APIServer.Port
	}
	// TODO (alberto): use equality semantic
	if !reflect.DeepEqual(new.Spec, old.Spec) {
		// TODO (alberto): leverage k8s.io/apimachinery/pkg/api/errors and k8s.io/apimachinery/pkg/util/validation/field
		// to return granular and meaningful output per field.
		return fmt.Errorf("attempted change to immutable field(s)")
	}

	return nil
}
