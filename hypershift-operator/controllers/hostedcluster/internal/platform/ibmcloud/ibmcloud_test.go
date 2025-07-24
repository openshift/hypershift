package ibmcloud

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"

	v1 "github.com/openshift/api/config/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	capiibmv1 "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta2"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/google/go-cmp/cmp"
)

func TestReconcileCAPIInfraCR(t *testing.T) {
	fakeControlPlaneNamespace := "master-cluster1"
	fakeHostedClusterName := "cluster1"
	fakeHostedClusterNamespace := "master"
	fakeAPIEndpoint := hyperv1.APIEndpoint{
		Host: "example.com",
		Port: 443,
	}
	tests := map[string]struct {
		inputHostedCluster         *hyperv1.HostedCluster
		inputAPIEndpoint           hyperv1.APIEndpoint
		inputControlPlaneNamespace string
		expectedObject             client.Object
	}{
		"when Classic provider type specified for IBMCloud no CAPI cluster is created": {
			inputControlPlaneNamespace: fakeControlPlaneNamespace,
			inputHostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fakeHostedClusterName,
					Namespace: fakeHostedClusterNamespace,
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.IBMCloudPlatform,
						IBMCloud: &hyperv1.IBMCloudPlatformSpec{
							ProviderType: v1.IBMCloudProviderTypeClassic,
						},
					},
				},
			},
			inputAPIEndpoint: fakeAPIEndpoint,
			expectedObject:   nil,
		},
		"when VPC provider type specified for IBMCloud no CAPI cluster is created": {
			inputControlPlaneNamespace: fakeControlPlaneNamespace,
			inputHostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fakeHostedClusterName,
					Namespace: fakeHostedClusterNamespace,
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.IBMCloudPlatform,
						IBMCloud: &hyperv1.IBMCloudPlatformSpec{
							ProviderType: v1.IBMCloudProviderTypeVPC,
						},
					},
				},
			},
			inputAPIEndpoint: fakeAPIEndpoint,
			expectedObject:   nil,
		},
		"when UPI provider type specified for IBMCloud no CAPI cluster is created": {
			inputControlPlaneNamespace: fakeControlPlaneNamespace,
			inputHostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fakeHostedClusterName,
					Namespace: fakeHostedClusterNamespace,
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.IBMCloudPlatform,
						IBMCloud: &hyperv1.IBMCloudPlatformSpec{
							ProviderType: v1.IBMCloudProviderTypeVPC,
						},
					},
				},
			},
			inputAPIEndpoint: fakeAPIEndpoint,
			expectedObject:   nil,
		},
		"when platform type not specified for IBMCloud a VPC CAPI cluster is created": {
			inputControlPlaneNamespace: fakeControlPlaneNamespace,
			inputHostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fakeHostedClusterName,
					Namespace: fakeHostedClusterNamespace,
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type:     hyperv1.IBMCloudPlatform,
						IBMCloud: &hyperv1.IBMCloudPlatformSpec{},
					},
				},
			},
			inputAPIEndpoint: fakeAPIEndpoint,
			expectedObject: &capiibmv1.IBMVPCCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: fakeControlPlaneNamespace,
					Name:      fakeHostedClusterName,
					Annotations: map[string]string{
						capiv1.ManagedByAnnotation: "external",
					},
					// since resource created through fakeclient this is set to 1 to ensure the struct compare works
					ResourceVersion: "1",
				},
				Status: capiibmv1.IBMVPCClusterStatus{
					Ready: true,
				},
				Spec: capiibmv1.IBMVPCClusterSpec{
					ControlPlaneEndpoint: capiv1.APIEndpoint{
						Port: fakeAPIEndpoint.Port,
						Host: fakeAPIEndpoint.Host,
					},
				},
			},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
			fakeReconciler := IBMCloud{}
			actualinfraCR, err := fakeReconciler.ReconcileCAPIInfraCR(t.Context(), fakeClient, controllerutil.CreateOrUpdate, test.inputHostedCluster, test.inputControlPlaneNamespace, test.inputAPIEndpoint)
			g.Expect(err).To(Not(HaveOccurred()))
			if diff := cmp.Diff(actualinfraCR, test.expectedObject); diff != "" {
				t.Errorf("actual and expected differ: %s", diff)
			}
		})
	}
}
