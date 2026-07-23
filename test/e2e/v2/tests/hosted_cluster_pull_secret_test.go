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
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hccomanifests "github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/support/netutil"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func RegisterGlobalPullSecretTests(getTestCtx internal.TestContextGetter) {
	EnsureGlobalPullSecretTest(getTestCtx)
}

func EnsureGlobalPullSecretTest(getTestCtx internal.TestContextGetter) {
	When("an additional pull secret is created in the hosted cluster", func() {
		It("should propagate it through the global pull secret pipeline and clean up on deletion", func() {
			tc := getTestCtx()
			if e2eutil.IsLessThan(e2eutil.Version419) {
				Skip("global pull secret test requires version >= 4.19")
			}

			hc := tc.GetHostedCluster()

			if hc.Spec.Platform.Type != hyperv1.AzurePlatform && hc.Spec.Platform.Type != hyperv1.AWSPlatform {
				Skip("global pull secret test is only supported on AWS and Azure platforms")
			}
			if hc.Spec.Platform.Type == hyperv1.AWSPlatform && e2eutil.IsLessThan(e2eutil.Version421) {
				Skip("AWS platform requires version >= 4.21 for global pull secret")
			}
			if !netutil.IsPublicHC(hc) {
				Skip("global pull secret test is only supported on public clusters")
			}

			tc.ValidateHostedClusterClient()
			hcClient := tc.GetHostedClusterClient()

			npList := &hyperv1.NodePoolList{}
			Expect(tc.MgmtClient.List(tc.Context, npList, crclient.InNamespace(hc.Namespace))).To(Succeed(),
				"failed to list NodePools")
			Expect(npList.Items).NotTo(BeEmpty(), "expected at least one NodePool")

			var np *hyperv1.NodePool
			for i := range npList.Items {
				candidate := &npList.Items[i]
				if candidate.Spec.Management.UpgradeType != hyperv1.UpgradeTypeInPlace &&
					candidate.Spec.Replicas != nil &&
					*candidate.Spec.Replicas > 0 {
					np = candidate
					break
				}
			}
			if np == nil {
				Skip("no suitable NodePool found (need non-InPlace upgrade type with replicas > 0)")
			}
			nodeCount := *np.Spec.Replicas

			var dummyPullSecretData = []byte(`{"auths": {"quay.io": {"auth": "YWRtaW46cGFzc3dvcmQ="}}}`)
			var updatedPullSecretData = []byte(`{"auths": {"registry.example.com": {"auth": "dXNlcjpwYXNzd29yZA=="}}}`)

			By("verifying in-place management-cluster pull secret propagation without rollout")
			if !e2eutil.IsLessThan(e2eutil.Version422) {
				verifyPullSecretPropagation(tc, hc, np, hcClient, nodeCount)
			}

			By("verifying Replace nodes have globalPS label")
			verifyGlobalPSLabel(tc, hcClient, np, nodeCount)

			By("creating additional-pull-secret with dummy data")
			createAdditionalPullSecret(tc, hcClient, dummyPullSecretData)
			DeferCleanup(func() {
				additionalPS := hccomanifests.AdditionalPullSecret()
				err := hcClient.Delete(tc.Context, additionalPS)
				if err != nil && !apierrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete additional-pull-secret")
				}
			})

			By("verifying GlobalPullSecret is created in the hosted cluster")
			var oldGlobalPSData []byte
			Eventually(func(g Gomega) {
				globalPS := hccomanifests.GlobalPullSecret()
				g.Expect(hcClient.Get(tc.Context, crclient.ObjectKeyFromObject(globalPS), globalPS)).To(Succeed(),
					"global-pull-secret should exist")
				g.Expect(globalPS.Data[corev1.DockerConfigJsonKey]).NotTo(BeEmpty(),
					"global-pull-secret data should not be empty")
				oldGlobalPSData = globalPS.Data[corev1.DockerConfigJsonKey]
			}, 30*time.Second, 5*time.Second).Should(Succeed())

			By("verifying critical DaemonSets are ready (first check)")
			verifyDaemonSetsReady(tc, hcClient, nodeCount)

			By("updating additional-pull-secret with valid pull secret data")
			additionalPS := hccomanifests.AdditionalPullSecret()
			Expect(hcClient.Get(tc.Context, crclient.ObjectKeyFromObject(additionalPS), additionalPS)).To(Succeed(),
				"failed to get additional-pull-secret")
			additionalPS.Data[corev1.DockerConfigJsonKey] = updatedPullSecretData
			Expect(hcClient.Update(tc.Context, additionalPS)).To(Succeed(),
				"failed to update additional-pull-secret with valid data")

			By("waiting for nodes to stabilize after pull secret update")
			e2eutil.WaitForReadyNodesByNodePool(GinkgoTB(), tc.Context, hcClient, np, hc.Spec.Platform.Type)

			By("verifying GlobalPullSecret is updated in the hosted cluster")
			Eventually(func(g Gomega) {
				globalPS := hccomanifests.GlobalPullSecret()
				g.Expect(hcClient.Get(tc.Context, crclient.ObjectKeyFromObject(globalPS), globalPS)).To(Succeed())
				g.Expect(globalPS.Data[corev1.DockerConfigJsonKey]).NotTo(BeEmpty())
				g.Expect(bytes.Equal(globalPS.Data[corev1.DockerConfigJsonKey], oldGlobalPSData)).To(BeFalse(),
					"global-pull-secret should be updated after adding valid pull secret")
			}, 30*time.Second, 5*time.Second).Should(Succeed())

			By("verifying critical DaemonSets are ready (second check)")
			verifyDaemonSetsReady(tc, hcClient, nodeCount)

			By("deleting additional-pull-secret")
			Expect(hcClient.Delete(tc.Context, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "additional-pull-secret",
					Namespace: "kube-system",
				},
			})).To(Succeed(), "failed to delete additional-pull-secret")

			By("verifying GlobalPullSecret is deleted from the hosted cluster")
			Eventually(func() error {
				globalPS := hccomanifests.GlobalPullSecret()
				err := hcClient.Get(tc.Context, crclient.ObjectKeyFromObject(globalPS), globalPS)
				if apierrors.IsNotFound(err) {
					return nil
				}
				if err != nil {
					return err
				}
				return fmt.Errorf("global-pull-secret still exists")
			}, 30*time.Second, 5*time.Second).Should(Succeed())

			By("waiting for nodes to stabilize after pull secret deletion")
			e2eutil.WaitForReadyNodesByNodePool(GinkgoTB(), tc.Context, hcClient, np, hc.Spec.Platform.Type)

			By("verifying global-pull-secret-syncer DaemonSet is ready after cleanup")
			Eventually(func(g Gomega) {
				ds := &appsv1.DaemonSet{}
				g.Expect(hcClient.Get(tc.Context, crclient.ObjectKey{
					Name:      hccomanifests.GlobalPullSecretDSName,
					Namespace: hccomanifests.GlobalPullSecretNamespace,
				}, ds)).To(Succeed())
				g.Expect(ds.Status.DesiredNumberScheduled).To(BeNumerically(">=", nodeCount))
				g.Expect(ds.Status.NumberReady).To(Equal(ds.Status.DesiredNumberScheduled))
			}, 20*time.Minute, 10*time.Second).Should(Succeed())
		})
	})
}

