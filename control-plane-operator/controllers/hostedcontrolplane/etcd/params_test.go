package etcd

import (
	"testing"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
)

func TestNewEtcdParams(t *testing.T) {
	tests := []struct {
		name   string
		hcp    *hyperv1.HostedControlPlane
		images map[string]string
	}{
		{
			name: "default managed storage options if unset",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Etcd: hyperv1.EtcdSpec{
						ManagementType: hyperv1.Managed,
						Managed:        nil,
					},
				},
			},
			images: map[string]string{"etcd": "someimage"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			g.Expect(NewEtcdParams(tt.hcp, tt.images)).ToNot(BeNil())
		})
	}
}
