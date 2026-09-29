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
	cpomanifests "github.com/openshift/hypershift/pkg/manifests/cpo"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/types"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// eventTTLTestMinutes is inside the supported 5-180 range and differs from the 180m default, so
	// the assertions below distinguish a propagated value from the default.
	eventTTLTestMinutes = 30

	// defaultEventTTL is the value the control plane operator renders when eventTTLMinutes is unset.
	defaultEventTTL = "180m"

	// hostedClusterCRDName is the HostedCluster CRD, inspected to detect whether the
	// KubeAPIServerEventTTL-gated eventTTLMinutes field is present in the installed schema.
	hostedClusterCRDName = "hostedclusters.hypershift.openshift.io"
)

func RegisterHostedClusterEventTTLTests(getTestCtx internal.TestContextGetter) {
	EventTTLPropagationTest(getTestCtx)
}

// kasEventTTL reads the --event-ttl value the control plane operator rendered into the
// kube-apiserver config ConfigMap in the control plane namespace.
func kasEventTTL(tc *internal.TestContext) (string, error) {
	cm := &corev1.ConfigMap{}
	if err := tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{
		Namespace: tc.ControlPlaneNamespace,
		Name:      cpomanifests.KubeAPIServerConfigMapName,
	}, cm); err != nil {
		return "", fmt.Errorf("failed to get %s ConfigMap: %w", cpomanifests.KubeAPIServerConfigMapName, err)
	}

	raw, ok := cm.Data[cpomanifests.KubeAPIServerConfigKey]
	if !ok {
		return "", fmt.Errorf("%s ConfigMap has no %q key", cpomanifests.KubeAPIServerConfigMapName, cpomanifests.KubeAPIServerConfigKey)
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

// eventTTLMinutesInCRDSchema reports whether the installed HostedCluster CRD exposes
// spec.operatorConfiguration.kubeAPIServer.eventTTLMinutes. The field is gated on
// KubeAPIServerEventTTL, so the HyperShift Operator only installs it in the
// TechPreviewNoUpgrade CRD variant. On a Default management cluster the field is absent from the
// schema and the API server silently prunes it from any write, which would otherwise leave this
// test waiting for a propagation that can never happen.
func eventTTLMinutesInCRDSchema(tc *internal.TestContext) (bool, error) {
	crd := &apiextensionsv1.CustomResourceDefinition{}
	if err := tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{Name: hostedClusterCRDName}, crd); err != nil {
		return false, fmt.Errorf("failed to get %s CRD: %w", hostedClusterCRDName, err)
	}

	for _, version := range crd.Spec.Versions {
		if version.Name != hyperv1.GroupVersion.Version || version.Schema == nil || version.Schema.OpenAPIV3Schema == nil {
			continue
		}

		schema := version.Schema.OpenAPIV3Schema
		for _, field := range []string{"spec", "operatorConfiguration", "kubeAPIServer", "eventTTLMinutes"} {
			next, ok := schema.Properties[field]
			if !ok {
				return false, nil
			}
			schema = &next
		}
		return true, nil
	}

	return false, fmt.Errorf("%s CRD does not serve version %s", hostedClusterCRDName, hyperv1.GroupVersion.Version)
}

// restoreEventTTLPatch builds the merge patch that undoes setting eventTTLMinutes on original.
// KubeAPIServerOperatorSpec carries MinProperties=1, so clearing eventTTLMinutes alone would
// leave an empty kubeAPIServer object and be rejected at admission. The whole object, or the
// whole operatorConfiguration, has to go when the test introduced it.
func restoreEventTTLPatch(original *hyperv1.HostedCluster) []byte {
	switch {
	case original.Spec.OperatorConfiguration == nil:
		return []byte(`{"spec":{"operatorConfiguration":null}}`)
	case original.Spec.OperatorConfiguration.KubeAPIServer == (hyperv1.KubeAPIServerOperatorSpec{}):
		return []byte(`{"spec":{"operatorConfiguration":{"kubeAPIServer":null}}}`)
	default:
		return []byte(`{"spec":{"operatorConfiguration":{"kubeAPIServer":{"eventTTLMinutes":null}}}}`)
	}
}

