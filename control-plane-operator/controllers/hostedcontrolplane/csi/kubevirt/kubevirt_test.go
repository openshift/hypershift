package kubevirt

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/api"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func TestReconcileKubevirtCSIDriver(t *testing.T) {
	targetNamespace := "test"

	testsCases := []struct {
		name                   string
		hcp                    *hyperv1.HostedControlPlane
		expectedData           map[string]string
		expectedStorageClasses []storagev1.StorageClass
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
			expectedStorageClasses: []storagev1.StorageClass{},
		},
		{
			name: "When Manual storage driver configuration is set",
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
								},
							},
						},
					},
				},
			},
			expectedData: map[string]string{
				"infraClusterNamespace":        targetNamespace,
				"infraClusterLabels":           fmt.Sprintf("%s=1234", hyperv1.InfraIDLabel),
				"infraStorageClassEnforcement": "allowAll: false\nallowList: [s1, s2]\n",
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
				"infraStorageClassEnforcement": "allowAll: false\nallowList: []\n",
			},
		},
	}

	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).Build()

			cm := manifests.KubevirtCSIDriverInfraConfigMap(targetNamespace)
			err := reconcileInfraConfigMap(cm, tc.hcp)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(reflect.DeepEqual(cm.Data, tc.expectedData)).To(BeTrue())

			err = reconcileTenantStorageClasses(fakeClient, tc.hcp, context.Background(), controllerutil.CreateOrUpdate)
			g.Expect(err).NotTo(HaveOccurred())

			var list storagev1.StorageClassList
			err = fakeClient.List(context.Background(), &list)
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(len(list.Items)).To(Equal(len(tc.expectedStorageClasses)))
			for i, sc := range list.Items {
				// ignore resource versioning here
				sc.ResourceVersion = ""
				g.Expect(&sc).To(Equal(&tc.expectedStorageClasses[i]))
			}
		})
	}
}
