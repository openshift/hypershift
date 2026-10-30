package oapi

import (
	"context"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperapi "github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"

	operatorv1 "github.com/openshift/api/operator/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newHCP(encryptionType hyperv1.SecretEncryptionType, conditions ...metav1.Condition) *hyperv1.HostedControlPlane {
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "test-ns",
		},
	}
	if encryptionType != "" {
		hcp.Spec.SecretEncryption = &hyperv1.SecretEncryptionSpec{
			Type: encryptionType,
		}
	}
	hcp.Status.Conditions = conditions
	return hcp
}

func newOpenShiftAPIServer(conditions ...operatorv1.OperatorCondition) *operatorv1.OpenShiftAPIServer {
	return &operatorv1.OpenShiftAPIServer{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Status: operatorv1.OpenShiftAPIServerStatus{
			OperatorStatus: operatorv1.OperatorStatus{
				Conditions: conditions,
			},
		},
	}
}

func findCondition(conditions []operatorv1.OperatorCondition, condType string) *operatorv1.OperatorCondition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}
	return nil
}

func buildClient(objects ...client.Object) client.Client {
	return fake.NewClientBuilder().
		WithScheme(hyperapi.Scheme).
		WithObjects(objects...).
		WithStatusSubresource(&operatorv1.OpenShiftAPIServer{}).
		Build()
}

func TestReconcileEncryptionCondition(t *testing.T) {
	tests := []struct {
		name                string
		hcp                 *hyperv1.HostedControlPlane
		existing            *operatorv1.OpenShiftAPIServer
		wantCondition       *operatorv1.OperatorCondition // nil = condition should not exist
		wantOtherConditions int                           // count of non-Encrypted conditions expected
	}{
		{
			name:     "When encryption is not configured, it should set Encrypted=False with EncryptionDisabled",
			hcp:      newHCP(""),
			existing: newOpenShiftAPIServer(),
			wantCondition: &operatorv1.OperatorCondition{
				Type:    "Encrypted",
				Status:  operatorv1.ConditionFalse,
				Reason:  "EncryptionDisabled",
				Message: "Encryption is not enabled",
			},
		},
		{
			name: "When AESCBC encryption is completed, it should set Encrypted=True with EncryptionCompleted",
			hcp: newHCP(hyperv1.AESCBC, metav1.Condition{
				Type:               string(hyperv1.EtcdDataEncryptionUpToDate),
				Status:             metav1.ConditionTrue,
				Reason:             hyperv1.ReEncryptionCompletedReason,
				Message:            "Encryption key initialized",
				LastTransitionTime: metav1.Now(),
			}),
			existing: newOpenShiftAPIServer(),
			wantCondition: &operatorv1.OperatorCondition{
				Type:    "Encrypted",
				Status:  operatorv1.ConditionTrue,
				Reason:  "EncryptionCompleted",
				Message: "Encryption key initialized",
			},
		},
		{
			name: "When KMS encryption is completed, it should set Encrypted=True with EncryptionCompleted",
			hcp: newHCP(hyperv1.KMS, metav1.Condition{
				Type:               string(hyperv1.EtcdDataEncryptionUpToDate),
				Status:             metav1.ConditionTrue,
				Reason:             hyperv1.ReEncryptionCompletedReason,
				Message:            "All etcd data is encrypted with the current active key",
				LastTransitionTime: metav1.Now(),
			}),
			existing: newOpenShiftAPIServer(),
			wantCondition: &operatorv1.OperatorCondition{
				Type:    "Encrypted",
				Status:  operatorv1.ConditionTrue,
				Reason:  "EncryptionCompleted",
				Message: "All etcd data is encrypted with the current active key",
			},
		},
		{
			name: "When encryption key rotation is in progress, it should set Encrypted=False with EncryptionInProgress",
			hcp: newHCP(hyperv1.AESCBC, metav1.Condition{
				Type:               string(hyperv1.EtcdDataEncryptionUpToDate),
				Status:             metav1.ConditionFalse,
				Reason:             hyperv1.ReEncryptionInProgressReason,
				Message:            "Re-encrypting etcd data with new encryption key",
				LastTransitionTime: metav1.Now(),
			}),
			existing: newOpenShiftAPIServer(),
			wantCondition: &operatorv1.OperatorCondition{
				Type:    "Encrypted",
				Status:  operatorv1.ConditionFalse,
				Reason:  "EncryptionInProgress",
				Message: "Re-encrypting etcd data with new encryption key",
			},
		},
		{
			name:     "When encryption is configured but HCP condition is not yet set, it should set Encrypted=False with EncryptionInProgress",
			hcp:      newHCP(hyperv1.AESCBC),
			existing: newOpenShiftAPIServer(),
			wantCondition: &operatorv1.OperatorCondition{
				Type:    "Encrypted",
				Status:  operatorv1.ConditionFalse,
				Reason:  "EncryptionInProgress",
				Message: "Waiting for encryption status",
			},
		},
		{
			name: "When other conditions exist on the operator resource, it should preserve them",
			hcp: newHCP(hyperv1.AESCBC, metav1.Condition{
				Type:               string(hyperv1.EtcdDataEncryptionUpToDate),
				Status:             metav1.ConditionTrue,
				Reason:             hyperv1.ReEncryptionCompletedReason,
				Message:            "Encryption key initialized",
				LastTransitionTime: metav1.Now(),
			}),
			existing: newOpenShiftAPIServer(
				operatorv1.OperatorCondition{
					Type:    "SomeOtherCondition",
					Status:  operatorv1.ConditionTrue,
					Reason:  "SomeReason",
					Message: "some message",
				},
			),
			wantCondition: &operatorv1.OperatorCondition{
				Type:    "Encrypted",
				Status:  operatorv1.ConditionTrue,
				Reason:  "EncryptionCompleted",
				Message: "Encryption key initialized",
			},
			wantOtherConditions: 1,
		},
		{
			name: "When FIPS is enabled without secretEncryption, it should set Encrypted=False with EncryptionDisabled",
			hcp: func() *hyperv1.HostedControlPlane {
				hcp := newHCP("")
				hcp.Spec.FIPS = true
				return hcp
			}(),
			existing: newOpenShiftAPIServer(),
			wantCondition: &operatorv1.OperatorCondition{
				Type:    "Encrypted",
				Status:  operatorv1.ConditionFalse,
				Reason:  "EncryptionDisabled",
				Message: "Encryption is not enabled",
			},
		},
		{
			name: "When the operator resource does not exist yet, it should skip without error",
			hcp: newHCP(hyperv1.AESCBC, metav1.Condition{
				Type:               string(hyperv1.EtcdDataEncryptionUpToDate),
				Status:             metav1.ConditionTrue,
				Reason:             hyperv1.ReEncryptionCompletedReason,
				Message:            "Encryption key initialized",
				LastTransitionTime: metav1.Now(),
			}),
			existing: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var guestClient client.Client
			if tt.existing != nil {
				guestClient = buildClient(tt.existing)
			} else {
				guestClient = buildClient()
			}

			err := ReconcileEncryptionCondition(context.Background(), guestClient, tt.hcp)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.existing == nil {
				// Nothing to verify — the resource didn't exist.
				return
			}

			// Re-fetch the resource to check the result.
			result := &operatorv1.OpenShiftAPIServer{}
			if err := guestClient.Get(context.Background(), client.ObjectKey{Name: "cluster"}, result); err != nil {
				t.Fatalf("failed to get OpenShiftAPIServer: %v", err)
			}

			encCond := findCondition(result.Status.Conditions, "Encrypted")
			if tt.wantCondition == nil {
				if encCond != nil {
					t.Errorf("expected no Encrypted condition, got: %+v", encCond)
				}
			} else {
				if encCond == nil {
					t.Fatalf("expected Encrypted condition, got none")
				}
				if encCond.Status != tt.wantCondition.Status {
					t.Errorf("condition status: got %s, want %s", encCond.Status, tt.wantCondition.Status)
				}
				if encCond.Reason != tt.wantCondition.Reason {
					t.Errorf("condition reason: got %s, want %s", encCond.Reason, tt.wantCondition.Reason)
				}
				if encCond.Message != tt.wantCondition.Message {
					t.Errorf("condition message: got %q, want %q", encCond.Message, tt.wantCondition.Message)
				}
			}

			// Count non-Encrypted conditions.
			otherCount := 0
			for _, c := range result.Status.Conditions {
				if c.Type != "Encrypted" {
					otherCount++
				}
			}
			if otherCount != tt.wantOtherConditions {
				t.Errorf("other conditions count: got %d, want %d", otherCount, tt.wantOtherConditions)
			}
		})
	}
}

