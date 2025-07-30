package hostedcluster

import (
	"reflect"
	"testing"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestValidateKVHostedClusterCreate(t *testing.T) {
	for _, testCase := range []struct {
		name           string
		hc             *v1beta1.HostedCluster
		cnvVersion     string
		k8sVersion     string
		expectError    bool
		expectWarnings bool
		imageVersion   string
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
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
					Release: v1beta1.Release{
						Image: "image-4.16.0",
					},
				},
			},
			cnvVersion:   "1.0.0",
			k8sVersion:   "1.27.0",
			expectError:  false,
			imageVersion: "4.16.0",
		},
		{
			name: "wrong json",
			hc: &v1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-under-test",
					Namespace: "myns",
					Annotations: map[string]string{
						v1beta1.JSONPatchAnnotation: `[{`,
					},
				},
				Spec: v1beta1.HostedClusterSpec{
					Platform: v1beta1.PlatformSpec{
						Type:     v1beta1.KubevirtPlatform,
						Kubevirt: &v1beta1.KubevirtPlatformSpec{},
					},
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
					Release: v1beta1.Release{
						Image: "image-4.16.0",
					},
				},
			},
			cnvVersion:   "1.0.0",
			k8sVersion:   "1.27.0",
			expectError:  true,
			imageVersion: "4.16.0",
		},
	} {
		t.Run(testCase.name, func(tt *testing.T) {
			hcVal := &hostedClusterValidator{}
			warnings, err := hcVal.ValidateCreate(tt.Context(), testCase.hc)

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

func TestValidateKVHostedClusterUpdate(t *testing.T) {
	for _, testCase := range []struct {
		name           string
		oldHC          *v1beta1.HostedCluster
		newHC          *v1beta1.HostedCluster
		expectError    bool
		expectWarnings bool
		imageVersion   string
	}{
		{
			name: "happy case - versions are valid",
			oldHC: &v1beta1.HostedCluster{
				Spec: v1beta1.HostedClusterSpec{
					Release: v1beta1.Release{
						Image: "image-4.14.0",
					},
				},
			},
			newHC: &v1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-under-test",
					Namespace: "myns",
				},
				Spec: v1beta1.HostedClusterSpec{
					Platform: v1beta1.PlatformSpec{
						Type:     v1beta1.KubevirtPlatform,
						Kubevirt: &v1beta1.KubevirtPlatformSpec{},
					},
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
					Release: v1beta1.Release{
						Image: "image-4.16.0",
					},
				},
			},
			expectError:  false,
			imageVersion: "4.16.0",
		},
		{
			name: "wrong json",
			oldHC: &v1beta1.HostedCluster{
				Spec: v1beta1.HostedClusterSpec{
					Release: v1beta1.Release{
						Image: "image-4.14.0",
					},
				},
			},
			newHC: &v1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-under-test",
					Namespace: "myns",
					Annotations: map[string]string{
						v1beta1.JSONPatchAnnotation: `[{`,
					},
				},
				Spec: v1beta1.HostedClusterSpec{
					Platform: v1beta1.PlatformSpec{
						Type:     v1beta1.KubevirtPlatform,
						Kubevirt: &v1beta1.KubevirtPlatformSpec{},
					},
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
					Release: v1beta1.Release{
						Image: "image-4.16.0",
					},
				},
			},
			expectError:  true,
			imageVersion: "4.16.0",
		},
	} {
		t.Run(testCase.name, func(tt *testing.T) {
			hcVal := &hostedClusterValidator{}
			warnings, err := hcVal.ValidateUpdate(tt.Context(), testCase.oldHC, testCase.newHC)

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

func TestValidateJsonAnnotation(t *testing.T) {
	for _, tc := range []struct {
		name        string
		annotations map[string]string
		expectError bool
	}{
		{
			name:        "no annotation",
			annotations: nil,
			expectError: false,
		},
		{
			name: "valid annotation",
			annotations: map[string]string{
				v1beta1.JSONPatchAnnotation: `[{"op": "replace","path": "/spec/domain/cpu/cores","value": 3}]`,
			},
			expectError: false,
		},
		{
			name: "valid remove without value",
			annotations: map[string]string{
				v1beta1.JSONPatchAnnotation: `[{"op": "remove","path": "/spec/template/metadata/annotations/kubevirt.io~1allow-pod-bridge-network-live-migration"}]`,
			},
			expectError: false,
		},

		{
			name: "not an array",
			annotations: map[string]string{
				v1beta1.JSONPatchAnnotation: `{"op": "replace","path": "/spec/domain/cpu/cores","value": 3}`,
			},
			expectError: true,
		},
		{
			name: "corrupted json",
			annotations: map[string]string{
				v1beta1.JSONPatchAnnotation: `[{"op": "replace","path": "/spec/domain/cpu/cores","value": 3}`,
			},
			expectError: true,
		},
		{
			name: "missing op",
			annotations: map[string]string{
				v1beta1.JSONPatchAnnotation: `[{"path": "/spec/domain/cpu/cores","value": 3}]`,
			},
			expectError: true,
		},
		{
			name: "missing path",
			annotations: map[string]string{
				v1beta1.JSONPatchAnnotation: `[{"op": "replace","value": 3}]`,
			},
			expectError: true,
		},
		{
			name: "missing value",
			annotations: map[string]string{
				v1beta1.JSONPatchAnnotation: `[{"op": "replace","path": "/spec/domain/cpu/cores"}]`,
			},
			expectError: true,
		},
		{
			name: "bad operation",
			annotations: map[string]string{
				v1beta1.JSONPatchAnnotation: `[{"op": "delete","path": "/spec/domain/cpu/cores", "value": "1"}]`,
			},
			expectError: true,
		},
	} {
		t.Run(tc.name, func(tt *testing.T) {
			err := validateJsonAnnotation(tc.annotations)
			if (err != nil) != tc.expectError {
				errMsgBool := []string{" ", "did"}
				if !tc.expectError {
					errMsgBool = []string{" not ", "didn't"}
				}
				tt.Errorf("should%sreturn error, but it %s. error: %v", errMsgBool[0], errMsgBool[1], err)
			}
		})
	}
}

func TestValidateKVNodePoolCreate(t *testing.T) {
	for _, testCase := range []struct {
		name           string
		hc             *v1beta1.HostedCluster
		np             *v1beta1.NodePool
		cnvVersion     string
		k8sVersion     string
		expectError    bool
		expectWarnings bool
		imageVersion   string
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
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
				},
			},
			np: &v1beta1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "np-under-test",
					Namespace: "myns",
				},
				Spec: v1beta1.NodePoolSpec{
					ClusterName: "cluster-under-test",
					Platform: v1beta1.NodePoolPlatform{
						Type:     v1beta1.KubevirtPlatform,
						Kubevirt: &v1beta1.KubevirtNodePoolPlatform{},
					},
					Release: v1beta1.Release{
						Image: "image-4.16.0",
					},
				},
			},
			cnvVersion:   "1.0.0",
			k8sVersion:   "1.27.0",
			expectError:  false,
			imageVersion: "4.16.0",
		},
		{
			name: "wrong json",
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
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
				},
			},
			np: &v1beta1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "np-under-test",
					Namespace: "myns",
					Annotations: map[string]string{
						v1beta1.JSONPatchAnnotation: `[{`,
					},
				},
				Spec: v1beta1.NodePoolSpec{
					ClusterName: "cluster-under-test",
					Platform: v1beta1.NodePoolPlatform{
						Type:     v1beta1.KubevirtPlatform,
						Kubevirt: &v1beta1.KubevirtNodePoolPlatform{},
					},
					Release: v1beta1.Release{
						Image: "image-4.16.0",
					},
				},
			},
			cnvVersion:   "1.0.0",
			k8sVersion:   "1.27.0",
			expectError:  true,
			imageVersion: "4.16.0",
		},
	} {
		t.Run(testCase.name, func(tt *testing.T) {
			npVal := &nodePoolValidator{}
			warnings, err := npVal.ValidateCreate(tt.Context(), testCase.np)

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

func TestValidateKVNodePoolUpdate(t *testing.T) {
	for _, testCase := range []struct {
		name           string
		hc             *v1beta1.HostedCluster
		oldNP          *v1beta1.NodePool
		newNP          *v1beta1.NodePool
		expectError    bool
		expectWarnings bool
		imageVersion   string
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
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
				},
			},
			oldNP: &v1beta1.NodePool{
				Spec: v1beta1.NodePoolSpec{
					Release: v1beta1.Release{
						Image: "image-4.15.0",
					},
				},
			},
			newNP: &v1beta1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "np-under-test",
					Namespace: "myns",
				},
				Spec: v1beta1.NodePoolSpec{
					ClusterName: "cluster-under-test",
					Platform: v1beta1.NodePoolPlatform{
						Type:     v1beta1.KubevirtPlatform,
						Kubevirt: &v1beta1.KubevirtNodePoolPlatform{},
					},
					Release: v1beta1.Release{
						Image: "image-4.16.0",
					},
				},
			},
			expectError:  false,
			imageVersion: "4.16.0",
		},
		{
			name: "wrong json",
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
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
				},
			},
			oldNP: &v1beta1.NodePool{
				Spec: v1beta1.NodePoolSpec{
					Release: v1beta1.Release{
						Image: "image-4.15.0",
					},
				},
			},
			newNP: &v1beta1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "np-under-test",
					Namespace: "myns",
					Annotations: map[string]string{
						v1beta1.JSONPatchAnnotation: `[{`,
					},
				},
				Spec: v1beta1.NodePoolSpec{
					ClusterName: "cluster-under-test",
					Platform: v1beta1.NodePoolPlatform{
						Type:     v1beta1.KubevirtPlatform,
						Kubevirt: &v1beta1.KubevirtNodePoolPlatform{},
					},
					Release: v1beta1.Release{
						Image: "image-4.16.0",
					},
				},
			},
			expectError:  true,
			imageVersion: "4.16.0",
		},
	} {
		t.Run(testCase.name, func(tt *testing.T) {
			npVal := &nodePoolValidator{}
			warnings, err := npVal.ValidateUpdate(tt.Context(), testCase.oldNP, testCase.newNP)

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

// util function used to generate a service map that is different than the defaults
func customKubeVirtServiceMap() []v1beta1.ServicePublishingStrategyMapping {
	// use the defaults as a basis
	defaults := core.GetIngressServicePublishingStrategyMapping(v1beta1.OVNKubernetes, false)

	custom := []v1beta1.ServicePublishingStrategyMapping{}
	for _, cur := range defaults {
		entry := v1beta1.ServicePublishingStrategyMapping{
			Service: cur.Service,
		}
		// none of the kubevirt defaults use nodeport, so this
		// is an easy way to make a service map different than
		// the default
		entry.ServicePublishingStrategy.NodePort = &v1beta1.NodePortPublishingStrategy{}
		custom = append(custom, entry)
	}

	return custom

}

func TestKubevirtClusterServiceDefaulting(t *testing.T) {
	for _, testCase := range []struct {
		name             string
		hc               *v1beta1.HostedCluster
		expectedServices []v1beta1.ServicePublishingStrategyMapping
	}{
		{
			name: "default services in webhook",
			hc: &v1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-under-test",
					Namespace: "myns",
				},
				Spec: v1beta1.HostedClusterSpec{
					Release: v1beta1.Release{
						Image: "example",
					},
					Platform: v1beta1.PlatformSpec{
						Type:     v1beta1.KubevirtPlatform,
						Kubevirt: &v1beta1.KubevirtPlatformSpec{},
					},
				},
			},
			expectedServices: core.GetIngressServicePublishingStrategyMapping(v1beta1.OVNKubernetes, false),
		},
		{
			name: "don't default when services already exist",
			hc: &v1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-under-test",
					Namespace: "myns",
				},
				Spec: v1beta1.HostedClusterSpec{
					Release: v1beta1.Release{
						Image: "example",
					},
					Platform: v1beta1.PlatformSpec{
						Type:     v1beta1.KubevirtPlatform,
						Kubevirt: &v1beta1.KubevirtPlatformSpec{},
					},
					Services: customKubeVirtServiceMap(),
				},
			},
			expectedServices: customKubeVirtServiceMap(),
		},
	} {
		t.Run(testCase.name, func(tt *testing.T) {
			d := hostedClusterDefaulter{}
			hc := testCase.hc.DeepCopy()
			err := d.Default(t.Context(), hc)
			if err != nil {
				tt.Errorf("unexpected error: %v", err)
			}
			if len(hc.Spec.Services) != len(testCase.expectedServices) {
				tt.Errorf("Expected %d len of services, but got %d", len(testCase.expectedServices), len(hc.Spec.Services))
			}

			for _, expected := range testCase.expectedServices {
				found := false
				for _, cur := range hc.Spec.Services {
					if reflect.DeepEqual(&expected, &cur) {
						found = true
						break
					}
				}
				if !found {
					tt.Errorf("Did not find expected matching service of type %s", expected.Service)
				}
			}
		})
	}
}

