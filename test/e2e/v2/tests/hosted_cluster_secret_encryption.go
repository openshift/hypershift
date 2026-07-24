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
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

// KMSFunctionalValidationTest validates that secrets stored in etcd are encrypted
// using KMSv2. Creates a test secret in the hosted cluster, reads it from etcd
// via etcdctl, and asserts the k8s:enc:kms:v2 prefix. Cleans up via DeferCleanup.
func KMSFunctionalValidationTest(getTestCtx internal.TestContextGetter) {
	Context("KMS Functional Validation", func() {
		It("should encrypt secrets in etcd using KMSv2", func() {
			e2eutil.GinkgoAtLeast(e2eutil.Version417)
			testCtx := getTestCtx()
			ctx := testCtx.Context

			hostedClusterClient := testCtx.GetHostedClusterClient()
			Expect(hostedClusterClient).NotTo(BeNil(),
				"hosted cluster client is required; KubeConfig may not be set")

			testSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "e2e-kms-test-secret",
					Namespace: "default",
				},
				Type: corev1.SecretTypeOpaque,
				Data: map[string][]byte{
					"testKey": []byte("testData"),
				},
			}
			Expect(hostedClusterClient.Create(ctx, testSecret)).To(Succeed(),
				"failed to create test secret in hosted cluster")

			DeferCleanup(func() {
				if err := hostedClusterClient.Delete(context.Background(), testSecret); err != nil && !apierrors.IsNotFound(err) {
					GinkgoWriter.Printf("WARNING: failed to cleanup test secret: %v\n", err)
				}
			})

			secretEtcdKey := fmt.Sprintf("/kubernetes.io/secrets/%s/%s",
				testSecret.Namespace, testSecret.Name)
			command := []string{
				"/usr/bin/etcdctl",
				"--endpoints=localhost:2379",
				"--cacert=/etc/etcd/tls/etcd-ca/ca.crt",
				"--cert=/etc/etcd/tls/client/etcd-client.crt",
				"--key=/etc/etcd/tls/client/etcd-client.key",
				"get",
				secretEtcdKey,
			}

			output, err := e2eutil.RunCommandInPod(ctx, testCtx.MgmtClient, "etcd",
				testCtx.ControlPlaneNamespace, command, "etcd", 5*time.Minute)
			Expect(err).NotTo(HaveOccurred(), "failed to execute etcdctl command")
			Expect(output).NotTo(BeEmpty(), "etcdctl returned empty output for key %s", secretEtcdKey)
			Expect(output).To(ContainSubstring("k8s:enc:kms:v2"),
				"secret should be encrypted using KMSv2")
			Expect(output).NotTo(ContainSubstring("testData"),
				"secret data should not be readable in plaintext from etcd")
		})
	})
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
