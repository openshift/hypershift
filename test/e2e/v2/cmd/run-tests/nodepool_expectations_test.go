//go:build e2ev2

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openshift/hypershift/test/e2e/v2/lifecycle"
)

func TestExpectedNodePoolsEnvironment(t *testing.T) {
	for _, test := range []struct {
		name    string
		pools   []map[string]int32
		want    string
		invalid bool
	}{
		{name: "When called standalone, it should preserve inherited explicit input"},
		{name: "When an old manifest omits intent, it should clear stale process input", pools: []map[string]int32{nil}, want: lifecycle.ExpectedNodePoolsEnv + "="},
		{name: "When zero pools are configured, it should pass an explicit empty map", pools: []map[string]int32{{}}, want: lifecycle.ExpectedNodePoolsEnv + "={}"},
		{name: "When pools are configured, it should pass each configured count", pools: []map[string]int32{{"zone-a": 2}}, want: lifecycle.ExpectedNodePoolsEnv + `={"zone-a":2}`},
		{name: "When replicas are invalid, it should reject the child input", pools: []map[string]int32{{"zone-a": -1}}, invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := expectedNodePoolsEnvironment(test.pools...)
			if (err != nil) != test.invalid || got != test.want {
				t.Fatalf("got %q, %v; want %q invalid=%t", got, err, test.want, test.invalid)
			}
		})
	}
}

func TestRunTestBinaryConfiguredNodePools(t *testing.T) {
	directory := t.TempDir()
	binary := filepath.Join(directory, "child")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s' \"$E2E_EXPECTED_NODE_POOLS\" > \"$NODE_POOL_RESULT\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	result := filepath.Join(directory, "result")
	t.Setenv("NODE_POOL_RESULT", result)
	t.Setenv(lifecycle.ExpectedNodePoolsEnv, `{"wrong-parent-cluster":99}`)
	for _, pools := range []map[string]int32{nil, {}, {"zone-a": 2, "zone-b": 0}} {
		if err := runTestBinary(t.Context(), binary, "fixture", "clusters", "", "", filepath.Join(directory, "report.xml"), pools); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(result)
		if err != nil {
			t.Fatal(err)
		}
		want, err := expectedNodePoolsEnvironment(pools)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != strings.TrimPrefix(want, lifecycle.ExpectedNodePoolsEnv+"=") {
			t.Fatalf("wrong per-child input %q", data)
		}
		if os.Getenv(lifecycle.ExpectedNodePoolsEnv) != `{"wrong-parent-cluster":99}` {
			t.Fatal("child handoff mutated concurrent runner environment")
		}
	}
}
