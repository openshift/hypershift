//go:build e2ev2 && backuprestore

package tests

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/backuprestore"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Automated etcd backup e2e tests validate the CronJob-based automated backup
// system managed by the etcd_backup_gcs CPO v2 component. These tests are
// GCP-only and require a pre-existing HostedCluster with automatedBackup
// configured in spec.etcd.managed.automatedBackup.
//
// Tier 2 (deferred): Full restore lifecycle (backup → break → recreate → restore → verify),
// no-snapshot path (empty bucket → NoSnapshotFound).
//
// Tier 3 (deferred): AESCBC encryption backup-restore, corrupt archive → ArchiveValidationFailed,
// upgrade/downgrade tests, concurrent operation with HCPEtcdBackup.

var _ = Describe("AutomatedEtcdBackup", Label("backup-restore", "automated-etcd-backup"), Ordered, Serial, func() {

	var (
		testCtx      *internal.TestContext
		backupConfig *hyperv1.AutomatedEtcdBackupConfig
		gcsBucket    string
		gcpSAEmail   string
		infraID      string
	)

	BeforeAll(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil())
		hostedCluster := testCtx.GetHostedCluster()
		Expect(hostedCluster).NotTo(BeNil(), "HostedCluster should be set up")

		if hostedCluster.Spec.Platform.Type != hyperv1.GCPPlatform {
			Skip("automated etcd backup test is only for GCP platform")
		}

		if hostedCluster.Spec.Etcd.ManagementType != hyperv1.Managed ||
			hostedCluster.Spec.Etcd.Managed == nil ||
			hostedCluster.Spec.Etcd.Managed.AutomatedBackup == nil {
			Skip("automatedBackup is not configured on this HostedCluster. " +
				"Set spec.etcd.managed.automatedBackup to enable these tests.")
		}

		backupConfig = hostedCluster.Spec.Etcd.Managed.AutomatedBackup

		if backupConfig.Storage.Type != hyperv1.AutomatedEtcdBackupStorageTypeGCS || backupConfig.Storage.GCS == nil {
			Skip("automatedBackup storage type is not GCS")
		}

		gcsBucket = backupConfig.Storage.GCS.Bucket
		gcpSAEmail = backupConfig.Storage.GCS.GCPServiceAccount
		infraID = hostedCluster.Spec.InfraID
	})

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil())
		if err := testCtx.ValidateControlPlaneNamespace(); err != nil {
			AbortSuite(err.Error())
		}
	})

	Context("When the HostedCluster has automatedBackup configured, it should validate feature gate and adoption", func() {
		It("When automatedBackup is present, it should have GCS storage configured", func() {
			Expect(backupConfig).NotTo(BeNil())
			Expect(backupConfig.Storage.Type).To(Equal(hyperv1.AutomatedEtcdBackupStorageTypeGCS))
			Expect(gcsBucket).NotTo(BeEmpty(), "GCS bucket should be configured")
			Expect(gcpSAEmail).NotTo(BeEmpty(), "GCP service account email should be configured")
		})

		It("When the cluster is established, it should have EtcdSnapshotRestored=True", func() {
			hostedCluster := &hyperv1.HostedCluster{}
			err := testCtx.MgmtClient.Get(testCtx.Context, crclient.ObjectKey{
				Name:      testCtx.ClusterName,
				Namespace: testCtx.ClusterNamespace,
			}, hostedCluster)
			Expect(err).NotTo(HaveOccurred())

			condition := meta.FindStatusCondition(hostedCluster.Status.Conditions,
				string(hyperv1.EtcdSnapshotRestored))
			Expect(condition).NotTo(BeNil(),
				"EtcdSnapshotRestored condition should exist on HostedCluster")
			Expect(condition.Status).To(Equal(metav1.ConditionTrue),
				"EtcdSnapshotRestored should be True for an established cluster, got reason=%s message=%s",
				condition.Reason, condition.Message)
		})
	})

	Context("When the backup component is reconciled, it should create CronJob and RBAC resources", func() {
		It("When etcd is available, it should have the etcd-backup-gcs CronJob", func() {
			cronJob := &batchv1.CronJob{}
			err := testCtx.MgmtClient.Get(testCtx.Context, crclient.ObjectKey{
				Namespace: testCtx.ControlPlaneNamespace,
				Name:      backuprestore.AutomatedBackupCronJobName,
			}, cronJob)
			Expect(err).NotTo(HaveOccurred(),
				"CronJob %s should exist in namespace %s",
				backuprestore.AutomatedBackupCronJobName, testCtx.ControlPlaneNamespace)
		})

		It("When the CronJob exists, it should have the correct spec", func() {
			err := backuprestore.ValidateCronJobSpec(testCtx,
				testCtx.ControlPlaneNamespace, backupConfig.Schedule)
			Expect(err).NotTo(HaveOccurred())
		})

		It("When the ServiceAccount exists, it should have RBAC with Workload Identity annotation", func() {
			err := backuprestore.ValidateBackupRBACResources(testCtx,
				testCtx.ControlPlaneNamespace, gcpSAEmail)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("When a backup Job is triggered, it should complete and update conditions", func() {
		var triggeredJobName string

		It("When a Job is created from the CronJob template, it should be accepted", func() {
			var err error
			triggeredJobName, err = backuprestore.TriggerCronJobManually(testCtx,
				testCtx.ControlPlaneNamespace)
			Expect(err).NotTo(HaveOccurred())
			GinkgoWriter.Printf("Triggered backup Job: %s\n", triggeredJobName)

			DeferCleanup(func() {
				job := &batchv1.Job{}
				if err := testCtx.MgmtClient.Get(testCtx.Context, crclient.ObjectKey{
					Namespace: testCtx.ControlPlaneNamespace,
					Name:      triggeredJobName,
				}, job); err == nil {
					propagation := metav1.DeletePropagationBackground
					_ = testCtx.MgmtClient.Delete(testCtx.Context, job,
						&crclient.DeleteOptions{PropagationPolicy: &propagation})
				}
			})
		})

		It("When the backup Job runs, it should complete successfully", func() {
			if triggeredJobName == "" {
				Skip("backup Job was not triggered")
			}
			err := backuprestore.WaitForJobCompletion(testCtx,
				testCtx.ControlPlaneNamespace, triggeredJobName,
				backuprestore.AutomatedBackupTimeout)
			Expect(err).NotTo(HaveOccurred())
		})

		It("When backup succeeds, it should set EtcdBackupSucceeded=True on the HostedCluster", func() {
			Eventually(func(g Gomega) {
				hostedCluster := &hyperv1.HostedCluster{}
				g.Expect(testCtx.MgmtClient.Get(testCtx.Context, crclient.ObjectKey{
					Name:      testCtx.ClusterName,
					Namespace: testCtx.ClusterNamespace,
				}, hostedCluster)).To(Succeed())

				condition := meta.FindStatusCondition(hostedCluster.Status.Conditions,
					string(hyperv1.EtcdBackupSucceeded))
				g.Expect(condition).NotTo(BeNil(),
					"EtcdBackupSucceeded condition should exist")
				g.Expect(condition.Status).To(Equal(metav1.ConditionTrue),
					fmt.Sprintf("EtcdBackupSucceeded should be True, got reason=%s message=%s",
						condition.Reason, condition.Message))
			}).WithPolling(backuprestore.PollInterval).
				WithTimeout(backuprestore.AutomatedBackupTimeout).Should(Succeed())
		})
	})

	Context("When backup has completed, it should store archives in GCS", func() {
		It("When GCS is checked, it should have backup objects at the expected path", func() {
			err := backuprestore.VerifyGCSBackupExists(testCtx, gcsBucket, infraID)
			Expect(err).NotTo(HaveOccurred(),
				"at least one backup archive should exist at gs://%s/%s/", gcsBucket, infraID)
		})
	})

	Context("When the backup component is reconciled, it should create alerting rules", func() {
		It("When PrometheusRule is checked, it should have EtcdBackupStale and EtcdRestoreFailed alerts", func() {
			err := backuprestore.ValidateAlertingRules(testCtx,
				testCtx.ControlPlaneNamespace)
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
