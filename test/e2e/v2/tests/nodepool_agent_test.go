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
	"crypto/tls"
	"fmt"
	"net/http"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	routev1 "github.com/openshift/api/route/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// RegisterNodePoolAgentTests registers all Agent NodePool test cases.
func RegisterNodePoolAgentTests(getTestCtx internal.TestContextGetter) {
	NodePoolAgentAddScaleDeleteTest(getTestCtx)
	NodePoolAgentReplaceTest(getTestCtx)
	NodePoolAgentAddWhenUnavailableTest(getTestCtx)
	NodePoolAgentOverScaleTest(getTestCtx)
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:NodePoolAgent] NodePool Agent Operations", Label("nodepool-agent"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
	})

	RegisterNodePoolAgentTests(func() *internal.TestContext { return testCtx })
})

// NodePoolAgentAddScaleDeleteTest creates an additional NodePool alongside the default one,
// scales both while an app is running behind a Route, then deletes the extra NodePool.
func NodePoolAgentAddScaleDeleteTest(getTestCtx internal.TestContextGetter) {
	It("should add, scale, and delete an extra NodePool while an app is running [OCP-69710]", Label("lifecycle"), func() {
		testCtx := getTestCtx()
		testCtx.SkipIfNotPlatform(hyperv1.AgentPlatform)
		hc, err := testCtx.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
		hcClient, err := testCtx.GetHostedClusterClient(hc)
		Expect(err).NotTo(HaveOccurred())
		ctx := testCtx.Context

		defaultNP := getDefaultNodePool(ctx, testCtx.MgmtClient, hc)
		Expect(defaultNP).NotTo(BeNil(), "default NodePool should exist")
		originalTemplate := defaultNP.DeepCopy()
		originalReplicas := defaultNP.Spec.Replicas
		DeferCleanup(func() {
			ensureDefaultNodePoolRestored(ctx, testCtx.MgmtClient, hcClient, hc.Spec.Platform.Type, originalTemplate, originalReplicas)
		})

		Expect(e2eutil.UpdateObject(GinkgoTB(), ctx, testCtx.MgmtClient, defaultNP, func(obj *hyperv1.NodePool) {
			obj.Spec.Replicas = ptr.To[int32](2)
		})).To(Succeed(), "failed to scale default NodePool %s to 2 replicas", defaultNP.Name)
		e2eutil.WaitForReadyNodesByNodePool(GinkgoTB(), ctx, hcClient, defaultNP, hc.Spec.Platform.Type)

		extraNP := buildAgentNodePool(defaultNP, "extra-cpu-nodes", 2)
		Expect(testCtx.MgmtClient.Create(ctx, extraNP)).To(Succeed(), "failed to create extra NodePool %s", extraNP.Name)
		GinkgoWriter.Printf("Created extra NodePool %s\n", extraNP.Name)
		DeferCleanup(func() {
			cleanupNodePool(ctx, testCtx.MgmtClient, extraNP)
		})
		e2eutil.WaitForReadyNodesByNodePool(GinkgoTB(), ctx, hcClient, extraNP, hc.Spec.Platform.Type)

		app := deployTestApp(ctx, hcClient, "nodepool-agent-add-scale")
		verifyRouteResponds(ctx, hcClient, app)

		Expect(e2eutil.UpdateObject(GinkgoTB(), ctx, testCtx.MgmtClient, extraNP, func(obj *hyperv1.NodePool) {
			obj.Spec.Replicas = ptr.To[int32](3)
		})).To(Succeed(), "failed to scale extra NodePool %s to 3 replicas", extraNP.Name)
		e2eutil.WaitForReadyNodesByNodePool(GinkgoTB(), ctx, hcClient, extraNP, hc.Spec.Platform.Type)
		verifyRouteResponds(ctx, hcClient, app)

		scaleDeploymentAndWaitReady(ctx, hcClient, app, 25)

		cleanupNodePool(ctx, testCtx.MgmtClient, extraNP)
		e2eutil.EventuallyNotFound(GinkgoTB(), ctx, testCtx.MgmtClient, extraNP, e2eutil.WithTimeout(10*time.Minute))
	})
}