func TestReconcileEncryptionConditionIdempotent(t *testing.T) {
	t.Run("When the condition is already set and unchanged, it should not update the resource", func(t *testing.T) {
		hcp := newHCP(hyperv1.AESCBC, metav1.Condition{
			Type:               string(hyperv1.EtcdDataEncryptionUpToDate),
			Status:             metav1.ConditionTrue,
			Reason:             hyperv1.ReEncryptionCompletedReason,
			Message:            "Encryption key initialized",
			LastTransitionTime: metav1.Now(),
		})
		existing := newOpenShiftAPIServer()
		guestClient := buildClient(existing)

		// First reconcile: should set the condition.
		if err := ReconcileEncryptionCondition(context.Background(), guestClient, hcp); err != nil {
			t.Fatalf("first reconcile: %v", err)
		}

		// Fetch and record resourceVersion after the first update.
		result := &operatorv1.OpenShiftAPIServer{}
		if err := guestClient.Get(context.Background(), client.ObjectKey{Name: "cluster"}, result); err != nil {
			t.Fatalf("failed to get: %v", err)
		}
		rvAfterFirst := result.ResourceVersion

		// Second reconcile: should be a no-op.
		if err := ReconcileEncryptionCondition(context.Background(), guestClient, hcp); err != nil {
			t.Fatalf("second reconcile: %v", err)
		}

		result2 := &operatorv1.OpenShiftAPIServer{}
		if err := guestClient.Get(context.Background(), client.ObjectKey{Name: "cluster"}, result2); err != nil {
			t.Fatalf("failed to get: %v", err)
		}
		if result2.ResourceVersion != rvAfterFirst {
			t.Errorf("expected no update on second reconcile, but resourceVersion changed from %s to %s", rvAfterFirst, result2.ResourceVersion)
		}
	})
}
