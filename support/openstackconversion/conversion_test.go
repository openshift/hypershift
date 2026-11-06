package openstackconversion

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"

	capov1beta1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
	capov1beta2 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta2"
	capicore "sigs.k8s.io/cluster-api-provider-openstack/capicore/api/core/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/webhook/conversion"
)

func TestRegister(t *testing.T) {
	t.Run("When the shared HTTP handler receives a review, it should use the registered converters", testConversionReview)
	registry := conversion.NewRegistry()
	if err := Register(registry); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"OpenStackCluster", "OpenStackMachine", "OpenStackClusterTemplate", "OpenStackMachineTemplate"} {
		if _, found := registry.GetConverter(schema.GroupKind{Group: "infrastructure.cluster.x-k8s.io", Kind: kind}); !found {
			t.Errorf("missing converter for %s", kind)
		}
	}
	if err := Register(registry); err == nil {
		t.Fatal("duplicate registration succeeded")
	}
}

func TestConverter_ConvertObject(t *testing.T) {
	t.Run("When populated resources cross versions, it should preserve their supported data", testPopulatedRoundTrips)
	t.Run("When intermediate fields are deleted, it should retain deletion", testConverterPreservationEdits)
	ctx := context.Background()
	t.Run("When legacy conditions omit a reason, it should normalize beta2 and preserve the legacy representation", func(t *testing.T) {
		for _, source := range []runtime.Object{&capov1beta1.OpenStackCluster{}, &capov1beta1.OpenStackMachine{}} {
			fixture := []byte(`{"metadata":{"name":"legacy","generation":3},"spec":{},"status":{"conditions":[{"type":"Ready","status":"False","severity":"Warning","lastTransitionTime":"2026-10-30T00:00:00Z"}]}}`)
			if err := json.Unmarshal(fixture, source); err != nil {
				t.Fatal(err)
			}
			var hub runtime.Object = &capov1beta2.OpenStackCluster{}
			if _, machine := source.(*capov1beta1.OpenStackMachine); machine {
				hub = &capov1beta2.OpenStackMachine{}
			}
			if err := (Converter{}).ConvertObject(ctx, source, hub); err != nil {
				t.Fatal(err)
			}
			hubData, err := runtime.DefaultUnstructuredConverter.ToUnstructured(hub)
			if err != nil {
				t.Fatal(err)
			}
			condition := hubData["status"].(map[string]any)["conditions"].([]any)[0].(map[string]any)
			if condition["reason"] != "Unknown" {
				t.Fatalf("missing normalization: %#v", condition)
			}
			restored := source.DeepCopyObject()
			if err := (Converter{}).ConvertObject(ctx, hub, restored); err != nil {
				t.Fatal(err)
			}
			delete(restored.(metav1.Object).GetAnnotations(), dataAnnotation)
			if len(restored.(metav1.Object).GetAnnotations()) == 0 {
				restored.(metav1.Object).SetAnnotations(nil)
			}
			if !reflect.DeepEqual(source, restored) {
				t.Fatalf("legacy condition round trip changed: %#v", restored)
			}
		}
	})
	for _, kind := range []string{"OpenStackCluster", "OpenStackMachine", "OpenStackClusterTemplate", "OpenStackMachineTemplate"} {
		for _, version := range []string{"v1beta1", "v1beta2"} {
			t.Run("When "+kind+" starts at "+version+", it should preserve metadata and supported data across a round trip", func(t *testing.T) {
				scheme := testScheme(t)
				gvk := schema.GroupVersionKind{Group: "infrastructure.cluster.x-k8s.io", Version: version, Kind: kind}
				source, err := scheme.New(gvk)
				if err != nil {
					t.Fatal(err)
				}
				fixture := `{"metadata":{"name":"worker","namespace":"clusters","generation":7,"labels":{"keep":"yes"},"annotations":{"keep":"yes"},"finalizers":["keep"]},"spec":{}}`
				if err := json.Unmarshal([]byte(fixture), source); err != nil {
					t.Fatal(err)
				}
				source.GetObjectKind().SetGroupVersionKind(gvk)
				if typed, ok := source.(*capov1beta2.OpenStackClusterTemplate); ok {
					typed.Spec.Template.ObjectMeta.Labels = map[string]string{"template": "keep"}
				}
				before := source.DeepCopyObject()
				otherVersion := "v1beta2"
				if version == otherVersion {
					otherVersion = "v1beta1"
				}
				intermediate, err := scheme.New(gvk.GroupKind().WithVersion(otherVersion))
				if err != nil {
					t.Fatal(err)
				}
				intermediate.GetObjectKind().SetGroupVersionKind(gvk.GroupKind().WithVersion(otherVersion))
				if err := (Converter{}).ConvertObject(ctx, source, intermediate); err != nil {
					t.Fatal(err)
				}
				restored := source.DeepCopyObject()
				if err := (Converter{}).ConvertObject(ctx, intermediate, restored); err != nil {
					t.Fatal(err)
				}
				metadata := restored.(metav1.Object)
				delete(metadata.GetAnnotations(), dataAnnotation)
				if !reflect.DeepEqual(source, before) || !reflect.DeepEqual(source, restored) {
					t.Fatalf("round trip or source mutation differs:\nsource=%#v\nrestored=%#v", source, restored)
				}
			})
		}
	}
	t.Run("When the stored hub changes spec and status, it should retain those changes instead of stale spoke data", func(t *testing.T) {
		source := &capov1beta1.OpenStackMachine{
			ObjectMeta: metav1.ObjectMeta{Name: "worker", Generation: 9},
			Spec:       capov1beta1.OpenStackMachineSpec{Flavor: ptr.To("old-name"), FlavorID: ptr.To("old-id")},
			Status:     capov1beta1.OpenStackMachineStatus{Conditions: capicore.Conditions{{Type: "Ready", Status: corev1.ConditionFalse, Severity: "Warning", Reason: "Waiting"}}},
		}
		hub := &capov1beta2.OpenStackMachine{}
		if err := (Converter{}).ConvertObject(ctx, source, hub); err != nil {
			t.Fatal(err)
		}
		hub.Spec.Flavor.ID = ptr.To("new-id")
		hub.Status.Conditions[0].Reason = "Provisioned"
		hub.Status.Conditions[0].Status = metav1.ConditionTrue
		spoke := &capov1beta1.OpenStackMachine{}
		if err := (Converter{}).ConvertObject(ctx, hub, spoke); err != nil {
			t.Fatal(err)
		}
		spokeData, err := runtime.DefaultUnstructuredConverter.ToUnstructured(spoke)
		if err != nil {
			t.Fatal(err)
		}
		if ptr.Deref(spoke.Spec.FlavorID, "") != "new-id" || spoke.Status.Conditions[0].Reason != "Provisioned" || spokeData["status"].(map[string]any)["ready"] != true {
			t.Fatalf("lost hub changes: %#v", spoke)
		}
	})
	t.Run("When preserved data is malformed or inputs are unsupported, it should reject conversion", func(t *testing.T) {
		for _, source := range []runtime.Object{nil, (*capov1beta1.OpenStackMachine)(nil), &corev1.Secret{}, &capov1beta1.OpenStackMachine{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{dataAnnotation: "{"}}}} {
			if err := (Converter{}).ConvertObject(ctx, source, &capov1beta2.OpenStackMachine{}); err == nil {
				t.Errorf("accepted invalid source %T", source)
			}
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if err := (Converter{}).ConvertObject(canceled, &capov1beta1.OpenStackMachine{}, &capov1beta2.OpenStackMachine{}); err == nil {
			t.Fatal("ignored cancellation")
		}
	})
}

