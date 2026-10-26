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
	secretencryption "github.com/openshift/hypershift/support/secretencryption"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// kasMaxAllowedRestarts is the per-rotation restart delta ceiling checked after
// re-encryption completes. One restart is expected (KAS rolls out with the updated
// EncryptionConfiguration); 2 allows a single transient failure without masking a
// true crash loop. Checked as a delta from a pre-rotation baseline to avoid false
// failures from restarts that occurred before the test on a shared cluster.
const kasMaxAllowedRestarts = int32(2)

var _ = Describe("[sig-hypershift][Jira:Hypershift] Etcd Encryption Re-encryption",
	Label("lifecycle", "etcd-encryption-reencryption"), func() {
		var testCtx *internal.TestContext

		BeforeEach(func() {
			testCtx = internal.GetTestContext()
			Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")

			hc, err := testCtx.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			if hc.Spec.SecretEncryption == nil {
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

// waitForReEncryptionStarted waits until TargetKey.Provider is non-empty (the reconciler
// has started a rotation) and EtcdDataEncryptionUpToDate is False (the in-progress condition
// is set). Together these assert the not-True step of the True → False → True lifecycle.
// TargetKey is a level trigger set for the duration of the rotation, so it cannot be missed
// regardless of poll interval.
func waitForReEncryptionStarted(ctx context.Context, mgmtClient crclient.Client, hcKey crclient.ObjectKey, timeout time.Duration) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		hc := &hyperv1.HostedCluster{}
		g.Expect(mgmtClient.Get(ctx, hcKey, hc)).To(Succeed())
		g.Expect(hc.Status.SecretEncryption.TargetKey.Provider).NotTo(BeEmpty(),
			"TargetKey.Provider should be set once rotation has started")
		cond := meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.EtcdDataEncryptionUpToDate))
		g.Expect(cond).NotTo(BeNil(), "EtcdDataEncryptionUpToDate condition should exist during rotation")
		g.Expect(cond.Status).To(Equal(metav1.ConditionFalse),
			"EtcdDataEncryptionUpToDate should be False while rotation is in progress, got reason=%s message=%s",
			cond.Reason, cond.Message)
	}, timeout, 5*time.Second).Should(Succeed())
}

func waitForReEncryptionComplete(ctx context.Context, mgmtClient crclient.Client, hcKey crclient.ObjectKey, timeout time.Duration) {
	GinkgoHelper()
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
	GinkgoHelper()
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

	// Verify that the history fingerprints reflect the actual key rotation.
	// From.Fingerprint and To.Fingerprint must differ (the key actually changed),
	// and To.Fingerprint must match the current ActiveKey (the rotation targeted the right key).
	var kvType hyperv1.AzureKMSKeyVaultType
	if hc.Spec.SecretEncryption.KMS != nil && hc.Spec.SecretEncryption.KMS.Azure != nil {
		kvType = hc.Spec.SecretEncryption.KMS.Azure.KeyVaultType
	}
	activeFP := secretencryption.FingerprintFromKeyStatus(&hc.Status.SecretEncryption.ActiveKey, kvType)
	Expect(activeFP).NotTo(BeEmpty(), "could not compute fingerprint from ActiveKey")
	Expect(hc.Status.SecretEncryption.History[0].To.Fingerprint).To(Equal(activeFP),
		"history[0].to.fingerprint should match the current active key fingerprint")
	Expect(hc.Status.SecretEncryption.History[0].From.Fingerprint).NotTo(Equal(activeFP),
		"history[0].from.fingerprint should differ from to.fingerprint — rotation must change the key")
}

// svmWriteKeyAnnotation is the annotation the library-go migrator stamps on every
// StorageVersionMigration to identify which encryption write-key triggered it.
const svmWriteKeyAnnotation = "encryption.apiserver.operator.openshift.io/write-key"

