package kubevirt

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	appsv1 "k8s.io/api/apps/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	storagev1 "k8s.io/api/storage/v1"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v6/apis/volumesnapshot/v1"
)

func TestGetStorageDriverType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		hcp      *hyperv1.HostedControlPlane
		expected hyperv1.KubevirtStorageDriverConfigType
	}{
		{
			name: "When HCP has no kubevirt platform spec, it should return DefaultKubevirtStorageDriverConfigType",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Kubevirt: nil,
					},
				},
			},
			expected: hyperv1.DefaultKubevirtStorageDriverConfigType,
		},
		{
			name: "When kubevirt platform has no storage driver, it should return default",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Kubevirt: &hyperv1.KubevirtPlatformSpec{
							StorageDriver: nil,
						},
					},
				},
			},
			expected: hyperv1.DefaultKubevirtStorageDriverConfigType,
		},
		{
			name: "When storage driver type is empty string, it should return default",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Kubevirt: &hyperv1.KubevirtPlatformSpec{
							StorageDriver: &hyperv1.KubevirtStorageDriverSpec{
								Type: "",
							},
						},
					},
				},
			},
			expected: hyperv1.DefaultKubevirtStorageDriverConfigType,
		},
		{
			name: "When storage driver type is explicitly set to Manual, it should return Manual",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Kubevirt: &hyperv1.KubevirtPlatformSpec{
							StorageDriver: &hyperv1.KubevirtStorageDriverSpec{
								Type: hyperv1.ManualKubevirtStorageDriverConfigType,
							},
						},
					},
				},
			},
			expected: hyperv1.ManualKubevirtStorageDriverConfigType,
		},
		{
			name: "When storage driver type is explicitly set to None, it should return None",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Kubevirt: &hyperv1.KubevirtPlatformSpec{
							StorageDriver: &hyperv1.KubevirtStorageDriverSpec{
								Type: hyperv1.NoneKubevirtStorageDriverConfigType,
							},
						},
					},
				},
			},
			expected: hyperv1.NoneKubevirtStorageDriverConfigType,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			result := getStorageDriverType(tt.hcp)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestReconcileDefaultTenantStorageClass(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		test func(t *testing.T, g Gomega)
	}{
		{
			name: "When called, it should set the is-default-class annotation to true",
			test: func(t *testing.T, g Gomega) {
				sc := &storagev1.StorageClass{}
				err := reconcileDefaultTenantStorageClass(sc)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(sc.Annotations).To(HaveKeyWithValue("storageclass.kubernetes.io/is-default-class", "true"))
			},
		},
		{
			name: "When called, it should set the provisioner to csi.kubevirt.io",
			test: func(t *testing.T, g Gomega) {
				sc := &storagev1.StorageClass{}
				err := reconcileDefaultTenantStorageClass(sc)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(sc.Provisioner).To(Equal("csi.kubevirt.io"))
			},
		},
		{
			name: "When called, it should set bus parameter to scsi",
			test: func(t *testing.T, g Gomega) {
				sc := &storagev1.StorageClass{}
				err := reconcileDefaultTenantStorageClass(sc)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(sc.Parameters).To(HaveKeyWithValue("bus", "scsi"))
			},
		},
		{
			name: "When called, it should enable volume expansion",
			test: func(t *testing.T, g Gomega) {
				sc := &storagev1.StorageClass{}
				err := reconcileDefaultTenantStorageClass(sc)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(sc.AllowVolumeExpansion).NotTo(BeNil())
				g.Expect(*sc.AllowVolumeExpansion).To(BeTrue())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			tt.test(t, g)
		})
	}
}

