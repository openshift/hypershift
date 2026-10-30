package oapi

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	operatorv1 "github.com/openshift/api/operator/v1"
	v1helpers "github.com/openshift/library-go/pkg/operator/v1helpers"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// These constants mirror the condition type and reasons used by standalone
	// OCP's openshift-apiserver-operator (library-go condition_controller.go).
	encryptedConditionType     = "Encrypted"
	encryptionCompletedReason  = "EncryptionCompleted"
	encryptionDisabledReason   = "EncryptionDisabled"
	encryptionInProgressReason = "EncryptionInProgress"
	encryptionDisabledMessage  = "Encryption is not enabled"
	encryptionWaitingMessage   = "Waiting for encryption status"
)

// ReconcileEncryptionCondition sets the "Encrypted" condition on the guest-side
// operator.openshift.io/v1 OpenShiftAPIServer resource, mirroring standalone
// OCP's library-go condition_controller.go behavior.
func ReconcileEncryptionCondition(ctx context.Context, guestClient client.Client, hcp *hyperv1.HostedControlPlane) error {
	log := ctrl.LoggerFrom(ctx)

	openshiftAPIServer := &operatorv1.OpenShiftAPIServer{}
	if err := guestClient.Get(ctx, client.ObjectKey{Name: "cluster"}, openshiftAPIServer); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("OpenShiftAPIServer resource not found, skipping encryption condition")
			return nil
		}
		return fmt.Errorf("failed to get OpenShiftAPIServer: %w", err)
	}

	originalStatus := openshiftAPIServer.Status.DeepCopy()

	cond := encryptedCondition(hcp)
	v1helpers.SetOperatorCondition(&openshiftAPIServer.Status.Conditions, cond)

	// Skip the status update if nothing changed (idempotency).
	if equality.Semantic.DeepEqual(originalStatus.Conditions, openshiftAPIServer.Status.Conditions) {
		return nil
	}

	if err := guestClient.Status().Update(ctx, openshiftAPIServer); err != nil {
		return fmt.Errorf("failed to update OpenShiftAPIServer Encrypted condition: %w", err)
	}
	return nil
}

// encryptedCondition builds the "Encrypted" OperatorCondition matching the
// convention used by standalone OCP's library-go condition_controller.go:
//
//   - Encrypted=True  / EncryptionCompleted   — encryption is active and all resources are encrypted
//   - Encrypted=False / EncryptionDisabled     — encryption is not configured
//   - Encrypted=False / EncryptionInProgress   — encryption key rotation is in progress
func encryptedCondition(hcp *hyperv1.HostedControlPlane) operatorv1.OperatorCondition {
	cond := operatorv1.OperatorCondition{
		Type:    encryptedConditionType,
		Status:  operatorv1.ConditionFalse,
		Reason:  encryptionDisabledReason,
		Message: encryptionDisabledMessage,
	}

	if hcp.Spec.SecretEncryption == nil {
		return cond
	}

	hcpCondition := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.EtcdDataEncryptionUpToDate))
	if hcpCondition == nil {
		// Encryption is configured but the reencryption controller hasn't
		// reported yet — treat as in-progress.
		cond.Reason = encryptionInProgressReason
		cond.Message = encryptionWaitingMessage
		return cond
	}

	cond.Message = hcpCondition.Message
	if hcpCondition.Status == metav1.ConditionTrue {
		cond.Status = operatorv1.ConditionTrue
		cond.Reason = encryptionCompletedReason
	} else {
		cond.Reason = encryptionInProgressReason
	}

	return cond
}
