//go:build e2ev2

package tests

import (
	"slices"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/blang/semver"
)

func RegisterHostedClusterCapabilitiesTests(getTestCtx internal.TestContextGetter) {
	for _, entry := range []struct {
		capability hyperv1.OptionalCapability
		operator   string
		namespaces []string
	}{
		{hyperv1.OpenShiftSamplesCapability, "openshift-samples", []string{"openshift-cluster-samples-operator"}},
		{hyperv1.InsightsCapability, "insights", []string{"openshift-insights"}},
		{hyperv1.ConsoleCapability, "console", []string{"openshift-console"}},
		{hyperv1.NodeTuningCapability, "node-tuning", []string{"openshift-cluster-node-tuning-operator"}},
		{hyperv1.IngressCapability, "ingress", []string{"openshift-ingress-operator", "openshift-ingress", "openshift-ingress-canary"}},
	} {
		capability := entry
		Context(string(capability.capability), func() {
			BeforeEach(func() {
				tc := getTestCtx()
				skipIfNativeOriginBelow(tc, semver.MustParse("4.20.0"))
				hc, err := tc.GetHostedCluster()
				Expect(err).NotTo(HaveOccurred())
				if hc.Spec.Capabilities == nil {
					Skip("capability must be disabled when creating the hosted cluster")
				}
				if !slices.Contains(hc.Spec.Capabilities.Disabled, capability.capability) {
					Skip("capability must be disabled when creating the hosted cluster")
				}
			})
			It("should not deploy its ClusterOperator or hosted cluster namespaces", func() {
				tc := getTestCtx()
				hc, err := tc.GetHostedCluster()
				Expect(err).NotTo(HaveOccurred())
				client, err := tc.GetHostedClusterClient(hc)
				Expect(err).NotTo(HaveOccurred())
				Expect(apierrors.IsNotFound(client.Get(tc.Context, crclient.ObjectKey{Name: capability.operator}, &configv1.ClusterOperator{}))).To(BeTrue())
				for _, namespace := range capability.namespaces {
					Expect(apierrors.IsNotFound(client.Get(tc.Context, crclient.ObjectKey{Name: namespace}, &corev1.Namespace{}))).To(BeTrue())
				}
				if capability.capability == hyperv1.NodeTuningCapability {
					for _, kind := range []string{"TunedList", "ProfileList"} {
						resources := &unstructured.UnstructuredList{}
						resources.SetGroupVersionKind(schema.GroupVersionKind{Group: "tuned.openshift.io", Version: "v1", Kind: kind})
						Expect(meta.IsNoMatchError(client.List(tc.Context, resources))).To(BeTrue(), "%s API must not exist", kind)
					}
					Expect(apierrors.IsNotFound(client.Get(tc.Context, crclient.ObjectKey{Namespace: capability.namespaces[0], Name: "tuned"}, &appsv1.DaemonSet{}))).To(BeTrue())
					configMaps := &corev1.ConfigMapList{}
					Expect(client.List(tc.Context, configMaps)).To(Succeed())
					for _, configMap := range configMaps.Items {
						Expect(strings.Contains(configMap.Name, "tuned") || strings.Contains(configMap.Name, "node-tuning") || configMap.Labels["tuned.openshift.io/tuned"] != "" || configMap.Labels["hypershift.openshift.io/nto-generated-machine-config"] != "").To(BeFalse(), "unexpected node tuning ConfigMap %s/%s", configMap.Namespace, configMap.Name)
					}
					Expect(apierrors.IsNotFound(tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{Namespace: tc.ControlPlaneNamespace, Name: "cluster-node-tuning-operator"}, &appsv1.Deployment{}))).To(BeTrue())
				}
				if capability.capability == hyperv1.IngressCapability {
					for _, resource := range []schema.GroupVersionKind{
						{Group: "operator.openshift.io", Version: "v1", Kind: "IngressControllerList"},
						{Group: "ingress.operator.openshift.io", Version: "v1", Kind: "DNSRecordList"},
						{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "GatewayClassList"},
						{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "GatewayList"},
						{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "HTTPRouteList"},
						{Group: "gateway.networking.k8s.io", Version: "v1beta1", Kind: "ReferenceGrantList"},
					} {
						resources := &unstructured.UnstructuredList{}
						resources.SetGroupVersionKind(resource)
						Expect(meta.IsNoMatchError(client.List(tc.Context, resources))).To(BeTrue(), "%s API must not exist", resource.Kind)
					}
					deployments := &appsv1.DeploymentList{}
					Expect(tc.MgmtClient.List(tc.Context, deployments, crclient.InNamespace(tc.ControlPlaneNamespace), crclient.MatchingLabels{"app": "ingress-operator"})).To(Succeed())
					Expect(deployments.Items).To(BeEmpty())
					pods := &corev1.PodList{}
					Expect(tc.MgmtClient.List(tc.Context, pods, crclient.InNamespace(tc.ControlPlaneNamespace), crclient.MatchingLabels{"app": "ingress-operator"})).To(Succeed())
					Expect(pods.Items).To(BeEmpty())
				}
			})
		})
	}
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:Capabilities] Hosted Cluster Disabled Capabilities", Label("hosted-cluster-capabilities"), func() {
	RegisterHostedClusterCapabilitiesTests(internal.GetTestContext)
})
