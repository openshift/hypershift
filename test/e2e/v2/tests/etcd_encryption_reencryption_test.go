//go:build e2ev2

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
	"crypto/rand"
	"fmt"
	"io"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:EtcdEncryptionReencryption] Etcd Encryption Re-encryption",
	Label("etcd-encryption-reencryption"), func() {
		var testCtx *internal.TestContext

		BeforeEach(func() {
			testCtx = internal.GetTestContext()
			Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")

			hc := testCtx.GetHostedCluster()
			if hc == nil || hc.Spec.SecretEncryption == nil {
				Skip("SecretEncryption is not configured on this hosted cluster")
			}
		})

		RegisterEtcdEncryptionReencryptionTests(func() *internal.TestContext { return testCtx })
	})

// RegisterEtcdEncryptionReencryptionTests registers all re-encryption lifecycle tests.
func RegisterEtcdEncryptionReencryptionTests(getTestCtx internal.TestContextGetter) {
	AWSKMSKeyRotationTest(getTestCtx)
	AzureKMSKeyRotationTest(getTestCtx)
	AzureKMSConsecutiveKeyRotationTest(getTestCtx)
	AESCBCKeyRotationTest(getTestCtx)
	ConditionBubbleUpTest(getTestCtx)
}

func waitForReEncryptionStarted(ctx context.Context, mgmtClient crclient.Client, hcKey crclient.ObjectKey, priorHistoryLen int, timeout time.Duration) {
	Eventually(func(g Gomega) {
		hc := &hyperv1.HostedCluster{}
		g.Expect(mgmtClient.Get(ctx, hcKey, hc)).To(Succeed())
		g.Expect(meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.EtcdDataEncryptionUpToDate))).
			NotTo(BeNil(), "EtcdDataEncryptionUpToDate condition should exist after the key rotation patch")
		g.Expect(len(hc.Status.SecretEncryption.History)).To(BeNumerically(">", priorHistoryLen),
			"a new migration history entry should be recorded after the key rotation patch")
	}, timeout, 5*time.Second).Should(Succeed())
}

func waitForReEncryptionComplete(ctx context.Context, mgmtClient crclient.Client, hcKey crclient.ObjectKey, timeout time.Duration) {
	Eventually(func(g Gomega) {
		hc := &hyperv1.HostedCluster{}
		g.Expect(mgmtClient.Get(ctx, hcKey, hc)).To(Succeed())
		cond := meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.EtcdDataEncryptionUpToDate))
		g.Expect(cond).NotTo(BeNil(), "EtcdDataEncryptionUpToDate condition should exist")
		g.Expect(cond.Status).To(Equal(metav1.ConditionTrue),
			"EtcdDataEncryptionUpToDate should be True, got reason=%s message=%s", cond.Reason, cond.Message)
	}, timeout, 15*time.Second).Should(Succeed())
}

func verifyReEncryptionStatus(hc *hyperv1.HostedCluster, expectedProvider hyperv1.SecretEncryptionProvider) {
	Expect(hc.Status.SecretEncryption.ActiveKey.Provider).To(Equal(expectedProvider),
		"activeKey provider should be %s", expectedProvider)
	Expect(hc.Status.SecretEncryption.TargetKey.Provider).To(BeEmpty(),
		"targetKey should be empty after completed rotation")
	Expect(hc.Status.SecretEncryption.History).NotTo(BeEmpty(),
		"history should have at least one entry")
	Expect(hc.Status.SecretEncryption.History[0].State).To(Equal(hyperv1.EncryptionMigrationStateCompleted),
		"most recent history entry should be Completed")
	Expect(hc.Status.SecretEncryption.History[0].From.Provider).To(Equal(expectedProvider),
		"history[0].from.provider should be %s", expectedProvider)
	Expect(hc.Status.SecretEncryption.History[0].To.Provider).To(Equal(expectedProvider),
		"history[0].to.provider should be %s", expectedProvider)
	Expect(hc.Status.SecretEncryption.History[0].StartedTime.IsZero()).To(BeFalse(),
		"history[0].startedTime should be set")
	Expect(hc.Status.SecretEncryption.History[0].CompletionTime.IsZero()).To(BeFalse(),
		"history[0].completionTime should be set")
}

