package hostedcluster

import (
	"context"
	"testing"

	"github.com/openshift/hypershift/kubevirtexternalinfra"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/openshift/hypershift/support/api"
)

func TestValidateKubevirtCluster(t *testing.T) {
	for _, testCase := range []struct {
		name           string
		hc             *v1beta1.HostedCluster
		cnvVersion     string
		k8sVersion     string
		expectError    bool
		expectWarnings bool
	}{
		{
			name: "happy case - versions are valid",
			hc: &v1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-under-test",
					Namespace: "myns",
				},
				Spec: v1beta1.HostedClusterSpec{
					Platform: v1beta1.PlatformSpec{
						Type:     v1beta1.KubevirtPlatform,
						Kubevirt: &v1beta1.KubevirtPlatformSpec{},
					},
				},
			},
			cnvVersion:  "1.0.0",
			k8sVersion:  "1.27.0",
			expectError: false,
		},
		{
			name: "cnv version not supported",
			hc: &v1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-under-test",
					Namespace: "myns",
				},
				Spec: v1beta1.HostedClusterSpec{
					Platform: v1beta1.PlatformSpec{
						Type:     v1beta1.KubevirtPlatform,
						Kubevirt: &v1beta1.KubevirtPlatformSpec{},
					},
				},
			},
			cnvVersion:  "0.111.0",
			k8sVersion:  "1.27.0",
			expectError: true,
		},
		{
			name: "k8s version not supported",
			hc: &v1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-under-test",
					Namespace: "myns",
				},
				Spec: v1beta1.HostedClusterSpec{
					Platform: v1beta1.PlatformSpec{
						Type:     v1beta1.KubevirtPlatform,
						Kubevirt: &v1beta1.KubevirtPlatformSpec{},
					},
				},
			},
			cnvVersion:  "1.0.0",
			k8sVersion:  "1.26.99",
			expectError: true,
		},
		{
			name: "no kubevirt field",
			hc: &v1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-under-test",
					Namespace: "myns",
				},
				Spec: v1beta1.HostedClusterSpec{
					Platform: v1beta1.PlatformSpec{
						Type: v1beta1.KubevirtPlatform,
					},
				},
			},
			cnvVersion:  "1.0.0",
			k8sVersion:  "1.27.0",
			expectError: true,
		},
	} {
		t.Run(testCase.name, func(tt *testing.T) {
			cl := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
			clientMap := kubevirtexternalinfra.NewMockKubevirtInfraClientMap(cl, testCase.cnvVersion, testCase.k8sVersion)

			v := kubevirtClusterValidator{
				clientMap: clientMap,
			}

			warnings, err := v.validate(context.Background(), cl, testCase.hc)

			if testCase.expectError && err == nil {
				t.Error("should return error but didn't")
			} else if !testCase.expectError && err != nil {
				t.Errorf("should not return error but returned %q", err.Error())
			}
			if testCase.expectWarnings && warnings == nil {
				t.Error("should return warnings but didn't")
			} else if !testCase.expectWarnings && warnings != nil {
				t.Errorf("should not return warnings but returned %q", warnings)
			}
		})
	}
}