// NodePoolAgentReplaceTest deletes the default NodePool while an app is running on a
// replacement NodePool, then re-creates the default NodePool to restore cluster state.
func NodePoolAgentReplaceTest(getTestCtx internal.TestContextGetter) {
	It("should survive replacing the default NodePool with a new one [OCP-71573]", Label("lifecycle"), func() {
		testCtx := getTestCtx()
		testCtx.SkipIfNotPlatform(hyperv1.AgentPlatform)
		hc, err := testCtx.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
		hcClient, err := testCtx.GetHostedClusterClient(hc)
		Expect(err).NotTo(HaveOccurred())
		ctx := testCtx.Context

		defaultNP := getDefaultNodePool(ctx, testCtx.MgmtClient, hc)
		Expect(defaultNP).NotTo(BeNil(), "default NodePool should exist")
		originalTemplate := defaultNP.DeepCopy()
		originalReplicas := defaultNP.Spec.Replicas
		DeferCleanup(func() {
			ensureDefaultNodePoolRestored(ctx, testCtx.MgmtClient, hcClient, hc.Spec.Platform.Type, originalTemplate, originalReplicas)
		})

		Expect(e2eutil.UpdateObject(GinkgoTB(), ctx, testCtx.MgmtClient, defaultNP, func(obj *hyperv1.NodePool) {
			obj.Spec.Replicas = ptr.To[int32](2)
		})).To(Succeed(), "failed to scale default NodePool %s to 2 replicas", defaultNP.Name)
		e2eutil.WaitForReadyNodesByNodePool(GinkgoTB(), ctx, hcClient, defaultNP, hc.Spec.Platform.Type)

		app := deployTestApp(ctx, hcClient, "nodepool-agent-replace")
		verifyRouteResponds(ctx, hcClient, app)

		replacementNP := buildAgentNodePool(defaultNP, "replacement", 2)
		Expect(testCtx.MgmtClient.Create(ctx, replacementNP)).To(Succeed(), "failed to create replacement NodePool %s", replacementNP.Name)
		GinkgoWriter.Printf("Created replacement NodePool %s\n", replacementNP.Name)
		DeferCleanup(func() {
			cleanupNodePool(ctx, testCtx.MgmtClient, replacementNP)
		})
		e2eutil.WaitForReadyNodesByNodePool(GinkgoTB(), ctx, hcClient, replacementNP, hc.Spec.Platform.Type)

		GinkgoWriter.Printf("Deleting default NodePool %s\n", defaultNP.Name)
		Expect(testCtx.MgmtClient.Delete(ctx, defaultNP)).To(Succeed(), "failed to delete default NodePool %s", defaultNP.Name)
		e2eutil.EventuallyNotFound(GinkgoTB(), ctx, testCtx.MgmtClient, defaultNP, e2eutil.WithTimeout(10*time.Minute))

		verifyRouteResponds(ctx, hcClient, app)
		scaleDeploymentAndWaitReady(ctx, hcClient, app, 10)

		recreatedDefault := recreateNodePool(ctx, testCtx.MgmtClient, originalTemplate, ptr.Deref(originalReplicas, 2))
		e2eutil.WaitForReadyNodesByNodePool(GinkgoTB(), ctx, hcClient, recreatedDefault, hc.Spec.Platform.Type)

		cleanupNodePool(ctx, testCtx.MgmtClient, replacementNP)
		e2eutil.EventuallyNotFound(GinkgoTB(), ctx, testCtx.MgmtClient, replacementNP, e2eutil.WithTimeout(10*time.Minute))

		verifyRouteResponds(ctx, hcClient, app)
	})
}