func listStorageVersionMigrations(ctx context.Context, guestClient crclient.Client) (*unstructured.UnstructuredList, error) {
	svmList := &unstructured.UnstructuredList{}
	svmList.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "migration.k8s.io",
		Version: "v1alpha1",
		Kind:    "StorageVersionMigrationList",
	})
	err := guestClient.List(ctx, svmList)
	return svmList, err
}

func verifyKASHealth(ctx context.Context, mgmtClient crclient.Client, controlPlaneNamespace string) {
	Eventually(func(g Gomega) {
		kasPods := &corev1.PodList{}
		g.Expect(mgmtClient.List(ctx, kasPods,
			crclient.InNamespace(controlPlaneNamespace),
			crclient.MatchingLabels{"app": "kube-apiserver"},
		)).To(Succeed())
		g.Expect(kasPods.Items).NotTo(BeEmpty(),
			"expected at least one kube-apiserver pod in namespace %s", controlPlaneNamespace)

		for _, pod := range kasPods.Items {
			g.Expect(pod.Status.Phase).To(Equal(corev1.PodRunning),
				"KAS pod %s should be Running, got %s", pod.Name, pod.Status.Phase)
			for _, cs := range pod.Status.ContainerStatuses {
				if cs.State.Waiting != nil {
					g.Expect(cs.State.Waiting.Reason).NotTo(Equal("CrashLoopBackOff"),
						"KAS pod %s container %s is in CrashLoopBackOff", pod.Name, cs.Name)
				}
				g.Expect(cs.RestartCount).To(BeNumerically("<=", int32(2)),
					"KAS pod %s container %s has %d restarts", pod.Name, cs.Name, cs.RestartCount)
			}
		}
	}, 5*time.Minute, 15*time.Second).Should(Succeed())
}

func verifyKASLogsNoDecryptionErrors(ctx context.Context, controlPlaneNamespace string) {
	mgmtRestConfig, err := e2eutil.GetConfig()
	Expect(err).NotTo(HaveOccurred(), "failed to get management cluster REST config")
	mgmtKubeClient, err := kubernetes.NewForConfig(mgmtRestConfig)
	Expect(err).NotTo(HaveOccurred(), "failed to create management cluster kubernetes clientset")

	kasPods, err := mgmtKubeClient.CoreV1().Pods(controlPlaneNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=kube-apiserver",
	})
	Expect(err).NotTo(HaveOccurred(), "failed to list KAS pods")
	Expect(kasPods.Items).NotTo(BeEmpty(),
		"expected at least one kube-apiserver pod in namespace %s", controlPlaneNamespace)

	for _, pod := range kasPods.Items {
		req := mgmtKubeClient.CoreV1().Pods(controlPlaneNamespace).GetLogs(pod.Name, &corev1.PodLogOptions{
			Container: "kube-apiserver",
			TailLines: int64Ptr(500),
		})
		logStream, err := req.Stream(ctx)
		if err != nil {
			GinkgoWriter.Printf("WARNING: failed to get logs for KAS pod %s: %v\n", pod.Name, err)
			continue
		}
		logBytes, err := io.ReadAll(logStream)
		logStream.Close()
		if err != nil {
			GinkgoWriter.Printf("WARNING: failed to read logs for KAS pod %s: %v\n", pod.Name, err)
			continue
		}
		logContent := string(logBytes)
		Expect(logContent).NotTo(ContainSubstring("no matching prefix found"),
			"KAS pod %s logs contain decryption error 'no matching prefix found'", pod.Name)
	}
}

func int64Ptr(i int64) *int64 {
	return &i
}

