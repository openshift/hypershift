package upsert

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"

	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	// LastAppliedConfigurationAnnotation stores the JSON representation of the
	// last desired manifest state applied by ApplyManifest. Used to detect
	// changes in desired state that DeepDerivative cannot detect.
	LastAppliedConfigurationAnnotation = "hypershift.openshift.io/last-applied-configuration"

	// maxLastAppliedAnnotationSize is the maximum annotation value size in bytes.
	// Objects exceeding this fall back to DeepDerivative-only comparison.
	maxLastAppliedAnnotationSize = 128 * 1024 // 128KB
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
		if desiredJSON := computeLastAppliedJSON(obj); desiredJSON != "" {
			setLastAppliedConfiguration(obj, desiredJSON)
		}
		if err := c.Create(ctx, obj); err != nil {
			return controllerutil.OperationResultNone, err
		}

		return controllerutil.OperationResultCreated, nil
	}

	// Jobs cannot be updated. If there is an existing one, and its spec is different, then it needs to be recreated.
	if existing != nil {
		switch typedObj := obj.(type) {
		case *batchv1.Job:
			existingTyped := existing.(*batchv1.Job)
			failed := util.FindJobCondition(existingTyped, batchv1.JobFailed)
			if failed == nil || failed.Status == corev1.ConditionFalse {
				if equality.Semantic.DeepDerivative(typedObj.Spec, existingTyped.Spec) {
					return controllerutil.OperationResultNone, nil
				}
			}
			// Delete the job if it has failed or it needs to be updated
			_, err := util.DeleteIfNeededWithOptions(ctx, c, obj, crclient.PropagationPolicy(metav1.DeletePropagationForeground))
			return controllerutil.OperationResultNone, err
		}
	}

	result, err := p.update(ctx, c, obj, existing)
	if err != nil || result == controllerutil.OperationResultNone {
		return result, err
	}

	return controllerutil.OperationResultUpdated, nil
}

func (p *applyProvider) update(ctx context.Context, c crclient.Client, obj crclient.Object, existing crclient.Object) (controllerutil.OperationResult, error) {
	key := crclient.ObjectKeyFromObject(obj)

	// Preserve immutable fields.
	switch existingTyped := existing.(type) {
	case *corev1.ServiceAccount:
		preserveServiceAccountPullSecrets(existingTyped, obj.(*corev1.ServiceAccount))
	case *appsv1.Deployment:
		// Selector field is immutable, always preserve original Selector to avoid hot error loops.
		if existingTyped.Spec.Selector != nil {
			obj.(*appsv1.Deployment).Spec.Selector = existingTyped.Spec.Selector
		}
	}

	// Compute desired JSON BEFORE preserveOriginalMetadata merges existing
	// metadata into obj. This captures our pure desired state.
	desiredJSON := computeLastAppliedJSON(obj)

	// Get the last-applied annotation from the existing object.
	lastAppliedJSON := ""
	if existingAnnotations := existing.GetAnnotations(); existingAnnotations != nil {
		lastAppliedJSON = existingAnnotations[LastAppliedConfigurationAnnotation]
	}

	// Merge existing metadata (labels, annotations, finalizers, resourceVersion) into obj.
	preserveOriginalMetadata(existing, obj)

	needsUpdate := false

	if desiredJSON != "" && lastAppliedJSON != "" {
		// Both annotation values available: compare desired vs last-applied.
		needsUpdate = desiredJSON != lastAppliedJSON
	} else if desiredJSON != "" && lastAppliedJSON == "" {
		// Migration: object was created before LastAppliedConfigurationAnnotation annotation was added.
		// Force-stamp the annotation on first reconcile to ensure full coverage.
		needsUpdate = true
	}

	// Fall back to DeepDerivative for drift detection (external modifications
	// to the cluster object) and when annotation comparison is unavailable.
	if !needsUpdate {
		current, err := toUnstructured(existing)
		if err != nil {
			return controllerutil.OperationResultNone, err
		}
		modified, err := toUnstructured(obj)
		if err != nil {
			return controllerutil.OperationResultNone, err
		}
		if equality.Semantic.DeepDerivative(modified, current) {
			if p.loopDetector != nil {
				p.loopDetector.recordNoOpUpdate(obj, key)
			}
			return controllerutil.OperationResultNone, nil
		}
	}

	// Stamp the last-applied annotation on obj, overwriting the old value
	// that was merged in by preserveOriginalMetadata.
	if desiredJSON != "" {
		setLastAppliedConfiguration(obj, desiredJSON)
	}

	// In the case of a job, if an update is needed, the previous job must be deleted.
	switch existingTyped := existing.(type) {
	case *batchv1.Job:
		if existingTyped.DeletionTimestamp.IsZero() {
			if err := c.Delete(ctx, existing); err != nil {
				return controllerutil.OperationResultNone, err
			}
		}
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

	// Required by the Kubernetes Update API for optimistic concurrency. Without it, every Update call fails.
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
	// ignoreMetadataFields lists read-only and server-set metadata fields to
	// strip when converting objects to unstructured form for comparison.
	ignoreMetadataFields = []string{
		"uid",
		"generation",
		"creationTimestamp",
		"resourceVersion",
		"managedFields",
	}
)

// toUnstructured converts a crclient.Object to an unstructured map, stripping
// server-set metadata fields, status, and the last-applied annotation.
func toUnstructured(obj crclient.Object) (map[string]any, error) {
	u, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, err
	}

	for _, field := range ignoreMetadataFields {
		unstructured.RemoveNestedField(u, "metadata", field)
	}

	// status is updated separately, ignore.
	unstructured.RemoveNestedField(u, "status")

	// Remove the last-applied annotation to avoid self-referential comparison
	// and to prevent it from affecting DeepDerivative results.
	if annotations, ok, _ := unstructured.NestedMap(u, "metadata", "annotations"); ok {
		delete(annotations, LastAppliedConfigurationAnnotation)
		if len(annotations) == 0 {
			unstructured.RemoveNestedField(u, "metadata", "annotations")
		} else {
			_ = unstructured.SetNestedField(u, annotations, "metadata", "annotations")
		}
	}

	return u, nil
}

// computeLastAppliedJSON computes the JSON representation of the desired state
// for storing in the last-applied annotation. Returns empty string if the object
// cannot be serialized or exceeds maxLastAppliedAnnotationSize.
func computeLastAppliedJSON(obj crclient.Object) string {
	u, err := toUnstructured(obj)
	if err != nil {
		return ""
	}
	data, err := json.Marshal(u)
	if err != nil {
		return ""
	}
	if len(data) > maxLastAppliedAnnotationSize {
		return ""
	}
	return string(data)
}

// setLastAppliedConfiguration sets the last-applied-configuration annotation on the object.
func setLastAppliedConfiguration(obj crclient.Object, config string) {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[LastAppliedConfigurationAnnotation] = config
	obj.SetAnnotations(annotations)
}
