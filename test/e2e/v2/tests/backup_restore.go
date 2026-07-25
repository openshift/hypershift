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
	"maps"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/oadp"
	"github.com/openshift/hypershift/support/conditions"
	"github.com/openshift/hypershift/support/supportedversion"
	"github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/backuprestore"
	"github.com/openshift/hypershift/test/e2e/v2/internal"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Context names for backup/restore test phases that can be shared between tests
// and unify naming conventions.
const (
	ContextPreBackupControlPlane   = "PreBackupControlPlane"
	ContextSetupContinual          = "SetupContinual"
	ContextBackup                  = "Backup"
	ContextVerifyContinual         = "VerifyContinual"
	ContextPostBackupControlPlane  = "PostBackupControlPlane"
	ContextRestore                 = "Restore"
	ContextPostRestoreControlPlane = "PostRestoreControlPlane"
	ContextBreakControlPlane       = "BreakControlPlane"
)

const (
	// oadpPluginConfigMapName is the name of the ConfigMap used to configure the hypershift OADP plugin.
	oadpPluginConfigMapName = "hypershift-oadp-plugin-config"
	// etcdBackupMethodKey is the ConfigMap key that controls the etcd backup method.
	etcdBackupMethodKey = "etcdBackupMethod"
	// etcdBackupMethodSnapshot is the value for etcdBackupMethodKey to use etcd snapshots.
	etcdBackupMethodSnapshot = "etcdSnapshot"
)

type backupRestorePlatformConfig struct {
	excludeWorkloads     []string
	postRestoreHook      func(testCtx *internal.TestContext) error
	additionalNamespaces []string
}

