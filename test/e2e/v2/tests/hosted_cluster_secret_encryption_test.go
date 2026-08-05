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
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/podspec"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// KMSSpecValidationTest validates that the HostedCluster's SecretEncryption KMS spec
// fields are correctly populated. Stateless — reads HC spec only.
func KMSSpecValidationTest(getTestCtx internal.TestContextGetter) {
	Context("KMS Spec Validation", func() {
		Context("Azure KMS", func() {
			BeforeEach(func() {
				testCtx := getTestCtx()
				hc := testCtx.GetHostedCluster()
				if hc == nil || hc.Spec.Platform.Type != hyperv1.AzurePlatform ||
					hc.Spec.SecretEncryption == nil || hc.Spec.SecretEncryption.KMS == nil ||
					hc.Spec.SecretEncryption.KMS.Azure == nil {
					Skip("Azure KMS spec validation requires Azure platform with KMS configured")
				}
			})

			It("should have ActiveKey fields populated", func() {
				testCtx := getTestCtx()
				hc := testCtx.GetHostedCluster()
				azureKMS := hc.Spec.SecretEncryption.KMS.Azure

				Expect(azureKMS.ActiveKey.KeyVaultName).NotTo(BeEmpty(),
					"ActiveKey.KeyVaultName must be set")
				Expect(azureKMS.ActiveKey.KeyName).NotTo(BeEmpty(),
					"ActiveKey.KeyName must be set")
				Expect(azureKMS.ActiveKey.KeyVersion).NotTo(BeEmpty(),
					"ActiveKey.KeyVersion must be set")
			})

			It("should have a valid KeyVaultType when set", func() {
				testCtx := getTestCtx()
				hc := testCtx.GetHostedCluster()
				azureKMS := hc.Spec.SecretEncryption.KMS.Azure

				if azureKMS.ActiveKey.KeyVaultType == "" {
					Skip("KeyVaultType is not set on this hosted cluster")
				}

				Expect(azureKMS.ActiveKey.KeyVaultType).To(
					BeElementOf(hyperv1.AzureKMSKeyVaultTypeKeyVault, hyperv1.AzureKMSKeyVaultTypeManagedHSM),
					"ActiveKey.KeyVaultType must be KeyVault or ManagedHSM")
			})

			It("should have KMS authentication configured", func() {
				testCtx := getTestCtx()
				hc := testCtx.GetHostedCluster()
				azureKMS := hc.Spec.SecretEncryption.KMS.Azure

				hasWorkloadIdentity := azureKMS.WorkloadIdentity.ClientID != ""
				hasManagedIdentity := azureKMS.KMS.CredentialsSecretName != ""
				Expect(hasWorkloadIdentity || hasManagedIdentity).To(BeTrue(),
					"either WorkloadIdentity.ClientID or KMS.CredentialsSecretName must be set")

				if hasWorkloadIdentity {
					Expect(string(azureKMS.WorkloadIdentity.ClientID)).NotTo(BeEmpty(),
						"WorkloadIdentity.ClientID must be non-empty for self-managed Azure")
				}
				if hasManagedIdentity {
					Expect(azureKMS.KMS.CredentialsSecretName).NotTo(BeEmpty(),
						"KMS.CredentialsSecretName must be non-empty for managed Azure")
					Expect(string(azureKMS.KMS.ObjectEncoding)).NotTo(BeEmpty(),
						"KMS.ObjectEncoding must be non-empty for managed Azure")
				}
			})
		})
	})
}