// verifyStorageVersionMigrations asserts that at least one StorageVersionMigration in
// the hosted cluster was created for the current rotation (identified by the write-key
// annotation) and that every such migration has a Succeeded condition = True.
// Filtering by the annotation rules out SVMs left over from bootstrap or prior rotations.
func verifyStorageVersionMigrations(ctx context.Context, guestClient crclient.Client, hc *hyperv1.HostedCluster) {
	GinkgoHelper()
	// Compute the fingerprint the controller uses for the write-key annotation.
	// ActiveKey holds the key that was just rotated to (TargetKey is cleared on completion).
	var kvType hyperv1.AzureKMSKeyVaultType
	if hc.Spec.SecretEncryption.KMS != nil && hc.Spec.SecretEncryption.KMS.Azure != nil {
		kvType = hc.Spec.SecretEncryption.KMS.Azure.KeyVaultType
	}
	fp := secretencryption.FingerprintFromKeyStatus(&hc.Status.SecretEncryption.ActiveKey, kvType)
	Expect(fp).NotTo(BeEmpty(), "could not compute fingerprint from ActiveKey — Status may not have been updated yet")
	writeKey := "encryption-key-" + fp

	svmList := &unstructured.UnstructuredList{}
	svmList.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "migration.k8s.io",
		Version: "v1alpha1",
		Kind:    "StorageVersionMigrationList",
	})
	Expect(guestClient.List(ctx, svmList)).To(Succeed(), "failed to list StorageVersionMigration CRs")

	var matched []unstructured.Unstructured
	for _, svm := range svmList.Items {
		if svm.GetAnnotations()[svmWriteKeyAnnotation] == writeKey {
			matched = append(matched, svm)
		}
	}
	Expect(matched).NotTo(BeEmpty(),
		"no StorageVersionMigration found with write-key annotation %q — rotation did not trigger migrations", writeKey)

	for _, svm := range matched {
		conditions, _, _ := unstructured.NestedSlice(svm.Object, "status", "conditions")
		var succeeded bool
		for _, raw := range conditions {
			cond, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			if cond["type"] == "Succeeded" && cond["status"] == "True" {
				succeeded = true
				break
			}
		}
		Expect(succeeded).To(BeTrue(),
			"StorageVersionMigration %s (write-key %q) did not reach Succeeded=True", svm.GetName(), writeKey)
	}
}

// captureKASRestartCounts snapshots the current restart count of every KAS pod container.
// Pass the result to verifyKASHealth to measure only the delta introduced by the rotation,
// avoiding false failures from restarts that already occurred on the shared cluster.
func captureKASRestartCounts(ctx context.Context, mgmtClient crclient.Client, controlPlaneNamespace string) map[string]int32 {
	GinkgoHelper()
	kasPods := &corev1.PodList{}
	Expect(mgmtClient.List(ctx, kasPods,
		crclient.InNamespace(controlPlaneNamespace),
		crclient.MatchingLabels{"app": "kube-apiserver"},
	)).To(Succeed())
	counts := make(map[string]int32)
	for _, pod := range kasPods.Items {
		for _, cs := range pod.Status.ContainerStatuses {
			counts[pod.Name+"/"+cs.Name] = cs.RestartCount
		}
	}
	return counts
}

// verifyKASHealth polls until all KAS pods are Running and no container's restart count
// has increased by more than kasMaxAllowedRestarts since the preRotationRestarts snapshot.
// Containers absent from the snapshot (new pods after a rollout) are compared against 0.
func verifyKASHealth(ctx context.Context, mgmtClient crclient.Client, controlPlaneNamespace string, preRotationRestarts map[string]int32) {
	GinkgoHelper()
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
				baseline := preRotationRestarts[pod.Name+"/"+cs.Name]
				delta := cs.RestartCount - baseline
				g.Expect(delta).To(BeNumerically("<=", kasMaxAllowedRestarts),
					"KAS pod %s container %s restarted %d time(s) during rotation (baseline %d, current %d)",
					pod.Name, cs.Name, delta, baseline, cs.RestartCount)
			}
		}
	}, 5*time.Minute, 15*time.Second).Should(Succeed())
}

