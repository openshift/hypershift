package capicrdmigrator

import (
	"context"
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	StatusConfigMapName = "capi-migration-status"
	statusDataKey       = "status"
)

type StatusReporter struct {
	client         client.Client
	namespace      string
	totalCRDs      int
	migrationState map[string]bool
}

func NewStatusReporter(c client.Client, namespace string, configByCRDName map[string]ByObjectConfig) *StatusReporter {
	state := make(map[string]bool, len(configByCRDName))
	for name := range configByCRDName {
		state[name] = false
	}
	return &StatusReporter{
		client:         c,
		namespace:      namespace,
		totalCRDs:      len(configByCRDName),
		migrationState: state,
	}
}

func (s *StatusReporter) SetCRDMigrated(crdName string) {
	s.migrationState[crdName] = true
}

func (s *StatusReporter) Reconcile(ctx context.Context, reconcileErr error) error {
	status := s.computeStatus()

	s.setConditions(status, reconcileErr)

	data, err := json.Marshal(status)
	if err != nil {
		return fmt.Errorf("failed to marshal migration status: %w", err)
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      StatusConfigMapName,
			Namespace: s.namespace,
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, s.client, cm, func() error {
		if cm.Data == nil {
			cm.Data = map[string]string{}
		}
		cm.Data[statusDataKey] = string(data)
		return nil
	})
	return err
}

func (s *StatusReporter) setConditions(status *CAPIMigrationStatus, reconcileErr error) {
	now := metav1.Now()
	complete := status.TotalCRDs > 0 && status.MigratedCRDs == status.TotalCRDs

	if complete {
		setCondition(&status.Conditions, metav1.Condition{
			Type:               MigrationCompleteCondition,
			Status:             metav1.ConditionTrue,
			Reason:             "MigrationComplete",
			Message:            fmt.Sprintf("All %d CRDs have been migrated", status.TotalCRDs),
			LastTransitionTime: now,
		})
		setCondition(&status.Conditions, metav1.Condition{
			Type:               ProgressingCondition,
			Status:             metav1.ConditionFalse,
			Reason:             "MigrationComplete",
			Message:            "Migration has completed",
			LastTransitionTime: now,
		})
	} else {
		setCondition(&status.Conditions, metav1.Condition{
			Type:               MigrationCompleteCondition,
			Status:             metav1.ConditionFalse,
			Reason:             "MigrationInProgress",
			Message:            fmt.Sprintf("%d/%d CRDs migrated", status.MigratedCRDs, status.TotalCRDs),
			LastTransitionTime: now,
		})
		setCondition(&status.Conditions, metav1.Condition{
			Type:               ProgressingCondition,
			Status:             metav1.ConditionTrue,
			Reason:             "MigrationInProgress",
			Message:            fmt.Sprintf("%d/%d CRDs migrated", status.MigratedCRDs, status.TotalCRDs),
			LastTransitionTime: now,
		})
	}

	if reconcileErr != nil {
		setCondition(&status.Conditions, metav1.Condition{
			Type:               DegradedCondition,
			Status:             metav1.ConditionTrue,
			Reason:             "MigrationError",
			Message:            reconcileErr.Error(),
			LastTransitionTime: now,
		})
	} else {
		setCondition(&status.Conditions, metav1.Condition{
			Type:               DegradedCondition,
			Status:             metav1.ConditionFalse,
			Reason:             "NoErrors",
			Message:            "No migration errors",
			LastTransitionTime: now,
		})
	}
}

func setCondition(conditions *[]metav1.Condition, condition metav1.Condition) {
	for i, existing := range *conditions {
		if existing.Type == condition.Type {
			if existing.Status == condition.Status {
				condition.LastTransitionTime = existing.LastTransitionTime
			}
			(*conditions)[i] = condition
			return
		}
	}
	*conditions = append(*conditions, condition)
}

func (s *StatusReporter) computeStatus() *CAPIMigrationStatus {
	status := &CAPIMigrationStatus{
		TotalCRDs: s.totalCRDs,
	}
	for _, migrated := range s.migrationState {
		if migrated {
			status.MigratedCRDs++
		}
	}
	return status
}