// NodePoolAgentAddWhenUnavailableTest exhausts all bare metal nodes on the default NodePool,
// then verifies a newly created NodePool's nodes queue at 0 ready until bare metal nodes are
// freed by scaling the default NodePool back down.
func NodePoolAgentAddWhenUnavailableTest(getTestCtx internal.TestContextGetter) {
	It("should queue a new NodePool's nodes until bare metal nodes are freed [OCP-71576]", Label("lifecycle"), func() {
		testCtx := getTestCtx()
		testCtx.SkipIfNotPlatform(hyperv1.AgentPlatform)
		hc, err := testCtx.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
		hcClient, err := testCtx.GetHostedClusterClient(hc)
		Expect(err).NotTo(HaveOccurred())
		ctx := testCtx.Context

		defaultNP := getDefaultNodePool(ctx, testCtx.MgmtClient, hc)
		Expect(defaultNP).NotTo(BeNil(), "default NodePool should exist")
		originalTemplate := defaultNP.DeepCopy()
		originalReplicas := defaultNP.Spec.Replicas
		DeferCleanup(func() {
			ensureDefaultNodePoolRestored(ctx, testCtx.MgmtClient, hcClient, hc.Spec.Platform.Type, originalTemplate, originalReplicas)
		})

		maxNodes := agentBMNodeCount()
		Expect(e2eutil.UpdateObject(GinkgoTB(), ctx, testCtx.MgmtClient, defaultNP, func(obj *hyperv1.NodePool) {
			obj.Spec.Replicas = ptr.To(maxNodes)
		})).To(Succeed(), "failed to scale default NodePool %s to %d replicas", defaultNP.Name, maxNodes)
		e2eutil.WaitForReadyNodesByNodePool(GinkgoTB(), ctx, hcClient, defaultNP, hc.Spec.Platform.Type)

		app := deployTestApp(ctx, hcClient, "nodepool-agent-unavailable")
		verifyRouteResponds(ctx, hcClient, app)

		extraNP := buildAgentNodePool(defaultNP, "queued", 2)
		Expect(testCtx.MgmtClient.Create(ctx, extraNP)).To(Succeed(), "failed to create extra NodePool %s", extraNP.Name)
		GinkgoWriter.Printf("Created extra NodePool %s while all bare metal nodes are exhausted\n", extraNP.Name)
		DeferCleanup(func() {
			cleanupNodePool(ctx, testCtx.MgmtClient, extraNP)
		})

		Consistently(func() (int, error) {
			return countReadyNodesForNodePool(ctx, hcClient, extraNP.Name)
		}).WithTimeout(45*time.Second).WithPolling(10*time.Second).
			Should(Equal(0), "NodePool %s should have 0 ready nodes while no bare metal nodes are available", extraNP.Name)

		Expect(e2eutil.UpdateObject(GinkgoTB(), ctx, testCtx.MgmtClient, defaultNP, func(obj *hyperv1.NodePool) {
			obj.Spec.Replicas = ptr.To[int32](2)
		})).To(Succeed(), "failed to scale default NodePool %s down to 2 replicas", defaultNP.Name)
		e2eutil.WaitForReadyNodesByNodePool(GinkgoTB(), ctx, hcClient, defaultNP, hc.Spec.Platform.Type)

		waitForExactReadyNodeCount(ctx, hcClient, extraNP, 2, 15*time.Minute)

		cleanupNodePool(ctx, testCtx.MgmtClient, extraNP)
		e2eutil.EventuallyNotFound(GinkgoTB(), ctx, testCtx.MgmtClient, extraNP, e2eutil.WithTimeout(10*time.Minute))

		verifyRouteResponds(ctx, hcClient, app)
	})
}

