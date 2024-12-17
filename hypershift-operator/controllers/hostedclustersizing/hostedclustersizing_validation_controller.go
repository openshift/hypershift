package hostedclustersizing

import (
	"context"
	"errors"
	"fmt"
	"time"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	schedulingv1alpha1 "github.com/openshift/hypershift/api/scheduling/v1alpha1"
	schedulingv1alpha1applyconfigurations "github.com/openshift/hypershift/client/applyconfiguration/scheduling/v1alpha1"
	hypershiftclient "github.com/openshift/hypershift/client/clientset/clientset"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	metav1applyconfigurations "k8s.io/client-go/applyconfigurations/meta/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type validator struct {
	client hypershiftclient.Interface
	lister client.Client
}

func (r *validator) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling")

	config := schedulingv1alpha1.ClusterSizingConfiguration{}
	if err := r.lister.Get(ctx, request.NamespacedName, &config); err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to get cluster sizing configuration %s: %w", request.NamespacedName.String(), err)
	}

	cfg := schedulingv1alpha1applyconfigurations.ClusterSizingConfiguration(request.Name)
	if validationErr := validateSizeConfigurations(config.Spec.Sizes); validationErr != nil {
		cfg.WithStatus(schedulingv1alpha1applyconfigurations.ClusterSizingConfigurationStatus().WithConditions(
			metav1applyconfigurations.Condition().
				WithType(schedulingv1alpha1.ClusterSizingConfigurationValidType).
				WithStatus(metav1.ConditionFalse).
				WithReason("SizeConfigurationInvalid").
				WithMessage(validationErr.Error()).
				WithLastTransitionTime(metav1.NewTime(time.Now())),
		))
	} else {
		cfg.WithStatus(schedulingv1alpha1applyconfigurations.ClusterSizingConfigurationStatus().WithConditions(
			metav1applyconfigurations.Condition().
				WithType(schedulingv1alpha1.ClusterSizingConfigurationValidType).
				WithStatus(metav1.ConditionTrue).
				WithReason(hypershiftv1beta1.AsExpectedReason).
				WithMessage("Cluster sizing configuration valid.").
				WithLastTransitionTime(metav1.NewTime(time.Now())),
		))
	}

	_, err := r.client.SchedulingV1alpha1().ClusterSizingConfigurations().ApplyStatus(ctx, cfg, metav1.ApplyOptions{FieldManager: ValidatingControllerName})
	return reconcile.Result{}, err
}

func validateSizeConfigurations(sizes []schedulingv1alpha1.SizeConfiguration) error {
	var errs []error
	errs = append(errs, validateIntervals(sizes))
	errs = append(errs, validateNonRequestServingSizeConfig(sizes))
	return utilerrors.NewAggregate(errs)
}

func validateIntervals(sizes []schedulingv1alpha1.SizeConfiguration) error {
	// n.b. we would prefer to do this with CEL but a bug in how the cost of expressions is calculated
	// keeps us from doing so - once https://github.com/google/cel-go/issues/900 is closed we can remove
	// this controller entirely; while we could write a much more clever routine here to determine which
	// part of the whole numbers is not covered or which size configurations overlap, let's not bother
	// as we won't be able to encode that in CEL, and it should hopefully be self-evident anyway for any
	// reasonable number of configured sizes
	// n.b. the existing CEL we *can* add to the CRD schema ensures we have one interval starting at 0 and
	// one ending at +inf, so we don't check it here
	starts := sets.New[uint32]()
	ends := sets.New[uint32]()
	for _, size := range sizes {
		if size.Criteria.From != 0 {
			starts.Insert(size.Criteria.From)
		}
		if size.Criteria.To != nil {
			ends.Insert(*size.Criteria.To + 1) // entries are inclusive, so we want to check to+1
		}
	}
	if !starts.Equal(ends) {
		return errors.New("a non-overlapping set of size configurations that cover all whole numbers is required, e.g. {(0,10),(11,100),(101,+inf)}")
	}
	return nil
}

func validateNonRequestServingSizeConfig(sizes []schedulingv1alpha1.SizeConfiguration) error {
	nilCount := 0
	setCount := 0
	for _, size := range sizes {
		if size.Management == nil || size.Management.NonRequestServingNodesPerZone == nil {
			nilCount++
		} else {
			setCount++
		}
	}
	if nilCount > 0 && setCount > 0 {
		return errors.New("all size configurations must have either all or none of the NonRequestServingNodesPerZone field set")
	}
	return nil
}
