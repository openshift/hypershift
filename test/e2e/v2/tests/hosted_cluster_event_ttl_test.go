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

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/kas"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// eventTTLTestMinutes is inside the supported 5-180 range and differs from the 3h default, so the
// assertions below distinguish a propagated value from the default.
const eventTTLTestMinutes = "30"

func RegisterHostedClusterEventTTLTests(getTestCtx internal.TestContextGetter) {
	EventTTLPropagationTest(getTestCtx)
}

// kasEventTTL reads the --event-ttl value the control plane operator rendered into the
// kube-apiserver config ConfigMap in the control plane namespace.
func kasEventTTL(tc *internal.TestContext) (string, error) {
	cm := &corev1.ConfigMap{}
	if err := tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{
		Namespace: tc.ControlPlaneNamespace,
		Name:      "kas-config",
	}, cm); err != nil {
		return "", fmt.Errorf("failed to get kas-config ConfigMap: %w", err)
	}

	raw, ok := cm.Data[kas.KubeAPIServerConfigKey]
	if !ok {
		return "", fmt.Errorf("kas-config ConfigMap has no %q key", kas.KubeAPIServerConfigKey)
	}

	// Decode only the argument of interest; the full KubeAPIServerConfig type is not needed here.
	var config struct {
		APIServerArguments map[string][]string `json:"apiServerArguments"`
	}
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		return "", fmt.Errorf("failed to unmarshal kube-apiserver config: %w", err)
	}

	values := config.APIServerArguments["event-ttl"]
	if len(values) != 1 {
		return "", fmt.Errorf("expected exactly one event-ttl argument, got %v", values)
	}

	return values[0], nil
}

func EventTTLPropagationTest(getTestCtx internal.TestContextGetter) {
	When("the event TTL annotation is set on a running HostedCluster", func() {
		var tc *internal.TestContext
		var hc *hyperv1.HostedCluster

		BeforeEach(func() {
			tc = getTestCtx()

			var err error
			hc, err = tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())

			if _, isSet := hc.Annotations[hyperv1.KubeAPIServerEventTTLMinutes]; isSet {
				Skip("HostedCluster already sets the event TTL annotation")
			}

			// The annotation is day-2 mutable, so restore the default rather than leaving the
			// hosted cluster configured for whichever test runs next.
			DeferCleanup(func() {
				patch := []byte(fmt.Sprintf(`{"metadata":{"annotations":{%q:null}}}`, hyperv1.KubeAPIServerEventTTLMinutes))
				Expect(tc.MgmtClient.Patch(tc.Context, hc, crclient.RawPatch(types.MergePatchType, patch))).To(Succeed())

				Eventually(func(g Gomega) {
					ttl, err := kasEventTTL(tc)
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(ttl).To(Equal("3h"))
				}, 10*time.Minute, 10*time.Second).Should(Succeed(), "kube-apiserver should return to the default event-ttl")
			})
		})

		It("should render the configured value as the kube-apiserver --event-ttl flag", func() {
			By("confirming the kube-apiserver starts on the 3h default")
			ttl, err := kasEventTTL(tc)
			Expect(err).NotTo(HaveOccurred())
			Expect(ttl).To(Equal("3h"), "an unconfigured HostedCluster should use the default event-ttl")

			By("setting the event TTL annotation on the HostedCluster")
			patch := []byte(fmt.Sprintf(`{"metadata":{"annotations":{%q:%q}}}`,
				hyperv1.KubeAPIServerEventTTLMinutes, eventTTLTestMinutes))
			Expect(tc.MgmtClient.Patch(tc.Context, hc, crclient.RawPatch(types.MergePatchType, patch))).To(Succeed())

			By("waiting for the value to reach the kube-apiserver config")
			Eventually(func(g Gomega) {
				ttl, err := kasEventTTL(tc)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(ttl).To(Equal(eventTTLTestMinutes + "m"))
			}, 10*time.Minute, 10*time.Second).Should(Succeed(),
				"the annotation should propagate to the kube-apiserver without recreating the cluster")

			By("confirming the HostedCluster configuration is still reported as valid")
			Eventually(func(g Gomega) {
				current := &hyperv1.HostedCluster{}
				g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(hc), current)).To(Succeed())

				for _, condition := range current.Status.Conditions {
					if condition.Type == string(hyperv1.ValidHostedClusterConfiguration) {
						g.Expect(string(condition.Status)).To(Equal("True"), "reason: %s, message: %s", condition.Reason, condition.Message)
						return
					}
				}
				g.Expect(false).To(BeTrue(), "ValidHostedClusterConfiguration condition not found")
			}, 5*time.Minute, 10*time.Second).Should(Succeed())
		})
	})
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:EventTTL] Hosted Cluster Event TTL", Label("hosted-cluster-event-ttl"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
	})

	RegisterHostedClusterEventTTLTests(func() *internal.TestContext { return testCtx })
})
