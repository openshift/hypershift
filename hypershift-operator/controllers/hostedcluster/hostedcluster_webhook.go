package hostedcluster

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/blang/semver"
	jsonpatch "github.com/evanphx/json-patch/v5"
	"github.com/go-logr/logr"
	apiexample "github.com/openshift/hypershift/examples/fixtures"
	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/kubevirtexternalinfra"
	"github.com/openshift/hypershift/support/supportedversion"
	"github.com/openshift/hypershift/support/supportedversion/supported"
	hyperutil "github.com/openshift/hypershift/support/util"
)

const versionLabel = "io.openshift.release"

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

	if hcluster.Spec.Release.Image == "" {
		pullSpec, err := supportedversion.LookupLatestSupportedRelease(ctx, hcluster)
		if err != nil {
			return fmt.Errorf("unable to find default release image: %w", err)
		}
		hcluster.Spec.Release.Image = pullSpec
	}

	// Default platform specific values
	switch hcluster.Spec.Platform.Type {
	case hyperv1.KubevirtPlatform:
		if hcluster.Spec.Platform.Kubevirt == nil {
			hcluster.Spec.Platform.Kubevirt = &hyperv1.KubevirtPlatformSpec{}
		}
		if hcluster.Spec.Platform.Kubevirt.GenerateID == "" {
			hcluster.Spec.Platform.Kubevirt.GenerateID = utilrand.String(10)
		}
		if hcluster.Spec.DNS.BaseDomain == "" {
			isTrue := true
			hcluster.Spec.Platform.Kubevirt.BaseDomainPassthrough = &isTrue
		}
		if hcluster.Spec.Networking.NetworkType == "" {
			hcluster.Spec.Networking.NetworkType = hyperv1.OVNKubernetes
		}

		// Default services for any service types that were not configured
		existingServices := map[hyperv1.ServiceType]bool{}
		defaults := apiexample.GetIngressServicePublishingStrategyMapping(hcluster.Spec.Networking.NetworkType, false)
		for _, entry := range hcluster.Spec.Services {
			existingServices[entry.Service] = true
		}

		for _, entry := range defaults {
			if existingServices[entry.Service] {
				continue
			}
			hcluster.Spec.Services = append(hcluster.Spec.Services, entry)
		}
	}

	return nil
}