// KMSFunctionalValidationTest validates KMS encryption, API read-back, and
// persistence across a control-plane-operator restart. It cleans up created
// secrets with DeferCleanup.
func KMSFunctionalValidationTest(getTestCtx internal.TestContextGetter) {
	Context("KMS Functional Validation", func() {
		It("When a secret is created, it should be encrypted in etcd and readable through the hosted cluster API", Label("kms-read-back"), func() {
			e2eutil.GinkgoAtLeast(e2eutil.Version417)
			testCtx := getTestCtx()

			testSecret := createKMSTestSecret(testCtx, "e2e-kms-read-back-", map[string][]byte{
				"testKey": []byte("testData"),
			})
			expectSecretReadable(testCtx, testSecret)
			expectSecretEncryptedInEtcd(testCtx, testSecret)
		})

		It("When the control-plane-operator restarts, it should preserve KMS secret encryption", Label("cpo-restart-persistence"), func() {
			e2eutil.GinkgoAtLeast(e2eutil.Version417)
			testCtx := getTestCtx()

			existingSecret := createKMSTestSecret(testCtx, "e2e-kms-before-cpo-restart-", map[string][]byte{
				"beforeRestart": []byte("encryptedBeforeRestart"),
			})
			expectSecretReadable(testCtx, existingSecret)
			expectSecretEncryptedInEtcd(testCtx, existingSecret)

			restartControlPlaneOperator(testCtx)
			Expect(internal.WaitForControlPlaneDeploymentsReadiness(testCtx, 10*time.Minute, nil)).To(Succeed(),
				"control plane deployments did not settle after the control-plane-operator restart")

			expectSecretReadable(testCtx, existingSecret)

			newSecret := createKMSTestSecret(testCtx, "e2e-kms-after-cpo-restart-", map[string][]byte{
				"afterRestart": []byte("encryptedAfterRestart"),
			})
			expectSecretReadable(testCtx, newSecret)
			expectSecretEncryptedInEtcd(testCtx, newSecret)
		})
	})
}

// ManagedHSMConfigurationTest verifies that Managed HSM-specific CI exercises the intended Azure KMS path.
func ManagedHSMConfigurationTest(getTestCtx internal.TestContextGetter) {
	It("When Managed HSM coverage is selected, it should configure ActiveKey.KeyVaultType as ManagedHSM", func() {
		hc := getTestCtx().GetHostedCluster()
		Expect(hc).NotTo(BeNil(), "HostedCluster is required for Managed HSM validation")
		Expect(hc.Spec.Platform.Type).To(Equal(hyperv1.AzurePlatform),
			"Managed HSM coverage requires an Azure HostedCluster")
		Expect(hc.Spec.SecretEncryption).NotTo(BeNil(),
			"Managed HSM coverage requires secret encryption")
		Expect(hc.Spec.SecretEncryption.KMS).NotTo(BeNil(),
			"Managed HSM coverage requires KMS secret encryption")
		Expect(hc.Spec.SecretEncryption.KMS.Azure).NotTo(BeNil(),
			"Managed HSM coverage requires Azure KMS")
		Expect(hc.Spec.SecretEncryption.KMS.Azure.ActiveKey.KeyVaultType).To(Equal(hyperv1.AzureKMSKeyVaultTypeManagedHSM),
			"Managed HSM coverage must not run with an ordinary Key Vault")
	})
}

func createKMSTestSecret(testCtx *internal.TestContext, namePrefix string, data map[string][]byte) *corev1.Secret {
	GinkgoHelper()

	hostedClusterClient := testCtx.GetHostedClusterClient()
	Expect(hostedClusterClient).NotTo(BeNil(),
		"hosted cluster client is required; KubeConfig may not be set")

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: namePrefix,
			Namespace:    "default",
		},
		Type: corev1.SecretTypeOpaque,
		Data: data,
	}
	Expect(hostedClusterClient.Create(testCtx.Context, secret)).To(Succeed(),
		"failed to create test secret in hosted cluster")
	DeferCleanup(func() {
		err := hostedClusterClient.Delete(testCtx.Context, secret)
		if apierrors.IsNotFound(err) {
			return
		}
		Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete test secret %s/%s", secret.Namespace, secret.Name)
	})
	return secret
}

func expectSecretReadable(testCtx *internal.TestContext, expected *corev1.Secret) {
	GinkgoHelper()

	actual := &corev1.Secret{}
	Expect(testCtx.GetHostedClusterClient().Get(testCtx.Context, crclient.ObjectKeyFromObject(expected), actual)).To(Succeed(),
		"failed to read test secret %s/%s through the hosted cluster API", expected.Namespace, expected.Name)
	Expect(actual.Data).To(Equal(expected.Data),
		"test secret %s/%s data changed after KMS decryption", expected.Namespace, expected.Name)
}

