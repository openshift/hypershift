//go:build e2ev2

package tests

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

func TestHostedClusterServicesImmutable(t *testing.T) {
	for _, platform := range []hyperv1.PlatformType{hyperv1.AWSPlatform, hyperv1.AzurePlatform, hyperv1.IBMCloudPlatform, hyperv1.KubevirtPlatform, hyperv1.NonePlatform, hyperv1.OpenStackPlatform, hyperv1.GCPPlatform, hyperv1.PowerVSPlatform} {
		t.Run("When the platform is "+string(platform)+", it should follow the Services CEL exception", func(t *testing.T) {
			if got, want := hostedClusterServicesImmutable(platform), platform != hyperv1.IBMCloudPlatform; got != want {
				t.Fatalf("Services immutable=%v, want %v", got, want)
			}
		})
	}
}
