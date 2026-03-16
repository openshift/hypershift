package hostedcluster

import (
	"context"
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
			warnings, err := hcVal.ValidateCreate(context.Background(), testCase.hc)

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
			warnings, err := hcVal.ValidateUpdate(context.Background(), testCase.oldHC, testCase.newHC)

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
			warnings, err := npVal.ValidateCreate(context.Background(), testCase.np)

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
			warnings, err := npVal.ValidateUpdate(context.Background(), testCase.oldNP, testCase.newNP)

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
			err := d.Default(context.Background(), hc)
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
			err := d.Default(context.Background(), np)
			if err != nil {
				tt.Errorf("unexpected error: %v", err)
			}
			if np.Spec.Management.UpgradeType != testCase.expectedUpgradeType {
				tt.Errorf("Expected upgrade type %s, but got %s", testCase.expectedUpgradeType, np.Spec.Management.UpgradeType)
			}
		})
	}
}
