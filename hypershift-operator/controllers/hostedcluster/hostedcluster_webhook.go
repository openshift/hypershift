package hostedcluster

import (
	"context"
	"fmt"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/kubevirtexternalinfra"
	"github.com/openshift/hypershift/support/supportedversion"
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
		WithValidator(newHostedClusterValidator(mgr.GetClient())).
		Complete()
	if err != nil {
		return fmt.Errorf("unable to register hostedcluster webhook: %w", err)
	}
	err = ctrl.NewWebhookManagedBy(mgr).
		For(&hyperv1.NodePool{}).
		WithDefaulter(&nodePoolDefaulter{client: mgr.GetClient()}).
		WithValidator(newNodePoolValidator(mgr.GetClient())).
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

var kvValidator = kubevirtClusterValidator{
	clientMap: kubevirtexternalinfra.NewKubevirtInfraClientMap(),
}

var _ admission.CustomValidator = (*hostedClusterValidator)(nil)

type hostedClusterValidator struct {
	client client.Client
}

func newHostedClusterValidator(client client.Client) *hostedClusterValidator {
	return &hostedClusterValidator{
		client: client,
	}
}

func (v hostedClusterValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	hc, ok := obj.(*hyperv1.HostedCluster)
	if !ok {
		return nil, fmt.Errorf("wrong type %T for validation, instead of HostedCluster", obj)
	}

	switch hc.Spec.Platform.Type {
	case hyperv1.KubevirtPlatform:
		return v.validateCreateKubevirtHostedCluster(ctx, hc)
	default:
		return nil, nil // no validation needed
	}
}

func (v hostedClusterValidator) ValidateUpdate(_ context.Context, _, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

func (v hostedClusterValidator) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

func (v hostedClusterValidator) validateCreateKubevirtHostedCluster(ctx context.Context, hc *hyperv1.HostedCluster) (admission.Warnings, error) {
	if hc.Spec.Platform.Kubevirt == nil {
		return nil, fmt.Errorf("the spec.platform.kubevirt field is missing in the HostedCluster resource")
	}

	return kvValidator.validate(ctx, v.client, hc)
}

type nodePoolValidator struct {
	client client.Client
}

func newNodePoolValidator(client client.Client) *nodePoolValidator {
	return &nodePoolValidator{
		client: client,
	}
}

func (v nodePoolValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	np, ok := obj.(*hyperv1.NodePool)
	if !ok {
		return nil, fmt.Errorf("wrong type %T for validation, instead of NodePool", obj)
	}

	switch np.Spec.Platform.Type {
	case hyperv1.KubevirtPlatform:
		return v.validateCreateKubevirtNodePool(ctx, np)
	default:
		return nil, nil // no validation needed
	}
}

func (v nodePoolValidator) ValidateUpdate(_ context.Context, _, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

func (v nodePoolValidator) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

func (v nodePoolValidator) validateCreateKubevirtNodePool(ctx context.Context, np *hyperv1.NodePool) (admission.Warnings, error) {
	hc := &hyperv1.HostedCluster{}
	err := v.client.Get(ctx, client.ObjectKey{Name: np.Spec.ClusterName, Namespace: np.Namespace}, hc)
	if err != nil {
		return nil, fmt.Errorf("failed to retrive HostedCluster %s/%s; %w", np.Namespace, np.Spec.ClusterName, err)
	}

	return kvValidator.validate(ctx, v.client, hc)
}

type kubevirtClusterValidator struct {
	clientMap kubevirtexternalinfra.KubevirtInfraClientMap
}

func (v kubevirtClusterValidator) validate(ctx context.Context, cli client.Client, hc *hyperv1.HostedCluster) (admission.Warnings, error) {
	if hc.Spec.Platform.Kubevirt == nil {
		return nil, fmt.Errorf("the spec.platform.kubevirt field is missing in the HostedCluster resource")
	}

	controlPlaneNamespace := manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name)
	cl, err := v.clientMap.DiscoverKubevirtClusterClient(ctx, cli, hc.Spec.InfraID, hc.Spec.Platform.Kubevirt.Credentials, controlPlaneNamespace, hc.Namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to connect external infra cluster; %w", err)
	}

	if _, isTimeout := ctx.Deadline(); !isTimeout {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Second*10)
		defer cancel()
	}

	return nil, kubevirtexternalinfra.ValidateClusterVersions(ctx, cl)
}