// verifyKASLogsNoDecryptionErrors checks both the current and previous container logs
// of every KAS pod for "no matching prefix found" decryption errors. Checking previous
// containers catches errors that occurred during pod restarts mid-rotation; a larger
// TailLines guards against errors scrolling past the tail during the up-to-25-min window.
func verifyKASLogsNoDecryptionErrors(ctx context.Context, controlPlaneNamespace string) {
	GinkgoHelper()
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
		for _, previous := range []bool{false, true} {
			req := mgmtKubeClient.CoreV1().Pods(controlPlaneNamespace).GetLogs(pod.Name, &corev1.PodLogOptions{
				Container: "kube-apiserver",
				TailLines: ptr.To(int64(5000)),
				Previous:  previous,
			})
			logStream, err := req.Stream(ctx)
			if err != nil {
				if previous {
					// Previous-container logs are absent when the pod has not restarted — skip silently.
					continue
				}
				Expect(err).NotTo(HaveOccurred(), "failed to stream current logs for KAS pod %s", pod.Name)
			}
			logBytes, err := io.ReadAll(logStream)
			logStream.Close()
			if err != nil {
				if previous {
					GinkgoWriter.Printf("WARNING: failed to read previous logs for KAS pod %s: %v\n", pod.Name, err)
					continue
				}
				Expect(err).NotTo(HaveOccurred(), "failed to read current logs for KAS pod %s", pod.Name)
			}
			Expect(string(logBytes)).NotTo(ContainSubstring("no matching prefix found"),
				"KAS pod %s logs (previous=%v) contain decryption error 'no matching prefix found'", pod.Name, previous)
		}
	}
}

// rotationCase parameterizes a single key-rotation lifecycle test. The shared
// test body (runSingleRotationTest) runs the common assertions; only the
// per-provider skip guard, key accessors, and provider-field verification differ.
type rotationCase struct {
	// contextLabel is the Ginkgo Context description, e.g. "[Feature:AWSKMSReencryption] AWS KMS Key Rotation".
	contextLabel string
	// itLabel is the Ginkgo It description.
	itLabel string
	// expectedProvider is the SecretEncryptionProvider expected in Status after rotation.
	expectedProvider hyperv1.SecretEncryptionProvider
	// applies returns (true, "") when this case should run, or (false, skipReason) to skip.
	applies func(hc *hyperv1.HostedCluster) (bool, string)
	// prepare does any provider-specific setup (e.g. creating secrets), registers its own
	// DeferCleanups, and returns the alternate key identifier to rotate to.
	prepare func(ctx context.Context, tc *internal.TestContext) (alternate string, err error)
	// getActiveKey extracts the current active-key identifier from the HC spec.
	getActiveKey func(hc *hyperv1.HostedCluster) string
	// setActiveKey mutates the HC spec's active-key field in-place.
	setActiveKey func(hc *hyperv1.HostedCluster, value string)
	// verifyProviderField asserts provider-specific Status fields after rotation completes.
	verifyProviderField func(hc *hyperv1.HostedCluster, alternate string)
}

