//go:build envtest

package envtest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/cmd/install"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"

	capov1alpha1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

var _ = Describe("CAPO persisted alpha upgrade", func() {
	for _, kind := range []string{"OpenStackServer", "OpenStackFloatingIPPool"} {
		It(fmt.Sprintf("When legacy %s objects are upgraded, it should record same-version field and client compatibility", kind), func(ctx SpecContext) {
			candidate := renderedAlphaCRD(ctx, kind)
			Expect(candidate.Spec.Versions).To(HaveLen(1))
			Expect(candidate.Spec.Versions[0].Name).To(Equal("v1alpha1"))
			Expect(candidate.Spec.Conversion == nil || candidate.Spec.Conversion.Strategy == apiextensionsv1.NoneConverter).To(BeTrue())
			data, err := os.ReadFile(filepath.Join("testdata", "capo-v0.14.8", candidate.Spec.Group+"_"+candidate.Spec.Names.Plural+".yaml"))
			Expect(err).NotTo(HaveOccurred())
			baseline := &apiextensionsv1.CustomResourceDefinition{}
			Expect(yaml.Unmarshal(data, baseline)).To(Succeed())
			crdScheme := runtime.NewScheme()
			Expect(apiextensionsv1.AddToScheme(crdScheme)).To(Succeed())
			crdClient, err := client.New(cfg, client.Options{Scheme: crdScheme})
			Expect(err).NotTo(HaveOccurred())
			Expect(crdClient.Create(ctx, baseline)).To(Succeed())
			DeferCleanup(func() { Expect(crdClient.Delete(context.Background(), candidate)).To(Succeed()) })
			Eventually(func() bool {
				Expect(crdClient.Get(ctx, client.ObjectKey{Name: baseline.Name}, baseline)).To(Succeed())
				for _, condition := range baseline.Status.Conditions {
					if condition.Type == apiextensionsv1.Established && condition.Status == apiextensionsv1.ConditionTrue {
						return true
					}
				}
				return false
			}, time.Minute, 100*time.Millisecond).Should(BeTrue())
			namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "capo-alpha-"}}
			Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
			DeferCleanup(func() { Expect(k8sClient.Delete(context.Background(), namespace)).To(Succeed()) })
			dynamicClient, err := dynamic.NewForConfig(cfg)
			Expect(err).NotTo(HaveOccurred())
			resource := dynamicClient.Resource(schema.GroupVersionResource{Group: candidate.Spec.Group, Version: "v1alpha1", Resource: candidate.Spec.Names.Plural}).Namespace(namespace.Name)
			observe := func(operation string, request, response *unstructured.Unstructured, err error) {
				record := map[string]any{"kind": kind, "operation": operation, "request": request, "response": response}
				if err != nil {
					record["error"] = err.Error()
				}
				encoded, marshalErr := json.Marshal(record)
				Expect(marshalErr).NotTo(HaveOccurred())
				fmt.Fprintf(GinkgoWriter, "CAPO_ALPHA_EVIDENCE %s\n", encoded)
			}
			for _, setting := range []string{"true", "false", "omitted"} {
				identity := map[string]any{"name": "clouds", "cloudName": "openstack", "type": "Secret"}
				spec := map[string]any{"identityRef": identity, "reclaimPolicy": "Retain"}
				if kind == "OpenStackServer" {
					port := map[string]any{"nameSuffix": "legacy"}
					if setting != "omitted" {
						port["disablePortSecurity"] = setting == "true"
					}
					spec = map[string]any{"identityRef": identity, "flavor": "m1.large", "image": map[string]any{"id": "8f56ebc1-703c-4c6d-9bde-07ba55a34df7"}, "sshKeyName": "test", "ports": []any{port}}
				}
				request := &unstructured.Unstructured{Object: map[string]any{"apiVersion": candidate.Spec.Group + "/v1alpha1", "kind": kind, "metadata": map[string]any{"name": "legacy-" + setting}, "spec": spec}}
				created, err := resource.Create(ctx, request, metav1.CreateOptions{})
				observe("POST baseline", request, created, err)
				Expect(err).NotTo(HaveOccurred())
				condition := map[string]any{"type": "Ready", "status": "False", "lastTransitionTime": "2026-10-30T00:00:00Z", "severity": "Warning", "reason": "LegacyWaiting", "message": "legacy controller"}
				if setting == "omitted" {
					delete(condition, "reason")
					delete(condition, "message")
				}
				status := map[string]any{"conditions": []any{condition}}
				if kind == "OpenStackServer" {
					port := map[string]any{"name": "legacy", "description": "legacy", "networkID": "8f56ebc1-703c-4c6d-9bde-07ba55a34df7"}
					if setting != "omitted" {
						port["disablePortSecurity"] = setting == "true"
					}
					status["resolved"] = map[string]any{"ports": []any{port}}
				}
				created.Object["status"] = status
				updated, err := resource.UpdateStatus(ctx, created, metav1.UpdateOptions{})
				observe("PUT baseline/status", created, updated, err)
				Expect(err).NotTo(HaveOccurred())
				before, err := resource.Get(ctx, created.GetName(), metav1.GetOptions{})
				observe("GET baseline", nil, before, err)
				Expect(err).NotTo(HaveOccurred())
				Expect(before.Object["status"].(map[string]any)["conditions"].([]any)[0].(map[string]any)).To(HaveKeyWithValue("severity", "Warning"))
				if kind == "OpenStackServer" {
					for _, port := range []map[string]any{before.Object["spec"].(map[string]any)["ports"].([]any)[0].(map[string]any), before.Object["status"].(map[string]any)["resolved"].(map[string]any)["ports"].([]any)[0].(map[string]any)} {
						if setting == "omitted" {
							Expect(port).NotTo(HaveKey("disablePortSecurity"))
						} else {
							Expect(port).To(HaveKeyWithValue("disablePortSecurity", setting == "true"))
						}
					}
				}
			}
			Expect(crdClient.Get(ctx, client.ObjectKey{Name: baseline.Name}, baseline)).To(Succeed())
			candidate.ResourceVersion = baseline.ResourceVersion
			Expect(crdClient.Update(ctx, candidate)).To(Succeed())
			probeSource, err := resource.Get(ctx, "legacy-false", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			probeSpec := runtime.DeepCopyJSON(probeSource.Object["spec"].(map[string]any))
			delete(probeSpec["identityRef"].(map[string]any), "type")
			probe := &unstructured.Unstructured{Object: map[string]any{"apiVersion": candidate.Spec.Group + "/v1alpha1", "kind": kind, "metadata": map[string]any{"name": "schema-probe"}, "spec": probeSpec}}
			var probeResponse *unstructured.Unstructured
			Eventually(func() error {
				probeResponse, err = resource.Create(ctx, probe, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}})
				return err
			}, time.Minute, 100*time.Millisecond).Should(Succeed())
			observe("POST candidate schema probe dry-run", probe, probeResponse, err)
			for _, setting := range []string{"true", "false", "omitted"} {
				current, err := resource.Get(ctx, "legacy-"+setting, metav1.GetOptions{})
				observe("GET candidate", nil, current, err)
				Expect(err).NotTo(HaveOccurred())
				if kind == "OpenStackServer" {
					for _, port := range []map[string]any{current.Object["spec"].(map[string]any)["ports"].([]any)[0].(map[string]any), current.Object["status"].(map[string]any)["resolved"].(map[string]any)["ports"].([]any)[0].(map[string]any)} {
						Expect(port).NotTo(HaveKey("enablePortSecurity"))
					}
					typed := &capov1alpha1.OpenStackServer{}
					Expect(runtime.DefaultUnstructuredConverter.FromUnstructured(current.Object, typed)).To(Succeed())
					Expect(typed.Spec.Ports[0].EnablePortSecurity).To(BeNil())
					Expect(typed.Status.Resolved.Ports[0].EnablePortSecurity).To(BeNil())
				}
				current.SetLabels(map[string]string{"updated": "true"})
				updated, updateErr := resource.Update(ctx, current, metav1.UpdateOptions{})
				observe("PUT candidate metadata", current, updated, updateErr)
				if updateErr != nil {
					Expect(apierrors.IsInvalid(updateErr)).To(BeTrue())
				}
				current, err = resource.Get(ctx, current.GetName(), metav1.GetOptions{})
				Expect(err).NotTo(HaveOccurred())
				legacy := current.DeepCopy()
				if kind == "OpenStackServer" && setting != "omitted" {
					legacy.Object["spec"].(map[string]any)["ports"].([]any)[0].(map[string]any)["disablePortSecurity"] = setting == "true"
				}
				updated, updateErr = resource.Update(ctx, legacy, metav1.UpdateOptions{})
				observe("PUT old client against candidate", legacy, updated, updateErr)
				if updateErr != nil {
					Expect(apierrors.IsInvalid(updateErr)).To(BeTrue())
				}
				current, err = resource.Get(ctx, current.GetName(), metav1.GetOptions{})
				Expect(err).NotTo(HaveOccurred())
				legacyStatus := current.DeepCopy()
				legacyStatus.Object["status"].(map[string]any)["conditions"].([]any)[0].(map[string]any)["severity"] = "Warning"
				if kind == "OpenStackServer" && setting != "omitted" {
					legacyStatus.Object["status"].(map[string]any)["resolved"].(map[string]any)["ports"].([]any)[0].(map[string]any)["disablePortSecurity"] = setting == "true"
				}
				updated, updateErr = resource.UpdateStatus(ctx, legacyStatus, metav1.UpdateOptions{})
				observe("PUT old client candidate/status", legacyStatus, updated, updateErr)
				if updateErr != nil {
					Expect(apierrors.IsInvalid(updateErr)).To(BeTrue())
				}
				current, err = resource.Get(ctx, current.GetName(), metav1.GetOptions{})
				Expect(err).NotTo(HaveOccurred())
				Expect(unstructured.SetNestedSlice(current.Object, []any{map[string]any{"type": "Ready", "status": "True", "reason": "Provisioned", "message": "candidate controller", "lastTransitionTime": "2026-10-30T00:00:00Z", "observedGeneration": current.GetGeneration()}}, "status", "conditions")).To(Succeed())
				if kind == "OpenStackServer" && setting != "omitted" {
					current.Object["status"].(map[string]any)["resolved"].(map[string]any)["ports"].([]any)[0].(map[string]any)["enablePortSecurity"] = setting == "false"
				}
				updated, err = resource.UpdateStatus(ctx, current, metav1.UpdateOptions{})
				observe("PUT new client candidate/status", current, updated, err)
				Expect(err).NotTo(HaveOccurred())
				fresh := &unstructured.Unstructured{Object: map[string]any{"apiVersion": candidate.Spec.Group + "/v1alpha1", "kind": kind, "metadata": map[string]any{"name": "new-" + setting}, "spec": runtime.DeepCopyJSON(current.Object["spec"].(map[string]any))}}
				if kind == "OpenStackServer" && setting != "omitted" {
					fresh.Object["spec"].(map[string]any)["ports"].([]any)[0].(map[string]any)["enablePortSecurity"] = setting == "false"
				}
				created, err := resource.Create(ctx, fresh, metav1.CreateOptions{})
				observe("POST new client candidate", fresh, created, err)
				Expect(err).NotTo(HaveOccurred())
				observed, err := resource.Get(ctx, created.GetName(), metav1.GetOptions{})
				observe("GET new client candidate", nil, observed, err)
				Expect(err).NotTo(HaveOccurred())
				if kind == "OpenStackServer" {
					port := observed.Object["spec"].(map[string]any)["ports"].([]any)[0].(map[string]any)
					if setting == "omitted" {
						Expect(port).NotTo(HaveKey("enablePortSecurity"))
					} else {
						Expect(port).To(HaveKeyWithValue("enablePortSecurity", setting == "false"))
					}
				}
				legacyNewStatus := observed.DeepCopy()
				condition := map[string]any{"type": "Ready", "status": "False", "severity": "Warning", "lastTransitionTime": "2026-10-30T00:00:00Z"}
				if setting != "omitted" {
					condition["reason"] = "LegacyWaiting"
					condition["message"] = "legacy controller"
				}
				Expect(unstructured.SetNestedSlice(legacyNewStatus.Object, []any{condition}, "status", "conditions")).To(Succeed())
				statusResponse, statusErr := resource.UpdateStatus(ctx, legacyNewStatus, metav1.UpdateOptions{})
				observe("PUT legacy status on new candidate object", legacyNewStatus, statusResponse, statusErr)
				if setting == "omitted" {
					Expect(apierrors.IsInvalid(statusErr)).To(BeTrue())
				} else {
					Expect(statusErr).NotTo(HaveOccurred())
				}
			}
		})
	}
})

func renderedAlphaCRD(ctx context.Context, kind string) *apiextensionsv1.CustomResourceDefinition {
	opts := install.NewInstallOptionsWithDefaults()
	opts.PlatformsToInstall = []string{"OpenStack"}
	opts.OutputTypes = string(install.OutputCRDs)
	opts.Format = install.RenderFormatYaml
	var manifests bytes.Buffer
	Expect(install.RenderHyperShiftOperator(ctx, &manifests, &opts)).To(Succeed())
	decoder := utilyaml.NewYAMLOrJSONDecoder(&manifests, 4096)
	for {
		object := &apiextensionsv1.CustomResourceDefinition{}
		err := decoder.Decode(object)
		if errors.Is(err, io.EOF) {
			break
		}
		Expect(err).NotTo(HaveOccurred())
		if object.Spec.Names.Kind == kind {
			return object
		}
	}
	Fail("rendered CRD not found for " + kind)
	return nil
}
