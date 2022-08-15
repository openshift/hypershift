package etcd

import (
	"testing"

	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
)

func TestNewEtcdParams(t *testing.T) {
	tests := []struct {
		name                     string
		hcp                      *hyperv1.HostedControlPlane
		images                   map[string]string
		expectedStorageSpec      hyperv1.ManagedEtcdStorageSpec
		expectedSnapshotRestored bool
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
		{
			name: "Managed with RestoreSnapshotURL conditions restored",
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
				Status: hyperv1.HostedControlPlaneStatus{
					Conditions: []metav1.Condition{
						{
							Type:   string(hyperv1.EtcdSnapshotRestored),
							Status: metav1.ConditionTrue,
						},
					},
				},
			},
			images: map[string]string{"etcd": "someimage"},
			expectedStorageSpec: hyperv1.ManagedEtcdStorageSpec{
				RestoreSnapshotURL: []string{"arestoreurl"},
			},
			expectedSnapshotRestored: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			p := NewEtcdParams(tt.hcp, tt.images)
			g.Expect(p).ToNot(BeNil())
			g.Expect(p.EtcdImage).To(Equal(tt.images["etcd"]))
			g.Expect(p.StorageSpec).To(Equal(tt.expectedStorageSpec))
			g.Expect(p.SnapshotRestored).To(Equal(tt.expectedSnapshotRestored))
		})
	}
}