// runSingleRotationTest registers a single-rotation lifecycle test for the given rotationCase.
// It handles: baseline wait, rotate patch, restore cleanup, started/complete waits,
// status verification, SVM check, KAS health/logs, and condition bubble-up.
func runSingleRotationTest(getTestCtx internal.TestContextGetter, rc rotationCase) {
	Context(rc.contextLabel, func() {
		BeforeEach(func() {
			tc := getTestCtx()
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			ok, reason := rc.applies(hc)
			if !ok {
				Skip(reason)
			}
		})

		It(rc.itLabel, Label(internal.InformingLabel), func() {
			tc := getTestCtx()
			ctx := tc.Context
			hcKey := crclient.ObjectKey{Namespace: tc.ClusterNamespace, Name: tc.ClusterName}

			hc := &hyperv1.HostedCluster{}
			Expect(tc.MgmtClient.Get(ctx, hcKey, hc)).To(Succeed())

			// Ensure no re-encryption is in flight before rotating.
			waitForReEncryptionComplete(ctx, tc.MgmtClient, hcKey, 5*time.Minute)

			hostedClusterClient, err := tc.GetHostedClusterClient(hc)
			Expect(err).NotTo(HaveOccurred(), "failed to get hosted cluster client")

			// Re-fetch to get a fresh resource version and current Spec before patching.
			Expect(tc.MgmtClient.Get(ctx, hcKey, hc)).To(Succeed())

			alternate, err := rc.prepare(ctx, tc)
			Expect(err).NotTo(HaveOccurred(), "failed to prepare rotation")

			original := rc.getActiveKey(hc)
			DeferCleanup(func() {
				rotateAndWait(tc.Context, tc, hcKey, func(hc *hyperv1.HostedCluster) {
					rc.setActiveKey(hc, original)
				})
			})

			kasBaseline := captureKASRestartCounts(ctx, tc.MgmtClient, tc.ControlPlaneNamespace)
			rotateAndWait(ctx, tc, hcKey, func(hc *hyperv1.HostedCluster) {
				rc.setActiveKey(hc, alternate)
			})

			Expect(tc.MgmtClient.Get(ctx, hcKey, hc)).To(Succeed())
			verifyReEncryptionStatus(hc, rc.expectedProvider)
			rc.verifyProviderField(hc, alternate)

			verifyStorageVersionMigrations(ctx, hostedClusterClient, hc)

			verifyKASHealth(ctx, tc.MgmtClient, tc.ControlPlaneNamespace, kasBaseline)
			verifyKASLogsNoDecryptionErrors(ctx, tc.ControlPlaneNamespace)

			Eventually(func(g Gomega) {
				verifyConditionBubbleUp(g, ctx, tc.MgmtClient, hcKey, tc.ControlPlaneNamespace)
			}, 2*time.Minute, 10*time.Second).Should(Succeed())
		})
	})
}

// rotateAndWait applies mutate to the HostedCluster spec, then waits for re-encryption
// to start and complete. mutate must set the new active key value in-place.
// If the mutation produces no diff (spec already at the desired key), rotateAndWait
// returns immediately without patching or waiting — the controller treats spec==activeKey
// as steady state and will not start a rotation.
func rotateAndWait(ctx context.Context, tc *internal.TestContext, hcKey crclient.ObjectKey, mutate func(*hyperv1.HostedCluster)) {
	GinkgoHelper()
	hc := &hyperv1.HostedCluster{}
	Expect(tc.MgmtClient.Get(ctx, hcKey, hc)).To(Succeed())
	base := hc.DeepCopy()
	mutate(hc)
	patchData, err := crclient.MergeFrom(base).Data(hc)
	Expect(err).NotTo(HaveOccurred(), "failed to compute patch data")
	if string(patchData) == "{}" {
		return
	}
	Expect(tc.MgmtClient.Patch(ctx, hc, crclient.MergeFrom(base))).To(Succeed())
	waitForReEncryptionStarted(ctx, tc.MgmtClient, hcKey, 5*time.Minute)
	waitForReEncryptionComplete(ctx, tc.MgmtClient, hcKey, 25*time.Minute)
}

