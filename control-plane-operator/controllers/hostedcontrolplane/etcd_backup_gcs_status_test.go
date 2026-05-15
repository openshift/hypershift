package hostedcontrolplane

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"

	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/go-logr/zapr"
	"go.uber.org/zap/zaptest"
)

func TestReconcileEtcdBackupCondition(t *testing.T) {
	ns := "test-ns"
	baseHCP := func() *hyperv1.HostedControlPlane {
		return &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-hcp",
				Namespace:  ns,
				Generation: 1,
			},
			Spec: hyperv1.HostedControlPlaneSpec{
				Etcd: hyperv1.EtcdSpec{
					ManagementType: hyperv1.Managed,
					Managed: &hyperv1.ManagedEtcdSpec{
						AutomatedBackup: &hyperv1.AutomatedEtcdBackupConfig{
							Schedule: "0 */4 * * *",
							Storage: hyperv1.AutomatedEtcdBackupStorage{
								Type: hyperv1.AutomatedEtcdBackupStorageTypeGCS,
								GCS: &hyperv1.AutomatedEtcdBackupGCS{
									Bucket:            "my-bucket",
									GCPServiceAccount: "backup@proj.iam.gserviceaccount.com",
								},
							},
						},
					},
				},
			},
		}
	}

	tests := []struct {
		name           string
		hcp            *hyperv1.HostedControlPlane
		cronJob        *batchv1.CronJob
		expectStatus   metav1.ConditionStatus
		expectReason   string
		expectNoChange bool
	}{
		{
			name: "When etcd managed is nil it should not set a condition",
			hcp: func() *hyperv1.HostedControlPlane {
				hcp := baseHCP()
				hcp.Spec.Etcd.Managed = nil
				return hcp
			}(),
			expectNoChange: true,
		},
		{
			name: "When automatedBackup is nil it should not set a condition",
			hcp: func() *hyperv1.HostedControlPlane {
				hcp := baseHCP()
				hcp.Spec.Etcd.Managed.AutomatedBackup = nil
				return hcp
			}(),
			expectNoChange: true,
		},
		{
			name:         "When CronJob is not found it should report WaitingForEtcd",
			hcp:          baseHCP(),
			expectStatus: metav1.ConditionFalse,
			expectReason: "WaitingForEtcd",
		},
		{
			name: "When CronJob has LastSuccessfulTime it should report BackupSucceeded with timestamp",
			hcp:  baseHCP(),
			cronJob: &batchv1.CronJob{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "etcd-backup-gcs",
					Namespace: ns,
				},
				Status: batchv1.CronJobStatus{
					LastSuccessfulTime: &metav1.Time{Time: time.Date(2026, 4, 30, 12, 10, 0, 0, time.UTC)},
				},
			},
			expectStatus: metav1.ConditionTrue,
			expectReason: hyperv1.BackupSucceededReason,
		},
		{
			name: "When CronJob is scheduled but no success yet it should report BackupInProgress",
			hcp:  baseHCP(),
			cronJob: &batchv1.CronJob{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "etcd-backup-gcs",
					Namespace: ns,
				},
				Status: batchv1.CronJobStatus{
					LastScheduleTime: &metav1.Time{Time: time.Now()},
				},
			},
			expectStatus: metav1.ConditionFalse,
			expectReason: hyperv1.BackupInProgressReason,
		},
		{
			name: "When CronJob has not been scheduled yet it should report WaitingForFirstSchedule",
			hcp:  baseHCP(),
			cronJob: &batchv1.CronJob{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "etcd-backup-gcs",
					Namespace: ns,
				},
			},
			expectStatus: metav1.ConditionFalse,
			expectReason: "WaitingForFirstSchedule",
		},
		{
			name: "When CronJob is suspended it should report CronJobSuspended",
			hcp:  baseHCP(),
			cronJob: &batchv1.CronJob{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "etcd-backup-gcs",
					Namespace: ns,
				},
				Spec: batchv1.CronJobSpec{
					Suspend: ptr.To(true),
				},
			},
			expectStatus: metav1.ConditionFalse,
			expectReason: "CronJobSuspended",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			builder := fake.NewClientBuilder().WithScheme(api.Scheme)
			if tc.cronJob != nil {
				builder = builder.WithObjects(tc.cronJob)
			}
			c := builder.Build()

			r := &HostedControlPlaneReconciler{
				Log: ctrl.Log.WithName("test"),
			}
			r.Client = c
			logger := zapr.NewLogger(zaptest.NewLogger(t))
			r.Log = logger

			r.reconcileEtcdBackupCondition(context.Background(), tc.hcp)

			cond := meta.FindStatusCondition(tc.hcp.Status.Conditions, string(hyperv1.EtcdBackupSucceeded))

			if tc.expectNoChange {
				g.Expect(cond).To(BeNil(), "expected no EtcdBackupSucceeded condition to be set")
				return
			}

			g.Expect(cond).ToNot(BeNil(), "expected EtcdBackupSucceeded condition to be set")
			g.Expect(cond.Status).To(Equal(tc.expectStatus))
			g.Expect(cond.Reason).To(Equal(tc.expectReason))

			if tc.expectStatus == metav1.ConditionTrue && tc.cronJob != nil && tc.cronJob.Status.LastSuccessfulTime != nil {
				g.Expect(cond.Message).To(ContainSubstring("2026-04-30T12:10:00Z"))
			}
		})
	}
}