// AWSKMSKeyRotationTest validates the re-encryption lifecycle after rotating the AWS KMS active key.
func AWSKMSKeyRotationTest(getTestCtx internal.TestContextGetter) {
	Context("[Feature:AWSKMSReencryption] AWS KMS Key Rotation", func() {
		BeforeEach(func() {
			tc := getTestCtx()
			hc := tc.GetHostedCluster()
			if hc.Spec.Platform.Type != hyperv1.AWSPlatform ||
				hc.Spec.SecretEncryption.Type != hyperv1.KMS ||
				hc.Spec.SecretEncryption.KMS == nil ||
				hc.Spec.SecretEncryption.KMS.AWS == nil {
				Skip("AWS KMS key rotation test requires AWS platform with KMS configured")
			}
			alternateARN := internal.GetEnvVarValue("E2E_AWS_KMS_KEY_ARN_ALTERNATE")
			if alternateARN == "" {
				Skip("E2E_AWS_KMS_KEY_ARN_ALTERNATE is not set")
			}
		})

		It("should re-encrypt all etcd data after active key rotation", func() {
			tc := getTestCtx()
			ctx := tc.Context
			hcKey := crclient.ObjectKey{Namespace: tc.ClusterNamespace, Name: tc.ClusterName}

			hc := &hyperv1.HostedCluster{}
			Expect(tc.MgmtClient.Get(ctx, hcKey, hc)).To(Succeed())
			originalARN := hc.Spec.SecretEncryption.KMS.AWS.ActiveKey.ARN

			waitForReEncryptionComplete(ctx, tc.MgmtClient, hcKey, 5*time.Minute)

			hostedClusterClient := tc.GetHostedClusterClient()
			Expect(hostedClusterClient).NotTo(BeNil(), "hosted cluster client is required")

			testSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "e2e-reencryption-test-aws",
					Namespace: "default",
				},
				Type: corev1.SecretTypeOpaque,
				Data: map[string][]byte{
					"testKey": []byte("aws-kms-reencryption-test-data"),
				},
			}
			Expect(hostedClusterClient.Create(ctx, testSecret)).To(Succeed())
			DeferCleanup(func() {
				if err := hostedClusterClient.Delete(context.Background(), testSecret); err != nil && !apierrors.IsNotFound(err) {
					GinkgoWriter.Printf("WARNING: failed to cleanup test secret: %v\n", err)
				}
			})

			alternateARN := internal.GetEnvVarValue("E2E_AWS_KMS_KEY_ARN_ALTERNATE")
			priorHistoryLen := len(hc.Status.SecretEncryption.History)
			patch := []byte(fmt.Sprintf(`{"spec":{"secretEncryption":{"kms":{"aws":{"activeKey":{"arn":%q}}}}}}`, alternateARN))
			Expect(tc.MgmtClient.Patch(ctx, hc, crclient.RawPatch(types.MergePatchType, patch))).To(Succeed())

			DeferCleanup(func() {
				restorePatch := []byte(fmt.Sprintf(`{"spec":{"secretEncryption":{"kms":{"aws":{"activeKey":{"arn":%q}}}}}}`, originalARN))
				hcRestore := &hyperv1.HostedCluster{
					ObjectMeta: metav1.ObjectMeta{Name: tc.ClusterName, Namespace: tc.ClusterNamespace},
				}
				if err := tc.MgmtClient.Patch(context.Background(), hcRestore, crclient.RawPatch(types.MergePatchType, restorePatch)); err != nil {
					GinkgoWriter.Printf("WARNING: failed to restore original KMS key ARN: %v\n", err)
					return
				}
				waitForReEncryptionComplete(context.Background(), tc.MgmtClient, hcKey, 25*time.Minute)
			})

			waitForReEncryptionStarted(ctx, tc.MgmtClient, hcKey, priorHistoryLen, 5*time.Minute)
			waitForReEncryptionComplete(ctx, tc.MgmtClient, hcKey, 25*time.Minute)

			Expect(tc.MgmtClient.Get(ctx, hcKey, hc)).To(Succeed())
			verifyReEncryptionStatus(hc, hyperv1.SecretEncryptionProviderAWS)
			Expect(hc.Status.SecretEncryption.ActiveKey.AWS.ARN).To(Equal(alternateARN),
				"activeKey ARN should match the alternate key")

			readBack := &corev1.Secret{}
			Expect(hostedClusterClient.Get(ctx, crclient.ObjectKeyFromObject(testSecret), readBack)).To(Succeed())
			Expect(readBack.Data["testKey"]).To(Equal([]byte("aws-kms-reencryption-test-data")),
				"test secret data should be readable after re-encryption")

			svmList, err := listStorageVersionMigrations(ctx, hostedClusterClient)
			Expect(err).NotTo(HaveOccurred(), "failed to list StorageVersionMigration CRs")
			Expect(svmList.Items).NotTo(BeEmpty(),
				"StorageVersionMigration CRs should exist after KMS key rotation")

			verifyKASHealth(ctx, tc.MgmtClient, tc.ControlPlaneNamespace)
			verifyKASLogsNoDecryptionErrors(ctx, tc.ControlPlaneNamespace)

			verifyConditionBubbleUp(ctx, tc.MgmtClient, hcKey, tc.ControlPlaneNamespace)
		})
	})
}