func (defaulter *nodePoolDefaulter) Default(ctx context.Context, obj runtime.Object) error {
	np, ok := obj.(*hyperv1.NodePool)
	if !ok {
		return apierrors.NewBadRequest(fmt.Sprintf("expected a NodePool but got a %T", obj))
	}

	if np.Spec.Release.Image == "" {
		if np.Spec.ClusterName == "" {
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
	}

	// Default platform specific values
	switch np.Spec.Platform.Type {
	case hyperv1.KubevirtPlatform:
		if np.Spec.Platform.Kubevirt == nil {
			// Setting the KubeVirtNodePoolPlatform to an empty struct allows for
			// the CRD defaulting for this struct to take place.
			np.Spec.Platform.Kubevirt = &hyperv1.KubevirtNodePoolPlatform{}
		}
		if np.Spec.Management.UpgradeType == "" {
			np.Spec.Management.UpgradeType = hyperv1.UpgradeTypeReplace
			np.Spec.Management.Replace = &hyperv1.ReplaceUpgrade{}
		}
	}

	return nil
}

// SetupWebhookWithManager sets up HostedCluster webhooks.
func SetupWebhookWithManager(mgr ctrl.Manager, imageMetaDataProvider *hyperutil.RegistryClientImageMetadataProvider, logger logr.Logger) error {
	kvValidator = &kubevirtClusterValidator{
		client:                mgr.GetClient(),
		clientMap:             kubevirtexternalinfra.NewKubevirtInfraClientMap(),
		imageMetaDataProvider: imageMetaDataProvider,
	}

	err := ctrl.NewWebhookManagedBy(mgr).
		For(&hyperv1.HostedCluster{}).
		WithDefaulter(&hostedClusterDefaulter{}).
		WithValidator(&hostedClusterValidator{}).
		Complete()
	if err != nil {
		return fmt.Errorf("unable to register hostedcluster webhook: %w", err)
	}
	err = ctrl.NewWebhookManagedBy(mgr).
		For(&hyperv1.NodePool{}).
		WithDefaulter(&nodePoolDefaulter{client: mgr.GetClient()}).
		WithValidator(newNodePoolValidator(mgr.GetClient(), logger)).
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

var kvValidator *kubevirtClusterValidator

var _ admission.CustomValidator = (*hostedClusterValidator)(nil)

type hostedClusterValidator struct{}

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

func (v hostedClusterValidator) ValidateUpdate(ctx context.Context, oldHC, newHC runtime.Object) (admission.Warnings, error) {
	hc, ok := newHC.(*hyperv1.HostedCluster)
	if !ok {
		return nil, fmt.Errorf("wrong type %T for validation, instead of HostedCluster", newHC)
	}

	hcOld, ok := oldHC.(*hyperv1.HostedCluster)
	if !ok {
		return nil, fmt.Errorf("wrong type %T for validation, instead of HostedCluster", oldHC)
	}

	switch hc.Spec.Platform.Type {
	case hyperv1.KubevirtPlatform:
		err := v.validateUpdateKubevirtHostedCluster(ctx, hcOld, hc)
		if err != nil {
			return nil, err
		}
	}
	return nil, nil
}

func (v hostedClusterValidator) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

func (v hostedClusterValidator) validateCreateKubevirtHostedCluster(ctx context.Context, hc *hyperv1.HostedCluster) (admission.Warnings, error) {
	err := validateJsonAnnotation(hc.Annotations)
	if err != nil {
		return nil, err
	}

	return kvValidator.validateCreate(ctx, hc, hyperutil.HCControlPlaneReleaseImage(hc))
}

func (v hostedClusterValidator) validateUpdateKubevirtHostedCluster(ctx context.Context, oldHC, newHC *hyperv1.HostedCluster) error {
	err := validateJsonAnnotation(newHC.Annotations)
	if err != nil {
		return err
	}

	newReleaseImage := hyperutil.HCControlPlaneReleaseImage(newHC)

	if newReleaseImage != hyperutil.HCControlPlaneReleaseImage(oldHC) {
		if _, isTimeout := ctx.Deadline(); !isTimeout {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, time.Second*10)
			defer cancel()
		}

		err = kvValidator.validateReleaseImage(ctx, newHC, newReleaseImage)
		if err != nil {
			return err
		}
	}

	return nil
}

type nodePoolValidator struct {
	client client.Client
	logger logr.Logger
}

func newNodePoolValidator(client client.Client, logger logr.Logger) *nodePoolValidator {
	return &nodePoolValidator{
		client: client,
		logger: logr.New(logger.GetSink()).WithName("nodePoolValidator"),
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

func (v nodePoolValidator) ValidateUpdate(ctx context.Context, oldNP, newNP runtime.Object) (admission.Warnings, error) {
	npNew, ok := newNP.(*hyperv1.NodePool)
	if !ok {
		return nil, fmt.Errorf("wrong type %T for validation, instead of NodePool", newNP)
	}

	npOld, ok := oldNP.(*hyperv1.NodePool)
	if !ok {
		return nil, fmt.Errorf("wrong type %T for validation, instead of NodePool", npOld)
	}

	switch npNew.Spec.Platform.Type {
	case hyperv1.KubevirtPlatform:
		err := v.validateUpdateKubevirtNodePool(ctx, npOld, npNew)
		if err != nil {
			return nil, err
		}
	}

	return nil, nil
}

func (v nodePoolValidator) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

func (v nodePoolValidator) validateCreateKubevirtNodePool(ctx context.Context, np *hyperv1.NodePool) (admission.Warnings, error) {
	err := validateJsonAnnotation(np.Annotations)
	if err != nil {
		return nil, err
	}

	hc := v.getHostedClusterOrSkip(ctx, np)
	if hc == nil {
		return nil, nil
	}

	return kvValidator.validateCreate(ctx, hc, np.Spec.Release.Image)
}

func (v nodePoolValidator) validateUpdateKubevirtNodePool(ctx context.Context, oldNP, newNP *hyperv1.NodePool) error {
	err := validateJsonAnnotation(newNP.Annotations)
	if err != nil {
		return err
	}

	if oldNP.Spec.Release.Image != newNP.Spec.Release.Image {
		if _, isTimeout := ctx.Deadline(); !isTimeout {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, time.Second*10)
			defer cancel()
		}
		hc := v.getHostedClusterOrSkip(ctx, newNP)
		if hc == nil {
			return nil
		}

		err = kvValidator.validateReleaseImage(ctx, hc, newNP.Spec.Release.Image)
		if err != nil {
			return err
		}
	}

	return nil
}

func (v nodePoolValidator) getHostedClusterOrSkip(ctx context.Context, np *hyperv1.NodePool) *hyperv1.HostedCluster {
	hc := &hyperv1.HostedCluster{}
	err := v.client.Get(ctx, client.ObjectKey{Name: np.Spec.ClusterName, Namespace: np.Namespace}, hc)
	if err != nil {
		v.logger.Error(err, "can't find HostedCluster; skipping NodePool validation",
			"HostedCluster", np.Spec.ClusterName,
			"NodePool", np.Name,
			"namespace", np.Namespace)
		return nil
	}

	return hc
}

type kubevirtClusterValidator struct {
	client                client.Client
	clientMap             kubevirtexternalinfra.KubevirtInfraClientMap
	imageMetaDataProvider hyperutil.ImageMetadataProvider
}

func (v *kubevirtClusterValidator) validateCreate(ctx context.Context, hc *hyperv1.HostedCluster, releaseImage string) (admission.Warnings, error) {
	if v == nil {
		return nil, errors.New("kubevirt validator is not initialized") // should never happen
	}

	if hc.Spec.Platform.Kubevirt == nil {
		return nil, fmt.Errorf("the spec.platform.kubevirt field is missing in the HostedCluster resource")
	}

	controlPlaneNamespace := manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name)
	cl, err := v.clientMap.DiscoverKubevirtClusterClient(ctx, v.client, hc.Spec.InfraID, hc.Spec.Platform.Kubevirt.Credentials, controlPlaneNamespace, hc.Namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to connect external infra cluster; %w", err)
	}

	if _, isTimeout := ctx.Deadline(); !isTimeout {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Second*10)
		defer cancel()
	}

	eg, egCtx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		return v.validateReleaseImage(egCtx, hc, releaseImage)
	})

	eg.Go(func() error {
		return kubevirtexternalinfra.ValidateClusterVersions(egCtx, cl)
	})

	return nil, eg.Wait()
}

