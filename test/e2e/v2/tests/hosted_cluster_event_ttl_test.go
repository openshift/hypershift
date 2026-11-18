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
	v2util "github.com/openshift/hypershift/test/e2e/v2/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilrand "k8s.io/apimachinery/pkg/util/rand"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// eventTTLTestMinutes is inside the supported 5-180 range and differs from the 180m default, so
	// the assertions below distinguish a propagated value from the default.
	eventTTLTestMinutes = 30

	// eventTTLMinimumMinutes is the smallest value the API accepts. The expiry spec uses it so the
	// test waits out a real TTL rather than parking a CI lane for hours.
	eventTTLMinimumMinutes = 5

	// defaultEventTTL is the value the control plane operator renders when eventTTLMinutes is unset.
	defaultEventTTL = "180m"

	// hostedClusterCRDName is the HostedCluster CRD, inspected to detect whether the
	// KubeAPIServerEventTTL-gated eventTTLMinutes field is present in the installed schema.
	hostedClusterCRDName = "hostedclusters.hypershift.openshift.io"

	// kasDeploymentName is the kube-apiserver deployment in the control plane namespace.
	kasDeploymentName = "kube-apiserver"

	// eventTTLProbeNamespace is the guest cluster namespace the probe events are written to.
	eventTTLProbeNamespace = "default"

	// eventTTLExpiryBudget bounds the wait for a 5 minute TTL to actually remove an event. The
	// kube-apiserver attaches an etcd lease at write time and reuses leases within a short window,
	// so expiry lands shortly after the TTL rather than exactly on it.
	eventTTLExpiryBudget = 10 * time.Minute
)

func RegisterHostedClusterEventTTLTests(getTestCtx internal.TestContextGetter) {
	EventTTLPropagationTest(getTestCtx)
}

// kasEventTTL reads the --event-ttl value the control plane operator rendered into the
// kube-apiserver config ConfigMap in the control plane namespace.
func kasEventTTL(tc *internal.TestContext) (string, error) {
	cm := cpomanifests.KubeAPIServerConfigMap(tc.ControlPlaneNamespace)
	if err := tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(cm), cm); err != nil {
		return "", fmt.Errorf("failed to get %s ConfigMap: %w", cm.Name, err)
	}

	raw, ok := cm.Data[cpomanifests.KubeAPIServerConfigKey]
	if !ok {
		return "", fmt.Errorf("%s ConfigMap has no %q key", cm.Name, cpomanifests.KubeAPIServerConfigKey)
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

// eventTTLMatches reports whether two --event-ttl values represent the same duration.
// The kube-apiserver accepts any Go duration spelling, so equivalent values such as
// "3h" and "180m" compare equal, while a genuinely different duration compares false.
func eventTTLMatches(got, want string) (bool, error) {
	gotDuration, err := time.ParseDuration(got)
	if err != nil {
		return false, fmt.Errorf("failed to parse event-ttl %q: %w", got, err)
	}
	wantDuration, err := time.ParseDuration(want)
	if err != nil {
		return false, fmt.Errorf("failed to parse expected event-ttl %q: %w", want, err)
	}
	return gotDuration == wantDuration, nil
}

// eventTTLMinutesInCRDSchema reports whether the installed HostedCluster CRD exposes
// spec.operatorConfiguration.kubeAPIServer.eventTTLMinutes. The field is gated on
// KubeAPIServerEventTTL, so the HyperShift Operator only installs it in the
// TechPreviewNoUpgrade CRD variant. On a Default management cluster the field is absent from the
// schema and the API server silently prunes it from any write, which would otherwise leave these
// tests waiting for a propagation that can never happen.
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

// waitForKASRolloutComplete waits until the kube-apiserver deployment has fully rolled out on
// the generation that carries the new config: the latest generation is observed and every
// replica is updated, present, and available.
//
// Rendering --event-ttl into the config ConfigMap only proves the control plane operator agreed
// with the field. This proves the kube-apiserver itself accepted the value: a flag the
// kube-apiserver rejects fails at startup, so the new pods would never become available and the
// old ones would stay up while the ConfigMap already shows the new value.
func waitForKASRolloutComplete(tc *internal.TestContext) {
	Eventually(func(g Gomega) {
		deployment := &appsv1.Deployment{}
		g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{Namespace: tc.ControlPlaneNamespace, Name: kasDeploymentName}, deployment)).
			To(Succeed(), "failed to get the %s deployment", kasDeploymentName)
		g.Expect(deployment.Spec.Replicas).NotTo(BeNil(), "%s deployment should have spec.replicas set", kasDeploymentName)

		desired := *deployment.Spec.Replicas
		g.Expect(deployment.Status.ObservedGeneration).To(BeNumerically(">=", deployment.Generation),
			"%s deployment status should observe the latest generation", kasDeploymentName)
		g.Expect(deployment.Status.UpdatedReplicas).To(Equal(desired), "%s should have all replicas updated", kasDeploymentName)
		g.Expect(deployment.Status.Replicas).To(Equal(desired), "%s should have no surge/terminating replicas remaining", kasDeploymentName)
		g.Expect(deployment.Status.AvailableReplicas).To(Equal(desired), "%s should have all replicas available", kasDeploymentName)
	}).WithContext(tc.Context).WithTimeout(15*time.Minute).WithPolling(10*time.Second).Should(Succeed(),
		"the %s deployment should roll out and stay available on the new event-ttl", kasDeploymentName)
}