// AzureKMSKeyRotationTest validates the re-encryption lifecycle after rotating the Azure KMS key version.
func AzureKMSKeyRotationTest(getTestCtx internal.TestContextGetter) {
	Context("[Feature:AzureKMSReencryption] Azure KMS Key Rotation", func() {
		BeforeEach(func() {
			tc := getTestCtx()
			hc := tc.GetHostedCluster()
			if hc.Spec.Platform.Type != hyperv1.AzurePlatform ||
				hc.Spec.SecretEncryption.Type != hyperv1.KMS ||
				hc.Spec.SecretEncryption.KMS == nil ||
				hc.Spec.SecretEncryption.KMS.Azure == nil {
				Skip("Azure KMS key rotation test requires Azure platform with KMS configured")
			}
			alternateVersion := internal.GetEnvVarValue("E2E_AZURE_KMS_KEY_VERSION_ALTERNATE")
			if alternateVersion == "" {
				Skip("E2E_AZURE_KMS_KEY_VERSION_ALTERNATE is not set")
			}
		})

		It("should re-encrypt all etcd data after key version rotation", func() {
			tc := getTestCtx()
			ctx := tc.Context
			hcKey := crclient.ObjectKey{Namespace: tc.ClusterNamespace, Name: tc.ClusterName}

			hc := &hyperv1.HostedCluster{}
			Expect(tc.MgmtClient.Get(ctx, hcKey, hc)).To(Succeed())
			originalVersion := hc.Spec.SecretEncryption.KMS.Azure.ActiveKey.KeyVersion

			waitForReEncryptionComplete(ctx, tc.MgmtClient, hcKey, 5*time.Minute)

			hostedClusterClient := tc.GetHostedClusterClient()
			Expect(hostedClusterClient).NotTo(BeNil(), "hosted cluster client is required")

			testSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "e2e-reencryption-test-azure",
					Namespace: "default",
				},
				Type: corev1.SecretTypeOpaque,
				Data: map[string][]byte{
					"testKey": []byte("azure-kms-reencryption-test-data"),
				},
			}
			Expect(hostedClusterClient.Create(ctx, testSecret)).To(Succeed())
			DeferCleanup(func() {
				if err := hostedClusterClient.Delete(context.Background(), testSecret); err != nil && !apierrors.IsNotFound(err) {
					GinkgoWriter.Printf("WARNING: failed to cleanup test secret: %v\n", err)
				}
			})

			alternateVersion := internal.GetEnvVarValue("E2E_AZURE_KMS_KEY_VERSION_ALTERNATE")
			priorHistoryLen := len(hc.Status.SecretEncryption.History)
			patch := []byte(fmt.Sprintf(`{"spec":{"secretEncryption":{"kms":{"azure":{"activeKey":{"keyVersion":%q}}}}}}`, alternateVersion))
			Expect(tc.MgmtClient.Patch(ctx, hc, crclient.RawPatch(types.MergePatchType, patch))).To(Succeed())

			DeferCleanup(func() {
				restorePatch := []byte(fmt.Sprintf(`{"spec":{"secretEncryption":{"kms":{"azure":{"activeKey":{"keyVersion":%q}}}}}}`, originalVersion))
				hcRestore := &hyperv1.HostedCluster{
					ObjectMeta: metav1.ObjectMeta{Name: tc.ClusterName, Namespace: tc.ClusterNamespace},
				}
				if err := tc.MgmtClient.Patch(context.Background(), hcRestore, crclient.RawPatch(types.MergePatchType, restorePatch)); err != nil {
					GinkgoWriter.Printf("WARNING: failed to restore original Azure key version: %v\n", err)
					return
				}
				waitForReEncryptionComplete(context.Background(), tc.MgmtClient, hcKey, 25*time.Minute)
			})

			waitForReEncryptionStarted(ctx, tc.MgmtClient, hcKey, priorHistoryLen, 5*time.Minute)
			waitForReEncryptionComplete(ctx, tc.MgmtClient, hcKey, 25*time.Minute)

			Expect(tc.MgmtClient.Get(ctx, hcKey, hc)).To(Succeed())
			verifyReEncryptionStatus(hc, hyperv1.SecretEncryptionProviderAzure)
			Expect(hc.Status.SecretEncryption.ActiveKey.Azure.KeyVersion).To(Equal(alternateVersion),
				"activeKey keyVersion should match the alternate version")

			readBack := &corev1.Secret{}
			Expect(hostedClusterClient.Get(ctx, crclient.ObjectKeyFromObject(testSecret), readBack)).To(Succeed())
			Expect(readBack.Data["testKey"]).To(Equal([]byte("azure-kms-reencryption-test-data")),
				"test secret data should be readable after re-encryption")

			svmList, err := listStorageVersionMigrations(ctx, hostedClusterClient)
			Expect(err).NotTo(HaveOccurred(), "failed to list StorageVersionMigration CRs")
			Expect(svmList.Items).NotTo(BeEmpty(),
				"StorageVersionMigration CRs should exist after Azure KMS key rotation")

			verifyKASHealth(ctx, tc.MgmtClient, tc.ControlPlaneNamespace)
			verifyKASLogsNoDecryptionErrors(ctx, tc.ControlPlaneNamespace)

			verifyConditionBubbleUp(ctx, tc.MgmtClient, hcKey, tc.ControlPlaneNamespace)
		})
	})
}

