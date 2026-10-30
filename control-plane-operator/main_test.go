package main

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/capabilities"
)

func TestPrivatePlatformControllersNeeded(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		platform hyperv1.PlatformType
		access   hyperv1.AWSEndpointAccessType
		routes   bool
		want     bool
	}{
		{name: "When AWS becomes public, it should keep finalizer controllers available after a restart", platform: hyperv1.AWSPlatform, access: hyperv1.Public, routes: true, want: true},
		{name: "When AWS is private, it should run PrivateLink controllers", platform: hyperv1.AWSPlatform, access: hyperv1.Private, routes: true, want: true},
		{name: "When AWS has both endpoints, it should run PrivateLink controllers", platform: hyperv1.AWSPlatform, access: hyperv1.PublicAndPrivate, routes: true, want: true},
		{name: "When routes are unavailable, it should not start private controllers", platform: hyperv1.AWSPlatform, access: hyperv1.Public},
		{name: "When the platform is public Azure, it should not start private controllers", platform: hyperv1.AzurePlatform, routes: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			hcp := &hyperv1.HostedControlPlane{Spec: hyperv1.HostedControlPlaneSpec{Platform: hyperv1.PlatformSpec{
				Type: tt.platform, AWS: &hyperv1.AWSPlatformSpec{EndpointAccess: tt.access},
			}}}
			caps := &capabilities.MockCapabilityChecker{MockHas: func(...capabilities.CapabilityType) bool { return tt.routes }}
			g.Expect(privatePlatformControllersNeeded(hcp, caps)).To(Equal(tt.want))
		})
	}
}