// AWSKMSKeyRotationTest validates the re-encryption lifecycle after rotating the AWS KMS active key.
func AWSKMSKeyRotationTest(getTestCtx internal.TestContextGetter) {
	runSingleRotationTest(getTestCtx, rotationCase{
		contextLabel:     "[Feature:AWSKMSReencryption] AWS KMS Key Rotation",
		itLabel:          "should re-encrypt all etcd data after active key rotation",
		expectedProvider: hyperv1.SecretEncryptionProviderAWS,
		applies: func(hc *hyperv1.HostedCluster) (bool, string) {
			if hc.Spec.Platform.Type != hyperv1.AWSPlatform ||
				hc.Spec.SecretEncryption.Type != hyperv1.KMS ||
				hc.Spec.SecretEncryption.KMS == nil ||
				hc.Spec.SecretEncryption.KMS.AWS == nil {
				return false, "AWS KMS key rotation test requires AWS platform with KMS configured"
			}
			if internal.GetEnvVarValue("E2E_AWS_KMS_KEY_ARN_ALTERNATE") == "" {
				return false, "E2E_AWS_KMS_KEY_ARN_ALTERNATE is not set"
			}
			return true, ""
		},
		prepare: func(_ context.Context, _ *internal.TestContext) (string, error) {
			return internal.GetEnvVarValue("E2E_AWS_KMS_KEY_ARN_ALTERNATE"), nil
		},
		getActiveKey: func(hc *hyperv1.HostedCluster) string {
			return hc.Spec.SecretEncryption.KMS.AWS.ActiveKey.ARN
		},
		setActiveKey: func(hc *hyperv1.HostedCluster, value string) {
			hc.Spec.SecretEncryption.KMS.AWS.ActiveKey.ARN = value
		},
		verifyProviderField: func(hc *hyperv1.HostedCluster, alternate string) {
			Expect(hc.Status.SecretEncryption.ActiveKey.AWS.ARN).To(Equal(alternate),
				"activeKey ARN should match the alternate key")
		},
	})
}

// azureKMSRotationApplies is the shared skip guard for Azure KMS rotation tests.
// It returns (true, "") when the cluster is Azure with KMS configured and
// E2E_AZURE_KMS_KEY_VERSION_ALTERNATE is set, or (false, reason) otherwise.
func azureKMSRotationApplies(hc *hyperv1.HostedCluster) (bool, string) {
	if hc.Spec.Platform.Type != hyperv1.AzurePlatform ||
		hc.Spec.SecretEncryption.Type != hyperv1.KMS ||
		hc.Spec.SecretEncryption.KMS == nil ||
		hc.Spec.SecretEncryption.KMS.Azure == nil {
		return false, "Azure KMS key rotation test requires Azure platform with KMS configured"
	}
	if internal.GetEnvVarValue("E2E_AZURE_KMS_KEY_VERSION_ALTERNATE") == "" {
		return false, "E2E_AZURE_KMS_KEY_VERSION_ALTERNATE is not set"
	}
	return true, ""
}

// AzureKMSKeyRotationTest validates the re-encryption lifecycle after rotating the Azure KMS key version.
func AzureKMSKeyRotationTest(getTestCtx internal.TestContextGetter) {
	runSingleRotationTest(getTestCtx, rotationCase{
		contextLabel:     "[Feature:AzureKMSReencryption] Azure KMS Key Rotation",
		itLabel:          "should re-encrypt all etcd data after key version rotation",
		expectedProvider: hyperv1.SecretEncryptionProviderAzure,
		applies:          azureKMSRotationApplies,
		prepare: func(_ context.Context, _ *internal.TestContext) (string, error) {
			return internal.GetEnvVarValue("E2E_AZURE_KMS_KEY_VERSION_ALTERNATE"), nil
		},
		getActiveKey: func(hc *hyperv1.HostedCluster) string {
			return hc.Spec.SecretEncryption.KMS.Azure.ActiveKey.KeyVersion
		},
		setActiveKey: func(hc *hyperv1.HostedCluster, value string) {
			hc.Spec.SecretEncryption.KMS.Azure.ActiveKey.KeyVersion = value
		},
		verifyProviderField: func(hc *hyperv1.HostedCluster, alternate string) {
			Expect(hc.Status.SecretEncryption.ActiveKey.Azure.KeyVersion).To(Equal(alternate),
				"activeKey keyVersion should match the alternate version")
		},
	})
}