// AzureKMSConsecutiveKeyRotationTest validates that two back-to-back key rotations
// complete successfully, exercising the mid-rotation handling code path.
func AzureKMSConsecutiveKeyRotationTest(getTestCtx internal.TestContextGetter) {
	Context("[Feature:AzureKMSReencryption] Azure KMS Consecutive Key Rotation", func() {
		BeforeEach(func() {
			tc := getTestCtx()
			hc := tc.GetHostedCluster()
			if hc.Spec.Platform.Type != hyperv1.AzurePlatform ||
				hc.Spec.SecretEncryption.Type != hyperv1.KMS ||
				hc.Spec.SecretEncryption.KMS == nil ||
				hc.Spec.SecretEncryption.KMS.Azure == nil {
				Skip("Azure KMS consecutive key rotation test requires Azure platform with KMS configured")
			}
			alternateVersion := internal.GetEnvVarValue("E2E_AZURE_KMS_KEY_VERSION_ALTERNATE")
			if alternateVersion == "" {
				Skip("E2E_AZURE_KMS_KEY_VERSION_ALTERNATE is not set")
			}
		})

		It("should complete two consecutive key rotations successfully", func() {
			tc := getTestCtx()
			ctx := tc.Context
			hcKey := crclient.ObjectKey{Namespace: tc.ClusterNamespace, Name: tc.ClusterName}

			hc := &hyperv1.HostedCluster{}
			Expect(tc.MgmtClient.Get(ctx, hcKey, hc)).To(Succeed())
			originalVersion := hc.Spec.SecretEncryption.KMS.Azure.ActiveKey.KeyVersion

			waitForReEncryptionComplete(ctx, tc.MgmtClient, hcKey, 5*time.Minute)

			hostedClusterClient := tc.GetHostedClusterClient()
			Expect(hostedClusterClient).NotTo(BeNil(), "hosted cluster client is required")

			testSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "e2e-reencryption-test-azure-consecutive",
					Namespace: "default",
				},
				Type: corev1.SecretTypeOpaque,
				Data: map[string][]byte{
					"testKey": []byte("azure-kms-consecutive-rotation-test-data"),
				},
			}
			Expect(hostedClusterClient.Create(ctx, testSecret)).To(Succeed())
			DeferCleanup(func() {
				if err := hostedClusterClient.Delete(context.Background(), testSecret); err != nil && !apierrors.IsNotFound(err) {
					GinkgoWriter.Printf("WARNING: failed to cleanup test secret: %v\n", err)
				}
			})

			DeferCleanup(func() {
				restorePatch := []byte(fmt.Sprintf(`{"spec":{"secretEncryption":{"kms":{"azure":{"activeKey":{"keyVersion":%q}}}}}}`, originalVersion))
				hcRestore := &hyperv1.HostedCluster{
					ObjectMeta: metav1.ObjectMeta{Name: tc.ClusterName, Namespace: tc.ClusterNamespace},
				}
				if err := tc.MgmtClient.Patch(context.Background(), hcRestore, crclient.RawPatch(types.MergePatchType, restorePatch)); err != nil {
					GinkgoWriter.Printf("WARNING: failed to restore original Azure key version: %v\n", err)
					return
				}
				waitForReEncryptionComplete(context.Background(), tc.MgmtClient, hcKey, 25*time.Minute)
			})

			alternateVersion := internal.GetEnvVarValue("E2E_AZURE_KMS_KEY_VERSION_ALTERNATE")

			By("Performing first key rotation: original -> alternate")
			priorHistoryLen := len(hc.Status.SecretEncryption.History)
			patch1 := []byte(fmt.Sprintf(`{"spec":{"secretEncryption":{"kms":{"azure":{"activeKey":{"keyVersion":%q}}}}}}`, alternateVersion))
			Expect(tc.MgmtClient.Patch(ctx, hc, crclient.RawPatch(types.MergePatchType, patch1))).To(Succeed())
			waitForReEncryptionStarted(ctx, tc.MgmtClient, hcKey, priorHistoryLen, 5*time.Minute)
			waitForReEncryptionComplete(ctx, tc.MgmtClient, hcKey, 25*time.Minute)

			Expect(tc.MgmtClient.Get(ctx, hcKey, hc)).To(Succeed())
			Expect(hc.Status.SecretEncryption.ActiveKey.Azure.KeyVersion).To(Equal(alternateVersion),
				"activeKey should be the alternate version after first rotation")
			Expect(hc.Status.SecretEncryption.History).NotTo(BeEmpty(),
				"history should have at least one entry after first rotation")

			verifyConditionBubbleUp(ctx, tc.MgmtClient, hcKey, tc.ControlPlaneNamespace)

			By("Performing second key rotation: alternate -> original")
			priorHistoryLen = len(hc.Status.SecretEncryption.History)
			patch2 := []byte(fmt.Sprintf(`{"spec":{"secretEncryption":{"kms":{"azure":{"activeKey":{"keyVersion":%q}}}}}}`, originalVersion))
			Expect(tc.MgmtClient.Patch(ctx, hc, crclient.RawPatch(types.MergePatchType, patch2))).To(Succeed())
			waitForReEncryptionStarted(ctx, tc.MgmtClient, hcKey, priorHistoryLen, 5*time.Minute)
			waitForReEncryptionComplete(ctx, tc.MgmtClient, hcKey, 25*time.Minute)

			Expect(tc.MgmtClient.Get(ctx, hcKey, hc)).To(Succeed())
			verifyReEncryptionStatus(hc, hyperv1.SecretEncryptionProviderAzure)
			Expect(hc.Status.SecretEncryption.ActiveKey.Azure.KeyVersion).To(Equal(originalVersion),
				"activeKey should be back to the original version after second rotation")
			Expect(len(hc.Status.SecretEncryption.History)).To(BeNumerically(">=", 2),
				"history should have at least two entries after consecutive rotations")
			Expect(hc.Status.SecretEncryption.History[0].State).To(Equal(hyperv1.EncryptionMigrationStateCompleted),
				"most recent history entry should be Completed")
			Expect(hc.Status.SecretEncryption.History[1].State).To(Equal(hyperv1.EncryptionMigrationStateCompleted),
				"second history entry should also be Completed")

			readBack := &corev1.Secret{}
			Expect(hostedClusterClient.Get(ctx, crclient.ObjectKeyFromObject(testSecret), readBack)).To(Succeed())
			Expect(readBack.Data["testKey"]).To(Equal([]byte("azure-kms-consecutive-rotation-test-data")),
				"test secret data should be readable after two consecutive re-encryptions")

			verifyKASHealth(ctx, tc.MgmtClient, tc.ControlPlaneNamespace)
			verifyKASLogsNoDecryptionErrors(ctx, tc.ControlPlaneNamespace)

			verifyConditionBubbleUp(ctx, tc.MgmtClient, hcKey, tc.ControlPlaneNamespace)
		})
	})
}