func verifyPullSecretPropagation(tc *internal.TestContext, hc *hyperv1.HostedCluster, np *hyperv1.NodePool, hcClient crclient.Client, nodeCount int32) {
	mgmtSecret := &corev1.Secret{}
	Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{
		Namespace: hc.Namespace,
		Name:      hc.Spec.PullSecret.Name,
	}, mgmtSecret)).To(Succeed(), "failed to get management-cluster pull secret")

	originalData := make([]byte, len(mgmtSecret.Data[corev1.DockerConfigJsonKey]))
	copy(originalData, mgmtSecret.Data[corev1.DockerConfigJsonKey])

	DeferCleanup(func() {
		fresh := &corev1.Secret{}
		err := tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{
			Namespace: hc.Namespace,
			Name:      hc.Spec.PullSecret.Name,
		}, fresh)
		if apierrors.IsNotFound(err) {
			return
		}
		Expect(err).NotTo(HaveOccurred(),
			"cleanup: failed to get management-cluster pull secret %s/%s", hc.Namespace, hc.Spec.PullSecret.Name)
		if fresh.Data == nil {
			fresh.Data = map[string][]byte{}
		}
		fresh.Data[corev1.DockerConfigJsonKey] = originalData
		Expect(tc.MgmtClient.Update(tc.Context, fresh)).To(Succeed(),
			"cleanup: failed to restore management-cluster pull secret %s/%s", hc.Namespace, hc.Spec.PullSecret.Name)
	})

	type dockerConfigJSON struct {
		Auths map[string]json.RawMessage `json:"auths"`
	}
	var cfg dockerConfigJSON
	Expect(json.Unmarshal(originalData, &cfg)).To(Succeed(), "failed to parse pull secret")
	cfg.Auths["e2e-dummy.example.com"] = json.RawMessage(`{"auth":"e2e-dummy-token"}`)
	modifiedData, err := json.Marshal(cfg)
	Expect(err).NotTo(HaveOccurred(), "failed to marshal modified pull secret")

	mgmtSecret.Data[corev1.DockerConfigJsonKey] = modifiedData
	Expect(tc.MgmtClient.Update(tc.Context, mgmtSecret)).To(Succeed(),
		"failed to update management-cluster pull secret")

	Eventually(func() bool {
		secret := &corev1.Secret{}
		if err := hcClient.Get(tc.Context, crclient.ObjectKey{Name: "pull-secret", Namespace: "openshift-config"}, secret); err != nil {
			return false
		}
		return bytes.Contains(secret.Data[corev1.DockerConfigJsonKey], []byte("e2e-dummy.example.com"))
	}, 150*time.Second, 5*time.Second).Should(BeTrue(),
		"openshift-config/pull-secret did not propagate dummy entry")

	Eventually(func() bool {
		secret := hccomanifests.OriginalPullSecret()
		if err := hcClient.Get(tc.Context, crclient.ObjectKeyFromObject(secret), secret); err != nil {
			return false
		}
		return bytes.Contains(secret.Data[corev1.DockerConfigJsonKey], []byte("e2e-dummy.example.com"))
	}, 150*time.Second, 5*time.Second).Should(BeTrue(),
		"kube-system/original-pull-secret did not propagate dummy entry")

	nodePool := &hyperv1.NodePool{}
	Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(np), nodePool)).To(Succeed())
	foundUpdatingConfig := false
	for _, cond := range nodePool.Status.Conditions {
		if cond.Type == hyperv1.NodePoolUpdatingConfigConditionType {
			foundUpdatingConfig = true
			Expect(string(cond.Status)).To(Equal(string(metav1.ConditionFalse)),
				"UpdatingConfig should be False — in-place pull secret update must not trigger a rollout")
			break
		}
	}
	Expect(foundUpdatingConfig).To(BeTrue(),
		"NodePool %s should have UpdatingConfig condition", nodePool.Name)

	nodeList := &corev1.NodeList{}
	Expect(hcClient.List(tc.Context, nodeList, crclient.MatchingLabels{
		hyperv1.NodePoolLabel: np.Name,
	})).To(Succeed())
	Expect(len(nodeList.Items)).To(Equal(int(nodeCount)),
		"node count changed — unexpected rollout")
}