// NodePoolAgentOverScaleTest scales the default NodePool beyond the number of available
// bare metal machines and verifies the excess nodes remain pending rather than ready.
func NodePoolAgentOverScaleTest(getTestCtx internal.TestContextGetter) {
	It("should leave nodes pending when scaled beyond available bare metal machines [OCP-71124]", Label("lifecycle"), func() {
		testCtx := getTestCtx()
		testCtx.SkipIfNotPlatform(hyperv1.AgentPlatform)
		hc, err := testCtx.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
		hcClient, err := testCtx.GetHostedClusterClient(hc)
		Expect(err).NotTo(HaveOccurred())
		ctx := testCtx.Context

		defaultNP := getDefaultNodePool(ctx, testCtx.MgmtClient, hc)
		Expect(defaultNP).NotTo(BeNil(), "default NodePool should exist")
		originalTemplate := defaultNP.DeepCopy()
		originalReplicas := defaultNP.Spec.Replicas
		DeferCleanup(func() {
			ensureDefaultNodePoolRestored(ctx, testCtx.MgmtClient, hcClient, hc.Spec.Platform.Type, originalTemplate, originalReplicas)
		})

		maxNodes := agentBMNodeCount()
		overScaledReplicas := maxNodes + 2
		Expect(e2eutil.UpdateObject(GinkgoTB(), ctx, testCtx.MgmtClient, defaultNP, func(obj *hyperv1.NodePool) {
			obj.Spec.Replicas = ptr.To(overScaledReplicas)
		})).To(Succeed(), "failed to scale default NodePool %s to %d replicas", defaultNP.Name, overScaledReplicas)

		waitForExactReadyNodeCount(ctx, hcClient, defaultNP, int(maxNodes), 20*time.Minute)

		Expect(testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(defaultNP), defaultNP)).To(Succeed(),
			"failed to refresh default NodePool %s", defaultNP.Name)
		Expect(ptr.Deref(defaultNP.Spec.Replicas, 0)).To(Equal(overScaledReplicas),
			"NodePool %s should still report desired replicas of %d", defaultNP.Name, overScaledReplicas)

		readyCount, err := countReadyNodesForNodePool(ctx, hcClient, defaultNP.Name)
		Expect(err).NotTo(HaveOccurred(), "failed to count ready nodes for NodePool %s", defaultNP.Name)
		Expect(readyCount).To(Equal(int(maxNodes)),
			"NodePool %s should have %d ready nodes out of %d desired, with the rest pending", defaultNP.Name, maxNodes, overScaledReplicas)
	})
}

// Helper functions

// agentBMNodeCount returns the number of bare metal Agent nodes available in the test
// environment, as configured via the E2E_AGENT_BM_NODE_COUNT environment variable.
func agentBMNodeCount() int32 {
	GinkgoHelper()

	value := internal.GetEnvVarValue("E2E_AGENT_BM_NODE_COUNT")
	count, err := strconv.Atoi(value)
	Expect(err).NotTo(HaveOccurred(), "E2E_AGENT_BM_NODE_COUNT must be an integer, got %q", value)
	Expect(count).To(BeNumerically(">", 0), "E2E_AGENT_BM_NODE_COUNT must be positive")

	return int32(count)
}

// buildAgentNodePool builds a new NodePool from a template with the given replica count.
func buildAgentNodePool(template *hyperv1.NodePool, namePrefix string, replicas int32) *hyperv1.NodePool {
	GinkgoHelper()

	return buildTestNodePool(template, namePrefix, func(pool *hyperv1.NodePool) {
		pool.Spec.Replicas = ptr.To(replicas)
	})
}

// recreateNodePool creates a new NodePool using the name, namespace, and spec of template,
// as if restoring a NodePool that was previously deleted.
func recreateNodePool(ctx context.Context, client crclient.Client, template *hyperv1.NodePool, replicas int32) *hyperv1.NodePool {
	GinkgoHelper()

	np := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      template.Name,
			Namespace: template.Namespace,
		},
	}
	template.Spec.DeepCopyInto(&np.Spec)
	np.Spec.Replicas = ptr.To(replicas)

	Expect(client.Create(ctx, np)).To(Succeed(), "failed to re-create NodePool %s", np.Name)
	GinkgoWriter.Printf("Re-created NodePool %s\n", np.Name)

	return np
}

// ensureDefaultNodePoolRestored recreates the default NodePool from template if the test
// deleted it, or restores its original replica count if it still exists. It is intended for
// use in DeferCleanup so the default NodePool is repaired regardless of how far a test
// progressed before failing.
func ensureDefaultNodePoolRestored(ctx context.Context, mgmtClient, hcClient crclient.Client, platform hyperv1.PlatformType, template *hyperv1.NodePool, replicas *int32) {
	GinkgoHelper()

	current := &hyperv1.NodePool{}
	err := mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(template), current)
	switch {
	case apierrors.IsNotFound(err):
		current = recreateNodePool(ctx, mgmtClient, template, ptr.Deref(replicas, 1))
	case err != nil:
		Expect(err).NotTo(HaveOccurred(), "cleanup: failed to get default NodePool %s", template.Name)
	default:
		Expect(e2eutil.UpdateObject(GinkgoTB(), ctx, mgmtClient, current, func(obj *hyperv1.NodePool) {
			obj.Spec.Replicas = replicas
		})).To(Succeed(), "cleanup: failed to restore NodePool %s replicas", template.Name)
	}

	e2eutil.WaitForReadyNodesByNodePool(GinkgoTB(), ctx, hcClient, current, platform)
}