// AESCBCKeyRotationTest validates the re-encryption lifecycle after rotating the AESCBC encryption key.
func AESCBCKeyRotationTest(getTestCtx internal.TestContextGetter) {
	Context("[Feature:AESCBCReencryption] AESCBC Key Rotation", func() {
		BeforeEach(func() {
			tc := getTestCtx()
			hc := tc.GetHostedCluster()
			if hc.Spec.SecretEncryption.Type != hyperv1.AESCBC ||
				hc.Spec.SecretEncryption.AESCBC == nil {
				Skip("AESCBC key rotation test requires AESCBC encryption configured")
			}
		})

		It("should re-encrypt secrets after active key rotation", func() {
			tc := getTestCtx()
			ctx := tc.Context
			hcKey := crclient.ObjectKey{Namespace: tc.ClusterNamespace, Name: tc.ClusterName}

			hc := &hyperv1.HostedCluster{}
			Expect(tc.MgmtClient.Get(ctx, hcKey, hc)).To(Succeed())
			originalKeyRef := hc.Spec.SecretEncryption.AESCBC.ActiveKey.Name

			waitForReEncryptionComplete(ctx, tc.MgmtClient, hcKey, 5*time.Minute)

			hostedClusterClient := tc.GetHostedClusterClient()
			Expect(hostedClusterClient).NotTo(BeNil(), "hosted cluster client is required")

			testSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "e2e-reencryption-test-aescbc",
					Namespace: "default",
				},
				Type: corev1.SecretTypeOpaque,
				Data: map[string][]byte{
					"testKey": []byte("aescbc-reencryption-test-data"),
				},
			}
			Expect(hostedClusterClient.Create(ctx, testSecret)).To(Succeed())
			DeferCleanup(func() {
				if err := hostedClusterClient.Delete(context.Background(), testSecret); err != nil && !apierrors.IsNotFound(err) {
					GinkgoWriter.Printf("WARNING: failed to cleanup test secret: %v\n", err)
				}
			})

			keyData := make([]byte, 32)
			_, err := rand.Read(keyData)
			Expect(err).NotTo(HaveOccurred(), "failed to generate random key data")

			newKeySecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "e2e-aescbc-rotation-key",
					Namespace: tc.ClusterNamespace,
				},
				Type: corev1.SecretTypeOpaque,
				Data: map[string][]byte{
					hyperv1.AESCBCKeySecretKey: keyData,
				},
			}
			Expect(tc.MgmtClient.Create(ctx, newKeySecret)).To(Succeed())
			DeferCleanup(func() {
				if err := tc.MgmtClient.Delete(context.Background(), newKeySecret); err != nil && !apierrors.IsNotFound(err) {
					GinkgoWriter.Printf("WARNING: failed to cleanup AESCBC key secret: %v\n", err)
				}
			})

			priorHistoryLen := len(hc.Status.SecretEncryption.History)
			patch := []byte(fmt.Sprintf(`{"spec":{"secretEncryption":{"aescbc":{"activeKey":{"name":%q}}}}}`, newKeySecret.Name))
			Expect(tc.MgmtClient.Patch(ctx, hc, crclient.RawPatch(types.MergePatchType, patch))).To(Succeed())

			DeferCleanup(func() {
				restorePatch := []byte(fmt.Sprintf(`{"spec":{"secretEncryption":{"aescbc":{"activeKey":{"name":%q}}}}}`, originalKeyRef))
				hcRestore := &hyperv1.HostedCluster{
					ObjectMeta: metav1.ObjectMeta{Name: tc.ClusterName, Namespace: tc.ClusterNamespace},
				}
				if err := tc.MgmtClient.Patch(context.Background(), hcRestore, crclient.RawPatch(types.MergePatchType, restorePatch)); err != nil {
					GinkgoWriter.Printf("WARNING: failed to restore original AESCBC key: %v\n", err)
					return
				}
				waitForReEncryptionComplete(context.Background(), tc.MgmtClient, hcKey, 25*time.Minute)
			})

			waitForReEncryptionStarted(ctx, tc.MgmtClient, hcKey, priorHistoryLen, 5*time.Minute)
			waitForReEncryptionComplete(ctx, tc.MgmtClient, hcKey, 25*time.Minute)

			Expect(tc.MgmtClient.Get(ctx, hcKey, hc)).To(Succeed())
			verifyReEncryptionStatus(hc, hyperv1.SecretEncryptionProviderAESCBC)
			Expect(hc.Status.SecretEncryption.ActiveKey.AESCBC.Secret.Name).To(Equal(newKeySecret.Name),
				"activeKey AESCBC secret should reference the new key")

			readBack := &corev1.Secret{}
			Expect(hostedClusterClient.Get(ctx, crclient.ObjectKeyFromObject(testSecret), readBack)).To(Succeed())
			Expect(readBack.Data["testKey"]).To(Equal([]byte("aescbc-reencryption-test-data")),
				"test secret data should be readable after re-encryption")

			svmList, err := listStorageVersionMigrations(ctx, hostedClusterClient)
			Expect(err).NotTo(HaveOccurred(), "failed to list StorageVersionMigration CRs")
			Expect(svmList.Items).NotTo(BeEmpty(),
				"StorageVersionMigration CRs should exist after AESCBC key rotation")

			verifyKASHealth(ctx, tc.MgmtClient, tc.ControlPlaneNamespace)
			verifyKASLogsNoDecryptionErrors(ctx, tc.ControlPlaneNamespace)

			verifyConditionBubbleUp(ctx, tc.MgmtClient, hcKey, tc.ControlPlaneNamespace)
		})
	})
}