func TestReconcileCustomTenantStorageClass(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		test func(t *testing.T, g Gomega)
	}{
		{
			name: "When infra storage class name is provided, it should set infraStorageClassName parameter",
			test: func(t *testing.T, g Gomega) {
				sc := &storagev1.StorageClass{}
				err := reconcileCustomTenantStorageClass(sc, "my-infra-sc")
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(sc.Parameters).To(HaveKeyWithValue("infraStorageClassName", "my-infra-sc"))
			},
		},
		{
			name: "When called, it should set provisioner and bus parameter",
			test: func(t *testing.T, g Gomega) {
				sc := &storagev1.StorageClass{}
				err := reconcileCustomTenantStorageClass(sc, "some-sc")
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(sc.Provisioner).To(Equal("csi.kubevirt.io"))
				g.Expect(sc.Parameters).To(HaveKeyWithValue("bus", "scsi"))
			},
		},
		{
			name: "When called, it should enable volume expansion",
			test: func(t *testing.T, g Gomega) {
				sc := &storagev1.StorageClass{}
				err := reconcileCustomTenantStorageClass(sc, "some-sc")
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(sc.AllowVolumeExpansion).NotTo(BeNil())
				g.Expect(*sc.AllowVolumeExpansion).To(BeTrue())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			tt.test(t, g)
		})
	}
}

func TestReconcileTenantDaemonset(t *testing.T) {
	t.Parallel()

	allImages := map[string]string{
		"kubevirt-csi-driver":       "registry.example.com/kubevirt-csi-driver:latest",
		"csi-node-driver-registrar": "registry.example.com/csi-node-driver-registrar:latest",
		"csi-livenessprobe":         "registry.example.com/csi-livenessprobe:latest",
	}

	tests := []struct {
		name string
		test func(t *testing.T, g Gomega)
	}{
		{
			name: "When all required images exist, it should set images on containers",
			test: func(t *testing.T, g Gomega) {
				ds := &appsv1.DaemonSet{}
				err := reconcileTenantDaemonset(ds, allImages)
				g.Expect(err).NotTo(HaveOccurred())

				containerImages := map[string]string{}
				for _, c := range ds.Spec.Template.Spec.Containers {
					containerImages[c.Name] = c.Image
				}
				g.Expect(containerImages).To(HaveKeyWithValue("csi-driver", "registry.example.com/kubevirt-csi-driver:latest"))
				g.Expect(containerImages).To(HaveKeyWithValue("csi-node-driver-registrar", "registry.example.com/csi-node-driver-registrar:latest"))
				g.Expect(containerImages).To(HaveKeyWithValue("csi-liveness-probe", "registry.example.com/csi-livenessprobe:latest"))
			},
		},
		{
			name: "When kubevirt-csi-driver image is missing, it should return an error",
			test: func(t *testing.T, g Gomega) {
				ds := &appsv1.DaemonSet{}
				images := map[string]string{
					"csi-node-driver-registrar": "registry.example.com/csi-node-driver-registrar:latest",
					"csi-livenessprobe":         "registry.example.com/csi-livenessprobe:latest",
				}
				err := reconcileTenantDaemonset(ds, images)
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("kubevirt-csi-driver"))
			},
		},
		{
			name: "When csi-node-driver-registrar image is missing, it should return an error",
			test: func(t *testing.T, g Gomega) {
				ds := &appsv1.DaemonSet{}
				images := map[string]string{
					"kubevirt-csi-driver": "registry.example.com/kubevirt-csi-driver:latest",
					"csi-livenessprobe":   "registry.example.com/csi-livenessprobe:latest",
				}
				err := reconcileTenantDaemonset(ds, images)
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("csi-node-driver-registrar"))
			},
		},
		{
			name: "When csi-livenessprobe image is missing, it should return an error",
			test: func(t *testing.T, g Gomega) {
				ds := &appsv1.DaemonSet{}
				images := map[string]string{
					"kubevirt-csi-driver":       "registry.example.com/kubevirt-csi-driver:latest",
					"csi-node-driver-registrar": "registry.example.com/csi-node-driver-registrar:latest",
				}
				err := reconcileTenantDaemonset(ds, images)
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("csi-livenessprobe"))
			},
		},
		{
			name: "When called, it should set management workload annotation",
			test: func(t *testing.T, g Gomega) {
				ds := &appsv1.DaemonSet{}
				err := reconcileTenantDaemonset(ds, allImages)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(ds.Spec.Template.ObjectMeta.Annotations).To(
					HaveKeyWithValue("target.workload.openshift.io/management", `{"effect": "PreferredDuringScheduling"}`),
				)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			tt.test(t, g)
		})
	}
}