func EventTTLPropagationTest(getTestCtx internal.TestContextGetter) {
	When("the event TTL is configured on a running HostedCluster", func() {
		var tc *internal.TestContext
		var hc *hyperv1.HostedCluster

		BeforeEach(func() {
			tc = getTestCtx()

			var err error
			hc, err = tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())

			supported, err := eventTTLMinutesInCRDSchema(tc)
			Expect(err).NotTo(HaveOccurred())
			if !supported {
				Skip("spec.operatorConfiguration.kubeAPIServer.eventTTLMinutes is gated on KubeAPIServerEventTTL " +
					"and is absent from the installed HostedCluster CRD. Set HYPERSHIFT_FEATURESET=TechPreviewNoUpgrade " +
					"on the HyperShift operator to enable it.")
			}

			if hc.Spec.OperatorConfiguration != nil && hc.Spec.OperatorConfiguration.KubeAPIServer.EventTTLMinutes != 0 {
				Skip("HostedCluster already sets spec.operatorConfiguration.kubeAPIServer.eventTTLMinutes")
			}

			// The field is day-2 mutable, so restore the captured configuration rather than
			// leaving the hosted cluster configured for whichever test runs next.
			restorePatch := restoreEventTTLPatch(hc)
			DeferCleanup(func() {
				Expect(tc.MgmtClient.Patch(tc.Context, hc, crclient.RawPatch(types.MergePatchType, restorePatch))).To(Succeed())

				Eventually(func(g Gomega) {
					ttl, err := kasEventTTL(tc)
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(ttl).To(Equal(defaultEventTTL))
				}, 10*time.Minute, 10*time.Second).Should(Succeed(), "kube-apiserver should return to the default event-ttl")
			})
		})

		It("should render the configured value as the kube-apiserver --event-ttl flag", Label("event-ttl-propagation"), func() {
			By("confirming the kube-apiserver starts on the 180m default")
			ttl, err := kasEventTTL(tc)
			Expect(err).NotTo(HaveOccurred())
			Expect(ttl).To(Equal(defaultEventTTL), "an unconfigured HostedCluster should use the default event-ttl")

			By("setting eventTTLMinutes on the HostedCluster")
			patch := []byte(fmt.Sprintf(`{"spec":{"operatorConfiguration":{"kubeAPIServer":{"eventTTLMinutes":%d}}}}`, eventTTLTestMinutes))
			Expect(tc.MgmtClient.Patch(tc.Context, hc, crclient.RawPatch(types.MergePatchType, patch))).To(Succeed())

			By("waiting for the value to reach the kube-apiserver config")
			Eventually(func(g Gomega) {
				ttl, err := kasEventTTL(tc)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(ttl).To(Equal(fmt.Sprintf("%dm", eventTTLTestMinutes)))
			}, 10*time.Minute, 10*time.Second).Should(Succeed(),
				"the field should propagate to the kube-apiserver without recreating the cluster")

			By("confirming the HostedCluster configuration is still reported as valid")
			Eventually(func(g Gomega) {
				current := &hyperv1.HostedCluster{}
				g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(hc), current)).To(Succeed())

				g.Expect(current.Status.Conditions).NotTo(BeEmpty(), "HostedCluster should report status conditions")

				found := false
				for _, condition := range current.Status.Conditions {
					if condition.Type == string(hyperv1.ValidHostedClusterConfiguration) {
						found = true
						g.Expect(string(condition.Status)).To(Equal("True"), "reason: %s, message: %s", condition.Reason, condition.Message)
						break
					}
				}
				g.Expect(found).To(BeTrue(), "ValidHostedClusterConfiguration condition not found")
			}, 5*time.Minute, 10*time.Second).Should(Succeed())
		})
	})
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:EventTTL] Hosted Cluster Event TTL", Label("lifecycle", "hosted-cluster-event-ttl"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
	})

	RegisterHostedClusterEventTTLTests(func() *internal.TestContext { return testCtx })
})
