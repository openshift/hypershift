//go:build envtest

package envtest

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/upsert"

	agentcore "github.com/openshift/cluster-api-provider-agent/api/capicore/api/core/v1beta2"
	agentalpha "github.com/openshift/cluster-api-provider-agent/api/v1alpha1"
	agentbeta "github.com/openshift/cluster-api-provider-agent/api/v1beta1"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	legacyconditions "sigs.k8s.io/cluster-api/util/conditions/deprecated/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	runtimeenvtest "sigs.k8s.io/controller-runtime/pkg/envtest"
)

func agentProviderTestSuites(testDir string) []SuiteSpec {
	var result []SuiteSpec
	for _, resource := range []string{"agentclusters", "agentmachines"} {
		kind := "AgentCluster"
		if resource == "agentmachines" {
			kind = "AgentMachine"
		}
		for _, version := range []string{"v1alpha1", "v1beta1"} {
			filename := filepath.Join(testDir, "../../cmd/install/assets/crds/cluster-api-provider-agent/capi-provider.agent-install.openshift.io_"+resource+".yaml")
			oldCRD, err := loadVersionedCRD(SuiteSpec{Version: version}, filepath.Join(testDir, "testdata/agent-provider/old-"+resource+".yaml"))
			Expect(err).NotTo(HaveOccurred())
			wire, err := json.Marshal(oldCRD.Spec.Versions)
			Expect(err).NotTo(HaveOccurred())
			var oldVersions any
			Expect(json.Unmarshal(wire, &oldVersions)).To(Succeed())
			patches := []Patch{{Op: "replace", Path: "/spec/versions", Value: &oldVersions}}
			base := agentProviderObject(version, kind)
			newCondition := []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Available", LastTransitionTime: metav1.Unix(1794240000, 0)}}
			withConditions := func(conditions any) string {
				object := base.DeepCopy()
				status := agentProviderStatus(kind)
				status["conditions"] = conditions
				object.Object["status"] = status
				return agentProviderJSON(object)
			}
			suite := SuiteSpec{
				Name:    "Agent provider upgrade " + resource + " " + version,
				CRDName: oldCRD.Name, Version: version,
				PerTestRuntimeInfo: &PerTestRuntimeInfo{CRDFilenames: []string{filename}},
			}
			for _, status := range []corev1.ConditionStatus{corev1.ConditionTrue, corev1.ConditionFalse} {
				writer := &clusterv1.Machine{}
				legacyconditions.MarkTrue(writer, "Existing")
				initial := withConditions(writer.GetV1Beta1Conditions())
				if status == corev1.ConditionTrue {
					legacyconditions.MarkTrue(writer, "Ready")
				} else {
					legacyconditions.MarkFalse(writer, "Ready", "", clusterv1.ConditionSeverityWarning, "")
				}
				legacy := withConditions(writer.GetV1Beta1Conditions())
				suite.Tests.OnUpdate = append(suite.Tests.OnUpdate, OnUpdateTestSpec{
					Name:              fmt.Sprintf("When a retained legacy writer sets %s, it should expose required condition fields after upgrade", status),
					InitialCRDPatches: patches, Initial: initial, Updated: legacy,
					ExpectedStatusError: "reason",
				})
			}
			suite.Tests.OnUpdate = append(suite.Tests.OnUpdate, OnUpdateTestSpec{
				Name:              "When the new writer serializes an empty message, it should admit status after upgrade",
				InitialCRDPatches: patches, Initial: agentProviderJSON(base), Updated: withConditions(newCondition), Expected: withConditions(newCondition),
			})
			for _, field := range []string{"reason", "message"} {
				conditionWire, err := json.Marshal(newCondition)
				Expect(err).NotTo(HaveOccurred())
				var conditions []map[string]any
				Expect(json.Unmarshal(conditionWire, &conditions)).To(Succeed())
				delete(conditions[0], field)
				suite.Tests.OnUpdate = append(suite.Tests.OnUpdate, OnUpdateTestSpec{
					Name:              "When a writer omits " + field + ", it should reject the missing required field after upgrade",
					InitialCRDPatches: patches, Initial: agentProviderJSON(base), Updated: withConditions(conditions), ExpectedStatusError: field,
				})
			}
			if kind == "AgentCluster" {
				withDomains := func(domains any) string {
					object := base.DeepCopy()
					status := agentProviderStatus(kind)
					status["failureDomains"] = domains
					object.Object["status"] = status
					return agentProviderJSON(object)
				}
				controlPlane := true
				newDomains := []agentcore.FailureDomain{{Name: "zone-a", ControlPlane: &controlPlane}}
				suite.Tests.OnUpdate = append(suite.Tests.OnUpdate,
					OnUpdateTestSpec{
						Name:              "When a new writer supplies a failure-domain list, it should preserve its fields",
						InitialCRDPatches: patches, Initial: agentProviderJSON(base), Updated: withDomains(newDomains), Expected: withDomains(newDomains),
					},
				)
				oldEndpoint := base.DeepCopy()
				Expect(unstructured.SetNestedField(oldEndpoint.Object, map[string]any{"host": "", "port": int64(0)}, "spec", "controlPlaneEndpoint")).To(Succeed())
				suite.Tests.OnUpdate = append(suite.Tests.OnUpdate, OnUpdateTestSpec{
					Name:              "When an old endpoint contains explicit zero values, it should permit a complete endpoint update after upgrade",
					InitialCRDPatches: patches, Initial: agentProviderJSON(oldEndpoint), Updated: agentProviderJSON(base), Expected: agentProviderJSON(base),
				})
				for _, endpointCase := range []struct {
					name     string
					endpoint agentcore.APIEndpoint
					err      string
				}{
					{"empty", agentcore.APIEndpoint{}, "controlPlaneEndpoint"},
					{"host-only", agentcore.APIEndpoint{Host: "api.example.com"}, ""},
					{"port-only", agentcore.APIEndpoint{Port: 6443}, ""},
					{"complete", agentcore.APIEndpoint{Host: "api.example.com", Port: 6443}, ""},
				} {
					var object any = &agentbeta.AgentCluster{TypeMeta: metav1.TypeMeta{APIVersion: base.GetAPIVersion(), Kind: kind}, Spec: agentbeta.AgentClusterSpec{ControlPlaneEndpoint: endpointCase.endpoint}}
					if version == "v1alpha1" {
						object = &agentalpha.AgentCluster{TypeMeta: metav1.TypeMeta{APIVersion: base.GetAPIVersion(), Kind: kind}, Spec: agentalpha.AgentClusterSpec{ControlPlaneEndpoint: endpointCase.endpoint}}
					}
					payload := agentProviderJSON(object)
					var expected map[string]any
					Expect(json.Unmarshal([]byte(payload), &expected)).To(Succeed())
					delete(expected, "status")
					testCase := OnCreateTestSpec{Name: "When serializing an " + endpointCase.name + " endpoint, it should enforce the upstream object schema", Initial: payload, ExpectedError: endpointCase.err}
					if endpointCase.err == "" {
						testCase.Expected = agentProviderJSON(expected)
					}
					suite.Tests.OnCreate = append(suite.Tests.OnCreate, testCase)
				}
				for _, endpoint := range []map[string]any{
					{"host": "", "port": int64(6443)},
					{"host": "api.example.com", "port": int64(0)},
					{"host": "api.example.com", "port": int64(65536)},
				} {
					object := base.DeepCopy()
					Expect(unstructured.SetNestedField(object.Object, endpoint, "spec", "controlPlaneEndpoint")).To(Succeed())
					suite.Tests.OnCreate = append(suite.Tests.OnCreate, OnCreateTestSpec{
						Name:    fmt.Sprintf("When an endpoint explicitly contains invalid bounds %v, it should reject the object", endpoint),
						Initial: agentProviderJSON(object), ExpectedError: "controlPlaneEndpoint",
					})
				}
			}
			result = append(result, suite)
		}
	}
	return result
}

