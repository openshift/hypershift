package cno

import (
	"strconv"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
)

func TestReconcileDeployment(t *testing.T) {
	tcs := []struct {
		name                        string
		params                      Params
		expectProxyAPIServerAddress bool
	}{
		{
			name:                        "No private apiserver connectivity, proxy apiserver address is set",
			expectProxyAPIServerAddress: true,
		},
		{
			name:   "Private apiserver connectivity, proxy apiserver address is unset",
			params: Params{IsPrivate: true},
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			if tc.params.ReleaseVersion == "" {
				tc.params.ReleaseVersion = "4.11.0"
			}

			dep := &appsv1.Deployment{}
			if err := ReconcileDeployment(dep, tc.params, nil); err != nil {
				t.Fatalf("ReconcileDeployment: %v", err)
			}

			var hasProxyAPIServerAddress bool
			for _, envVar := range dep.Spec.Template.Spec.Containers[0].Env {
				if envVar.Name == "PROXY_INTERNAL_APISERVER_ADDRESS" {
					hasProxyAPIServerAddress = envVar.Value == "true"
					break
				}
			}

			if hasProxyAPIServerAddress != tc.expectProxyAPIServerAddress {
				t.Errorf("expected 'PROXY_INTERNAL_APISERVER_ADDRESS' env var to be %s, was %s",
					strconv.FormatBool(tc.expectProxyAPIServerAddress),
					strconv.FormatBool(hasProxyAPIServerAddress))
			}
		})
	}
}