func TestReconcileDefaultTenantCSIDriverResource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		test func(t *testing.T, g Gomega)
	}{
		{
			name: "When called, it should set AttachRequired, PodInfoOnMount, and FSGroupPolicy",
			test: func(t *testing.T, g Gomega) {
				csiDriver := &storagev1.CSIDriver{}
				err := reconcileDefaultTenantCSIDriverResource(csiDriver)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(csiDriver.Spec.AttachRequired).NotTo(BeNil())
				g.Expect(*csiDriver.Spec.AttachRequired).To(BeTrue())
				g.Expect(csiDriver.Spec.PodInfoOnMount).NotTo(BeNil())
				g.Expect(*csiDriver.Spec.PodInfoOnMount).To(BeTrue())
				g.Expect(csiDriver.Spec.FSGroupPolicy).NotTo(BeNil())
				g.Expect(*csiDriver.Spec.FSGroupPolicy).To(Equal(storagev1.ReadWriteOnceWithFSTypeFSGroupPolicy))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			tt.test(t, g)
		})
	}
}

func TestReconcileTenantVolumeSnapshotClass(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		test func(t *testing.T, g Gomega)
	}{
		{
			name: "When infraVSCName is empty, it should not set parameters",
			test: func(t *testing.T, g Gomega) {
				vsc := &snapshotv1.VolumeSnapshotClass{}
				err := reconcileTenantVolumeSnapshotClass(vsc, "")
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(vsc.Parameters).To(BeNil())
			},
		},
		{
			name: "When infraVSCName is provided, it should set infraSnapshotClassName parameter",
			test: func(t *testing.T, g Gomega) {
				vsc := &snapshotv1.VolumeSnapshotClass{}
				err := reconcileTenantVolumeSnapshotClass(vsc, "my-infra-vsc")
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(vsc.Parameters).To(HaveKeyWithValue("infraSnapshotClassName", "my-infra-vsc"))
			},
		},
		{
			name: "When called, it should set driver and deletion policy",
			test: func(t *testing.T, g Gomega) {
				vsc := &snapshotv1.VolumeSnapshotClass{}
				err := reconcileTenantVolumeSnapshotClass(vsc, "")
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(vsc.Driver).To(Equal("csi.kubevirt.io"))
				g.Expect(vsc.DeletionPolicy).To(Equal(snapshotv1.VolumeSnapshotContentDelete))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			tt.test(t, g)
		})
	}
}

func TestReconcileTenantControllerClusterRoleBinding(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		test func(t *testing.T, g Gomega)
	}{
		{
			name: "When called with a namespace, it should set the namespace on all subjects",
			test: func(t *testing.T, g Gomega) {
				crb := &rbacv1.ClusterRoleBinding{}
				err := reconcileTenantControllerClusterRoleBinding(crb, "test-namespace")
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(crb.Subjects).NotTo(BeEmpty())
				for _, subject := range crb.Subjects {
					g.Expect(subject.Namespace).To(Equal("test-namespace"))
				}
				g.Expect(crb.RoleRef.Kind).To(Equal("ClusterRole"))
				g.Expect(crb.RoleRef.Name).To(Equal("kubevirt-csi-controller-cr"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			tt.test(t, g)
		})
	}
}

func TestReconcileTenantNodeClusterRoleBinding(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		test func(t *testing.T, g Gomega)
	}{
		{
			name: "When called with a namespace, it should set the namespace on all subjects",
			test: func(t *testing.T, g Gomega) {
				crb := &rbacv1.ClusterRoleBinding{}
				err := reconcileTenantNodeClusterRoleBinding(crb, "my-namespace")
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(crb.Subjects).NotTo(BeEmpty())
				for _, subject := range crb.Subjects {
					g.Expect(subject.Namespace).To(Equal("my-namespace"))
				}
				g.Expect(crb.RoleRef.Kind).To(Equal("ClusterRole"))
				g.Expect(crb.RoleRef.Name).To(Equal("kubevirt-csi-node-cr"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			tt.test(t, g)
		})
	}
}