func agentProviderObject(version, kind string) *unstructured.Unstructured {
	object := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "capi-provider.agent-install.openshift.io/" + version,
		"kind":       kind, "spec": map[string]any{},
	}}
	if kind == "AgentCluster" {
		object.Object["spec"] = map[string]any{"controlPlaneEndpoint": map[string]any{"host": "api.example.com", "port": int64(6443)}}
	}
	return object
}

func agentProviderJSON(object any) string {
	wire, err := json.Marshal(object)
	Expect(err).NotTo(HaveOccurred())
	return string(wire)
}

func agentProviderStatus(kind string) map[string]any {
	status := map[string]any{"ready": false}
	if kind == "AgentCluster" {
		status["clusterDeploymentReference"] = map[string]any{"name": "cluster", "namespace": "default"}
	}
	return status
}

var _ = Describe("Agent provider persisted objects", func() {
	for _, version := range []string{"v1alpha1", "v1beta1"} {
		for _, populated := range []bool{false, true} {
			It(fmt.Sprintf("When upgrading %s with populated failure domains %t, it should check typed reads and endpoint updates", version, populated), func() {
				oldCRD, err := loadCRDFromFile("testdata/agent-provider/old-agentclusters.yaml")
				Expect(err).NotTo(HaveOccurred())
				installed, err := runtimeenvtest.InstallCRDs(cfg, runtimeenvtest.CRDInstallOptions{CRDs: []*apiextensionsv1.CustomResourceDefinition{oldCRD}})
				Expect(err).NotTo(HaveOccurred())
				DeferCleanup(func() {
					Expect(runtimeenvtest.UninstallCRDs(cfg, runtimeenvtest.CRDInstallOptions{CRDs: installed})).To(Succeed())
					Eventually(func() bool {
						return apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(oldCRD), &apiextensionsv1.CustomResourceDefinition{}))
					}, "120s", "1s").Should(BeTrue())
				})
				object := agentProviderObject(version, "AgentCluster")
				object.SetName("legacy")
				object.SetNamespace("default")
				Expect(unstructured.SetNestedField(object.Object, map[string]any{"host": "", "port": int64(0)}, "spec", "controlPlaneEndpoint")).To(Succeed())
				Expect(k8sClient.Create(ctx, object)).To(Succeed())
				object.Object["status"] = agentProviderStatus("AgentCluster")
				if populated {
					object.Object["status"].(map[string]any)["failureDomains"] = map[string]any{"zone-a": map[string]any{"controlPlane": true}}
				}
				Expect(k8sClient.Status().Update(ctx, object)).To(Succeed())
				if populated {
					Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(object), object)).To(Succeed())
					domains, found, err := unstructured.NestedMap(object.Object, "status", "failureDomains")
					Expect(err).NotTo(HaveOccurred())
					Expect(found).To(BeTrue())
					Expect(domains).To(HaveKey("zone-a"))
				}
				target, err := loadCRDFromFile("../../cmd/install/assets/crds/cluster-api-provider-agent/capi-provider.agent-install.openshift.io_agentclusters.yaml")
				Expect(err).NotTo(HaveOccurred())
				_, err = runtimeenvtest.InstallCRDs(cfg, runtimeenvtest.CRDInstallOptions{CRDs: []*apiextensionsv1.CustomResourceDefinition{target}})
				Expect(err).NotTo(HaveOccurred())
				Eventually(func() bool {
					probe := object.DeepCopy()
					probe.SetName("schema-probe")
					probe.SetResourceVersion("")
					probe.SetUID("")
					err := k8sClient.Create(ctx, probe)
					if err == nil {
						Expect(k8sClient.Delete(ctx, probe)).To(Succeed())
					}
					return apierrors.IsInvalid(err)
				}, "10s").Should(BeTrue())
				scheme := runtime.NewScheme()
				Expect(agentalpha.AddToScheme(scheme)).To(Succeed())
				Expect(agentbeta.AddToScheme(scheme)).To(Succeed())
				typedClient, err := client.New(cfg, client.Options{Scheme: scheme})
				Expect(err).NotTo(HaveOccurred())
				var typed client.Object = &agentbeta.AgentCluster{}
				var list client.ObjectList = &agentbeta.AgentClusterList{}
				if version == "v1alpha1" {
					typed = &agentalpha.AgentCluster{}
					list = &agentalpha.AgentClusterList{}
				}
				err = typedClient.Get(ctx, client.ObjectKeyFromObject(object), typed)
				if populated {
					Expect(err).To(MatchError(ContainSubstring("cannot unmarshal object")))
					Expect(typedClient.List(ctx, list)).To(MatchError(ContainSubstring("cannot unmarshal object")))
					Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(object), object)).To(Succeed())
					domains, found, err := unstructured.NestedMap(object.Object, "status", "failureDomains")
					Expect(err).NotTo(HaveOccurred())
					Expect(found).To(BeTrue())
					Expect(domains).To(BeEmpty())
					object.Object["status"].(map[string]any)["failureDomains"] = map[string]any{
						"zone-a": map[string]any{"controlPlane": true},
						"zone-b": map[string]any{"controlPlane": false},
					}
					err = k8sClient.Status().Update(ctx, object)
					if err != nil {
						Expect(apierrors.IsInvalid(err)).To(BeTrue())
						Expect(err.Error()).To(ContainSubstring("failureDomains"))
					} else {
						Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(object), object)).To(Succeed())
						domains, found, err = unstructured.NestedMap(object.Object, "status", "failureDomains")
						Expect(err).NotTo(HaveOccurred())
						Expect(found).To(BeTrue())
						Expect(domains).To(BeEmpty())
					}
					return
				}
				Expect(err).NotTo(HaveOccurred())
				Expect(typedClient.List(ctx, list)).To(Succeed())
				before := object.DeepCopy()
				Expect(unstructured.SetNestedField(object.Object, true, "status", "ready")).To(Succeed())
				Expect(k8sClient.Status().Patch(ctx, object, client.MergeFrom(before))).To(Succeed())
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(object), object)).To(Succeed())
				endpoint, _, err := unstructured.NestedMap(object.Object, "spec", "controlPlaneEndpoint")
				Expect(err).NotTo(HaveOccurred())
				Expect(endpoint).To(Equal(map[string]any{"host": "", "port": int64(0)}))
				_, err = upsert.New(false).CreateOrUpdate(ctx, typedClient, typed, func() error {
					switch cluster := typed.(type) {
					case *agentbeta.AgentCluster:
						cluster.Spec.ControlPlaneEndpoint = agentcore.APIEndpoint{Host: "api.example.com", Port: 6443}
					case *agentalpha.AgentCluster:
						cluster.Spec.ControlPlaneEndpoint = agentcore.APIEndpoint{Host: "api.example.com", Port: 6443}
					}
					return nil
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(object), object)).To(Succeed())
				endpoint, found, err := unstructured.NestedMap(object.Object, "spec", "controlPlaneEndpoint")
				Expect(err).NotTo(HaveOccurred())
				Expect(found).To(BeTrue())
				Expect(endpoint).To(Equal(map[string]any{"host": "api.example.com", "port": int64(6443)}))
			})
		}
	}
})
