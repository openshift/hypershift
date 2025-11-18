package controlplaneoperator

import (
	"reflect"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestAdaptRole_SecretProviderClassRBAC verifies that SecretProviderClass RBAC permissions
// are granted unconditionally, regardless of the platform type.
// This is a regression test for OCPBUGS-65687 where the RBAC was previously only granted for Azure,
// causing "reflector forbidden" errors on other platforms when the Secrets Store CSI Driver CRD was installed.
func TestAdaptRole_SecretProviderClassRBAC(t *testing.T) {
	tests := map[string]struct {
		platform hyperv1.PlatformType
	}{
		"When platform is AWS it should include SecretProviderClass RBAC": {
			platform: hyperv1.AWSPlatform,
		},
		"When platform is Azure it should include SecretProviderClass RBAC": {
			platform: hyperv1.AzurePlatform,
		},
		"When platform is KubeVirt it should include SecretProviderClass RBAC": {
			platform: hyperv1.KubevirtPlatform,
		},
		"When platform is Agent it should include SecretProviderClass RBAC": {
			platform: hyperv1.AgentPlatform,
		},
		"When platform is PowerVS it should include SecretProviderClass RBAC": {
			platform: hyperv1.PowerVSPlatform,
		},
		"When platform is OpenStack it should include SecretProviderClass RBAC": {
			platform: hyperv1.OpenStackPlatform,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "clusters",
					Annotations: map[string]string{
						util.HostedClusterAnnotation: "clusters/test-cluster",
					},
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: test.platform,
					},
				},
			}

			cpContext := component.WorkloadContext{
				HCP: hcp,
			}

			role := &rbacv1.Role{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "control-plane-operator",
					Namespace: "clusters-test-cluster",
				},
			}

			err := adaptRole(cpContext, role)
			g.Expect(err).ToNot(HaveOccurred())

			// Verify SecretProviderClass RBAC rule exists
			expectedRule := rbacv1.PolicyRule{
				APIGroups: []string{"secrets-store.csi.x-k8s.io"},
				Resources: []string{"secretproviderclasses"},
				Verbs:     []string{"get", "list", "create", "update", "watch"},
			}

			found := false
			for _, rule := range role.Rules {
				if reflect.DeepEqual(rule, expectedRule) {
					found = true
					break
				}
			}

			g.Expect(found).To(BeTrue(),
				"SecretProviderClass RBAC should be present for all platforms to prevent 'reflector forbidden' errors when Secrets Store CSI Driver CRD is installed (OCPBUGS-65687)")

			// Verify HostedCluster annotation was set
			g.Expect(role.Annotations).To(HaveKey(util.HostedClusterAnnotation))
			g.Expect(role.Annotations[util.HostedClusterAnnotation]).To(Equal("clusters/test-cluster"))
		})
	}
}