// countReadyNodesForNodePool returns the number of Ready nodes labeled for the given NodePool.
func countReadyNodesForNodePool(ctx context.Context, hcClient crclient.Client, npName string) (int, error) {
	nodes := &corev1.NodeList{}
	if err := hcClient.List(ctx, nodes, crclient.MatchingLabels{hyperv1.NodePoolLabel: npName}); err != nil {
		return 0, err
	}

	ready := 0
	for i := range nodes.Items {
		for _, cond := range nodes.Items[i].Status.Conditions {
			if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
				ready++
				break
			}
		}
	}

	return ready, nil
}

// waitForExactReadyNodeCount polls until exactly expected Ready nodes are labeled for np.
// Unlike e2eutil.WaitForReadyNodesByNodePool, this does not require expected to match
// np.Spec.Replicas, so it can verify partial readiness (e.g. an over-scaled NodePool).
func waitForExactReadyNodeCount(ctx context.Context, hcClient crclient.Client, np *hyperv1.NodePool, expected int, timeout time.Duration) {
	GinkgoHelper()

	Eventually(func() (int, error) {
		return countReadyNodesForNodePool(ctx, hcClient, np.Name)
	}).WithTimeout(timeout).WithPolling(15*time.Second).
		Should(Equal(expected), "NodePool %s should have exactly %d ready nodes", np.Name, expected)
}

// testApp identifies the Deployment, Service, and Route created by deployTestApp.
type testApp struct {
	namespace      string
	deploymentName string
	routeName      string
}

// deployTestApp creates a Namespace, Deployment, Service, and edge Route serving HTTP in the
// hosted cluster, registers cleanup of the namespace, and waits for the Deployment to be ready.
func deployTestApp(ctx context.Context, hcClient crclient.Client, namePrefix string) *testApp {
	GinkgoHelper()

	name := e2eutil.SimpleNameGenerator.GenerateName(namePrefix + "-")
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	Expect(hcClient.Create(ctx, ns)).To(Succeed(), "failed to create test app namespace %s", name)
	DeferCleanup(func() {
		if err := hcClient.Delete(ctx, ns); err != nil && !apierrors.IsNotFound(err) {
			Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete namespace %s", name)
		}
	})

	podLabels := map[string]string{"app": name}
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: name},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To[int32](1),
			Selector: &metav1.LabelSelector{MatchLabels: podLabels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: podLabels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:    "app",
						Image:   testAppImage,
						Command: []string{"/agnhost", "netexec", fmt.Sprintf("--http-port=%d", testAppContainerPort)},
						Ports:   []corev1.ContainerPort{{ContainerPort: testAppContainerPort}},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("50m"),
								corev1.ResourceMemory: resource.MustParse(testAppMemoryRequest),
							},
						},
					}},
				},
			},
		},
	}
	Expect(hcClient.Create(ctx, deployment)).To(Succeed(), "failed to create test app Deployment %s/%s", name, name)

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: name},
		Spec: corev1.ServiceSpec{
			Selector: podLabels,
			Ports: []corev1.ServicePort{{
				Name:       "http",
				Port:       testAppContainerPort,
				TargetPort: intstr.FromInt32(testAppContainerPort),
			}},
		},
	}
	Expect(hcClient.Create(ctx, service)).To(Succeed(), "failed to create test app Service %s/%s", name, name)

	route := &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: name},
		Spec: routev1.RouteSpec{
			To:   routev1.RouteTargetReference{Kind: "Service", Name: name},
			Port: &routev1.RoutePort{TargetPort: intstr.FromString("http")},
			TLS: &routev1.TLSConfig{
				Termination:                   routev1.TLSTerminationEdge,
				InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyRedirect,
			},
		},
	}
	Expect(hcClient.Create(ctx, route)).To(Succeed(), "failed to create test app Route %s/%s", name, name)

	app := &testApp{namespace: name, deploymentName: name, routeName: name}

	e2eutil.EventuallyObject(GinkgoTB(), ctx, fmt.Sprintf("Deployment %s/%s to be ready", name, name),
		func(ctx context.Context) (*appsv1.Deployment, error) {
			d := &appsv1.Deployment{}
			err := hcClient.Get(ctx, crclient.ObjectKeyFromObject(deployment), d)
			return d, err
		},
		[]e2eutil.Predicate[*appsv1.Deployment]{
			func(d *appsv1.Deployment) (bool, string, error) {
				return d.Status.ReadyReplicas == 1, fmt.Sprintf("ready replicas: %d", d.Status.ReadyReplicas), nil
			},
		},
		e2eutil.WithTimeout(5*time.Minute),
	)

	return app
}