func verifyConditionBubbleUp(ctx context.Context, mgmtClient crclient.Client, hcKey crclient.ObjectKey, controlPlaneNamespace string) {
	hc := &hyperv1.HostedCluster{}
	Expect(mgmtClient.Get(ctx, hcKey, hc)).To(Succeed())

	hcp := &hyperv1.HostedControlPlane{}
	Expect(mgmtClient.Get(ctx, crclient.ObjectKey{
		Name:      hc.Name,
		Namespace: controlPlaneNamespace,
	}, hcp)).To(Succeed())

	hcCond := meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.EtcdDataEncryptionUpToDate))
	hcpCond := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.EtcdDataEncryptionUpToDate))

	Expect(hcCond).NotTo(BeNil(),
		"HostedCluster %s/%s should have EtcdDataEncryptionUpToDate condition", hcKey.Namespace, hcKey.Name)
	Expect(hcpCond).NotTo(BeNil(),
		"HostedControlPlane should have EtcdDataEncryptionUpToDate condition if HostedCluster does")
	Expect(hcCond.Status).To(Equal(hcpCond.Status),
		"EtcdDataEncryptionUpToDate status should match between HostedCluster and HostedControlPlane")
	Expect(hcCond.Reason).To(Equal(hcpCond.Reason),
		"EtcdDataEncryptionUpToDate reason should match between HostedCluster and HostedControlPlane")
}

