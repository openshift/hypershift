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
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/oadp"
	"github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/backuprestore"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:BackupRestore] Velero 1.13 plugin compatibility",
	Label("velero-1-13"), Ordered, Serial, func() {
		var (
			testCtx            *internal.TestContext
			backupName         string
			restoreName        string
			expectedConditions []util.Condition
		)

		BeforeAll(func() {
			testCtx = internal.GetTestContext()
			Expect(testCtx).NotTo(BeNil())
			hc, err := testCtx.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			Expect(hc).NotTo(BeNil())
			if hc.Spec.Platform.Type != hyperv1.AzurePlatform {
				Skip("Velero 1.13 compatibility test requires an Azure hosted cluster")
			}
		})

		AfterAll(func() {
			// Ordered specs need the backup until the restore and recovery checks finish.
			if testCtx == nil {
				return
			}
			if restoreName != "" {
				restore := &unstructured.Unstructured{}
				restore.SetGroupVersionKind(schema.GroupVersionKind{Group: "velero.io", Version: "v1", Kind: "Restore"})
				restore.SetNamespace(backuprestore.DefaultOADPNamespace)
				restore.SetName(restoreName)
				err := testCtx.MgmtClient.Delete(testCtx.Context, restore)
				Expect(err == nil || apierrors.IsNotFound(err)).To(BeTrue(), "failed to delete Restore %s: %v", restoreName, err)
			}
			if backupName != "" {
				backup := &unstructured.Unstructured{}
				backup.SetGroupVersionKind(schema.GroupVersionKind{Group: "velero.io", Version: "v1", Kind: "Backup"})
				if err := testCtx.MgmtClient.Get(testCtx.Context, crclient.ObjectKey{
					Namespace: backuprestore.DefaultOADPNamespace, Name: backupName,
				}, backup); apierrors.IsNotFound(err) {
					return
				} else {
					Expect(err).NotTo(HaveOccurred())
				}
				// Directly deleting a Backup CR can leave Azure disk snapshots behind.
				// Ask Velero to delete the backup and wait for its cleanup to finish.
				request := &unstructured.Unstructured{Object: map[string]interface{}{
					"apiVersion": "velero.io/v1",
					"kind":       "DeleteBackupRequest",
					"metadata": map[string]interface{}{
						"generateName": "velero-1-13-delete-",
						"namespace":    backuprestore.DefaultOADPNamespace,
					},
					"spec": map[string]interface{}{"backupName": backupName},
				}}
				Expect(testCtx.MgmtClient.Create(testCtx.Context, request)).To(Succeed())
				err := wait.PollUntilContextTimeout(testCtx.Context, 10*time.Second, 15*time.Minute, true,
					func(ctx context.Context) (bool, error) {
						current := &unstructured.Unstructured{}
						current.SetGroupVersionKind(schema.GroupVersionKind{Group: "velero.io", Version: "v1", Kind: "DeleteBackupRequest"})
						if err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(request), current); err != nil {
							return false, err
						}
						errors, _, err := unstructured.NestedStringSlice(current.Object, "status", "errors")
						if err != nil {
							return false, err
						}
						if len(errors) > 0 {
							return false, fmt.Errorf("Velero reported backup deletion errors: %v", errors)
						}
						phase, _, err := unstructured.NestedString(current.Object, "status", "phase")
						return phase == "Processed", err
					})
				Expect(err).NotTo(HaveOccurred(), "failed to finish deletion of Backup %s", backupName)
			}
		})

		It("should load the HyperShift plugin in Velero 1.13 and have a healthy control plane", func() {
			By("checking the Velero version and plugin image")
			deployment := &appsv1.Deployment{}
			Expect(testCtx.MgmtClient.Get(testCtx.Context, crclient.ObjectKey{
				Namespace: backuprestore.DefaultOADPNamespace, Name: "velero",
			}, deployment)).To(Succeed())
			var veleroImage string
			for _, container := range deployment.Spec.Template.Spec.Containers {
				if container.Name == "velero" {
					veleroImage = container.Image
					break
				}
			}
			Expect(veleroImage).To(MatchRegexp(`(?:^|/)velero:v1\.13\.[0-9]+$`), "Velero deployment must run the 1.13 server")
			Expect(backuprestore.EnsureVeleroPodRunning(testCtx)).To(Succeed())
			pluginImage, err := backuprestore.GetReadyHypershiftPluginImage(testCtx)
			Expect(err).NotTo(HaveOccurred())
			assertExpectedPluginImage(pluginImage)

			By("waiting for Azure Blob storage to become available")
			Expect(backuprestore.WaitForBackupStorageLocationAvailable(testCtx, "default")).To(Succeed())
			expectedConditions = validatePreBackupControlPlane(testCtx, backupRestorePlatforms[hyperv1.AzurePlatform].excludeWorkloads)
		})

		It("should back up the Azure hosted cluster and its volumes", func() {
			backupName = oadp.GenerateBackupName(testCtx.ClusterName, testCtx.ClusterNamespace)
			// Generate the complete Azure backup used by the HyperShift CLI. Its RunBackup
			// path requires OADP+DPA, while this suite uses standalone Velero.
			backupOpts := &oadp.CreateOptions{
				HCName: testCtx.ClusterName, HCNamespace: testCtx.ClusterNamespace,
				BackupCustomName: backupName, OADPNamespace: backuprestore.DefaultOADPNamespace,
				StorageLocation: "default", TTL: 4 * time.Hour,
			}
			backup, resourcePolicy, err := backupOpts.GenerateBackupObject("Azure")
			Expect(err).NotTo(HaveOccurred())
			Expect(resourcePolicy).To(BeNil())
			By("creating a Velero backup that includes Azure volume snapshots")
			Expect(testCtx.MgmtClient.Create(testCtx.Context, backup)).To(Succeed())

			Expect(backuprestore.WaitForBackupCompletion(testCtx, backupName)).To(Succeed())
			completed := &unstructured.Unstructured{}
			completed.SetGroupVersionKind(schema.GroupVersionKind{Group: "velero.io", Version: "v1", Kind: "Backup"})
			Expect(testCtx.MgmtClient.Get(testCtx.Context, crclient.ObjectKeyFromObject(backup), completed)).To(Succeed())
			itemsBackedUp, found, err := unstructured.NestedInt64(completed.Object, "status", "progress", "itemsBackedUp")
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeTrue(), "Velero should report backed-up items")
			Expect(itemsBackedUp).To(BeNumerically(">=", 2), "Velero should back up multiple hosted cluster resources")
			snapshotsCompleted, found, err := unstructured.NestedInt64(completed.Object, "status", "volumeSnapshotsCompleted")
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeTrue(), "Velero should report completed Azure volume snapshots")
			Expect(snapshotsCompleted).To(BeNumerically(">", 0), "backup must capture a control plane volume")

			By("checking the archived HostedCluster for the plugin's restore annotation")
			archive, err := backuprestore.DownloadBackupContents(testCtx.Context, testCtx.MgmtClient,
				backuprestore.DefaultOADPNamespace, backupName)
			Expect(err).NotTo(HaveOccurred())
			defer archive.Close()
			annotated, err := backuprestore.BackupArchiveHasHostedClusterRestoreAnnotation(archive,
				testCtx.ClusterNamespace, testCtx.ClusterName)
			Expect(err).NotTo(HaveOccurred())
			Expect(annotated).To(BeTrue(), fmt.Sprintf("HyperShift plugin did not annotate HostedCluster %s/%s in backup %s",
				testCtx.ClusterNamespace, testCtx.ClusterName, backupName))
		})

		It("should remove the hosted control plane while preserving its machines", func() {
			Expect(backuprestore.BreakHostedClusterPreservingMachines(testCtx, GinkgoLogr.WithName("velero-1-13"))).To(Succeed())
		})

		It("should restore the hosted cluster with Velero 1.13", func() {
			restoreOpts := &oadp.CreateOptions{
				HCName: testCtx.ClusterName, HCNamespace: testCtx.ClusterNamespace,
				BackupName: backupName, OADPNamespace: backuprestore.DefaultOADPNamespace,
			}
			restore, name, err := restoreOpts.GenerateRestoreObject()
			Expect(err).NotTo(HaveOccurred())
			restoreName = name
			Expect(testCtx.MgmtClient.Create(testCtx.Context, restore)).To(Succeed())
			Expect(backuprestore.WaitForRestoreCompletion(testCtx, restoreName)).To(Succeed())
		})

		It("should recover the control plane and report completed plugin recovery", func() {
			validatePostRestoreControlPlane(testCtx, backupRestorePlatforms[hyperv1.AzurePlatform].excludeWorkloads, expectedConditions, false)
			Eventually(func(g Gomega) {
				hc, err := testCtx.GetHostedCluster()
				g.Expect(err).NotTo(HaveOccurred())
				condition := meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.HostedClusterRestoredFromBackup))
				g.Expect(condition).NotTo(BeNil(), "expected HostedClusterRestoredFromBackup condition on %s/%s", hc.Namespace, hc.Name)
				if condition == nil {
					return
				}
				g.Expect(condition.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(condition.Reason).To(Equal(hyperv1.RecoveryFinishedReason))
			}).WithPolling(backuprestore.PollInterval).WithTimeout(backuprestore.RestoreTimeout).Should(Succeed())
		})
	})