// verifyRouteResponds waits for the app's Route host to be assigned, then polls it with an
// HTTPS GET until it responds successfully.
func verifyRouteResponds(ctx context.Context, hcClient crclient.Client, app *testApp) {
	GinkgoHelper()

	httpClient := &http.Client{
		// The test route uses the hosted cluster's default ingress certificate, whose CA
		// isn't threaded through here; this check only cares that the app layer responds.
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec
		Timeout:   30 * time.Second,
	}

	var host string
	e2eutil.EventuallyObject(GinkgoTB(), ctx, fmt.Sprintf("Route %s/%s to respond over HTTPS", app.namespace, app.routeName),
		func(ctx context.Context) (*routev1.Route, error) {
			route := &routev1.Route{}
			err := hcClient.Get(ctx, crclient.ObjectKey{Namespace: app.namespace, Name: app.routeName}, route)
			return route, err
		},
		[]e2eutil.Predicate[*routev1.Route]{
			func(route *routev1.Route) (bool, string, error) {
				host = route.Spec.Host
				if host == "" {
					for _, ingress := range route.Status.Ingress {
						if ingress.Host != "" {
							host = ingress.Host
							break
						}
					}
				}
				if host == "" {
					return false, "route host not yet assigned", nil
				}

				req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+host, nil)
				if err != nil {
					return false, "", err
				}
				resp, err := httpClient.Do(req)
				if err != nil {
					return false, err.Error(), nil
				}
				defer resp.Body.Close()

				return resp.StatusCode == http.StatusOK, fmt.Sprintf("status code: %d", resp.StatusCode), nil
			},
		},
		e2eutil.WithTimeout(5*time.Minute),
		e2eutil.WithInterval(10*time.Second),
	)
}

// scaleDeploymentAndWaitReady patches the app's Deployment to the given replica count and
// waits for that many pods to become ready.
func scaleDeploymentAndWaitReady(ctx context.Context, hcClient crclient.Client, app *testApp, replicas int32) {
	GinkgoHelper()

	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: app.namespace, Name: app.deploymentName}}
	Expect(e2eutil.UpdateObject(GinkgoTB(), ctx, hcClient, deployment, func(obj *appsv1.Deployment) {
		obj.Spec.Replicas = ptr.To(replicas)
	})).To(Succeed(), "failed to scale Deployment %s/%s to %d replicas", app.namespace, app.deploymentName, replicas)

	e2eutil.EventuallyObject(GinkgoTB(), ctx, fmt.Sprintf("Deployment %s/%s to have %d ready replicas", app.namespace, app.deploymentName, replicas),
		func(ctx context.Context) (*appsv1.Deployment, error) {
			d := &appsv1.Deployment{}
			err := hcClient.Get(ctx, crclient.ObjectKeyFromObject(deployment), d)
			return d, err
		},
		[]e2eutil.Predicate[*appsv1.Deployment]{
			func(d *appsv1.Deployment) (bool, string, error) {
				return d.Status.ReadyReplicas == replicas, fmt.Sprintf("ready replicas: %d/%d", d.Status.ReadyReplicas, replicas), nil
			},
		},
		e2eutil.WithTimeout(15*time.Minute),
		e2eutil.WithInterval(10*time.Second),
	)
}

// Constants

const (
	testAppImage         = "registry.k8s.io/e2e-test-images/agnhost:2.53"
	testAppContainerPort = 8080
	testAppMemoryRequest = "256Mi"
)