// ConditionBubbleUpTest verifies that the EtcdDataEncryptionUpToDate condition
// is consistent between HostedControlPlane and HostedCluster.
func ConditionBubbleUpTest(getTestCtx internal.TestContextGetter) {
	Context("Condition Bubble-Up", func() {
		It("should have matching EtcdDataEncryptionUpToDate on HCP and HostedCluster", func() {
			tc := getTestCtx()
			hcKey := crclient.ObjectKey{Namespace: tc.ClusterNamespace, Name: tc.ClusterName}
			Eventually(func(g Gomega) {
				hc := &hyperv1.HostedCluster{}
				g.Expect(tc.MgmtClient.Get(tc.Context, hcKey, hc)).To(Succeed())

				hcp := &hyperv1.HostedControlPlane{}
				g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{
					Name:      hc.Name,
					Namespace: tc.ControlPlaneNamespace,
				}, hcp)).To(Succeed())

				hcCond := meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.EtcdDataEncryptionUpToDate))
				hcpCond := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.EtcdDataEncryptionUpToDate))

				g.Expect(hcCond).NotTo(BeNil(),
					"HostedCluster %s/%s should have EtcdDataEncryptionUpToDate condition", hcKey.Namespace, hcKey.Name)
				g.Expect(hcpCond).NotTo(BeNil(),
					"HostedControlPlane should have EtcdDataEncryptionUpToDate condition if HostedCluster does")
				g.Expect(hcCond.Status).To(Equal(hcpCond.Status),
					"EtcdDataEncryptionUpToDate status should match between HostedCluster and HostedControlPlane")
				g.Expect(hcCond.Reason).To(Equal(hcpCond.Reason),
					"EtcdDataEncryptionUpToDate reason should match between HostedCluster and HostedControlPlane")
			}, 2*time.Minute, 10*time.Second).Should(Succeed())
		})
	})
}

