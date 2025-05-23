package kas

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestReconcileService(t *testing.T) {
	testCases := []struct {
		name                 string
		platformType         hyperv1.PlatformType
		apiServerServicePort int
		strategy             hyperv1.ServicePublishingStrategy
		svcIn                corev1.Service
		svcOutValidate       func(svcOut corev1.Service, t *testing.T)
	}{
		{
			name:                 "IBM Cloud, NodePort strategy",
			platformType:         hyperv1.IBMCloudPlatform,
			apiServerServicePort: 2040,
			strategy:             hyperv1.ServicePublishingStrategy{Type: hyperv1.NodePort, NodePort: &hyperv1.NodePortPublishingStrategy{Port: 30000}},
			svcIn:                corev1.Service{Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeNodePort}},
			svcOutValidate: func(svcOut corev1.Service, t *testing.T) {
				assert.Equal(t, svcOut.Spec.Type, corev1.ServiceTypeNodePort)
				assert.Equal(t, len(svcOut.Spec.Ports), 1)
				assert.Equal(t, svcOut.Spec.Ports[0].Port, int32(2040))
				assert.Equal(t, svcOut.Spec.Ports[0].NodePort, int32(30000))
			},
		},
		{
			name:                 "IBM Cloud, Route stragety, backend service nodeport untouched",
			platformType:         hyperv1.IBMCloudPlatform,
			apiServerServicePort: 2040,
			strategy:             hyperv1.ServicePublishingStrategy{Type: hyperv1.Route},
			svcIn:                corev1.Service{Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeNodePort, Ports: []corev1.ServicePort{corev1.ServicePort{Port: 1234, NodePort: 30000}}}},
			svcOutValidate: func(svcOut corev1.Service, t *testing.T) {
				assert.Equal(t, svcOut.Spec.Type, corev1.ServiceTypeNodePort)
				assert.Equal(t, len(svcOut.Spec.Ports), 1)
				assert.Equal(t, svcOut.Spec.Ports[0].Port, int32(2040))
				assert.Equal(t, svcOut.Spec.Ports[0].NodePort, int32(30000))
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hcp",
					Namespace: "namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: tc.platformType,
					},
				},
			}
			svcIn := tc.svcIn
			if err := ReconcileService(&svcIn, &tc.strategy, nil, 2040, []string{}, hcp); err != nil {
				t.Fatalf("error in ReconcileService: %s", err.Error())
			}
			tc.svcOutValidate(svcIn, t)
			t.Logf("%+v", svcIn)
		})
	}

	// svc := corev1.Service{Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeNodePort}}
	// hcp := v1beta1.HostedControlPlane{Spec: v1beta1.HostedControlPlaneSpec{Platform: v1beta1.PlatformSpec{Type: v1beta1.IBMCloudPlatform}}}
}
