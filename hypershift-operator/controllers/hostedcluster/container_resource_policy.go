package hostedcluster

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	schedulingv1alpha1 "github.com/openshift/hypershift/api/scheduling/v1alpha1"
	schedulerutil "github.com/openshift/hypershift/hypershift-operator/controllers/scheduler/util"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// bootstrapContainerResourcePolicy publishes only the initial resource policy,
// before either HO or CPO can create workloads. The scheduler owns subsequent
// updates; bootstrapping must not mark the cluster scheduled or bypass size
// transition delays once scheduling has completed.
func (r *HostedClusterReconciler) bootstrapContainerResourcePolicy(ctx context.Context, hc *hyperv1.HostedCluster) error {
	if !hc.DeletionTimestamp.IsZero() || hc.Annotations[hyperv1.HostedClusterScheduledAnnotation] == "true" {
		return nil
	}
	if paused, _, err := util.ProcessPausedUntilField(hc.Spec.PausedUntil, time.Now()); err != nil {
		return fmt.Errorf("startup container resource policy: invalid pausedUntil: %w", err)
	} else if paused {
		return nil
	}
	config := &schedulingv1alpha1.ClusterSizingConfiguration{}
	if err := r.Get(ctx, client.ObjectKey{Name: "cluster"}, config); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("reading startup cluster sizing configuration: %w", err)
	}
	policyEnabled := false
	for _, size := range config.Spec.Sizes {
		if size.Effects != nil && (!size.Effects.ContainerResourcePolicy.DefaultRequests.CPU.IsZero() || !size.Effects.ContainerResourcePolicy.DefaultRequests.Memory.IsZero()) {
			policyEnabled = true
			break
		}
	}
	if !policyEnabled {
		return nil
	}
	if condition := meta.FindStatusCondition(config.Status.Conditions, schedulingv1alpha1.ClusterSizingConfigurationValidType); condition == nil || condition.Status != metav1.ConditionTrue {
		return fmt.Errorf("waiting for valid startup cluster sizing configuration")
	}
	sizeName := hc.Annotations[hyperv1.ClusterSizeOverrideAnnotation]
	if sizeName == "" {
		sizeName = hc.Labels[hyperv1.HostedClusterSizeLabel]
	}
	var selected *schedulingv1alpha1.SizeConfiguration
	if sizeName != "" {
		selected = schedulerutil.SizeConfiguration(config, sizeName)
		if selected == nil {
			return fmt.Errorf("startup container resource policy: unknown size %q", sizeName)
		}
	} else {
		// Before the first size label, there is no observed guest node count.
		for i := range config.Spec.Sizes {
			if config.Spec.Sizes[i].Criteria.From == 0 {
				if selected != nil {
					return fmt.Errorf("startup container resource policy: multiple size classes include zero nodes")
				}
				selected = &config.Spec.Sizes[i]
			}
		}
		if selected == nil {
			return fmt.Errorf("startup container resource policy: no size class includes zero nodes")
		}
	}
	desired := hc.DeepCopy()
	delete(desired.Annotations, hyperv1.ContainerResourcePolicyAnnotation)
	if selected.Effects != nil && (!selected.Effects.ContainerResourcePolicy.DefaultRequests.CPU.IsZero() || !selected.Effects.ContainerResourcePolicy.DefaultRequests.Memory.IsZero()) {
		encoded, err := json.Marshal(selected.Effects.ContainerResourcePolicy)
		if err != nil {
			return fmt.Errorf("encoding startup container resource policy: %w", err)
		}
		if desired.Annotations == nil {
			desired.Annotations = map[string]string{}
		}
		desired.Annotations[hyperv1.ContainerResourcePolicyAnnotation] = string(encoded)
	}
	if err := controlplanecomponent.ApplyContainerResourcePolicy("", &corev1.PodTemplateSpec{}, desired.Annotations); err != nil {
		return fmt.Errorf("invalid startup container resource policy for size %q: %w", selected.Name, err)
	}
	if desired.Annotations[hyperv1.ContainerResourcePolicyAnnotation] == hc.Annotations[hyperv1.ContainerResourcePolicyAnnotation] {
		return nil
	}
	if err := r.Patch(ctx, desired, client.MergeFromWithOptions(hc, client.MergeFromWithOptimisticLock{})); err != nil {
		return fmt.Errorf("publishing startup container resource policy: %w", err)
	}
	*hc = *desired
	return nil
}