func TestKubevirtNodePoolManagementDefaulting(t *testing.T) {
	for _, testCase := range []struct {
		name                string
		np                  *v1beta1.NodePool
		expectedUpgradeType v1beta1.UpgradeType
	}{
		{
			name: "default upgrade type in webhook",
			np: &v1beta1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-under-test",
					Namespace: "myns",
				},
				Spec: v1beta1.NodePoolSpec{
					Release: v1beta1.Release{
						Image: "example",
					},
					Platform: v1beta1.NodePoolPlatform{
						Type: v1beta1.KubevirtPlatform,
					},
				},
			},
			expectedUpgradeType: v1beta1.UpgradeTypeReplace,
		},
		{
			name: "non default upgrade type in webhook",
			np: &v1beta1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-under-test",
					Namespace: "myns",
				},
				Spec: v1beta1.NodePoolSpec{
					Release: v1beta1.Release{
						Image: "example",
					},
					Platform: v1beta1.NodePoolPlatform{
						Type: v1beta1.KubevirtPlatform,
					},
					Management: v1beta1.NodePoolManagement{
						UpgradeType: v1beta1.UpgradeTypeInPlace,
					},
				},
			},
			expectedUpgradeType: v1beta1.UpgradeTypeInPlace,
		},
	} {
		t.Run(testCase.name, func(tt *testing.T) {
			d := nodePoolDefaulter{}
			np := testCase.np.DeepCopy()
			err := d.Default(t.Context(), np)
			if err != nil {
				tt.Errorf("unexpected error: %v", err)
			}
			if np.Spec.Management.UpgradeType != testCase.expectedUpgradeType {
				tt.Errorf("Expected upgrade type %s, but got %s", testCase.expectedUpgradeType, np.Spec.Management.UpgradeType)
			}
		})
	}
}

