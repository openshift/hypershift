//go:build e2ev2

package main

import (
	"strings"
	"testing"
)

func TestDumpClusterArgs(t *testing.T) {
	args := dumpClusterArgs("/artifacts/hc", "hc", "clusters-hc")
	joinedArgs := strings.Join(args, " ")
	for _, want := range []string{
		"dump cluster",
		"--artifact-dir=/artifacts/hc",
		"--dump-guest-cluster=true",
		"--name=hc",
		"--namespace=clusters-hc",
	} {
		if !strings.Contains(joinedArgs, want) {
			t.Fatalf("dump cluster args %v do not contain %q", args, want)
		}
	}
	if strings.Contains(joinedArgs, "--azure-creds=") {
		t.Fatalf("dump cluster args unexpectedly contain Azure credentials: %v", args)
	}
}