func verifyGlobalPSLabel(tc *internal.TestContext, hcClient crclient.Client, np *hyperv1.NodePool, nodeCount int32) {
	globalPSLabelKey := "hypershift.openshift.io/nodepool-globalps-enabled"
	Eventually(func(g Gomega) {
		nodeList := &corev1.NodeList{}
		g.Expect(hcClient.List(tc.Context, nodeList, crclient.MatchingLabels{
			hyperv1.NodePoolLabel: np.Name,
		})).To(Succeed())
		g.Expect(nodeList.Items).To(HaveLen(int(nodeCount)),
			"expected %d nodes for NodePool %s", nodeCount, np.Name)
		for _, node := range nodeList.Items {
			g.Expect(node.Labels).To(HaveKeyWithValue(globalPSLabelKey, "true"),
				"node %s should have the globalPS label", node.Name)
		}
	}, 30*time.Second, 5*time.Second).Should(Succeed())
}

func createAdditionalPullSecret(tc *internal.TestContext, hcClient crclient.Client, pullSecretData []byte) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "additional-pull-secret",
			Namespace: "kube-system",
		},
		Type: corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: pullSecretData,
		},
	}
	err := hcClient.Create(tc.Context, secret)
	if apierrors.IsAlreadyExists(err) {
		existing := &corev1.Secret{}
		Expect(hcClient.Get(tc.Context, crclient.ObjectKeyFromObject(secret), existing)).To(Succeed(),
			"failed to get existing additional-pull-secret for reconciliation")
		existing.Data = secret.Data
		existing.Type = secret.Type
		Expect(hcClient.Update(tc.Context, existing)).To(Succeed(),
			"failed to reconcile existing additional-pull-secret")
		return
	}
	Expect(err).NotTo(HaveOccurred(), "failed to create additional-pull-secret")
}

func verifyDaemonSetsReady(tc *internal.TestContext, hcClient crclient.Client, nodeCount int32) {
	daemonSets := []struct {
		name      string
		namespace string
	}{
		{"ovnkube-node", "openshift-ovn-kubernetes"},
		{hccomanifests.GlobalPullSecretDSName, hccomanifests.GlobalPullSecretNamespace},
	}

	konnDS := hccomanifests.KonnectivityAgentDaemonSet()
	daemonSets = append(daemonSets, struct {
		name      string
		namespace string
	}{konnDS.Name, konnDS.Namespace})

	for _, ds := range daemonSets {
		Eventually(func(g Gomega) {
			daemonSet := &appsv1.DaemonSet{}
			g.Expect(hcClient.Get(tc.Context, crclient.ObjectKey{
				Name:      ds.name,
				Namespace: ds.namespace,
			}, daemonSet)).To(Succeed(), "failed to get DaemonSet %s/%s", ds.namespace, ds.name)

			g.Expect(daemonSet.Status.DesiredNumberScheduled).To(BeNumerically(">=", nodeCount),
				"DaemonSet %s desired=%d < minExpected=%d", ds.name, daemonSet.Status.DesiredNumberScheduled, nodeCount)
			g.Expect(daemonSet.Status.NumberReady).To(Equal(daemonSet.Status.DesiredNumberScheduled),
				"DaemonSet %s not fully ready: %d/%d", ds.name, daemonSet.Status.NumberReady, daemonSet.Status.DesiredNumberScheduled)
		}, 20*time.Minute, 10*time.Second).Should(Succeed(), "DaemonSet %s/%s did not become ready", ds.namespace, ds.name)
	}
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:GlobalPullSecret] Global Pull Secret", Label("lifecycle", "global-pull-secret"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")

		testCtx.ValidateHostedCluster()
	})

	RegisterGlobalPullSecretTests(func() *internal.TestContext { return testCtx })
})