func TestValidateImageTagMirrorSet(t *testing.T) {
	tests := []struct {
		name        string
		itms        []v1beta1.ImageTagMirror
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid single mirror",
			itms: []v1beta1.ImageTagMirror{
				{
					Source:  "quay.io/openshift",
					Mirrors: []string{"mirror.example.com/openshift"},
				},
			},
			expectError: false,
		},
		{
			name: "valid multiple mirrors",
			itms: []v1beta1.ImageTagMirror{
				{
					Source:  "quay.io/openshift",
					Mirrors: []string{"mirror1.example.com/openshift", "mirror2.example.com/openshift"},
				},
			},
			expectError: false,
		},
		{
			name: "valid with mirror source policy",
			itms: []v1beta1.ImageTagMirror{
				{
					Source:  "quay.io/openshift",
					Mirrors: []string{"mirror.example.com/openshift"},
					MirrorSourcePolicy: func() *v1beta1.MirrorSourcePolicy {
						policy := v1beta1.MirrorSourcePolicy("AllowContactingSource")
						return &policy
					}(),
				},
			},
			expectError: false,
		},
		{
			name: "empty source",
			itms: []v1beta1.ImageTagMirror{
				{
					Source:  "",
					Mirrors: []string{"mirror.example.com/openshift"},
				},
			},
			expectError: true,
			errorMsg:    "source cannot be empty",
		},
		{
			name: "duplicate sources",
			itms: []v1beta1.ImageTagMirror{
				{
					Source:  "quay.io/openshift",
					Mirrors: []string{"mirror1.example.com/openshift"},
				},
				{
					Source:  "quay.io/openshift",
					Mirrors: []string{"mirror2.example.com/openshift"},
				},
			},
			expectError: true,
			errorMsg:    "duplicate source",
		},
		{
			name: "invalid mirror source policy",
			itms: []v1beta1.ImageTagMirror{
				{
					Source:  "quay.io/openshift",
					Mirrors: []string{"mirror.example.com/openshift"},
					MirrorSourcePolicy: func() *v1beta1.MirrorSourcePolicy {
						policy := v1beta1.MirrorSourcePolicy("InvalidPolicy")
						return &policy
					}(),
				},
			},
			expectError: true,
			errorMsg:    "invalid mirrorSourcePolicy",
		},
		{
			name: "empty mirrors list",
			itms: []v1beta1.ImageTagMirror{
				{
					Source:  "quay.io/openshift",
					Mirrors: []string{},
				},
			},
			expectError: true, // Empty mirrors list requires at least one mirror
			errorMsg:    "at least one mirror must be specified",
		},
		{
			name: "nil mirrors list",
			itms: []v1beta1.ImageTagMirror{
				{
					Source:  "quay.io/openshift",
					Mirrors: nil,
				},
			},
			expectError: true, // Nil mirrors list requires at least one mirror
			errorMsg:    "at least one mirror must be specified",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			hc := &v1beta1.HostedCluster{
				Spec: v1beta1.HostedClusterSpec{
					ImageTagMirrorSet: test.itms,
				},
			}

			validator := hostedClusterValidator{}
			err := validator.validateImageTagMirrorSet(hc.Spec.ImageTagMirrorSet)

			if test.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				} else if test.errorMsg != "" && !contains(err.Error(), test.errorMsg) {
					t.Errorf("expected error message to contain %q, but got %q", test.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}

			// Note: the validation function doesn't return warnings for ITMS
		})
	}
}

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > len(substr) && s[:len(substr)] == substr) ||
		(len(s) > len(substr) && s[len(s)-len(substr):] == substr) ||
		indexOfSubstring(s, substr) >= 0)
}

func indexOfSubstring(s, substr string) int {
	if len(substr) == 0 {
		return 0
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
