//go:build e2ev2

package main

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/openshift/hypershift/test/e2e/v2/lifecycle"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func configuredPool(name string, replicas int64) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "hypershift.openshift.io/v1beta1", "kind": "NodePool",
		"metadata": map[string]interface{}{"name": name, "namespace": "clusters"},
		"spec":     map[string]interface{}{"clusterName": "fixture", "replicas": replicas},
	}}
}

func TestRecordAWSExpectedNodePools(t *testing.T) {
	for _, test := range []struct {
		name    string
		objects []*unstructured.Unstructured
		invalid bool
	}{
		{name: "When no pools are rendered, it should record an explicit empty map", objects: nil},
		{name: "When zones are rendered, it should retain each configured replica count", objects: []*unstructured.Unstructured{configuredPool("zone-a", 3), configuredPool("zone-b", 0)}},
		{name: "When a pool is duplicated, it should reject ambiguous intent", objects: []*unstructured.Unstructured{configuredPool("zone-a", 3), configuredPool("zone-a", 3)}, invalid: true},
		{name: "When replicas are negative, it should reject rendered intent", objects: []*unstructured.Unstructured{configuredPool("zone-a", -1)}, invalid: true},
		{name: "When replicas overflow int32, it should reject rendered intent", objects: []*unstructured.Unstructured{configuredPool("zone-a", 2147483648)}, invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var captured map[string]int32
			spec := namedSpec{name: "fixture", recordExpectedNodePools: func(pools map[string]int32) error { captured = pools; return nil }}
			err := recordAWSExpectedNodePools("clusters", spec, test.objects)
			if (err != nil) != test.invalid {
				t.Fatalf("unexpected capture error %v", err)
			}
			if !test.invalid && captured == nil {
				t.Fatal("zero pools must be explicit, not absent")
			}
			if len(captured) == 2 && !reflect.DeepEqual(captured, map[string]int32{"zone-a": 3, "zone-b": 0}) {
				t.Fatal(captured)
			}
		})
	}
	for _, field := range []string{"clusterName", "replicas"} {
		pool := configuredPool("zone-a", 1)
		unstructured.RemoveNestedField(pool.Object, "spec", field)
		spec := namedSpec{name: "fixture", recordExpectedNodePools: func(map[string]int32) error {
			t.Fatal("invalid rendered pool reached the intent recorder")
			return nil
		}}
		if err := recordAWSExpectedNodePools("clusters", spec, []*unstructured.Unstructured{pool}); err == nil {
			t.Fatal("accepted missing " + field)
		}
	}
	pool := configuredPool("zone-a", 1)
	pool.SetNamespace("wrong")
	spec := namedSpec{name: "fixture", recordExpectedNodePools: func(map[string]int32) error {
		t.Fatal("wrong namespace reached the intent recorder")
		return nil
	}}
	if err := recordAWSExpectedNodePools("clusters", spec, []*unstructured.Unstructured{pool}); err == nil {
		t.Fatal("accepted wrong namespace")
	}
	if err := recordAWSExpectedNodePools("clusters", namedSpec{name: "fixture"}, []*unstructured.Unstructured{configuredPool("zone-a", 1)}); err == nil {
		t.Fatal("accepted missing recorder for valid pool")
	}
	want := errors.New("persist failed")
	err := recordAWSExpectedNodePools("clusters", namedSpec{name: "fixture", recordExpectedNodePools: func(map[string]int32) error { return want }}, nil)
	if !errors.Is(err, want) {
		t.Fatalf("lost persistence failure: %v", err)
	}
}

func TestConfigureExpectedNodePoolRecorders(t *testing.T) {
	directory := t.TempDir()
	specs := []namedSpec{{name: "first"}, {name: "second"}}
	manifest := &lifecycle.ClusterManifest{Clusters: []lifecycle.ClusterEntry{{Name: "first"}, {Name: "second"}}}
	configureExpectedNodePoolRecorders(specs, manifest, directory)
	var group sync.WaitGroup
	for index := range specs {
		group.Go(func() {
			if err := specs[index].recordExpectedNodePools(map[string]int32{specs[index].name: int32(index)}); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	got, err := lifecycle.ReadManifest(directory)
	if err != nil {
		t.Fatal(err)
	}
	for index, entry := range got.Clusters {
		if !reflect.DeepEqual(entry.ExpectedNodePools, map[string]int32{specs[index].name: int32(index)}) {
			t.Fatalf("crossed or lost concurrent creation intent: %v", got)
		}
		if !reflect.DeepEqual(specs[index].expectedNodePools, entry.ExpectedNodePools) {
			t.Fatalf("readiness intent differs from manifest: %v != %v", specs[index].expectedNodePools, entry.ExpectedNodePools)
		}
	}
}
