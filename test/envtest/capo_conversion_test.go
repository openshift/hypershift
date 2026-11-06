//go:build envtest

package envtest

import (
	"context"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	crdassets "github.com/openshift/hypershift/cmd/install/assets/crds"
	"github.com/openshift/hypershift/support/openstackconversion"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/utils/ptr"

	capov1beta1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
	capov1beta2 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/conversion"
)

var _ = Describe("CAPO persisted conversion", func() {
	It("preserves beta1 objects through beta2 storage, status updates and webhook recovery", func(ctx SpecContext) {
		scheme := runtime.NewScheme()
		Expect(capov1beta1.AddToScheme(scheme)).To(Succeed())
		Expect(capov1beta2.AddToScheme(scheme)).To(Succeed())
		registry := conversion.NewRegistry()
		Expect(openstackconversion.Register(registry)).To(Succeed())
		handler := conversion.NewWebhookHandler(scheme, registry)
		legacyScheme := runtime.NewScheme()
		Expect(capov1beta1.AddToScheme(legacyScheme)).To(Succeed())
		legacyHandler := conversion.NewWebhookHandler(legacyScheme, conversion.NewRegistry())
		var unavailable atomic.Bool
		var legacyEndpoint atomic.Bool
		server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if unavailable.Load() {
				http.Error(writer, "conversion unavailable", http.StatusServiceUnavailable)
				return
			}
			if legacyEndpoint.Load() {
				legacyHandler.ServeHTTP(writer, request)
				return
			}
			handler.ServeHTTP(writer, request)
		}))
		DeferCleanup(server.Close)
		caBundle := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
		namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "capo-conversion-"}}
		Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(context.Background(), namespace)).To(Succeed()) })
		dynamicClient, err := dynamic.NewForConfig(cfg)
		Expect(err).NotTo(HaveOccurred())
		for _, object := range crdassets.CustomResourceDefinitions("v1beta1", func(_ string, _ *apiextensionsv1.CustomResourceDefinition) bool { return true }, nil) {
			candidate := object.(*apiextensionsv1.CustomResourceDefinition)
			if candidate.Spec.Group != "infrastructure.cluster.x-k8s.io" || len(candidate.Spec.Versions) != 2 {
				continue
			}
			switch candidate.Spec.Names.Kind {
			case "OpenStackCluster", "OpenStackClusterTemplate", "OpenStackMachine", "OpenStackMachineTemplate":
			default:
				continue
			}
			By(fmt.Sprintf("upgrading persisted %s", candidate.Spec.Names.Kind))
			baseline := candidate.DeepCopy()
			baseline.Spec.Conversion = nil
			baseline.Spec.Versions = nil
			for _, version := range candidate.Spec.Versions {
				if version.Name == "v1beta1" {
					version.Storage = true
					baseline.Spec.Versions = append(baseline.Spec.Versions, version)
				}
			}
			Expect(k8sClient.Create(ctx, baseline)).To(Succeed())
			DeferCleanup(func() { Expect(k8sClient.Delete(context.Background(), candidate)).To(Succeed()) })
			Eventually(func() bool {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: candidate.Name}, baseline); err != nil {
					return false
				}
				for _, condition := range baseline.Status.Conditions {
					if condition.Type == apiextensionsv1.Established && condition.Status == apiextensionsv1.ConditionTrue {
						return true
					}
				}
				return false
			}, time.Minute, 100*time.Millisecond).Should(BeTrue())
			groupResource := schema.GroupVersionResource{Group: candidate.Spec.Group, Version: "v1beta1", Resource: candidate.Spec.Names.Plural}
			spokes := dynamicClient.Resource(groupResource).Namespace(namespace.Name)
			groupResource.Version = "v1beta2"
			hubs := dynamicClient.Resource(groupResource).Namespace(namespace.Name)
			spec := map[string]any{"identityRef": map[string]any{"name": "clouds", "cloudName": "openstack", "type": "Secret"}}
			if candidate.Spec.Names.Kind == "OpenStackMachine" || candidate.Spec.Names.Kind == "OpenStackMachineTemplate" {
				spec["flavor"] = "m1.large"
				spec["image"] = map[string]any{"id": "8f56ebc1-703c-4c6d-9bde-07ba55a34df7"}
			}
			if candidate.Spec.Names.Kind == "OpenStackClusterTemplate" || candidate.Spec.Names.Kind == "OpenStackMachineTemplate" {
				spec = map[string]any{"template": map[string]any{"spec": spec}}
			}
			resource := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": candidate.Spec.Group + "/v1beta1", "kind": candidate.Spec.Names.Kind,
				"metadata": map[string]any{"name": "existing", "annotations": map[string]any{"keep": "yes"}}, "spec": spec,
			}}
			created, err := spokes.Create(ctx, resource, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			if candidate.Spec.Names.Kind == "OpenStackMachine" || candidate.Spec.Names.Kind == "OpenStackCluster" {
				Expect(unstructured.SetNestedSlice(created.Object, []any{map[string]any{"type": "Ready", "status": "False", "severity": "Warning", "lastTransitionTime": "2026-10-30T00:00:00Z"}}, "status", "conditions")).To(Succeed())
				_, err = spokes.UpdateStatus(ctx, created, metav1.UpdateOptions{})
				Expect(err).NotTo(HaveOccurred())
			}
			candidate.ResourceVersion = baseline.ResourceVersion
			candidate.Spec.Conversion = &apiextensionsv1.CustomResourceConversion{Strategy: apiextensionsv1.WebhookConverter, Webhook: &apiextensionsv1.WebhookConversion{
				ConversionReviewVersions: []string{"v1"}, ClientConfig: &apiextensionsv1.WebhookClientConfig{URL: ptr.To(server.URL + "/convert"), CABundle: caBundle},
			}}
			legacyEndpoint.Store(true)
			Expect(k8sClient.Update(ctx, candidate)).To(Succeed())
			Eventually(func() bool {
				_, err := hubs.Get(ctx, "existing", metav1.GetOptions{})
				if err != nil && strings.Contains(err.Error(), "conversion") {
					fmt.Fprintf(GinkgoWriter, "CAPO_BETA_EVIDENCE kind=%s old-handler-read=%s\n", candidate.Spec.Names.Kind, err)
					return true
				}
				return false
			}, time.Minute, 100*time.Millisecond).Should(BeTrue())
			legacyEndpoint.Store(false)
			var hub *unstructured.Unstructured
			Eventually(func() error { hub, err = hubs.Get(ctx, "existing", metav1.GetOptions{}); return err }, time.Minute, 100*time.Millisecond).Should(Succeed())
			Expect(hub.GetAnnotations()).To(HaveKeyWithValue("keep", "yes"))
			labels := hub.GetLabels()
			if labels == nil {
				labels = map[string]string{}
			}
			labels["edited"] = "beta2"
			hub.SetLabels(labels)
			hub, err = hubs.Update(ctx, hub, metav1.UpdateOptions{})
			Expect(err).NotTo(HaveOccurred())
			if candidate.Spec.Names.Kind == "OpenStackMachine" || candidate.Spec.Names.Kind == "OpenStackCluster" {
				Expect(unstructured.SetNestedSlice(hub.Object, []any{map[string]any{"type": "Ready", "status": "True", "reason": "Provisioned", "message": "ready", "observedGeneration": hub.GetGeneration(), "lastTransitionTime": "2026-10-30T00:00:00Z"}}, "status", "conditions")).To(Succeed())
				hub, err = hubs.UpdateStatus(ctx, hub, metav1.UpdateOptions{})
				Expect(err).NotTo(HaveOccurred())
			}
			spoke, err := spokes.Get(ctx, "existing", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(spoke.GetLabels()).To(HaveKeyWithValue("edited", "beta2"))
			if candidate.Spec.Names.Kind == "OpenStackMachine" || candidate.Spec.Names.Kind == "OpenStackCluster" {
				ready, _, err := unstructured.NestedBool(spoke.Object, "status", "ready")
				Expect(err).NotTo(HaveOccurred())
				Expect(ready).To(BeTrue())
			}
			unavailable.Store(true)
			_, err = spokes.Get(ctx, "existing", metav1.GetOptions{})
			Expect(err).To(HaveOccurred())
			unavailable.Store(false)
			Eventually(func() error { _, err := spokes.Get(ctx, "existing", metav1.GetOptions{}); return err }, time.Minute, 100*time.Millisecond).Should(Succeed())
		}
	})
})
