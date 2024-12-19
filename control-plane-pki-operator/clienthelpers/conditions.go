package clienthelpers

import (
	"context"
	"fmt"
	"time"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hypershiftv1beta1applyconfigurations "github.com/openshift/hypershift/client/applyconfiguration/hypershift/v1beta1"
	hypershiftv1beta1client "github.com/openshift/hypershift/client/clientset/clientset/typed/hypershift/v1beta1"

	"github.com/openshift/library-go/pkg/operator/certrotation"
	"github.com/openshift/library-go/pkg/operator/condition"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientgoapplyconfig "k8s.io/client-go/applyconfigurations/meta/v1"
)

func NewHostedControlPlaneStatusReporter(name, namespace string, client hypershiftv1beta1client.HostedControlPlanesGetter) *HostedControlPlaneStatusReporter {
	return &HostedControlPlaneStatusReporter{
		namespace: namespace,
		name:      name,
		client:    client,
	}
}

type HostedControlPlaneStatusReporter struct {
	// namespace, name identify the HostedControlPlane we report to
	namespace, name string

	client hypershiftv1beta1client.HostedControlPlanesGetter
}

func (h *HostedControlPlaneStatusReporter) Report(ctx context.Context, conditionName string, syncErr error) (updated bool, updateErr error) {
	newCondition := metav1.Condition{
		Type:   fmt.Sprintf(condition.CertRotationDegradedConditionTypeFmt, conditionName),
		Status: metav1.ConditionFalse,
		Reason: hypershiftv1beta1.AsExpectedReason,
	}
	if syncErr != nil {
		newCondition.Status = metav1.ConditionTrue
		newCondition.Reason = "RotationError"
		newCondition.Message = syncErr.Error()
	}

	return UpdateHostedControlPlaneStatusCondition(ctx, newCondition, h.namespace, h.name, "cert-rotation-controller", h.client)
}

var _ certrotation.StatusReporter = (*HostedControlPlaneStatusReporter)(nil)

func UpdateHostedControlPlaneStatusCondition(ctx context.Context, newCondition metav1.Condition, namespace, name, fieldManager string, client hypershiftv1beta1client.HostedControlPlanesGetter) (bool, error) {
	current, err := client.HostedControlPlanes(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return false, err
	}

	existingCondition := FindCondition(current.Status.Conditions, newCondition.Type)
	if existingCondition == nil {
		newCondition.LastTransitionTime = metav1.NewTime(time.Now())
	} else {
		if existingCondition.Status != newCondition.Status {
			existingCondition.Status = newCondition.Status
			existingCondition.LastTransitionTime = metav1.NewTime(time.Now())
		}

		existingCondition.Reason = newCondition.Reason
		existingCondition.Message = newCondition.Message
		newCondition = *existingCondition
	}

	cfg := hypershiftv1beta1applyconfigurations.HostedControlPlane(name, namespace).
		WithStatus(hypershiftv1beta1applyconfigurations.HostedControlPlaneStatus().WithConditions(
			&clientgoapplyconfig.ConditionApplyConfiguration{
				Type:               &newCondition.Type,
				Status:             &newCondition.Status,
				Reason:             &newCondition.Reason,
				Message:            &newCondition.Message,
				LastTransitionTime: &newCondition.LastTransitionTime,
			}))

	_, updateErr := client.HostedControlPlanes(namespace).ApplyStatus(ctx, cfg, metav1.ApplyOptions{FieldManager: fieldManager})
	return true, updateErr
}

func FindCondition(conditions []metav1.Condition, conditionType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}

	return nil
}
