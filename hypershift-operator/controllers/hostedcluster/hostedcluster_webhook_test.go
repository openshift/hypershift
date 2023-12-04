package hostedcluster

import (
	"context"
	"fmt"
	"github.com/openshift/api/image/docker10"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	hyperutil "github.com/openshift/hypershift/support/util"
	"github.com/openshift/hypershift/support/util/fakeimagemetadataprovider"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/kubevirtexternalinfra"
	"github.com/openshift/hypershift/support/api"
)

func TestValidateKVHostedClusterCreate(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pull-secret",
			Namespace: "myns",
		},
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: []byte("test secret"),
		},
	}

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
						Image: "image-4.15.0",
					},
				},
			},
			cnvVersion:   "1.0.0",
			k8sVersion:   "1.27.0",
			expectError:  false,
			imageVersion: "4.15.0",
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
						Image: "image-4.15.0",
					},
				},
			},
			cnvVersion:   "1.0.0",
			k8sVersion:   "1.27.0",
			expectError:  true,
			imageVersion: "4.15.0",
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
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
					Release: v1beta1.Release{
						Image: "image-4.15.0",
					},
				},
			},
			cnvVersion:   "0.111.0",
			k8sVersion:   "1.27.0",
			expectError:  true,
			imageVersion: "4.15.0",
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
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
					Release: v1beta1.Release{
						Image: "image-4.15.0",
					},
				},
			},
			cnvVersion:   "1.0.0",
			k8sVersion:   "1.26.99",
			expectError:  true,
			imageVersion: "4.15.0",
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
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
					Release: v1beta1.Release{
						Image: "image-4.15.0",
					},
				},
			},
			cnvVersion:   "1.0.0",
			k8sVersion:   "1.27.0",
			expectError:  true,
			imageVersion: "4.15.0",
		},
		{
			name: "image version too old",
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
						Image: "image-4.13.0",
					},
				},
			},
			cnvVersion:   "1.0.0",
			k8sVersion:   "1.27.0",
			expectError:  true,
			imageVersion: "4.13.0",
		},
		{
			name: fmt.Sprintf("skip image version validation if the %q annotation is set", v1beta1.SkipReleaseImageValidation),
			hc: &v1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-under-test",
					Namespace: "myns",
					Annotations: map[string]string{
						v1beta1.SkipReleaseImageValidation: "true",
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
						Image: "image-4.13.0",
					},
				},
			},
			cnvVersion:   "1.0.0",
			k8sVersion:   "1.27.0",
			expectError:  false,
			imageVersion: "4.13.0",
		},
		{
			name: "unknown image",
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
						Image: "unknown",
					},
				},
			},
			cnvVersion:   "1.0.0",
			k8sVersion:   "1.27.0",
			expectError:  true,
			imageVersion: "",
		},
	} {
		t.Run(testCase.name, func(tt *testing.T) {
			origValidator := kvValidator
			defer func() {
				kvValidator = origValidator
			}()

			cl := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(secret).Build()
			clientMap := kubevirtexternalinfra.NewMockKubevirtInfraClientMap(cl, testCase.cnvVersion, testCase.k8sVersion)

			kvValidator = &kubevirtClusterValidator{
				client:    cl,
				clientMap: clientMap,
				imageMetaDataProvider: &fakeimagemetadataprovider.FakeImageMetadataProvider{Result: &dockerv1client.DockerImageConfig{Config: &docker10.DockerConfig{
					Labels: map[string]string{versionLabel: testCase.imageVersion}}},
				},
			}

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
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pull-secret",
			Namespace: "myns",
		},
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: []byte("test secret"),
		},
	}

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
						Image: "image-4.13.0",
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
						Image: "image-4.15.0",
					},
				},
			},
			expectError:  false,
			imageVersion: "4.15.0",
		},
		{
			name: "wrong json",
			oldHC: &v1beta1.HostedCluster{
				Spec: v1beta1.HostedClusterSpec{
					Release: v1beta1.Release{
						Image: "image-4.13.0",
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
						Image: "image-4.15.0",
					},
				},
			},
			expectError:  true,
			imageVersion: "4.15.0",
		},
		{
			name: "image version too old",
			oldHC: &v1beta1.HostedCluster{
				Spec: v1beta1.HostedClusterSpec{
					Release: v1beta1.Release{
						Image: "image-4.12.0",
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
						Image: "image-4.13.0",
					},
				},
			},
			expectError:  true,
			imageVersion: "4.13.0",
		},
		{
			name: fmt.Sprintf("skip image version validation if the %q annotation is set", v1beta1.SkipReleaseImageValidation),
			oldHC: &v1beta1.HostedCluster{
				Spec: v1beta1.HostedClusterSpec{
					Release: v1beta1.Release{
						Image: "image-4.12.0",
					},
				},
			},
			newHC: &v1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-under-test",
					Namespace: "myns",
					Annotations: map[string]string{
						v1beta1.SkipReleaseImageValidation: "true",
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
						Image: "image-4.13.0",
					},
				},
			},
			expectError:  false,
			imageVersion: "4.13.0",
		},
		{
			name: "unknown image",
			oldHC: &v1beta1.HostedCluster{
				Spec: v1beta1.HostedClusterSpec{
					Release: v1beta1.Release{
						Image: "image-4.12.0",
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
						Image: "unknown",
					},
				},
			},
			expectError:  true,
			imageVersion: "",
		},
		{
			name: "release image wasn't changed",
			oldHC: &v1beta1.HostedCluster{
				Spec: v1beta1.HostedClusterSpec{
					Release: v1beta1.Release{
						Image: "unknown",
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
						Image: "unknown", // wrong image, but the same as old HC
					},
				},
			},
			expectError:  false,
			imageVersion: "",
		},
	} {
		t.Run(testCase.name, func(tt *testing.T) {
			origValidator := kvValidator
			defer func() {
				kvValidator = origValidator
			}()

			cl := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(secret).Build()

			kvValidator = &kubevirtClusterValidator{
				client: cl,
				//clientMap: nil,
				imageMetaDataProvider: &fakeimagemetadataprovider.FakeImageMetadataProvider{Result: &dockerv1client.DockerImageConfig{Config: &docker10.DockerConfig{
					Labels: map[string]string{versionLabel: testCase.imageVersion}}},
				},
			}

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

func TestKVClusterValidator_getImageVersion(t *testing.T) {
	hc := &v1beta1.HostedCluster{
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
				Image: "image-4.15.0",
			},
		},
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      hc.Spec.PullSecret.Name,
			Namespace: hc.Namespace,
		},
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: []byte("test secret"),
		},
	}
	cl := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(secret).Build()

	v := kubevirtClusterValidator{
		client:    cl,
		clientMap: nil,
		imageMetaDataProvider: &fakeimagemetadataprovider.FakeImageMetadataProvider{Result: &dockerv1client.DockerImageConfig{Config: &docker10.DockerConfig{
			Labels: map[string]string{versionLabel: "4.15.0"}}},
		},
	}

	ctx := context.Background()
	ver, err := v.getImageVersion(ctx, hc, hyperutil.HCControlPlaneReleaseImage(hc))
	if err != nil {
		t.Fatalf("should not return error but it did: %v", err)
	}

	if ver == nil {
		t.Fatalf("should return version but it didn't")
	}

	if ver.Major != 4 || ver.Minor != 15 || ver.Patch != 0 {
		t.Errorf("version should be 4.15.0, but it's %s", ver.String())
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
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pull-secret",
			Namespace: "myns",
		},
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: []byte("test secret"),
		},
	}

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
						Image: "image-4.15.0",
					},
				},
			},
			cnvVersion:   "1.0.0",
			k8sVersion:   "1.27.0",
			expectError:  false,
			imageVersion: "4.15.0",
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
						Image: "image-4.15.0",
					},
				},
			},
			cnvVersion:   "1.0.0",
			k8sVersion:   "1.27.0",
			expectError:  true,
			imageVersion: "4.15.0",
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
						Image: "image-4.15.0",
					},
				},
			},
			cnvVersion:   "0.111.0",
			k8sVersion:   "1.27.0",
			expectError:  true,
			imageVersion: "4.15.0",
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
						Image: "image-4.15.0",
					},
				},
			},
			cnvVersion:   "1.0.0",
			k8sVersion:   "1.26.99",
			expectError:  true,
			imageVersion: "4.15.0",
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
						Image: "image-4.15.0",
					},
				},
			},
			cnvVersion:   "1.0.0",
			k8sVersion:   "1.27.0",
			expectError:  true,
			imageVersion: "4.15.0",
		},
		{
			name: "image version too old",
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
						Image: "image-4.13.0",
					},
				},
			},
			cnvVersion:   "1.0.0",
			k8sVersion:   "1.27.0",
			expectError:  true,
			imageVersion: "4.13.0",
		},
		{
			name: `skip image version validation if the "hypershift.openshift.io/skip-release-image-validation" annotation is set`,
			hc: &v1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-under-test",
					Namespace: "myns",
					Annotations: map[string]string{
						v1beta1.SkipReleaseImageValidation: "true",
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
						Image: "image-4.13.0",
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
						Image: "image-4.13.0",
					},
				},
			},
			cnvVersion:   "1.0.0",
			k8sVersion:   "1.27.0",
			expectError:  false,
			imageVersion: "4.13.0",
		},
		{
			name: "unknown image",
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
						Image: "unknown",
					},
				},
			},
			cnvVersion:   "1.0.0",
			k8sVersion:   "1.27.0",
			expectError:  true,
			imageVersion: "",
		},
	} {
		t.Run(testCase.name, func(tt *testing.T) {
			origValidator := kvValidator
			defer func() {
				kvValidator = origValidator
			}()

			cl := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(secret, testCase.hc).Build()
			clientMap := kubevirtexternalinfra.NewMockKubevirtInfraClientMap(cl, testCase.cnvVersion, testCase.k8sVersion)

			kvValidator = &kubevirtClusterValidator{
				client:    cl,
				clientMap: clientMap,
				imageMetaDataProvider: &fakeimagemetadataprovider.FakeImageMetadataProvider{Result: &dockerv1client.DockerImageConfig{Config: &docker10.DockerConfig{
					Labels: map[string]string{versionLabel: testCase.imageVersion}}},
				},
			}

			npVal := &nodePoolValidator{client: cl}
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
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pull-secret",
			Namespace: "myns",
		},
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: []byte("test secret"),
		},
	}

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
						Image: "image-4.14.0",
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
						Image: "image-4.15.0",
					},
				},
			},
			expectError:  false,
			imageVersion: "4.15.0",
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
						Image: "image-4.14.0",
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
						Image: "image-4.15.0",
					},
				},
			},
			expectError:  true,
			imageVersion: "4.15.0",
		},
		{
			name: "image version too old",
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
						Image: "image-4.12.0",
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
						Image: "image-4.13.0",
					},
				},
			},
			expectError:  true,
			imageVersion: "4.13.0",
		},
		{
			name: `skip image version validation if the "hypershift.openshift.io/skip-release-image-validation" annotation is set`,
			hc: &v1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-under-test",
					Namespace: "myns",
					Annotations: map[string]string{
						v1beta1.SkipReleaseImageValidation: "true",
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
				},
			},
			oldNP: &v1beta1.NodePool{
				Spec: v1beta1.NodePoolSpec{
					Release: v1beta1.Release{
						Image: "image-4.12.0",
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
						Image: "image-4.13.0",
					},
				},
			},
			expectError:  false,
			imageVersion: "4.13.0",
		},
		{
			name: "unknown image",
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
						Image: "image-4.12.0",
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
						Image: "unknown",
					},
				},
			},
			expectError:  true,
			imageVersion: "",
		},
		{
			name: "release image wasn't changed",
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
						Image: "unknown",
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
						Image: "unknown",
					},
				},
			},
			expectError:  false,
			imageVersion: "",
		},
	} {
		t.Run(testCase.name, func(tt *testing.T) {
			origValidator := kvValidator
			defer func() {
				kvValidator = origValidator
			}()

			cl := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(secret, testCase.hc).Build()

			kvValidator = &kubevirtClusterValidator{
				client: cl,
				imageMetaDataProvider: &fakeimagemetadataprovider.FakeImageMetadataProvider{
					Result: &dockerv1client.DockerImageConfig{
						Config: &docker10.DockerConfig{
							Labels: map[string]string{versionLabel: testCase.imageVersion},
						},
					},
				},
			}

			npVal := &nodePoolValidator{client: cl}
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
