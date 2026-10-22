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
	"github.com/openshift/hypershift/test/e2e/v2/internal"
	v2util "github.com/openshift/hypershift/test/e2e/v2/util"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/blang/semver"
)

// KMSSpecValidationTest validates that the HostedCluster's SecretEncryption KMS spec
// fields are correctly populated. Stateless — reads HC spec only.
func KMSSpecValidationTest(getTestCtx internal.TestContextGetter) {
	Context("KMS Spec Validation", func() {
		Context("AWS KMS", func() {
			BeforeEach(func() {
				getTestCtx().SkipIfNotPlatform(hyperv1.AWSPlatform)
			})
			It("should configure the active AWS KMS key and authentication role consistently in the control plane", func() {
				tc := getTestCtx()
				hc, err := tc.GetHostedCluster()
				Expect(err).NotTo(HaveOccurred())
				Expect(hc.Spec.SecretEncryption.KMS.AWS).NotTo(BeNil())
				awsKMS := hc.Spec.SecretEncryption.KMS.AWS
				Expect(awsKMS.ActiveKey.ARN).NotTo(BeEmpty())
				expectedKey, err := configuredHostedEtcdKMSKeyARN(tc.Context, hc)
				Expect(err).NotTo(HaveOccurred())
				if expectedKey != "" {
					Expect(awsKMS.ActiveKey.ARN).To(Equal(expectedKey), "HostedCluster must use the key supplied when creating the KMS fixture")
				}
				Expect(awsKMS.Auth.AWSKMSRoleARN).NotTo(BeEmpty())
				hcp := &hyperv1.HostedControlPlane{}
				Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{Namespace: tc.ControlPlaneNamespace, Name: hc.Name}, hcp)).To(Succeed())
				Expect(hcp.Spec.SecretEncryption).To(Equal(hc.Spec.SecretEncryption))
			})
		})
		Context("Azure KMS", func() {
			BeforeEach(func() {
				getTestCtx().SkipIfNotPlatform(hyperv1.AzurePlatform)
				hc, err := getTestCtx().GetHostedCluster()
				Expect(err).NotTo(HaveOccurred())
				if hc.Spec.SecretEncryption == nil || hc.Spec.SecretEncryption.KMS == nil ||
					hc.Spec.SecretEncryption.KMS.Azure == nil {
					Skip("Azure KMS spec validation requires KMS configured")
				}
			})

			It("should have ActiveKey fields populated", func() {
				hc, err := getTestCtx().GetHostedCluster()
				Expect(err).NotTo(HaveOccurred())
				azureKMS := hc.Spec.SecretEncryption.KMS.Azure

				Expect(azureKMS.ActiveKey.KeyVaultName).NotTo(BeEmpty(),
					"ActiveKey.KeyVaultName must be set")
				Expect(azureKMS.ActiveKey.KeyName).NotTo(BeEmpty(),
					"ActiveKey.KeyName must be set")
				Expect(azureKMS.ActiveKey.KeyVersion).NotTo(BeEmpty(),
					"ActiveKey.KeyVersion must be set")
			})

			It("should have KMS authentication configured", func() {
				hc, err := getTestCtx().GetHostedCluster()
				Expect(err).NotTo(HaveOccurred())
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
			testCtx := getTestCtx()
			ctx := testCtx.Context

			testCtx.SkipIfVersionBelow(semver.MustParse("4.17.0"))

			hc, err := testCtx.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())

			hostedClusterClient, err := testCtx.GetHostedClusterClient(hc)
			Expect(err).NotTo(HaveOccurred())

			testSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "e2e-kms-test-secret-",
					Namespace:    "default",
				},
				Type: corev1.SecretTypeOpaque,
				Data: map[string][]byte{
					"testKey": []byte("testData"),
				},
			}
			Expect(hostedClusterClient.Create(ctx, testSecret)).To(Succeed(),
				"failed to create test secret in hosted cluster")

			DeferCleanup(func() {
				cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
				defer cancel()
				if err := hostedClusterClient.Delete(cleanupContext, testSecret); err != nil && !apierrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred(), "failed to cleanup test secret")
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

			managementConfig, err := v2util.GetConfig()
			Expect(err).NotTo(HaveOccurred())
			managementClient, err := kubernetes.NewForConfig(managementConfig)
			Expect(err).NotTo(HaveOccurred())
			pods := &corev1.PodList{}
			Expect(testCtx.MgmtClient.List(ctx, pods, crclient.InNamespace(testCtx.ControlPlaneNamespace), crclient.MatchingLabels{"app": "etcd"})).To(Succeed())
			Expect(pods.Items).NotTo(BeEmpty())
			var output string
			Eventually(func(g Gomega) {
				output, err = v2util.RunCommandInPod(ctx, managementClient, managementConfig, testCtx.ControlPlaneNamespace, pods.Items[0].Name, "etcd", command...)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).NotTo(BeEmpty())
			}, 5*time.Minute, 5*time.Second).Should(Succeed())
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

		hc, err := testCtx.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
		if hc.Spec.SecretEncryption == nil || hc.Spec.SecretEncryption.KMS == nil {
			Skip("SecretEncryption with KMS is not configured on this hosted cluster")
		}
	})

	RegisterHostedClusterSecretEncryptionTests(func() *internal.TestContext { return testCtx })
})
