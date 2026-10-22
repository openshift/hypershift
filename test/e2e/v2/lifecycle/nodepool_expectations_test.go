//go:build e2ev2

package lifecycle

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/onsi/ginkgo/v2/types"
)

func TestParseExpectedNodePools(t *testing.T) {
	for _, test := range []struct {
		name    string
		value   string
		want    map[string]int32
		invalid bool
	}{
		{name: "When standalone input is absent, it should preserve an optional contract"},
		{name: "When zero pools are configured, it should retain an explicit empty map", value: `{}`, want: map[string]int32{}},
		{name: "When zones have different replica counts, it should retain each configured count", value: `{"zone-a":3,"zone-b":0}`, want: map[string]int32{"zone-a": 3, "zone-b": 0}},
		{name: "When replicas are negative, it should reject the input", value: `{"zone-a":-1}`, invalid: true},
		{name: "When replicas overflow int32, it should reject the input", value: `{"zone-a":2147483648}`, invalid: true},
		{name: "When replicas are fractional, it should reject the input", value: `{"zone-a":1.5}`, invalid: true},
		{name: "When a pool name is invalid, it should reject the input", value: `{"BAD NAME":1}`, invalid: true},
		{name: "When JSON is malformed, it should reject the input", value: `{`, invalid: true},
		{name: "When the configured map is null, it should reject the input", value: `null`, invalid: true},
		{name: "When replicas are null, it should reject the input", value: `{"zone-a":null}`, invalid: true},
		{name: "When the configured value is an array, it should reject the input", value: `[]`, invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseExpectedNodePools(test.value)
			if (err != nil) != test.invalid || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("got %v, %v; want %v invalid=%t", got, err, test.want, test.invalid)
			}
		})
	}
}

func TestManifestExpectedNodePools(t *testing.T) {
	for _, pools := range []map[string]int32{nil, {}, {"fixture-zone-a": 2, "fixture-zone-b": 0}} {
		directory := t.TempDir()
		manifest := &ClusterManifest{Clusters: []ClusterEntry{{Variant: "public", ExpectedNodePools: pools}}}
		if err := WriteManifest(directory, manifest); err != nil {
			t.Fatal(err)
		}
		got, err := ReadManifest(directory)
		if err != nil || !reflect.DeepEqual(manifest, got) {
			t.Fatalf("manifest roundtrip: %v, %v", got, err)
		}
	}

	directory := t.TempDir()
	if err := WriteManifest(directory, &ClusterManifest{Clusters: []ClusterEntry{{ExpectedNodePools: map[string]int32{"bad": -1}}}}); err == nil {
		t.Fatal("accepted negative replicas on write")
	}
	if err := os.WriteFile(filepath.Join(directory, ManifestFileName), []byte(`{"clusters":[{"expectedNodePools":{"bad":-1}}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadManifest(directory); err == nil {
		t.Fatal("accepted negative replicas on read")
	}
}

func TestReadManifestExpectedNodePools(t *testing.T) {
	for _, test := range []struct {
		name    string
		field   string
		want    map[string]int32
		invalid bool
	}{
		{name: "When old manifests omit worker intent, it should preserve compatibility"},
		{name: "When whole-field intent is null, it should preserve compatibility", field: `,"expectedNodePools":null`},
		{name: "When zero pools are configured, it should preserve explicit intent", field: `,"expectedNodePools":{}`, want: map[string]int32{}},
		{name: "When numeric counts are configured, it should retain each count", field: `,"expectedNodePools":{"pool":2}`, want: map[string]int32{"pool": 2}},
		{name: "When a replica count is null, it should reject the manifest", field: `,"expectedNodePools":{"pool":null}`, invalid: true},
		{name: "When a replica count is a string, it should reject the manifest", field: `,"expectedNodePools":{"pool":"2"}`, invalid: true},
		{name: "When a replica count is fractional, it should reject the manifest", field: `,"expectedNodePools":{"pool":0.5}`, invalid: true},
		{name: "When a replica count is negative, it should reject the manifest", field: `,"expectedNodePools":{"pool":-1}`, invalid: true},
		{name: "When a replica count overflows int32, it should reject the manifest", field: `,"expectedNodePools":{"pool":2147483648}`, invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			data := `{"clusters":[{"variant":"public","name":"fixture"` + test.field + `}]}`
			if err := os.WriteFile(filepath.Join(directory, ManifestFileName), []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			manifest, err := ReadManifest(directory)
			if (err != nil) != test.invalid {
				t.Fatalf("unexpected manifest decode result: %v", err)
			}
			if !test.invalid && (manifest.Clusters[0].Name != "fixture" || !reflect.DeepEqual(manifest.Clusters[0].ExpectedNodePools, test.want)) {
				t.Fatalf("lost identity or configured intent: %v", manifest)
			}
		})
	}
}

func TestAWSRootAfterSuccessSteps(t *testing.T) {
	for _, lane := range (&AWSPlatformConfig{}).TestMatrix().Sequential {
		if lane.Name != "public" && lane.Name != "autoscaling" && lane.Name != "external-oidc" {
			continue
		}
		first, last := lane.Steps[0], lane.Steps[len(lane.Steps)-1]
		if first.Name != lane.Name+"-configured-workers" || first.LabelFilter != "hosted-cluster-health-configured-workers" || last.Name != lane.Name+"-common-after" {
			t.Fatalf("missing independent initial/final composition for %s", lane.Name)
		}
		filter, err := types.ParseLabelFilter(last.LabelFilter)
		if err != nil {
			t.Fatal(err)
		}
		for _, label := range []string{"hosted-cluster-health", "control-plane-workloads", "hosted-cluster-compliance", "hosted-cluster-security", "hosted-cluster-ingress", "hosted-cluster-node-communication", "hosted-cluster-metrics", "aws-control-plane-customization"} {
			if !filter([]string{label}) {
				t.Fatalf("%s final check omits %s", lane.Name, label)
			}
		}
		if filter([]string{"hosted-cluster-health", "hosted-cluster-health-configured-workers"}) {
			t.Fatal("final check reuses stale initial replica expectations")
		}
	}
}
