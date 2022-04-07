package etcd

import (
	"testing"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
)

func TestNewEtcdParams(t *testing.T) {
	tests := []struct {
		name                string
		hcp                 *hyperv1.HostedControlPlane
		images              map[string]string
		expectedStorageSpec hyperv1.ManagedEtcdStorageSpec
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
			expectedStorageSpec: hyperv1.ManagedEtcdStorageSpec{
				PersistentVolume: &hyperv1.PersistentVolumeEtcdStorageSpec{
					Size: &hyperv1.DefaultPersistentVolumeEtcdStorageSize,
				},
			},
		},
		{
			name: "Managed with RestoreSnapshotURL",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Etcd: hyperv1.EtcdSpec{
						ManagementType: hyperv1.Managed,
						Managed: &hyperv1.ManagedEtcdSpec{
							Storage: hyperv1.ManagedEtcdStorageSpec{
								RestoreSnapshotURL: []string{"arestoreurl"},
							},
						},
					},
				},
			},
			images: map[string]string{"etcd": "someimage"},
			expectedStorageSpec: hyperv1.ManagedEtcdStorageSpec{
				RestoreSnapshotURL: []string{"arestoreurl"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			p := NewEtcdParams(tt.hcp, tt.images)
			g.Expect(p).ToNot(BeNil())
			g.Expect(p.EtcdImage).To(Equal(tt.images["etcd"]))
			g.Expect(p.StorageSpec).To(Equal(tt.expectedStorageSpec))
		})
	}
}
