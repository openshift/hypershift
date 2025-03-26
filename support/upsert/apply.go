package upsert

import (
	"context"
	"fmt"
	"maps"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type ApplyProvider interface {
	ApplyManifest(ctx context.Context, c crclient.Client, obj crclient.Object) (controllerutil.OperationResult, error)
	ValidateUpdateEvents(threshold int) error
}

var _ ApplyProvider = &applyProvider{}

type applyProvider struct {
	loopDetector *updateLoopDetector
}

func NewApplyProvider(enableUpdateLoopDetector bool) ApplyProvider {
	p := &applyProvider{}
	if enableUpdateLoopDetector {
		p.loopDetector = newUpdateLoopDetector()
	}
	return p
}

// ValidateUpdateEvents implements ApplyProvider.
func (p *applyProvider) ValidateUpdateEvents(threshold int) error {
	if p.loopDetector == nil {
		return nil
	}

	var errs []error
	for key, count := range p.loopDetector.updateEventCount {
		if count > threshold {
			errs = append(errs, fmt.Errorf("%s object has %d updates", key, count))
		}
	}
	return errors.NewAggregate(errs)
}

// ApplyManifest updates a resource from a yaml manifest configuration. The resource will be created if it doesn't exist yet.
// This doesn't update status, use 'controllerutil.CreateOrPatch()' instead.
func (p *applyProvider) ApplyManifest(ctx context.Context, c crclient.Client, obj crclient.Object) (controllerutil.OperationResult, error) {
	existing := obj.DeepCopyObject().(crclient.Object) //nolint

	if err := c.Get(ctx, crclient.ObjectKeyFromObject(obj), existing); err != nil {
		if !apierrors.IsNotFound(err) {
			return controllerutil.OperationResultNone, err
		}
		if err := c.Create(ctx, obj); err != nil {
			return controllerutil.OperationResultNone, err
		}

		return controllerutil.OperationResultCreated, nil
	}

	result, err := p.update(ctx, c, obj, existing)
	if err != nil || result == controllerutil.OperationResultNone {
		return result, err
	}

	return controllerutil.OperationResultUpdated, nil
}

func (p *applyProvider) update(ctx context.Context, c crclient.Client, obj crclient.Object, existing crclient.Object) (controllerutil.OperationResult, error) {
	key := crclient.ObjectKeyFromObject(obj)

	switch existingTyped := existing.(type) {
	case *corev1.ServiceAccount:
		preserveServiceAccountPullSecrets(existingTyped, obj.(*corev1.ServiceAccount))
	}
	preserveOriginalMetadata(existing, obj)

	current, err := toUnstructured(existing)
	if err != nil {
		return controllerutil.OperationResultNone, err
	}
	modified, err := toUnstructured(obj)
	if err != nil {
		return controllerutil.OperationResultNone, err
	}

	// DeepDerivative ignores unset fields in 'modified' (empty/nil arrays, empty strings, etc.)
	if equality.Semantic.DeepDerivative(modified, current) {
		if p.loopDetector != nil {
			p.loopDetector.recordNoOpUpdate(obj, key)
		}
		return controllerutil.OperationResultNone, nil
	}

	if p.loopDetector != nil {
		p.loopDetector.recordActualUpdate(existing, obj, key)
	}

	if err := c.Update(ctx, obj); err != nil {
		return controllerutil.OperationResultNone, err
	}

	return controllerutil.OperationResultUpdated, nil
}

func preserveOriginalMetadata(original, mutated crclient.Object) {
	labels := original.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	maps.Copy(labels, mutated.GetLabels())
	mutated.SetLabels(labels)

	annotations := original.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	maps.Copy(annotations, mutated.GetAnnotations())
	mutated.SetAnnotations(annotations)

	finalizers := sets.New(original.GetFinalizers()...).Insert(mutated.GetFinalizers()...)
	mutated.SetFinalizers(sets.List(finalizers))

	mutated.SetResourceVersion(original.GetResourceVersion())
}

func preserveServiceAccountPullSecrets(original, mutated *corev1.ServiceAccount) {
	// keep original pull secrets, as those will be injected after the serviceAccount is created.
	// this is necessary to avoid infinite update loop.
	imagePullSecretsSet := sets.New(mutated.ImagePullSecrets...)
	for _, pullSecret := range original.ImagePullSecrets {
		if !imagePullSecretsSet.Has(pullSecret) {
			mutated.ImagePullSecrets = append(mutated.ImagePullSecrets, pullSecret)
		}
	}

	mutated.Secrets = original.Secrets
}

var (
	// ignore read-only fields managed by k8s.
	ignoreMetadataFields = []string{
		"uid",
		"generation",
		"creationTimestamp",
	}
)

func toUnstructured(obj crclient.Object) (map[string]any, error) {
	// Create a copy of the original object as well as converting that copy to
	// unstructured data.
	u, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, err
	}

	for _, field := range ignoreMetadataFields {
		unstructured.RemoveNestedField(u, "metadata", field)
	}

	// status is updated separately, ignore.
	unstructured.RemoveNestedField(u, "status")
	return u, nil
}
