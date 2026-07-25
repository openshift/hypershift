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
	"encoding/json"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"
	imageregistryv1 "github.com/openshift/api/imageregistry/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/gcputil"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// RegisterHostedClusterImageRegistryTests registers all hosted cluster image registry tests.
func RegisterHostedClusterImageRegistryTests(getTestCtx internal.TestContextGetter) {
	ImageRegistryCapabilityEnabledTest(getTestCtx)
	ImageRegistryCapabilityDisabledTest(getTestCtx)
}

// ImageRegistryCapabilityEnabledTest verifies that the image registry works correctly when the
// ImageRegistry capability is enabled. Uses an Ordered container so that if the first spec
// (waiting for the ClusterOperator to become Available) fails, all subsequent specs are
// automatically skipped.
func ImageRegistryCapabilityEnabledTest(getTestCtx internal.TestContextGetter) {
	var (
		tc                  *internal.TestContext
		hc                  *hyperv1.HostedCluster
		hostedClusterClient crclient.Client
	)

	When("the ImageRegistry capability is enabled", Ordered, func() {
		BeforeAll(func() {
			tc = getTestCtx()
			hc = tc.GetHostedCluster()
			if hc == nil {
				Skip("HostedCluster is not available")
			}
			if hc.Spec.Capabilities != nil {
				for _, disabled := range hc.Spec.Capabilities.Disabled {
					if disabled == hyperv1.ImageRegistryCapability {
						Skip("ImageRegistry capability is disabled on this HostedCluster")
					}
				}
			}
			hostedClusterClient = tc.GetHostedClusterClient()
			Expect(hostedClusterClient).NotTo(BeNil(), "hosted cluster client is nil; HostedCluster may not have KubeConfig status set")
		})

		It("should have a healthy image-registry ClusterOperator", func() {
			By("waiting for the image-registry ClusterOperator to be Available and not Degraded")
			Eventually(func(g Gomega) {
				co := &configv1.ClusterOperator{}
				g.Expect(hostedClusterClient.Get(tc.Context, crclient.ObjectKey{Name: "image-registry"}, co)).To(Succeed())

				var avail, degraded *configv1.ClusterOperatorStatusCondition
				for i := range co.Status.Conditions {
					switch co.Status.Conditions[i].Type {
					case configv1.OperatorAvailable:
						avail = &co.Status.Conditions[i]
					case configv1.OperatorDegraded:
						degraded = &co.Status.Conditions[i]
					}
				}

				g.Expect(avail).NotTo(BeNil(), "ClusterOperator should have Available condition")
				g.Expect(avail.Status).To(Equal(configv1.ConditionTrue),
					"Available should be True, got reason: %s, message: %s", avail.Reason, avail.Message)

				if degraded != nil {
					g.Expect(degraded.Status).NotTo(Equal(configv1.ConditionTrue),
						"Degraded should not be True, got reason: %s, message: %s", degraded.Reason, degraded.Message)
				}
			}).WithTimeout(10 * time.Minute).WithPolling(30 * time.Second).Should(Succeed())
		})

		It("should have installer-cloud-credentials in the hosted cluster", func() {
			secret := &corev1.Secret{}
			Expect(hostedClusterClient.Get(tc.Context, crclient.ObjectKey{
				Namespace: "openshift-image-registry",
				Name:      "installer-cloud-credentials",
			}, secret)).To(Succeed())
			Expect(secret.Data).NotTo(BeEmpty(),
				"installer-cloud-credentials secret should have data")
		})

		It("should have storage configured in the registry config", func() {
			config := &imageregistryv1.Config{}
			Expect(hostedClusterClient.Get(tc.Context, crclient.ObjectKey{Name: "cluster"}, config)).To(Succeed())

			storage := config.Spec.Storage
			hasStorage := storage.S3 != nil ||
				storage.GCS != nil ||
				storage.Azure != nil ||
				storage.Swift != nil ||
				storage.PVC != nil ||
				storage.IBMCOS != nil ||
				storage.OSS != nil ||
				storage.EmptyDir != nil
			Expect(hasStorage).To(BeTrue(),
				"image registry Config should have at least one storage backend configured")
		})

		It("should have a ready cluster-image-registry-operator deployment", func() {
			deployment := &appsv1.Deployment{}
			Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{
				Namespace: tc.ControlPlaneNamespace,
				Name:      "cluster-image-registry-operator",
			}, deployment)).To(Succeed())
			Expect(deployment.Status.ReadyReplicas).To(BeNumerically(">=", 1),
				"cluster-image-registry-operator deployment should have at least one ready replica")
		})

		Context("on GCP clusters", func() {
			var imageRegistryEmail string

			BeforeEach(func() {
				if hc == nil || hc.Spec.Platform.Type != hyperv1.GCPPlatform {
					Skip("image registry GCP tests are only for GCP platform")
				}
				Expect(hc.Spec.Platform.GCP).NotTo(BeNil(),
					"GCP platform spec must be set for GCP HostedCluster %s/%s", hc.Namespace, hc.Name)
				Expect(hc.Spec.Platform.GCP.WorkloadIdentity).NotTo(BeNil(),
					"GCP platform spec should have WorkloadIdentity configured for HostedCluster %s/%s", hc.Namespace, hc.Name)
				imageRegistryEmail = string(hc.Spec.Platform.GCP.WorkloadIdentity.ServiceAccountsEmails.ImageRegistry)
				Expect(imageRegistryEmail).NotTo(BeEmpty(),
					"imageRegistry service account email must be set in GCP WorkloadIdentity config for %s/%s",
					hc.Namespace, hc.Name)
			})

			It("should use GCS storage with a bucket name", func() {
				config := &imageregistryv1.Config{}
				Expect(hostedClusterClient.Get(tc.Context, crclient.ObjectKey{Name: "cluster"}, config)).To(Succeed())
				Expect(config.Spec.Storage.GCS).NotTo(BeNil(),
					"image registry Config should use GCS storage on GCP clusters")
				Expect(config.Spec.Storage.GCS.Bucket).NotTo(BeEmpty(),
					"GCS storage bucket name should not be empty")
			})

			It("should have valid WIF credentials in installer-cloud-credentials", func() {
				secret := &corev1.Secret{}
				Expect(hostedClusterClient.Get(tc.Context, crclient.ObjectKey{
					Namespace: "openshift-image-registry",
					Name:      "installer-cloud-credentials",
				}, secret)).To(Succeed())

				credData, ok := secret.Data["service_account.json"]
				Expect(ok).To(BeTrue(),
					"installer-cloud-credentials should contain key service_account.json")
				verifyExternalAccountCred(credData, "service_account.json", imageRegistryEmail)
			})

		})
	})
}