// configureEventTTL sets eventTTLMinutes on the HostedCluster, waits for the control plane
// operator to render it and for the kube-apiserver to come back on the new value, and registers
// the cleanup that restores the cluster to the configuration it was found in. The field is day-2
// mutable, so leaving it set would hand the next spec a differently configured cluster.
func configureEventTTL(tc *internal.TestContext, hc *hyperv1.HostedCluster, minutes int32) {
	restorePatch := restoreEventTTLPatch(hc)
	DeferCleanup(func() {
		Expect(tc.MgmtClient.Patch(tc.Context, hc, crclient.RawPatch(types.MergePatchType, restorePatch))).To(Succeed())

		Eventually(func(g Gomega) {
			ttl, err := kasEventTTL(tc)
			g.Expect(err).NotTo(HaveOccurred())
			matched, err := eventTTLMatches(ttl, defaultEventTTL)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(matched).To(BeTrue(), "expected the default event-ttl %q, got %q", defaultEventTTL, ttl)
		}).WithContext(tc.Context).WithTimeout(10*time.Minute).WithPolling(10*time.Second).
			Should(Succeed(), "kube-apiserver should return to the default event-ttl")

		// Leave the control plane settled so a later failure is attributed to the spec that
		// caused it rather than to whichever spec shares the cluster next.
		waitForKASRolloutComplete(tc)
	})

	patch := []byte(fmt.Sprintf(`{"spec":{"operatorConfiguration":{"kubeAPIServer":{"eventTTLMinutes":%d}}}}`, minutes))
	Expect(tc.MgmtClient.Patch(tc.Context, hc, crclient.RawPatch(types.MergePatchType, patch))).To(Succeed())

	Eventually(func(g Gomega) {
		ttl, err := kasEventTTL(tc)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(ttl).To(Equal(fmt.Sprintf("%dm", minutes)))
	}).WithContext(tc.Context).WithTimeout(10*time.Minute).WithPolling(10*time.Second).Should(Succeed(),
		"the field should propagate to the kube-apiserver without recreating the cluster")

	waitForKASRolloutComplete(tc)
}

// eventTTLProbeEvent builds a synthetic event for the guest cluster. The involved object is the
// namespace the event lives in, which keeps it valid without depending on any workload: core v1
// event validation only requires that the event namespace and the involved object namespace
// agree, not that the referenced object exists.
func eventTTLProbeEvent(prefix string) *corev1.Event {
	now := metav1.Now()
	return &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s", prefix, utilrand.String(8)),
			Namespace: eventTTLProbeNamespace,
		},
		InvolvedObject: corev1.ObjectReference{
			APIVersion: "v1",
			Kind:       "Namespace",
			Name:       eventTTLProbeNamespace,
			Namespace:  eventTTLProbeNamespace,
		},
		Reason:         "HyperShiftEventTTLProbe",
		Message:        "synthetic event written by the HyperShift event TTL e2e test",
		Type:           corev1.EventTypeNormal,
		Source:         corev1.EventSource{Component: "hypershift-e2e"},
		FirstTimestamp: now,
		LastTimestamp:  now,
		Count:          1,
	}
}

