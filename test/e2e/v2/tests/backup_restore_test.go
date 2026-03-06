//go:build e2ev2 && backuprestore

/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package tests

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/oadp"
	"github.com/openshift/hypershift/test/e2e/v2/backuprestore"
	"github.com/openshift/hypershift/test/e2e/v2/internal"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Context names for backup/restore test phases that can be shared between tests
// and unify naming conventions.
const (
	ContextPreBackupControlPlane   = "PreBackupControlPlane"
	ContextPreBackupGuest          = "PreBackupGuest"
	ContextSetupContinual          = "SetupContinual"
	ContextBackup                  = "Backup"
	ContextVerifyContinual         = "VerifyContinual"
	ContextPostBackupControlPlane  = "PostBackupControlPlane"
	ContextPostBackupGuest         = "PostBackupGuest"
	ContextRestore                 = "Restore"
	ContextPostRestoreControlPlane = "PostRestoreControlPlane"
	ContextPostRestoreGuest        = "PostRestoreGuest"
	ContextBreakControlPlane       = "BreakControlPlane"
)

var _ = Describe("BackupRestore", Label("backup-restore", "aws"), Ordered, Serial, func() {

	var (
		prober           backuprestore.ProberManager
		testCtx          *internal.TestContext
		backupName       string
		restoreName      string
		scheduleName     string
		excludeWorkloads []string = []string{
			"router", "karpenter", "karpenter-operator", "aws-node-termination-handler",
		}
	)

	AfterAll(func() {
		// Safety net for Prober
		if prober != nil {
			err := prober.Stop()
			if err != nil {
				GinkgoLogr.Error(err, "Failed to stop prober")
			}
		}
	})

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		if err := testCtx.ValidateControlPlaneNamespace(); err != nil {
			AbortSuite(err.Error())
		}
		hostedCluster := testCtx.GetHostedCluster()
		Expect(hostedCluster).NotTo(BeNil(), "HostedCluster should be set up")
		if hostedCluster.Spec.Platform.Type != hyperv1.AWSPlatform {
			Skip("Test is only supported on AWS platform")
		}

		// Ensure Velero pod is running before proceeding with backup/restore tests
		err := backuprestore.EnsureVeleroPodRunning(testCtx)
		if err != nil {
			Fail(fmt.Sprintf("Velero is not running: %v", err))
		}
	})

	Context(ContextPreBackupControlPlane, func() {
		It("should have control plane healthy before backup", func() {
			err := internal.ValidateControlPlaneDeploymentsReadiness(testCtx, excludeWorkloads)
			Expect(err).NotTo(HaveOccurred())
			err = internal.ValidateControlPlaneStatefulSetsReadiness(testCtx, excludeWorkloads)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context(ContextPreBackupGuest, func() {
		It("should have guest cluster ready before backup", func() {
			Skip("Skipping due to OCPBUGS-59876")
		})
	})

	// Setup the continual operations
	Context(ContextSetupContinual, func() {
		It("should setup continual operations successfully", func() {
			Skip("Skipping until CNTRLPLANE-2676 is implemented")
			verifyReconciliationActiveFunction := func() error {
				hostedCluster := &hyperv1.HostedCluster{}
				err := testCtx.MgmtClient.Get(testCtx.Context, crclient.ObjectKey{
					Name:      testCtx.ClusterName,
					Namespace: testCtx.ClusterNamespace,
				}, hostedCluster)
				if err != nil {
					return fmt.Errorf("failed to get HostedCluster: %w", err)
				}
				condition := meta.FindStatusCondition(hostedCluster.Status.Conditions, string(hyperv1.ReconciliationActive))
				if condition == nil {
					return fmt.Errorf("ReconciliationActive condition should exist")
				}
				if condition.Status != metav1.ConditionTrue {
					return fmt.Errorf("ReconciliationActive should be always True, but is %s at time %s: %s", condition.Status, condition.LastTransitionTime, condition.Message)
				}
				return nil
			}
			prober = backuprestore.NewProberManager(time.Second)
			prober.Spawn(verifyReconciliationActiveFunction)
		})
	})

	Context(ContextBackup, func() {
		It("should create backup and schedule successfully", func() {
			// Create schedule first to test parallel execution of backup and schedule and
			// to speed up the test execution.
			By("Creating schedule")
			scheduleName = oadp.GenerateScheduleName(testCtx.ClusterName, testCtx.ClusterNamespace)
			scheduleOpts := &backuprestore.OADPScheduleOptions{
				Name:            scheduleName,
				Schedule:        "* * * * *", // Every minute
				HCName:          testCtx.ClusterName,
				HCNamespace:     testCtx.ClusterNamespace,
				StorageLocation: testCtx.ClusterName,
			}
			err := backuprestore.RunOADPSchedule(testCtx.Context, GinkgoLogr.WithName("backup-restore"), testCtx.ArtifactDir, scheduleOpts)
			Expect(err).NotTo(HaveOccurred())

			DeferCleanup(func() {
				// Prevent creating backups indefinitely through the schedule
				By("Cleaning up schedule")
				if err := backuprestore.DeleteOADPSchedule(testCtx, scheduleName); err != nil {
					GinkgoWriter.Printf("Failed to delete schedule %s during cleanup: %v\n", scheduleName, err)
				}
			})

			By("Creating backup")
			backupName = oadp.GenerateBackupName(
				testCtx.ClusterName,
				testCtx.ClusterNamespace,
			)
			backupOpts := &backuprestore.OADPBackupOptions{
				Name:            backupName,
				HCName:          testCtx.ClusterName,
				HCNamespace:     testCtx.ClusterNamespace,
				StorageLocation: testCtx.ClusterName,
			}
			err = backuprestore.RunOADPBackup(testCtx.Context, GinkgoLogr.WithName("backup-restore"), testCtx.ArtifactDir, backupOpts)
			Expect(err).NotTo(HaveOccurred())

			By("Waiting for backup to complete")
			err = backuprestore.WaitForBackupCompletion(testCtx, backupName)
			Expect(err).NotTo(HaveOccurred())

			By("Waiting for schedule to have one backup completed")
			err = backuprestore.WaitForScheduleCompletion(testCtx, scheduleName)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	// Verify the continual operations
	Context(ContextVerifyContinual, func() {
		It("should verify continual operations completed successfully", func() {
			Skip("Skipping until CNTRLPLANE-2676 is implemented")
			err := prober.Stop()
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context(ContextPostBackupControlPlane, func() {
		It("should have control plane healthy after backup", func() {
			err := internal.ValidateControlPlaneDeploymentsReadiness(testCtx, excludeWorkloads)
			Expect(err).NotTo(HaveOccurred())
			err = internal.ValidateControlPlaneStatefulSetsReadiness(testCtx, excludeWorkloads)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context(ContextPostBackupGuest, func() {
		It("should have guest cluster healthy after backup", func() {
			Skip("Skipping due to OCPBUGS-59876")
		})
	})

	Context(ContextBreakControlPlane, func() {
		It("should break hosted cluster", func() {
			err := backuprestore.BreakHostedClusterPreservingMachines(testCtx, GinkgoLogr.WithName("cleanup"))
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context(ContextRestore, func() {
		It("should restore from backup successfully", func() {
			By("Creating Restore")
			restoreName = oadp.GenerateRestoreName(testCtx.ClusterName, testCtx.ClusterNamespace)
			restoreOpts := &backuprestore.OADPRestoreOptions{
				Name:        restoreName,
				FromBackup:  backupName,
				HCName:      testCtx.ClusterName,
				HCNamespace: testCtx.ClusterNamespace,
			}
			err := backuprestore.RunOADPRestore(testCtx.Context, GinkgoLogr.WithName("backup-restore"), testCtx.ArtifactDir, restoreOpts)
			Expect(err).NotTo(HaveOccurred())
			By("Waiting for restore to complete")
			err = backuprestore.WaitForRestoreCompletion(testCtx, restoreName)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context(ContextPostRestoreControlPlane, func() {
		It("should have control plane healthy after restore", func() {
			By("Waiting for control plane statefulsets to be ready")
			err := internal.WaitForControlPlaneStatefulSetsReadiness(testCtx, backuprestore.RestoreTimeout, excludeWorkloads)
			Expect(err).NotTo(HaveOccurred())
			By("Waiting for control plane deployments to be ready")
			err = internal.WaitForControlPlaneDeploymentsReadiness(testCtx, backuprestore.RestoreTimeout, excludeWorkloads)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context(ContextPostRestoreGuest, func() {
		It("should have guest cluster healthy after restore", func() {
			Skip("Skipping due to OCPBUGS-59876")
		})
	})
})