func expectSecretEncryptedInEtcd(testCtx *internal.TestContext, secret *corev1.Secret) {
	GinkgoHelper()

	secretEtcdKey := fmt.Sprintf("/kubernetes.io/secrets/%s/%s", secret.Namespace, secret.Name)
	command := []string{
		"/usr/bin/etcdctl",
		"--endpoints=localhost:2379",
		"--cacert=/etc/etcd/tls/etcd-ca/ca.crt",
		"--cert=/etc/etcd/tls/client/etcd-client.crt",
		"--key=/etc/etcd/tls/client/etcd-client.key",
		"get",
		secretEtcdKey,
	}

	output, err := e2eutil.RunCommandInPod(testCtx.Context, testCtx.MgmtClient, "etcd",
		testCtx.ControlPlaneNamespace, command, "etcd", 5*time.Minute)
	Expect(err).NotTo(HaveOccurred(), "failed to execute etcdctl command")
	Expect(output).NotTo(BeEmpty(), "etcdctl returned empty output for key %s", secretEtcdKey)
	Expect(output).To(ContainSubstring("k8s:enc:kms:v2"),
		"secret should be encrypted using KMSv2")
	for key, value := range secret.Data {
		Expect(output).NotTo(ContainSubstring(string(value)),
			"secret data key %q should not be readable in plaintext from etcd", key)
	}
}

func restartControlPlaneOperator(testCtx *internal.TestContext) {
	GinkgoHelper()

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "control-plane-operator",
			Namespace: testCtx.ControlPlaneNamespace,
		},
	}
	Expect(testCtx.MgmtClient.Get(testCtx.Context, crclient.ObjectKeyFromObject(deployment), deployment)).To(Succeed(),
		"failed to get the control-plane-operator Deployment")
	workload := internal.WorkloadInfo{
		Kind:      "Deployment",
		Name:      deployment.Name,
		Namespace: deployment.Namespace,
		UID:       deployment.UID,
	}
	pods, err := internal.GetWorkloadPods(testCtx.Context, testCtx.MgmtClient, workload)
	Expect(err).NotTo(HaveOccurred(), "failed to get control-plane-operator pods")
	Expect(pods).NotTo(BeEmpty(), "expected at least one control-plane-operator pod")

	var originalPod *corev1.Pod
	for i := range pods {
		if podspec.IsPodReady(&pods[i]) {
			originalPod = &pods[i]
			break
		}
	}
	Expect(originalPod).NotTo(BeNil(), "expected a ready control-plane-operator pod")
	originalUID := originalPod.UID
	Expect(testCtx.MgmtClient.Delete(testCtx.Context, originalPod)).To(Succeed(),
		"failed to delete control-plane-operator pod %s", originalPod.Name)

	e2eutil.EventuallyObjects(GinkgoTB(), testCtx.Context, "control-plane-operator pod to be replaced and ready",
		func(ctx context.Context) ([]*corev1.Pod, error) {
			currentPods, err := internal.GetWorkloadPods(ctx, testCtx.MgmtClient, workload)
			podPointers := make([]*corev1.Pod, len(currentPods))
			for i := range currentPods {
				podPointers[i] = &currentPods[i]
			}
			return podPointers, err
		},
		[]e2eutil.Predicate[[]*corev1.Pod]{
			func(pods []*corev1.Pod) (bool, string, error) {
				return len(pods) > 0, "expected at least one control-plane-operator pod", nil
			},
		},
		[]e2eutil.Predicate[*corev1.Pod]{
			func(pod *corev1.Pod) (bool, string, error) {
				return pod.UID != originalUID, fmt.Sprintf("pod %s still has original UID %s", pod.Name, originalUID), nil
			},
			func(pod *corev1.Pod) (bool, string, error) {
				return podspec.IsPodReady(pod), fmt.Sprintf("pod %s is not ready", pod.Name), nil
			},
		},
		e2eutil.WithInterval(5*time.Second),
		e2eutil.WithTimeout(10*time.Minute),
	)
}

// RegisterHostedClusterSecretEncryptionTests registers all secret encryption tests.
func RegisterHostedClusterSecretEncryptionTests(getTestCtx internal.TestContextGetter) {
	KMSSpecValidationTest(getTestCtx)
	KMSFunctionalValidationTest(getTestCtx)
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:SecretEncryption] Hosted Cluster Secret Encryption", Label("secret-encryption"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")

		hc := testCtx.GetHostedCluster()
		if hc == nil || hc.Spec.SecretEncryption == nil || hc.Spec.SecretEncryption.KMS == nil {
			Skip("SecretEncryption with KMS is not configured on this hosted cluster")
		}
	})

	RegisterHostedClusterSecretEncryptionTests(func() *internal.TestContext { return testCtx })
})

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:AzureManagedHSM] Azure Managed HSM Secret Encryption", Label("azure-managed-hsm"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
	})

	ManagedHSMConfigurationTest(func() *internal.TestContext { return testCtx })
})