// AESCBCKeyRotationTest validates the re-encryption lifecycle after rotating the AESCBC encryption key.
func AESCBCKeyRotationTest(getTestCtx internal.TestContextGetter) {
	runSingleRotationTest(getTestCtx, rotationCase{
		contextLabel:     "[Feature:AESCBCReencryption] AESCBC Key Rotation",
		itLabel:          "should re-encrypt secrets after active key rotation",
		expectedProvider: hyperv1.SecretEncryptionProviderAESCBC,
		applies: func(hc *hyperv1.HostedCluster) (bool, string) {
			if hc.Spec.SecretEncryption.Type != hyperv1.AESCBC || hc.Spec.SecretEncryption.AESCBC == nil {
				return false, "AESCBC key rotation test requires AESCBC encryption configured"
			}
			return true, ""
		},
		prepare: func(ctx context.Context, tc *internal.TestContext) (string, error) {
			keyData := make([]byte, 32)
			if _, err := rand.Read(keyData); err != nil {
				return "", fmt.Errorf("failed to generate random AESCBC key data: %w", err)
			}
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
			if err := tc.MgmtClient.Create(ctx, newKeySecret); err != nil {
				return "", fmt.Errorf("failed to create AESCBC key secret: %w", err)
			}
			DeferCleanup(func() {
				if err := tc.MgmtClient.Delete(tc.Context, newKeySecret); err != nil && !apierrors.IsNotFound(err) {
					GinkgoWriter.Printf("WARNING: failed to cleanup AESCBC key secret: %v\n", err)
				}
			})
			return newKeySecret.Name, nil
		},
		getActiveKey: func(hc *hyperv1.HostedCluster) string {
			return hc.Spec.SecretEncryption.AESCBC.ActiveKey.Name
		},
		setActiveKey: func(hc *hyperv1.HostedCluster, value string) {
			hc.Spec.SecretEncryption.AESCBC.ActiveKey.Name = value
		},
		verifyProviderField: func(hc *hyperv1.HostedCluster, alternate string) {
			Expect(hc.Status.SecretEncryption.ActiveKey.AESCBC).NotTo(BeNil(),
				"activeKey AESCBC should not be nil after AESCBC key rotation")
			Expect(hc.Status.SecretEncryption.ActiveKey.AESCBC.Secret.Name).To(Equal(alternate),
				"activeKey AESCBC secret should reference the new key")
		},
	})
}

