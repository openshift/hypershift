package openstackconversion

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func testPopulatedRoundTrips(t *testing.T) {
	t.Helper()
	fixtures := map[string]string{
		"OpenStackCluster": `{"spec":{"networkMTU":1450,"disablePortSecurity":false,"disableExternalNetwork":true,"apiServerPort":6443,"disableAPIServerFloatingIP":false,"apiServerFixedIP":"192.0.2.10","managedSecurityGroups":{"allNodesSecurityGroupRules":[{"name":"ingress","direction":"ingress","etherType":"IPv4","protocol":"tcp","portRangeMin":443,"portRangeMax":443}]},"bastion":{"enabled":true,"spec":{"flavor":"small","ports":[{"nameSuffix":"bastion","disablePortSecurity":true}]}}},"status":{"ready":true,"failureDomains":{"zone-b":{"controlPlane":false,"attributes":{"region":"test"}},"zone-a":{"controlPlane":true}},"conditions":[{"type":"Ready","status":"True","severity":"Info","reason":"Provisioned","message":"ready","lastTransitionTime":"2026-10-30T00:00:00Z"}]}}`,
		"OpenStackMachine": `{"spec":{"flavor":"small","flavorID":"legacy-id","ports":[{"nameSuffix":"enabled","disablePortSecurity":false},{"nameSuffix":"disabled","disablePortSecurity":true},{"nameSuffix":"unset"}],"securityGroups":[{"id":"group-id"}],"serverMetadata":[{"key":"keep","value":"value"}]},"status":{"ready":true,"instanceID":"instance-id","conditions":[{"type":"Ready","status":"True","severity":"Info","reason":"Provisioned","message":"ready","lastTransitionTime":"2026-10-30T00:00:00Z"}]}}`,
	}
	for kind, fixture := range fixtures {
		for _, template := range []bool{false, true} {
			name := kind
			if template {
				name += "Template"
			}
			t.Run("When populated "+name+" crosses both APIs, it should retain unchanged data and map typed fields", func(t *testing.T) {
				scheme := testScheme(t)
				gvk := schema.GroupVersionKind{Group: "infrastructure.cluster.x-k8s.io", Version: "v1beta1", Kind: name}
				source, err := scheme.New(gvk)
				if err != nil {
					t.Fatal(err)
				}
				var fields map[string]any
				if err := json.Unmarshal([]byte(fixture), &fields); err != nil {
					t.Fatal(err)
				}
				if template {
					fields = map[string]any{"spec": map[string]any{"template": map[string]any{"spec": fields["spec"]}}}
				}
				encoded, err := json.Marshal(fields)
				if err != nil {
					t.Fatal(err)
				}
				decoder := json.NewDecoder(bytes.NewReader(encoded))
				decoder.DisallowUnknownFields()
				if err := decoder.Decode(source); err != nil {
					t.Fatal(err)
				}
				hub, err := scheme.New(gvk.GroupKind().WithVersion("v1beta2"))
				if err != nil {
					t.Fatal(err)
				}
				if err := (Converter{}).ConvertObject(context.Background(), source, hub); err != nil {
					t.Fatal(err)
				}
				actual, err := runtime.DefaultUnstructuredConverter.ToUnstructured(hub)
				if err != nil {
					t.Fatal(err)
				}
				spec := actual["spec"].(map[string]any)
				if template {
					spec = spec["template"].(map[string]any)["spec"].(map[string]any)
				}
				if kind == "OpenStackCluster" {
					network := spec["managedNetwork"].(map[string]any)
					if network["enablePortSecurity"] != true || network["mtu"] != int64(1450) || spec["enableExternalNetwork"] != false {
						t.Fatalf("incorrect network mapping: %#v", spec)
					}
					server := spec["apiServer"].(map[string]any)
					if server["enableFloatingIP"] != true || server["port"] != int64(6443) {
						t.Fatalf("incorrect API server mapping: %#v", server)
					}
					groups := spec["managedSecurityGroups"].(map[string]any)
					if len(groups["clusterNodesSecurityGroupRules"].([]any)) != 1 {
						t.Fatalf("lost security group rules: %#v", groups)
					}
					if !template {
						domains := actual["status"].(map[string]any)["failureDomains"].([]any)
						if len(domains) != 2 || domains[0].(map[string]any)["name"] != "zone-a" {
							t.Fatalf("incorrect failure domains: %#v", domains)
						}
					}
				} else {
					ports := spec["ports"].([]any)
					if ports[0].(map[string]any)["enablePortSecurity"] != true || ports[1].(map[string]any)["enablePortSecurity"] != false || ports[2].(map[string]any)["enablePortSecurity"] != nil {
						t.Fatalf("incorrect port mapping: %#v", ports)
					}
				}
				restored := source.DeepCopyObject()
				if err := (Converter{}).ConvertObject(context.Background(), hub, restored); err != nil {
					t.Fatal(err)
				}
				restored.(metav1.Object).SetAnnotations(nil)
				if !reflect.DeepEqual(source, restored) {
					t.Fatalf("populated round trip changed %s: %#v", name, restored)
				}
				secondHub := hub.DeepCopyObject()
				if err := (Converter{}).ConvertObject(context.Background(), restored, secondHub); err != nil {
					t.Fatal(err)
				}
				secondHub.(metav1.Object).SetAnnotations(hub.(metav1.Object).GetAnnotations())
				if !reflect.DeepEqual(hub, secondHub) {
					t.Fatalf("beta2 round trip changed %s", name)
				}
			})
		}
	}
}

