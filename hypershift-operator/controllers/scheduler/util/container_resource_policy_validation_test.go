//go:build envtest

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

package util

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

func TestContainerResourcePolicyValidation(t *testing.T) {
	testEnv := &envtest.Environment{CRDDirectoryPaths: []string{filepath.Join("..", "..", "..", "..", "cmd", "install", "assets", "crds", "hypershift-operator", "scheduling.hypershift.openshift.io_clustersizingconfigurations.yaml")}, ErrorIfCRDPathMissing: true}
	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := testEnv.Stop(); err != nil {
			t.Error(err)
		}
	})
	c, err := client.New(cfg, client.Options{})
	if err != nil {
		t.Fatal(err)
	}
	const policy = `{"defaultRequests":{"cpu":"100m","memory":"128Mi"},"goMemoryLimitPercent":80,"memoryLimitMultiplier":2,"containers":[{"workload":"etcd","container":"etcd","requests":{"cpu":"1","memory":"1Gi"},"goMemoryLimitPercent":0,"goMaxProcs":1}]}`
	for _, tt := range []struct {
		name      string
		old       string
		new       string
		wantError string
	}{
		{name: "When a policy is valid, it should accept explicit defaults and a disabled container memory setting"},
		{name: "When constrained sizing is requested, it should accept CPU limits and 110 percent memory", old: `"memoryLimitMultiplier":2`, new: `"memoryLimitPercent":110,"cpuLimitPolicy":"EqualsRequest"`},
		{name: "When CPU limit policy is None, it should accept it", old: `"memoryLimitMultiplier":2`, new: `"memoryLimitMultiplier":2,"cpuLimitPolicy":"None"`},
		{name: "When CPU limit policy is unknown, it should reject it", old: `"memoryLimitMultiplier":2`, new: `"memoryLimitMultiplier":2,"cpuLimitPolicy":"Unknown"`, wantError: "Unsupported value"},
		{name: "When CPU limit policy is explicitly empty, it should reject it", old: `"memoryLimitMultiplier":2`, new: `"memoryLimitMultiplier":2,"cpuLimitPolicy":""`, wantError: "Unsupported value"},
		{name: "When both memory modes are present, it should reject ambiguity", old: `"memoryLimitMultiplier":2`, new: `"memoryLimitMultiplier":2,"memoryLimitPercent":110`, wantError: "exactly one"},
		{name: "When neither memory mode is present, it should reject missing limits", old: `"memoryLimitMultiplier":2,`, wantError: "exactly one"},
		{name: "When memory percentage is below request, it should reject it", old: `"memoryLimitMultiplier":2`, new: `"memoryLimitPercent":99`, wantError: "greater than or equal to 100"},
		{name: "When memory percentage exceeds maximum, it should reject it", old: `"memoryLimitMultiplier":2`, new: `"memoryLimitPercent":1601`, wantError: "less than or equal to 1600"},
		{name: "When CPU is zero, it should reject the default request", old: `"100m"`, new: `"0"`, wantError: "cpu must be a positive quantity"},
		{name: "When CPU is negative, it should reject the default request", old: `"100m"`, new: `"-1"`, wantError: "cpu must be a positive quantity"},
		{name: "When CPU is malformed, it should reject the default request", old: `"100m"`, new: `"invalid"`, wantError: "cpu must be a positive quantity"},
		{name: "When memory is zero, it should reject the default request", old: `"128Mi"`, new: `"0"`, wantError: "memory must be a positive quantity"},
		{name: "When memory is negative, it should reject the container request", old: `"1Gi"`, new: `"-1Gi"`, wantError: "memory must be a positive quantity"},
		{name: "When memory is missing, it should require both default requests", old: `,"memory":"128Mi"`, wantError: "memory: Required value"},
		{name: "When container CPU is missing, it should require both override requests", old: `"cpu":"1",`, wantError: "cpu: Required value"},
		{name: "When the policy percentage is zero, it should reject it", old: `"goMemoryLimitPercent":80`, new: `"goMemoryLimitPercent":0`, wantError: "greater than or equal to 1"},
		{name: "When the policy percentage exceeds 100, it should reject it", old: `"goMemoryLimitPercent":80`, new: `"goMemoryLimitPercent":101`, wantError: "less than or equal to 100"},
		{name: "When the multiplier is zero, it should reject it", old: `"memoryLimitMultiplier":2`, new: `"memoryLimitMultiplier":0`, wantError: "greater than or equal to 1"},
		{name: "When the multiplier exceeds 16, it should reject it", old: `"memoryLimitMultiplier":2`, new: `"memoryLimitMultiplier":17`, wantError: "less than or equal to 16"},
		{name: "When the container percentage is negative, it should reject it", old: `"goMemoryLimitPercent":0`, new: `"goMemoryLimitPercent":-1`, wantError: "greater than or equal to 0"},
		{name: "When the container percentage exceeds 100, it should reject it", old: `"goMemoryLimitPercent":0`, new: `"goMemoryLimitPercent":101`, wantError: "less than or equal to 100"},
		{name: "When GoMaxProcs is explicitly zero in JSON, it should reject it", old: `"goMaxProcs":1`, new: `"goMaxProcs":0`, wantError: "greater than or equal to 1"},
		{name: "When GoMaxProcs is omitted, it should accept the runtime default", old: `,"goMaxProcs":1`},
		{name: "When GoMaxProcs exceeds 1024, it should reject it", old: `"goMaxProcs":1`, new: `"goMaxProcs":1025`, wantError: "less than or equal to 1024"},
		{name: "When workload is empty, it should reject the override", old: `"workload":"etcd"`, new: `"workload":""`, wantError: "at least 1 chars long"},
		{name: "When overrides are duplicated, it should reject ambiguous container settings", old: `"containers":[`, new: `"containers":[{"workload":"etcd","container":"etcd","requests":{"cpu":"1","memory":"1Gi"}},`, wantError: "Duplicate value"},
		{name: "When policy is omitted, it should accept legacy sizing", old: `"containerResourcePolicy":` + policy, new: `"kasGoMemLimit":"1GiB"`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			data := `{"apiVersion":"scheduling.hypershift.openshift.io/v1alpha1","kind":"ClusterSizingConfiguration","metadata":{"name":"cluster"},"spec":{"sizes":[{"name":"small","criteria":{"from":0},"effects":{"containerResourcePolicy":` + policy + `}}]}}`
			if tt.old != "" {
				data = strings.Replace(data, tt.old, tt.new, 1)
			}
			obj := &unstructured.Unstructured{}
			if err := json.Unmarshal([]byte(data), &obj.Object); err != nil {
				t.Fatal(err)
			}
			err := c.Create(context.Background(), obj, client.DryRunAll)
			if tt.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(tt.new, `"cpuLimitPolicy":"EqualsRequest"`) {
					policies, _, _ := unstructured.NestedSlice(obj.Object, "spec", "sizes")
					stored, _, _ := unstructured.NestedMap(policies[0].(map[string]interface{}), "effects", "containerResourcePolicy")
					if stored["cpuLimitPolicy"] != "EqualsRequest" || stored["memoryLimitPercent"] != int64(110) {
						t.Fatalf("new fields were pruned: %+v", stored)
					}
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("expected error containing %q, got %v", tt.wantError, err)
			}
		})
	}
}
