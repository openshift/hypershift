package kubevirt

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/api"

	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v6/apis/volumesnapshot/v1"
)

func TestReconcileKubevirtCSIDriver(t *testing.T) {
	targetNamespace := "test"

	testsCases := []struct {
		name                          string
		hcp                           *hyperv1.HostedControlPlane
		expectedData                  map[string]string
		expectedStorageClasses        []storagev1.StorageClass
		expectedVolumeSnapshotClasses []snapshotv1.VolumeSnapshotClass
	}{
		{
			name: "When no storage driver configuration is set",
			hcp: &hyperv1.HostedControlPlane{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: targetNamespace,
					Name:      "cluster1",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID: "1234",
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.KubevirtPlatform,
					},
				},
			},
			expectedData: map[string]string{
				"infraClusterNamespace":        targetNamespace,
				"infraClusterLabels":           fmt.Sprintf("%s=1234", hyperv1.InfraIDLabel),
				"infraStorageClassEnforcement": "allowDefault: true\nallowAll: false\n",
			},
			expectedStorageClasses: []storagev1.StorageClass{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "kubevirt-csi-infra-default",
						Annotations: map[string]string{
							"storageclass.kubernetes.io/is-default-class": "true",
						},
					},
					Provisioner: "csi.kubevirt.io",
					Parameters: map[string]string{
						"bus": "scsi",
					},
					AllowVolumeExpansion: ptr.To(true),
				},
			},
			expectedVolumeSnapshotClasses: []snapshotv1.VolumeSnapshotClass{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "kubevirt-csi-snapshot",
					},
					Driver:         "csi.kubevirt.io",
					DeletionPolicy: snapshotv1.VolumeSnapshotContentDelete,
				},
			},
		},
		{
			name: "When Default storage driver configuration is set",
			hcp: &hyperv1.HostedControlPlane{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: targetNamespace,
					Name:      "cluster1",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID: "1234",
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: &hyperv1.KubevirtPlatformSpec{
							StorageDriver: &hyperv1.KubevirtStorageDriverSpec{
								Type: hyperv1.DefaultKubevirtStorageDriverConfigType,
							},
						},
					},
				},
			},
			expectedData: map[string]string{
				"infraClusterNamespace":        targetNamespace,
				"infraClusterLabels":           fmt.Sprintf("%s=1234", hyperv1.InfraIDLabel),
				"infraStorageClassEnforcement": "allowDefault: true\nallowAll: false\n",
			},
			expectedStorageClasses: []storagev1.StorageClass{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "kubevirt-csi-infra-default",
						Annotations: map[string]string{
							"storageclass.kubernetes.io/is-default-class": "true",
						},
					},
					Provisioner: "csi.kubevirt.io",
					Parameters: map[string]string{
						"bus": "scsi",
					},
					AllowVolumeExpansion: ptr.To(true),
				},
			},
			expectedVolumeSnapshotClasses: []snapshotv1.VolumeSnapshotClass{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "kubevirt-csi-snapshot",
					},
					Driver:         "csi.kubevirt.io",
					DeletionPolicy: snapshotv1.VolumeSnapshotContentDelete,
				},
			},
		},
		{
			name: "When NONE storage driver configuration is set",
			hcp: &hyperv1.HostedControlPlane{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: targetNamespace,
					Name:      "cluster1",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID: "1234",
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: &hyperv1.KubevirtPlatformSpec{
							StorageDriver: &hyperv1.KubevirtStorageDriverSpec{
								Type: hyperv1.NoneKubevirtStorageDriverConfigType,
							},
						},
					},
				},
			},
			expectedData: map[string]string{
				"infraClusterNamespace":        targetNamespace,
				"infraClusterLabels":           fmt.Sprintf("%s=1234", hyperv1.InfraIDLabel),
				"infraStorageClassEnforcement": "allowDefault: false\nallowAll: false\n",
			},
			expectedStorageClasses:        []storagev1.StorageClass{},
			expectedVolumeSnapshotClasses: []snapshotv1.VolumeSnapshotClass{},
		},
		{
			name: "When Manual storage driver configuration is set with grouping",
			hcp: &hyperv1.HostedControlPlane{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: targetNamespace,
					Name:      "cluster1",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID: "1234",
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: &hyperv1.KubevirtPlatformSpec{
							StorageDriver: &hyperv1.KubevirtStorageDriverSpec{
								Type: hyperv1.ManualKubevirtStorageDriverConfigType,
								Manual: &hyperv1.KubevirtManualStorageDriverConfig{
									StorageClassMapping: []hyperv1.KubevirtStorageClassMapping{
										{
											Group:                 "groupa",
											InfraStorageClassName: "s1",
											GuestStorageClassName: "guest-s1",
										},
										{
											Group:                 "groupa",
											InfraStorageClassName: "s2",
											GuestStorageClassName: "guest-s2",
										},
									},
									VolumeSnapshotClassMapping: []hyperv1.KubevirtVolumeSnapshotClassMapping{
										{
											Group:                        "groupa",
											InfraVolumeSnapshotClassName: "vs1",
											GuestVolumeSnapshotClassName: "guest-vs1",
										},
										{
											Group:                        "groupb",
											InfraVolumeSnapshotClassName: "vs2",
											GuestVolumeSnapshotClassName: "guest-vs2",
										},
									},
								},
							},
						},
					},
				},
			},
			expectedData: map[string]string{
				"infraClusterNamespace":        targetNamespace,
				"infraClusterLabels":           fmt.Sprintf("%s=1234", hyperv1.InfraIDLabel),
				"infraStorageClassEnforcement": "allowAll: false\nallowList: [s1, s2]\nstorageSnapshotMapping: \n- storageClasses:\n  - s1\n  - s2\n  volumeSnapshotClasses:\n  - vs1\n- storageClasses: null\n  volumeSnapshotClasses:\n  - vs2\n",
			},
			expectedStorageClasses: []storagev1.StorageClass{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "guest-s1",
					},
					Provisioner: "csi.kubevirt.io",
					Parameters: map[string]string{
						"bus":                   "scsi",
						"infraStorageClassName": "s1",
					},
					AllowVolumeExpansion: ptr.To(true),
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "guest-s2",
					},
					Provisioner: "csi.kubevirt.io",
					Parameters: map[string]string{
						"bus":                   "scsi",
						"infraStorageClassName": "s2",
					},
					AllowVolumeExpansion: ptr.To(true),
				},
			},
			expectedVolumeSnapshotClasses: []snapshotv1.VolumeSnapshotClass{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "guest-vs1",
					},
					Driver:         "csi.kubevirt.io",
					DeletionPolicy: snapshotv1.VolumeSnapshotContentDelete,
					Parameters: map[string]string{
						"infraSnapshotClassName": "vs1",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "guest-vs2",
					},
					Driver:         "csi.kubevirt.io",
					DeletionPolicy: snapshotv1.VolumeSnapshotContentDelete,
					Parameters: map[string]string{
						"infraSnapshotClassName": "vs2",
					},
				},
			},
		},
		{
			name: "When Manual storage driver configuration is set without grouping",
			hcp: &hyperv1.HostedControlPlane{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: targetNamespace,
					Name:      "cluster1",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID: "1234",
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: &hyperv1.KubevirtPlatformSpec{
							StorageDriver: &hyperv1.KubevirtStorageDriverSpec{
								Type: hyperv1.ManualKubevirtStorageDriverConfigType,
								Manual: &hyperv1.KubevirtManualStorageDriverConfig{
									StorageClassMapping: []hyperv1.KubevirtStorageClassMapping{
										{
											InfraStorageClassName: "s1",
											GuestStorageClassName: "guest-s1",
										},
										{
											InfraStorageClassName: "s2",
											GuestStorageClassName: "guest-s2",
										},
									},
									VolumeSnapshotClassMapping: []hyperv1.KubevirtVolumeSnapshotClassMapping{
										{
											InfraVolumeSnapshotClassName: "vs1",
											GuestVolumeSnapshotClassName: "guest-vs1",
										},
									},
								},
							},
						},
					},
				},
			},
			expectedData: map[string]string{
				"infraClusterNamespace":        targetNamespace,
				"infraClusterLabels":           fmt.Sprintf("%s=1234", hyperv1.InfraIDLabel),
				"infraStorageClassEnforcement": "allowAll: false\nallowList: [s1, s2]\nstorageSnapshotMapping: \n- storageClasses:\n  - s1\n  - s2\n  volumeSnapshotClasses:\n  - vs1\n",
			},
			expectedStorageClasses: []storagev1.StorageClass{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "guest-s1",
					},
					Provisioner: "csi.kubevirt.io",
					Parameters: map[string]string{
						"bus":                   "scsi",
						"infraStorageClassName": "s1",
					},
					AllowVolumeExpansion: ptr.To(true),
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "guest-s2",
					},
					Provisioner: "csi.kubevirt.io",
					Parameters: map[string]string{
						"bus":                   "scsi",
						"infraStorageClassName": "s2",
					},
					AllowVolumeExpansion: ptr.To(true),
				},
			},
			expectedVolumeSnapshotClasses: []snapshotv1.VolumeSnapshotClass{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "guest-vs1",
					},
					Driver:         "csi.kubevirt.io",
					DeletionPolicy: snapshotv1.VolumeSnapshotContentDelete,
					Parameters: map[string]string{
						"infraSnapshotClassName": "vs1",
					},
				},
			},
		},
		{
			name: "When Manual storage driver configuration is set but no mappings are set",
			hcp: &hyperv1.HostedControlPlane{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: targetNamespace,
					Name:      "cluster1",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID: "1234",
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: &hyperv1.KubevirtPlatformSpec{
							StorageDriver: &hyperv1.KubevirtStorageDriverSpec{
								Type:   hyperv1.ManualKubevirtStorageDriverConfigType,
								Manual: &hyperv1.KubevirtManualStorageDriverConfig{},
							},
						},
					},
				},
			},
			expectedData: map[string]string{
				"infraClusterNamespace":        targetNamespace,
				"infraClusterLabels":           fmt.Sprintf("%s=1234", hyperv1.InfraIDLabel),
				"infraStorageClassEnforcement": "allowAll: false\nallowList: []\nstorageSnapshotMapping: \n[]\n",
			},
		},
	}

	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).Build()

			cm := manifests.KubevirtCSIDriverInfraConfigMap(targetNamespace)
			err := reconcileInfraConfigMap(cm, tc.hcp)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(cm.Data).To(Equal(tc.expectedData))

			err = reconcileTenantStorageClasses(fakeClient, tc.hcp, context.Background(), controllerutil.CreateOrUpdate)
			g.Expect(err).NotTo(HaveOccurred())

			var list storagev1.StorageClassList
			err = fakeClient.List(context.Background(), &list)
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(list.Items).To(HaveLen(len(tc.expectedStorageClasses)))
			for i, sc := range list.Items {
				// ignore resource versioning here
				sc.ResourceVersion = ""
				g.Expect(&sc).To(Equal(&tc.expectedStorageClasses[i]))
			}
			err = reconcileTenantVolumeSnapshotClasses(fakeClient, tc.hcp, context.Background(), controllerutil.CreateOrUpdate)
			g.Expect(err).NotTo(HaveOccurred())
			volumeSnapshotClasses := &snapshotv1.VolumeSnapshotClassList{}
			err = fakeClient.List(context.Background(), volumeSnapshotClasses)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(volumeSnapshotClasses.Items).To(HaveLen(len(tc.expectedVolumeSnapshotClasses)))
			for i, vsc := range volumeSnapshotClasses.Items {
				// ignore resource versioning here
				vsc.ResourceVersion = ""
				g.Expect(&vsc).To(Equal(&tc.expectedVolumeSnapshotClasses[i]))
			}
		})
	}
}