// ImageRegistryCapabilityDisabledTest verifies that when the ImageRegistry capability is disabled,
// no image registry resources are created in the hosted cluster. This is ported from the v1
// EnsureImageRegistryCapabilityDisabled test.
func ImageRegistryCapabilityDisabledTest(getTestCtx internal.TestContextGetter) {
	When("the ImageRegistry capability is disabled", func() {
		var (
			tc                  *internal.TestContext
			hostedClusterClient crclient.Client
		)

		BeforeEach(func() {
			tc = getTestCtx()
			hc := tc.GetHostedCluster()
			if hc == nil {
				Skip("HostedCluster is not available")
			}

			isDisabled := false
			if hc.Spec.Capabilities != nil {
				for _, disabled := range hc.Spec.Capabilities.Disabled {
					if disabled == hyperv1.ImageRegistryCapability {
						isDisabled = true
						break
					}
				}
			}
			if !isDisabled {
				Skip("ImageRegistry capability is not disabled on this HostedCluster")
			}

			hostedClusterClient = tc.GetHostedClusterClient()
			Expect(hostedClusterClient).NotTo(BeNil(), "hosted cluster client is nil; HostedCluster may not have KubeConfig status set")
		})

		It("should not have the image-registry ClusterOperator", func() {
			co := &configv1.ClusterOperator{}
			err := hostedClusterClient.Get(tc.Context, crclient.ObjectKey{Name: "image-registry"}, co)
			if err != nil && !apierrors.IsNotFound(err) {
				Fail(fmt.Sprintf("unexpected error checking for image-registry ClusterOperator: %v", err))
			}
			Expect(apierrors.IsNotFound(err)).To(BeTrue(),
				"image-registry ClusterOperator should not exist when ImageRegistry capability is disabled")
		})

		It("should not have the openshift-image-registry namespace", func() {
			ns := &corev1.Namespace{}
			err := hostedClusterClient.Get(tc.Context, crclient.ObjectKey{Name: "openshift-image-registry"}, ns)
			if err != nil && !apierrors.IsNotFound(err) {
				Fail(fmt.Sprintf("unexpected error checking for openshift-image-registry namespace: %v", err))
			}
			Expect(apierrors.IsNotFound(err)).To(BeTrue(),
				"openshift-image-registry namespace should not exist when ImageRegistry capability is disabled")
		})

		It("should not add ImagePullSecrets to default service accounts", func() {
			sa := &corev1.ServiceAccount{}
			Expect(hostedClusterClient.Get(tc.Context, crclient.ObjectKey{
				Namespace: "default",
				Name:      "default",
			}, sa)).To(Succeed())
			Expect(sa.ImagePullSecrets).To(BeEmpty(),
				"default service account in default namespace should not have ImagePullSecrets when ImageRegistry capability is disabled")
		})

		It("should not inject ImagePullSecrets into newly created service accounts", func() {
			ns := &corev1.Namespace{}
			ns.Name = "image-registry-test-namespace"
			Expect(hostedClusterClient.Create(tc.Context, ns)).To(Succeed())
			DeferCleanup(func() {
				err := hostedClusterClient.Delete(tc.Context, ns)
				if err != nil && !apierrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete test namespace %s", ns.Name)
				}
			})

			Eventually(func(g Gomega) {
				sa := &corev1.ServiceAccount{}
				g.Expect(hostedClusterClient.Get(tc.Context, crclient.ObjectKey{
					Namespace: ns.Name,
					Name:      "default",
				}, sa)).To(Succeed())
				g.Expect(sa.ImagePullSecrets).To(BeEmpty(),
					"default service account in newly created namespace should not have ImagePullSecrets when ImageRegistry capability is disabled")
			}).WithTimeout(2 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
		})
	})
}

// verifyExternalAccountCred asserts that credData is valid JSON for a GCP external_account
// credential and that its service_account_impersonation_url references expectedEmail.
func verifyExternalAccountCred(credData []byte, key, expectedEmail string) {
	GinkgoHelper()
	var cred gcputil.ExternalAccountCredential
	Expect(json.Unmarshal(credData, &cred)).To(Succeed(),
		"%s should be valid JSON", key)
	Expect(cred.Type).To(Equal("external_account"),
		"credential type should be external_account")
	Expect(cred.ServiceAccountImpersonationURL).To(ContainSubstring(expectedEmail),
		"service_account_impersonation_url should reference the imageRegistry GSA email %s", expectedEmail)
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:ImageRegistry] Hosted Cluster Image Registry", Label("hosted-cluster-image-registry"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")

		testCtx.ValidateHostedCluster()
	})

	RegisterHostedClusterImageRegistryTests(func() *internal.TestContext { return testCtx })
})