func TestRestoreUnchanged(t *testing.T) {
	for _, test := range []struct {
		name                        string
		current, original, baseline any
		want                        any
		present                     bool
	}{
		{"When a field is unchanged, it should restore its original representation", false, nil, false, nil, false},
		{"When a field changed, it should preserve the new value", "new", "legacy", "old", "new", true},
		{"When a version-only field was lost, it should restore it", nil, "legacy", nil, "legacy", true},
		{"When normalization added an unchanged field, it should remove it", map[string]any{"edited": "new", "normalized": false}, map[string]any{"edited": "old"}, map[string]any{"edited": "old", "normalized": false}, map[string]any{"edited": "new"}, true},
		{"When a client deleted a representable field, it should keep it deleted", map[string]any{}, map[string]any{"deleted": "old"}, map[string]any{"deleted": "old"}, map[string]any{}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			actual, present := restoreUnchanged(test.current, test.original, test.baseline)
			if !reflect.DeepEqual(actual, test.want) || present != test.present {
				t.Fatalf("got %#v, want %#v", actual, test.want)
			}
		})
	}
}

func TestNormalizeConditions(t *testing.T) {
	for _, reason := range []string{"", "invalid reason", "Ready"} {
		t.Run("When a legacy condition has reason "+reason+", it should produce a valid beta2 reason", func(t *testing.T) {
			conditions := []metav1.Condition{{Reason: reason}}
			normalizeConditions(conditions, 7)
			expected := "Unknown"
			if reason == "Ready" {
				expected = reason
			}
			if conditions[0].Reason != expected || conditions[0].ObservedGeneration != 7 {
				t.Fatalf("unexpected normalized condition: %#v", conditions[0])
			}
		})
	}
}

func TestConvertFields(t *testing.T) {
	if err := convertFields(&capov1beta1.OpenStackMachine{}, &capov1beta2.OpenStackCluster{}); err == nil {
		t.Fatal("accepted mismatched kinds")
	}
}

func testConversionReview(t *testing.T) {
	t.Helper()
	registry := conversion.NewRegistry()
	if err := Register(registry); err != nil {
		t.Fatal(err)
	}
	handler := conversion.NewWebhookHandler(testScheme(t), registry)
	review := apiextensionsv1.ConversionReview{Request: &apiextensionsv1.ConversionRequest{UID: "capo-review", DesiredAPIVersion: "infrastructure.cluster.x-k8s.io/v1beta2", Objects: []runtime.RawExtension{
		{Raw: []byte(`{"apiVersion":"infrastructure.cluster.x-k8s.io/v1beta1","kind":"OpenStackMachine","metadata":{"name":"one"},"spec":{"flavor":"m1.large"}}`)},
		{Raw: []byte(`{"apiVersion":"infrastructure.cluster.x-k8s.io/v1beta1","kind":"OpenStackCluster","metadata":{"name":"two"},"spec":{}}`)},
	}}}
	data, err := json.Marshal(review)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/convert", bytes.NewReader(data)))
	var converted apiextensionsv1.ConversionReview
	if err := json.Unmarshal(response.Body.Bytes(), &converted); err != nil {
		t.Fatal(err)
	}
	if converted.Response == nil || converted.Response.Result.Status != "Success" || converted.Response.UID != "capo-review" || len(converted.Response.ConvertedObjects) != 2 {
		t.Fatalf("failed conversion: %s", response.Body.String())
	}
	for _, object := range converted.Response.ConvertedObjects {
		var metadata metav1.TypeMeta
		if err := json.Unmarshal(object.Raw, &metadata); err != nil || metadata.APIVersion != review.Request.DesiredAPIVersion {
			t.Fatalf("wrong response GVK: %s", object.Raw)
		}
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/convert", bytes.NewBufferString("{")))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("malformed review returned %d", response.Code)
	}
}

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := capov1beta1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := capov1beta2.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return scheme
}