func testConverterPreservationEdits(t *testing.T) {
	t.Helper()
	for _, kind := range []string{"OpenStackCluster", "OpenStackClusterTemplate", "OpenStackMachine", "OpenStackMachineTemplate"} {
		for _, version := range []string{"v1beta1", "v1beta2"} {
			t.Run("When "+kind+" at "+version+" has intermediate fields removed, it should not resurrect stale annotation values", func(t *testing.T) {
				scheme := testScheme(t)
				gvk := schema.GroupVersionKind{Group: "infrastructure.cluster.x-k8s.io", Version: version, Kind: kind}
				source, err := scheme.New(gvk)
				if err != nil {
					t.Fatal(err)
				}
				spec := map[string]any{"identityRef": map[string]any{"name": "credential", "cloudName": "test"}}
				if kind == "OpenStackClusterTemplate" || kind == "OpenStackMachineTemplate" {
					spec = map[string]any{"template": map[string]any{"spec": spec}}
				}
				if err := runtime.DefaultUnstructuredConverter.FromUnstructured(map[string]any{"spec": spec}, source); err != nil {
					t.Fatal(err)
				}
				other := "v1beta2"
				if version == other {
					other = "v1beta1"
				}
				intermediate, err := scheme.New(gvk.GroupKind().WithVersion(other))
				if err != nil {
					t.Fatal(err)
				}
				if err := (Converter{}).ConvertObject(context.Background(), source, intermediate); err != nil {
					t.Fatal(err)
				}
				data, err := runtime.DefaultUnstructuredConverter.ToUnstructured(intermediate)
				if err != nil {
					t.Fatal(err)
				}
				fields := data["spec"].(map[string]any)
				if kind == "OpenStackClusterTemplate" || kind == "OpenStackMachineTemplate" {
					fields = fields["template"].(map[string]any)["spec"].(map[string]any)
				}
				delete(fields, "identityRef")
				fresh, err := scheme.New(gvk.GroupKind().WithVersion(other))
				if err != nil {
					t.Fatal(err)
				}
				if err := runtime.DefaultUnstructuredConverter.FromUnstructured(data, fresh); err != nil {
					t.Fatal(err)
				}
				restored := source.DeepCopyObject()
				if err := (Converter{}).ConvertObject(context.Background(), fresh, restored); err != nil {
					t.Fatal(err)
				}
				result, err := runtime.DefaultUnstructuredConverter.ToUnstructured(restored)
				if err != nil {
					t.Fatal(err)
				}
				path := []string{"spec"}
				if kind == "OpenStackClusterTemplate" || kind == "OpenStackMachineTemplate" {
					path = append(path, "template", "spec")
				}
				for _, field := range []string{"name", "cloudName"} {
					value, _, err := unstructured.NestedString(result, append(append([]string{}, path...), "identityRef", field)...)
					if err != nil || value != "" {
						t.Fatalf("resurrected deleted identity %s: %q, %v", field, value, err)
					}
				}
			})
		}
	}
}