func (v *kubevirtClusterValidator) validateReleaseImage(ctx context.Context, hc *hyperv1.HostedCluster, releaseImage string) error {
	if _, exists := hc.Annotations[hyperv1.SkipReleaseImageValidation]; exists {
		return nil
	}

	version, err := v.getImageVersion(ctx, hc, releaseImage)
	if err != nil {
		return err
	}

	if version == nil {
		return nil
	}

	minSupportedVersion := supportedversion.GetMinSupportedVersion(hc)

	return supportedversion.IsValidReleaseVersion(version, nil, &supported.LatestSupportedVersion, &minSupportedVersion, hc.Spec.Networking.NetworkType, hc.Spec.Platform.Type)
}

func (v *kubevirtClusterValidator) getImageVersion(ctx context.Context, hc *hyperv1.HostedCluster, releaseImage string) (*semver.Version, error) {
	var pullSecret corev1.Secret
	err := v.client.Get(ctx, types.NamespacedName{Namespace: hc.Namespace, Name: hc.Spec.PullSecret.Name}, &pullSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to get pull secret: %w", err)
	}
	pullSecretBytes, ok := pullSecret.Data[corev1.DockerConfigJsonKey]
	if !ok {
		return nil, fmt.Errorf("expected %s key in pull secret", corev1.DockerConfigJsonKey)
	}

	metadata, err := v.imageMetaDataProvider.ImageMetadata(ctx, releaseImage, pullSecretBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to retrive release image metadata: %w", err)
	}

	ver, ok := metadata.Config.Labels[versionLabel]
	if !ok { // no version. Can't validate
		return nil, nil
	}

	version, err := semver.Parse(ver)
	if err != nil {
		return nil, fmt.Errorf("wrong version structure %q: %w", ver, err)
	}

	return &version, nil
}

func validateJsonAnnotation(annotations map[string]string) error {
	if ann, exists := annotations[hyperv1.JSONPatchAnnotation]; exists {
		patch, err := jsonpatch.DecodePatch([]byte(ann))
		if err != nil {
			return fmt.Errorf("wrong json patch structure in the %q annotation: %w", hyperv1.JSONPatchAnnotation, err)
		}

		for _, p := range patch {
			kind := p.Kind()
			if kind == "unknown" {
				return fmt.Errorf("wrong json patch structure in the %q annotation: missing op field", hyperv1.JSONPatchAnnotation)
			} else if kind != "delete" {
				v, err := p.ValueInterface()
				if err != nil {
					return fmt.Errorf("wrong json patch structure in the %q annotation: %w, %v", hyperv1.JSONPatchAnnotation, err, v)
				}
			}
			_, err = p.Path()
			if err != nil {
				return fmt.Errorf("wrong json patch structure in the %q annotation: %w", hyperv1.JSONPatchAnnotation, err)
			}
		}
	}

	return nil
}
