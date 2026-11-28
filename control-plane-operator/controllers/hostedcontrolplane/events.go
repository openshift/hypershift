package hostedcontrolplane

import (
	"context"
	"strings"
	"unicode/utf8"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	ControllerName             = "hosted-control-plane"
	reconciliationFailedReason = "ReconciliationFailed"
	maxFailureMessageBytes     = 1024
)

func (r *HostedControlPlaneReconciler) requestsForEvent(ctx context.Context, object client.Object) []reconcile.Request {
	if object == nil {
		return nil
	}
	if event, ok := object.(*corev1.Event); ok && event.Reason == reconciliationFailedReason &&
		(event.Source.Component == ControllerName || event.ReportingController == ControllerName) &&
		(event.Source.Component == "" || event.Source.Component == ControllerName) &&
		(event.ReportingController == "" || event.ReportingController == ControllerName) {
		reference := event.InvolvedObject
		if reference.APIVersion == hyperv1.GroupVersion.String() && reference.Kind == "HostedControlPlane" &&
			reference.Namespace == event.Namespace && reference.Name != "" && reference.UID != "" {
			hcp := &hyperv1.HostedControlPlane{}
			if err := r.Get(ctx, client.ObjectKey{Namespace: reference.Namespace, Name: reference.Name}, hcp); err == nil && hcp.UID == reference.UID {
				return nil
			}
		}
	}
	return r.hostedControlPlaneInNamespace(ctx, object)
}

func reconciliationFailureMessage(err error) string {
	message := strings.ToValidUTF8(err.Error(), "�")
	if len(message) > maxFailureMessageBytes {
		message = message[:maxFailureMessageBytes]
		for !utf8.ValidString(message) {
			message = message[:len(message)-1]
		}
	}
	return message
}

func (r *HostedControlPlaneReconciler) recordReconciliationFailure(hcp *hyperv1.HostedControlPlane, err error) {
	if err == nil || r.Recorder == nil || isConflictOnly(err) {
		return
	}
	r.Recorder.Event(hcp, corev1.EventTypeWarning, reconciliationFailedReason, reconciliationFailureMessage(err))
}

func isConflictOnly(err error) bool {
	if err == nil {
		return false
	}
	var children []error
	switch typed := any(err).(type) {
	case interface{ Errors() []error }:
		children = typed.Errors()
	case interface{ Unwrap() []error }:
		children = typed.Unwrap()
	case interface{ Unwrap() error }:
		return isConflictOnly(typed.Unwrap())
	default:
		return apierrors.IsConflict(err)
	}
	if len(children) == 0 {
		return false
	}
	for _, child := range children {
		if !isConflictOnly(child) {
			return false
		}
	}
	return true
}
