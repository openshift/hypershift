//go:build e2ev2

package main

import (
	"strings"
	"testing"
)

func TestDumpClusterArgs(t *testing.T) {
	tests := []struct {
		name       string
		azureCreds string
		wantArg    string
	}{
		{
			name:       "When Azure credentials are set, it should forward the credentials path",
			azureCreds: "/etc/azure/credentials.json",
			wantArg:    "--azure-creds=/etc/azure/credentials.json",
		},
		{
			name: "When Azure credentials are empty, it should omit the credentials flag",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			args := dumpClusterArgs("/artifacts/hc", "hc", "clusters-hc", test.azureCreds)
			joinedArgs := strings.Join(args, " ")
			if test.wantArg != "" && !strings.Contains(joinedArgs, test.wantArg) {
				t.Fatalf("dump cluster args %v do not contain %q", args, test.wantArg)
			}
			if test.wantArg == "" && strings.Contains(joinedArgs, "--azure-creds=") {
				t.Fatalf("dump cluster args unexpectedly contain Azure credentials: %v", args)
			}
		})
	}
}