// createEventTTLProbe writes a probe event into the guest cluster and registers its deletion.
// The whole point of the expiry spec is that the kube-apiserver removes these on its own, so the
// cleanup tolerates an already-deleted event.
func createEventTTLProbe(tc *internal.TestContext, guestClient crclient.Client, prefix string) *corev1.Event {
	event := eventTTLProbeEvent(prefix)
	Expect(guestClient.Create(tc.Context, event)).To(Succeed(), "failed to create the %s probe event", prefix)
	DeferCleanup(func() {
		if err := guestClient.Delete(tc.Context, event); err != nil && !apierrors.IsNotFound(err) {
			Expect(err).NotTo(HaveOccurred(), "failed to clean up the %s probe event", prefix)
		}
	})
	return event
}

func EventTTLPropagationTest(getTestCtx internal.TestContextGetter) {
	When("the event TTL is configured on a running HostedCluster", func() {
		var tc *internal.TestContext
		var hc *hyperv1.HostedCluster

		BeforeEach(func() {
			tc = getTestCtx()

			// eventTTLMinutes and its rendering into the kube-apiserver only exist from 5.1
			// onwards. This test image is also consumed by release-branch presubmits (5.0,
			// 4.22) whose CPO hard-codes --event-ttl to 3h and ignores the field, so running
			// there would only time out waiting for a propagation that can never happen.
			tc.SkipIfVersionBelow(v2util.Version51)

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
		})

		It("should render the platform default --event-ttl when eventTTLMinutes is unset", Label(internal.InformingLabel), func() {
			By("reading the rendered kube-apiserver configuration")
			ttl, err := kasEventTTL(tc)
			Expect(err).NotTo(HaveOccurred())

			matched, err := eventTTLMatches(ttl, defaultEventTTL)
			Expect(err).NotTo(HaveOccurred())
			Expect(matched).To(BeTrue(),
				"an unconfigured HostedCluster should run the platform default event-ttl %q, got %q", defaultEventTTL, ttl)
		})

		It("should reject eventTTLMinutes outside the supported range", Label(internal.InformingLabel), func() {
			// The bounds are enforced by the CRD schema, so this proves the installed CRD carries
			// them rather than silently accepting a value the kube-apiserver would choke on.
			By("rejecting a value below the supported minimum")
			belowMinimum := []byte(`{"spec":{"operatorConfiguration":{"kubeAPIServer":{"eventTTLMinutes":4}}}}`)
			err := tc.MgmtClient.Patch(tc.Context, hc, crclient.RawPatch(types.MergePatchType, belowMinimum))
			Expect(err).To(HaveOccurred(), "eventTTLMinutes below the 5 minute minimum should be rejected at admission")
			Expect(apierrors.IsInvalid(err)).To(BeTrue(), "expected a validation error, got %v", err)

			By("rejecting a value above the supported maximum")
			aboveMaximum := []byte(`{"spec":{"operatorConfiguration":{"kubeAPIServer":{"eventTTLMinutes":181}}}}`)
			err = tc.MgmtClient.Patch(tc.Context, hc, crclient.RawPatch(types.MergePatchType, aboveMaximum))
			Expect(err).To(HaveOccurred(), "eventTTLMinutes above the 180 minute maximum should be rejected at admission")
			Expect(apierrors.IsInvalid(err)).To(BeTrue(), "expected a validation error, got %v", err)

			By("confirming the rejected writes left the rendered configuration untouched")
			ttl, err := kasEventTTL(tc)
			Expect(err).NotTo(HaveOccurred())
			matched, err := eventTTLMatches(ttl, defaultEventTTL)
			Expect(err).NotTo(HaveOccurred())
			Expect(matched).To(BeTrue(), "a rejected write should not change the rendered event-ttl, got %q", ttl)
		})

		It("should render the configured value as the kube-apiserver --event-ttl flag", Label("event-ttl-propagation", internal.InformingLabel), func() {
			By("setting eventTTLMinutes on the HostedCluster")
			configureEventTTL(tc, hc, eventTTLTestMinutes)

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
			}).WithContext(tc.Context).WithTimeout(5 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
		})

		It("should expire guest cluster events once the configured TTL has elapsed", Label(internal.InformingLabel), func() {
			// This is the user-visible contract: lowering eventTTLMinutes has to make events
			// actually disappear from the hosted cluster, not merely change a rendered flag.
			guestClient, err := tc.GetHostedClusterClient(hc)
			Expect(err).NotTo(HaveOccurred())

			By("writing a control event while the cluster is still on the default TTL")
			// The kube-apiserver attaches the TTL as an etcd lease at write time, so this event
			// keeps the 180m default and must outlive the short-TTL probe below. It is what
			// separates "the TTL was applied" from "something deleted every event".
			controlEvent := createEventTTLProbe(tc, guestClient, "event-ttl-control")

			By("lowering eventTTLMinutes to the supported minimum")
			configureEventTTL(tc, hc, eventTTLMinimumMinutes)

			By("writing a probe event under the lowered TTL")
			probeEvent := createEventTTLProbe(tc, guestClient, "event-ttl-probe")

			By("confirming the probe event is readable before its TTL elapses")
			Expect(guestClient.Get(tc.Context, crclient.ObjectKeyFromObject(probeEvent), &corev1.Event{})).
				To(Succeed(), "the probe event should exist immediately after being written")

			By("waiting for the kube-apiserver to expire the probe event")
			Eventually(func(g Gomega) {
				err := guestClient.Get(tc.Context, crclient.ObjectKeyFromObject(probeEvent), &corev1.Event{})
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue(),
					"expected the probe event to be gone, got err=%v", err)
			}).WithContext(tc.Context).WithTimeout(eventTTLExpiryBudget).WithPolling(15*time.Second).Should(Succeed(),
				"an event written under a %d minute TTL should be removed within %s",
				eventTTLMinimumMinutes, eventTTLExpiryBudget)

			By("confirming the control event written under the default TTL is still present")
			Eventually(func(g Gomega) {
				g.Expect(guestClient.Get(tc.Context, crclient.ObjectKeyFromObject(controlEvent), &corev1.Event{})).To(Succeed())
			}).WithContext(tc.Context).WithTimeout(2*time.Minute).WithPolling(10*time.Second).Should(Succeed(),
				"an event written before the TTL was lowered should keep its original %s TTL", defaultEventTTL)
		})

		It("should restore the platform default when eventTTLMinutes is cleared", Label(internal.InformingLabel), func() {
			// Capture the clearing patch before the field is set: a successful Patch writes the
			// server's response back into hc, so computing it afterwards would see the
			// configuration this spec just added and emit a patch that empties kubeAPIServer,
			// which MinProperties=1 rejects.
			clearPatch := restoreEventTTLPatch(hc)

			By("setting eventTTLMinutes on the HostedCluster")
			// configureEventTTL registers the restore as a cleanup, so this spec drives the
			// change and then asserts the revert explicitly rather than relying on teardown.
			configureEventTTL(tc, hc, eventTTLTestMinutes)

			By("clearing the field again")
			Expect(tc.MgmtClient.Patch(tc.Context, hc, crclient.RawPatch(types.MergePatchType, clearPatch))).To(Succeed())

			By("waiting for the kube-apiserver to return to the platform default")
			Eventually(func(g Gomega) {
				ttl, err := kasEventTTL(tc)
				g.Expect(err).NotTo(HaveOccurred())
				matched, err := eventTTLMatches(ttl, defaultEventTTL)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(matched).To(BeTrue(), "expected the default event-ttl %q, got %q", defaultEventTTL, ttl)
			}).WithContext(tc.Context).WithTimeout(10*time.Minute).WithPolling(10*time.Second).Should(Succeed(),
				"clearing eventTTLMinutes should return the kube-apiserver to the platform default")

			waitForKASRolloutComplete(tc)
		})
	})
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][OCPFeatureGate:KubeAPIServerEventTTL] Hosted Cluster Event TTL", Label("lifecycle", "hosted-cluster-event-ttl"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
	})

	RegisterHostedClusterEventTTLTests(func() *internal.TestContext { return testCtx })
})