// AzureKMSConsecutiveKeyRotationTest validates that two sequential key rotations
// (alternate → original) each complete successfully end-to-end.
func AzureKMSConsecutiveKeyRotationTest(getTestCtx internal.TestContextGetter) {
	Context("[Feature:AzureKMSReencryption] Azure KMS Consecutive Key Rotation", func() {
		BeforeEach(func() {
			tc := getTestCtx()
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			ok, reason := azureKMSRotationApplies(hc)
			if !ok {
				Skip(reason)
			}
		})

		It("should complete two consecutive key rotations successfully", Label(internal.InformingLabel), func() {
			tc := getTestCtx()
			ctx := tc.Context
			hcKey := crclient.ObjectKey{Namespace: tc.ClusterNamespace, Name: tc.ClusterName}

			hc := &hyperv1.HostedCluster{}
			Expect(tc.MgmtClient.Get(ctx, hcKey, hc)).To(Succeed())
			originalVersion := hc.Spec.SecretEncryption.KMS.Azure.ActiveKey.KeyVersion

			waitForReEncryptionComplete(ctx, tc.MgmtClient, hcKey, 5*time.Minute)

			DeferCleanup(func() {
				rotateAndWait(tc.Context, tc, hcKey, func(hc *hyperv1.HostedCluster) {
					hc.Spec.SecretEncryption.KMS.Azure.ActiveKey.KeyVersion = originalVersion
				})
			})

			alternateVersion := internal.GetEnvVarValue("E2E_AZURE_KMS_KEY_VERSION_ALTERNATE")

			By("Performing first key rotation: original -> alternate")
			kas1Baseline := captureKASRestartCounts(ctx, tc.MgmtClient, tc.ControlPlaneNamespace)
			rotateAndWait(ctx, tc, hcKey, func(hc *hyperv1.HostedCluster) {
				hc.Spec.SecretEncryption.KMS.Azure.ActiveKey.KeyVersion = alternateVersion
			})

			Expect(tc.MgmtClient.Get(ctx, hcKey, hc)).To(Succeed())
			Expect(hc.Status.SecretEncryption.ActiveKey.Azure.KeyVersion).To(Equal(alternateVersion),
				"activeKey should be the alternate version after first rotation")
			Expect(hc.Status.SecretEncryption.History).NotTo(BeEmpty(),
				"history should have at least one entry after first rotation")

			verifyKASHealth(ctx, tc.MgmtClient, tc.ControlPlaneNamespace, kas1Baseline)
			Eventually(func(g Gomega) {
				verifyConditionBubbleUp(g, ctx, tc.MgmtClient, hcKey, tc.ControlPlaneNamespace)
			}, 2*time.Minute, 10*time.Second).Should(Succeed())

			By("Performing second key rotation: alternate -> original")
			kas2Baseline := captureKASRestartCounts(ctx, tc.MgmtClient, tc.ControlPlaneNamespace)
			rotateAndWait(ctx, tc, hcKey, func(hc *hyperv1.HostedCluster) {
				hc.Spec.SecretEncryption.KMS.Azure.ActiveKey.KeyVersion = originalVersion
			})

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

			verifyKASHealth(ctx, tc.MgmtClient, tc.ControlPlaneNamespace, kas2Baseline)
			verifyKASLogsNoDecryptionErrors(ctx, tc.ControlPlaneNamespace)

			Eventually(func(g Gomega) {
				verifyConditionBubbleUp(g, ctx, tc.MgmtClient, hcKey, tc.ControlPlaneNamespace)
			}, 2*time.Minute, 10*time.Second).Should(Succeed())
		})
	})
}

// verifyConditionBubbleUp asserts that EtcdDataEncryptionUpToDate is consistent between
// the HostedControlPlane and the HostedCluster. HCP→HC propagation is async, so callers
// must wrap this inside Eventually rather than calling it single-shot.
func verifyConditionBubbleUp(g Gomega, ctx context.Context, mgmtClient crclient.Client, hcKey crclient.ObjectKey, controlPlaneNamespace string) {
	hc := &hyperv1.HostedCluster{}
	g.Expect(mgmtClient.Get(ctx, hcKey, hc)).To(Succeed())

	hcp := &hyperv1.HostedControlPlane{}
	g.Expect(mgmtClient.Get(ctx, crclient.ObjectKey{
		Name:      hc.Name,
		Namespace: controlPlaneNamespace,
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
}

// ConditionBubbleUpTest verifies that the EtcdDataEncryptionUpToDate condition
// is consistent between HostedControlPlane and HostedCluster.
func ConditionBubbleUpTest(getTestCtx internal.TestContextGetter) {
	Context("Condition Bubble-Up", func() {
		It("should have matching EtcdDataEncryptionUpToDate on HCP and HostedCluster", Label(internal.InformingLabel), func() {
			tc := getTestCtx()
			hcKey := crclient.ObjectKey{Namespace: tc.ClusterNamespace, Name: tc.ClusterName}
			Eventually(func(g Gomega) {
				verifyConditionBubbleUp(g, tc.Context, tc.MgmtClient, hcKey, tc.ControlPlaneNamespace)
			}, 2*time.Minute, 10*time.Second).Should(Succeed())
		})
	})
}