var backupRestorePlatforms = map[hyperv1.PlatformType]backupRestorePlatformConfig{
	hyperv1.AWSPlatform: {
		excludeWorkloads: []string{"router", "karpenter", "karpenter-operator", "aws-node-termination-handler"},
		postRestoreHook: func(testCtx *internal.TestContext) error {
			awsCredsFile := internal.GetEnvVarValue("AWS_GUEST_INFRA_CREDENTIALS_FILE")
			fixOpts := &backuprestore.FixDrOidcIamOptions{
				HCName:       testCtx.ClusterName,
				HCNamespace:  testCtx.ClusterNamespace,
				AWSCredsFile: awsCredsFile,
				Timeout:      backuprestore.OIDCTimeout,
			}
			return backuprestore.RunFixDrOidcIam(testCtx.Context, GinkgoLogr.WithName("backup-restore"), testCtx.ArtifactDir, fixOpts)
		},
	},
	hyperv1.AgentPlatform: {
		excludeWorkloads: []string{"router", "karpenter", "karpenter-operator", "cloud-network-config-controller"},
		postRestoreHook: func(testCtx *internal.TestContext) error {
			// Restore brings back paused CAPI resources; unpause them so reconciliation resumes.
			return backuprestore.UnpauseAgentCAPIResources(testCtx, GinkgoLogr.WithName("backup-restore"))
		},
	},
	hyperv1.KubevirtPlatform: {
		excludeWorkloads: []string{"router", "karpenter", "karpenter-operator", "cloud-network-config-controller"},
		postRestoreHook:  nil,
	},
	hyperv1.AzurePlatform: {
		excludeWorkloads: []string{"router", "karpenter", "karpenter-operator"},
		postRestoreHook:  nil,
	},
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:BackupRestore] BackupRestore", Label("backup-restore"), Ordered, Serial, func() {

	var (
		platformCfg        backupRestorePlatformConfig
		prober             backuprestore.ProberManager
		testCtx            *internal.TestContext
		backupName         string
		scheduleName       string
		expectedConditions []util.Condition
	)

	BeforeAll(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil())
		hostedCluster := testCtx.GetHostedCluster()
		Expect(hostedCluster).NotTo(BeNil(), "HostedCluster should be set up")
		cfg, supported := backupRestorePlatforms[hostedCluster.Spec.Platform.Type]
		if !supported {
			Skip(fmt.Sprintf("Backup/restore test not supported on platform %s", hostedCluster.Spec.Platform.Type))
		}
		platformCfg = cfg
		if hostedCluster.Spec.Platform.Type == hyperv1.AgentPlatform &&
			hostedCluster.Spec.Platform.Agent != nil &&
			hostedCluster.Spec.Platform.Agent.AgentNamespace != "" {
			platformCfg.additionalNamespaces = []string{hostedCluster.Spec.Platform.Agent.AgentNamespace}
		}
	})

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
		Expect(testCtx).NotTo(BeNil())
		validateBeforeEach(testCtx)
	})

	Context(ContextPreBackupControlPlane, func() {
		It("should have control plane healthy before backup", func() {
			expectedConditions = validatePreBackupControlPlane(testCtx, platformCfg.excludeWorkloads)
		})
	})

	// Setup the continual operations
	Context(ContextSetupContinual, func() {
		It("should setup continual operations successfully", func() {
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
			if testCtx.GetHostedCluster().Spec.Platform.Type == hyperv1.AgentPlatform {
				By("Pausing AgentMachine and AgentCluster CRs")
				err := backuprestore.PauseAgentCAPIResources(testCtx, GinkgoLogr.WithName("backup-restore"))
				Expect(err).NotTo(HaveOccurred())
				DeferCleanup(func() {
					if err := backuprestore.UnpauseAgentCAPIResources(testCtx, GinkgoLogr.WithName("backup-restore")); err != nil {
						GinkgoWriter.Printf("Failed to unpause Agent CAPI resources during cleanup: %v\n", err)
					}
				})
			}

			// Create schedule first to test parallel execution of backup and schedule and
			// to speed up the test execution.
			By("Creating schedule")
			scheduleName = oadp.GenerateScheduleName(testCtx.ClusterName, testCtx.ClusterNamespace)
			scheduleOpts := &backuprestore.OADPScheduleOptions{
				Name:              scheduleName,
				Schedule:          "* * * * *", // Every minute
				HCName:            testCtx.ClusterName,
				HCNamespace:       testCtx.ClusterNamespace,
				StorageLocation:   testCtx.ClusterName,
				IncludeNamespaces: platformCfg.additionalNamespaces,
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
				Name:              backupName,
				HCName:            testCtx.ClusterName,
				HCNamespace:       testCtx.ClusterNamespace,
				StorageLocation:   testCtx.ClusterName,
				IncludeNamespaces: platformCfg.additionalNamespaces,
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
			if prober == nil {
				Skip("prober not initialized")
			}
			err := prober.Stop()
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context(ContextPostBackupControlPlane, func() {
		It("should have control plane healthy after backup", func() {
			err := internal.WaitForControlPlaneDeploymentsReadiness(testCtx, 5*time.Minute, platformCfg.excludeWorkloads)
			Expect(err).NotTo(HaveOccurred())
			err = internal.WaitForControlPlaneStatefulSetsReadiness(testCtx, 5*time.Minute, platformCfg.excludeWorkloads)
			Expect(err).NotTo(HaveOccurred())
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
			restoreName := oadp.GenerateRestoreName(testCtx.ClusterName, testCtx.ClusterNamespace)
			restoreOpts := &backuprestore.OADPRestoreOptions{
				Name:              restoreName,
				FromBackup:        backupName,
				HCName:            testCtx.ClusterName,
				HCNamespace:       testCtx.ClusterNamespace,
				IncludeNamespaces: platformCfg.additionalNamespaces,
			}
			executeRestore(testCtx, restoreOpts, platformCfg.postRestoreHook)
		})
	})

	Context(ContextPostRestoreControlPlane, func() {
		It("should have control plane healthy after restore", func() {
			// TODO(mgencur): Remove this condition once https://redhat.atlassian.net/browse/MGMT-23509 is fixed
			skipNodePoolValidation := testCtx.GetHostedCluster().Spec.Platform.Type == hyperv1.AgentPlatform
			validatePostRestoreControlPlane(testCtx, platformCfg.excludeWorkloads, expectedConditions, skipNodePoolValidation)
		})
	})
})

func getNodePool(testCtx *internal.TestContext) (*hyperv1.NodePool, error) {
	nodePoolList := &hyperv1.NodePoolList{}
	err := testCtx.MgmtClient.List(testCtx.Context, nodePoolList, crclient.InNamespace(testCtx.ClusterNamespace))

	if err != nil {
		return nil, err
	}
	if len(nodePoolList.Items) == 0 {
		return nil, fmt.Errorf("no NodePools found in namespace %s", testCtx.ClusterNamespace)
	}
	for i := range nodePoolList.Items {
		if nodePoolList.Items[i].Spec.ClusterName == testCtx.ClusterName {
			return &nodePoolList.Items[i], nil
		}
	}
	return nil, fmt.Errorf("no NodePool found for cluster %s", testCtx.ClusterName)
}

func validateBeforeEach(testCtx *internal.TestContext) {
	testCtx.ValidateHostedCluster()

	err := backuprestore.EnsureVeleroPodRunning(testCtx)
	if err != nil {
		Fail(fmt.Sprintf("Velero is not running: %v", err))
	}
}

// validatePreBackupControlPlane validates that deployments, statefulsets, and NodePool conditions
// are healthy before a backup. It returns the expected conditions for later post-restore validation.
func validatePreBackupControlPlane(testCtx *internal.TestContext, excludeWorkloads []string) []util.Condition {
	err := internal.WaitForControlPlaneDeploymentsReadiness(testCtx, 5*time.Minute, excludeWorkloads)
	Expect(err).NotTo(HaveOccurred())
	err = internal.WaitForControlPlaneStatefulSetsReadiness(testCtx, 5*time.Minute, excludeWorkloads)
	Expect(err).NotTo(HaveOccurred())
	nodePool, err := getNodePool(testCtx)
	Expect(err).NotTo(HaveOccurred())
	Expect(nodePool).NotTo(BeNil())
	npConditions := conditions.ExpectedNodePoolConditions(nodePool)

	latestVersion, err := supportedversion.GetLatestSupportedOCPVersion(testCtx.Context, testCtx.MgmtClient)
	Expect(err).NotTo(HaveOccurred())
	if latestVersion.LT(util.Version421) {
		delete(npConditions, hyperv1.NodePoolSupportedVersionSkewConditionType)
	}

	var expectedConditions []util.Condition
	for conditionType, conditionStatus := range npConditions {
		expectedConditions = append(expectedConditions, util.Condition{
			Type:   conditionType,
			Status: metav1.ConditionStatus(conditionStatus),
		})
	}
	internal.ValidateConditions(NewWithT(GinkgoT()), nodePool, expectedConditions)
	return expectedConditions
}

// executeRestore runs an OADP restore, waits for completion, and optionally runs a post-restore hook.
func executeRestore(testCtx *internal.TestContext, restoreOpts *backuprestore.OADPRestoreOptions, postRestoreHook func(*internal.TestContext) error) {
	err := backuprestore.RunOADPRestore(testCtx.Context, GinkgoLogr.WithName("backup-restore"), testCtx.ArtifactDir, restoreOpts)
	Expect(err).NotTo(HaveOccurred())
	By("Waiting for restore to complete")
	err = backuprestore.WaitForRestoreCompletion(testCtx, restoreOpts.Name)
	Expect(err).NotTo(HaveOccurred())

	if postRestoreHook != nil {
		By("Running platform-specific post-restore operations")
		err = postRestoreHook(testCtx)
		Expect(err).NotTo(HaveOccurred())
	}
}

// validatePostRestoreControlPlane waits for statefulsets and deployments to become ready after a
// restore, and optionally validates NodePool conditions. Set skipNodePoolValidation to true when
// NodePool validation is not applicable (e.g. Agent platform workaround).
func validatePostRestoreControlPlane(testCtx *internal.TestContext, excludeWorkloads []string, expectedConditions []util.Condition, skipNodePoolValidation bool) {
	By("Waiting for control plane statefulsets to be ready")
	err := internal.WaitForControlPlaneStatefulSetsReadiness(testCtx, backuprestore.RestoreTimeout, excludeWorkloads)
	Expect(err).NotTo(HaveOccurred())
	By("Waiting for control plane deployments to be ready")
	err = internal.WaitForControlPlaneDeploymentsReadiness(testCtx, backuprestore.RestoreTimeout, excludeWorkloads)
	Expect(err).NotTo(HaveOccurred())
	if !skipNodePoolValidation {
		By("Validating NodePool conditions")
		Eventually(func(g Gomega) {
			nodePool, err := getNodePool(testCtx)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(nodePool).NotTo(BeNil())
			internal.ValidateConditions(g, nodePool, expectedConditions)
		}).WithPolling(backuprestore.PollInterval).WithTimeout(backuprestore.OIDCTimeout).Should(Succeed())
	}
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:EtcdSnapshot] BackupRestoreEtcdSnapshot", Label("backup-restore", "etcd-snapshot"), Ordered, Serial, func() {

	var (
		platformCfg        backupRestorePlatformConfig
		testCtx            *internal.TestContext
		backupName         string
		snapshotURL        string
		expectedConditions []util.Condition
	)

	BeforeAll(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil())
		hostedCluster := testCtx.GetHostedCluster()
		Expect(hostedCluster).NotTo(BeNil(), "HostedCluster should be set up")
		if hostedCluster.Spec.Platform.Type != hyperv1.AWSPlatform {
			Skip("etcd snapshot backup test only supported on AWS")
		}
		platformCfg = backupRestorePlatforms[hyperv1.AWSPlatform]

		By("Checking if HCPEtcdBackup feature gate is enabled")
		hcpEtcdBackupList := &hyperv1.HCPEtcdBackupList{}
		err := testCtx.MgmtClient.List(testCtx.Context, hcpEtcdBackupList, crclient.InNamespace(testCtx.ControlPlaneNamespace))
		if err != nil {
			if meta.IsNoMatchError(err) || apierrors.IsNotFound(err) {
				Skip("HCPEtcdBackup feature gate is not enabled (CRD not installed). " +
					"Set HYPERSHIFT_FEATURESET=TechPreviewNoUpgrade on the HyperShift operator to enable it.")
			}
			// Other errors are unexpected - fail loudly
			Expect(err).NotTo(HaveOccurred(), "unexpected error listing HCPEtcdBackup resources")
		}

		By("Configuring the hypershift-oadp-plugin-config ConfigMap")
		cm := &corev1.ConfigMap{}
		cmKey := types.NamespacedName{
			Name:      oadpPluginConfigMapName,
			Namespace: backuprestore.DefaultOADPNamespace,
		}
		var originalData map[string]string
		cmExisted := true
		err = testCtx.MgmtClient.Get(testCtx.Context, cmKey, cm)
		if apierrors.IsNotFound(err) {
			cmExisted = false
			cm = &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cmKey.Name,
					Namespace: cmKey.Namespace,
				},
				Data: map[string]string{
					etcdBackupMethodKey: etcdBackupMethodSnapshot,
				},
			}
			err = testCtx.MgmtClient.Create(testCtx.Context, cm)
			Expect(err).NotTo(HaveOccurred())
		} else {
			Expect(err).NotTo(HaveOccurred())
			originalData = maps.Clone(cm.Data)
			if cm.Data == nil {
				cm.Data = map[string]string{}
			}
			cm.Data[etcdBackupMethodKey] = etcdBackupMethodSnapshot
			err = testCtx.MgmtClient.Update(testCtx.Context, cm)
			Expect(err).NotTo(HaveOccurred())
		}

		DeferCleanup(func() {
			By("Cleaning up hypershift-oadp-plugin-config ConfigMap")
			configMap := &corev1.ConfigMap{}
			if err := testCtx.MgmtClient.Get(testCtx.Context, cmKey, configMap); err != nil {
				if !apierrors.IsNotFound(err) {
					GinkgoWriter.Printf("Failed to get ConfigMap during cleanup: %v\n", err)
				}
				return
			}
			if !cmExisted {
				if err := testCtx.MgmtClient.Delete(testCtx.Context, configMap); err != nil && !apierrors.IsNotFound(err) {
					GinkgoWriter.Printf("Failed to delete ConfigMap during cleanup: %v\n", err)
				}
				return
			}
			configMap.Data = originalData
			if err := testCtx.MgmtClient.Update(testCtx.Context, configMap); err != nil {
				GinkgoWriter.Printf("Failed to restore ConfigMap during cleanup: %v\n", err)
			}
		})

		By("Ensuring DPA has the hypershift plugin")
		dpaState, err := backuprestore.EnsureDPAHypershiftPlugin(testCtx)
		Expect(err).NotTo(HaveOccurred(), "failed to ensure DPA has hypershift plugin")

		if dpaState.PluginsModified {
			DeferCleanup(func() {
				By("Restoring original DPA plugins")
				if err := backuprestore.RestoreDPAPlugins(testCtx, dpaState); err != nil {
					GinkgoWriter.Printf("Failed to restore DPA plugins during cleanup: %v\n", err)
				}
			})
		}
	})

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil())
		validateBeforeEach(testCtx)
	})

	Context(ContextPreBackupControlPlane, func() {
		It("should have control plane healthy before backup", func() {
			expectedConditions = validatePreBackupControlPlane(testCtx, platformCfg.excludeWorkloads)
		})
	})

	Context(ContextBackup, func() {
		It("should create backup with etcd snapshot method", func() {
			By("Creating backup with etcd snapshot options")
			backupName = oadp.GenerateBackupName(
				testCtx.ClusterName,
				testCtx.ClusterNamespace,
			)
			backupOpts := &backuprestore.OADPBackupOptions{
				Name:            backupName,
				HCName:          testCtx.ClusterName,
				HCNamespace:     testCtx.ClusterNamespace,
				StorageLocation: testCtx.ClusterName,
				UseEtcdSnapshot: true,
			}
			err := backuprestore.RunOADPBackup(testCtx.Context, GinkgoLogr.WithName("backup-restore"), testCtx.ArtifactDir, backupOpts)
			Expect(err).NotTo(HaveOccurred())

			By("Waiting for backup to complete")
			err = backuprestore.WaitForBackupCompletion(testCtx, backupName)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("VerifyEtcdSnapshotBackup", func() {
		It("should have HCPEtcdBackup with BackupCompleted=True", func() {
			By("Waiting for HCPEtcdBackup BackupCompleted condition to be True")
			err := backuprestore.WaitForHCPEtcdBackupCondition(testCtx, backupName, metav1.ConditionTrue)
			Expect(err).NotTo(HaveOccurred(), "HCPEtcdBackup %s should have BackupCompleted=True", backupName)
		})

		It("should have HCPEtcdBackup with snapshotURL matching the backup created in this run", func() {
			By("Waiting for HCPEtcdBackup to have a snapshotURL")
			Eventually(func(g Gomega) {
				hcpEtcdBackupList := &hyperv1.HCPEtcdBackupList{}
				err := testCtx.MgmtClient.List(testCtx.Context, hcpEtcdBackupList, crclient.InNamespace(testCtx.ControlPlaneNamespace))
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(hcpEtcdBackupList.Items).NotTo(BeEmpty(), "expected at least one HCPEtcdBackup resource")

				found := false
				for _, backup := range hcpEtcdBackupList.Items {
					if backuprestore.MatchesHCPEtcdBackupName(backup.Name, backupName) && backup.Status.SnapshotURL != "" {
						snapshotURL = backup.Status.SnapshotURL
						found = true
						GinkgoWriter.Printf("Found HCPEtcdBackup %s with non-empty snapshotURL\n", backup.Name)
						break
					}
				}
				g.Expect(found).To(BeTrue(), fmt.Sprintf("expected HCPEtcdBackup matching OADP backup %s to have a non-empty snapshotURL", backupName))
			}).WithPolling(backuprestore.PollInterval).WithTimeout(backuprestore.BackupTimeout).Should(Succeed())
		})

		It("should have lastSuccessfulEtcdBackupURL on HostedCluster status matching the snapshot", func() {
			if snapshotURL == "" {
				Skip("snapshotURL was not captured; the snapshotURL verification spec may have failed")
			}
			By("Waiting for HostedCluster lastSuccessfulEtcdBackupURL to match the snapshot")
			Eventually(func(g Gomega) {
				hostedCluster := &hyperv1.HostedCluster{}
				err := testCtx.MgmtClient.Get(testCtx.Context, crclient.ObjectKey{
					Name:      testCtx.ClusterName,
					Namespace: testCtx.ClusterNamespace,
				}, hostedCluster)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(hostedCluster.Status.LastSuccessfulEtcdBackupURL).To(Equal(snapshotURL),
					"expected HostedCluster lastSuccessfulEtcdBackupURL to match the snapshot created in this run")
			}).WithPolling(backuprestore.PollInterval).WithTimeout(backuprestore.BackupTimeout).Should(Succeed())
			GinkgoWriter.Printf("HostedCluster lastSuccessfulEtcdBackupURL matches expected snapshotURL\n")
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
			By("Creating Restore with etcd snapshot options")
			restoreName := oadp.GenerateRestoreName(testCtx.ClusterName, testCtx.ClusterNamespace)
			restoreOpts := &backuprestore.OADPRestoreOptions{
				Name:            restoreName,
				FromBackup:      backupName,
				HCName:          testCtx.ClusterName,
				HCNamespace:     testCtx.ClusterNamespace,
				UseEtcdSnapshot: true,
			}
			executeRestore(testCtx, restoreOpts, platformCfg.postRestoreHook)
		})
	})

	Context(ContextPostRestoreControlPlane, func() {
		It("should have control plane healthy after restore", func() {
			validatePostRestoreControlPlane(testCtx, platformCfg.excludeWorkloads, expectedConditions, false)
		})

		It("should have restoreSnapshotURL set on HostedCluster after restore", func() {
			// RestoreSnapshotURL contains a presigned URL, which differs from the
			// original S3 URL stored in HCPEtcdBackup.Status.SnapshotURL. We verify
			// the field is populated rather than comparing exact values.
			Eventually(func(g Gomega) {
				hostedCluster := &hyperv1.HostedCluster{}
				err := testCtx.MgmtClient.Get(testCtx.Context, crclient.ObjectKey{
					Name:      testCtx.ClusterName,
					Namespace: testCtx.ClusterNamespace,
				}, hostedCluster)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(hostedCluster.Spec.Etcd.Managed).NotTo(BeNil(), "expected managed etcd spec to be set")
				g.Expect(hostedCluster.Spec.Etcd.Managed.Storage.RestoreSnapshotURL).To(HaveLen(1),
					"expected restoreSnapshotURL to contain exactly one entry")
				g.Expect(hostedCluster.Spec.Etcd.Managed.Storage.RestoreSnapshotURL[0]).NotTo(BeEmpty(),
					"expected restoreSnapshotURL to be a non-empty presigned URL")
			}).WithPolling(backuprestore.PollInterval).WithTimeout(backuprestore.RestoreTimeout).Should(Succeed())
			GinkgoWriter.Printf("RestoreSnapshotURL is set on HostedCluster\n")
		})

		It("should have etcd-init container logs showing successful snapshot restore", func() {
			By("Verifying etcd-0 init container logs for snapshot restore traces")
			restConfig, err := util.GetConfig()
			Expect(err).NotTo(HaveOccurred(), "failed to get REST config for pod log access")
			kubeClient, err := kubernetes.NewForConfig(restConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create kubernetes clientset")

			err = backuprestore.VerifyEtcdInitLogs(testCtx.Context, GinkgoLogr.WithName("etcd-init"), kubeClient, testCtx.ControlPlaneNamespace)
			Expect(err).NotTo(HaveOccurred(), "etcd-init container logs should confirm snapshot restore")
		})
	})
})
