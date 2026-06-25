package nodepool

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/releaseinfo"

	imageapi "github.com/openshift/api/image/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func Test_getRHELStream(t *testing.T) {
	testCases := []struct {
		name           string
		nodePool       *hyperv1.NodePool
		releaseImage   *releaseinfo.ReleaseImage
		configs        []client.Object
		expectedStream string
		expectErr      bool
	}{
		{
			name: "When spec.osImageStream.Name is rhel-10 and version is 5.x, it should return rhel-10",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					OSImageStream: hyperv1.OSImageStreamReference{Name: "rhel-10"},
				},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "5.0.0"}},
			},
			expectedStream: "rhel-10",
		},
		{
			name: "When spec.osImageStream.Name is rhel-9 and version is 4.x, it should return rhel-9",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					OSImageStream: hyperv1.OSImageStreamReference{Name: "rhel-9"},
				},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "4.18.0"}},
			},
			expectedStream: "rhel-9",
		},
		{
			name: "When spec.osImageStream.Name is rhel-10 and version is 4.x, it should return an error",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					OSImageStream: hyperv1.OSImageStreamReference{Name: "rhel-10"},
				},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "4.18.0"}},
			},
			expectErr: true,
		},
		{
			name: "When spec.osImageStream.Name is invalid, it should return an error",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					OSImageStream: hyperv1.OSImageStreamReference{Name: "rhel-8"},
				},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "5.0.0"}},
			},
			expectErr: true,
		},
		{
			name: "When spec.osImageStream.Name is empty and version is 4.x, it should return rhel-9",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "4.18.0"}},
			},
			expectedStream: "rhel-9",
		},
		{
			name: "When spec.osImageStream.Name is empty and version is 5.x, it should return rhel-10",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "5.0.0"}},
			},
			expectedStream: "rhel-10",
		},
		{
			name: "When spec.osImageStream.Name is empty and version is 6.x, it should return rhel-10",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "6.1.0"}},
			},
			expectedStream: "rhel-10",
		},
		{
			name: "When spec.osImageStream.Name is empty and version is unparsable, it should return an error",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "not-a-version"}},
			},
			expectErr: true,
		},
		{
			name: "When ContainerRuntimeConfig sets runc and release is 5.0 with no explicit stream it should return rhel-9",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-np",
					Namespace: "clusters",
				},
				Spec: hyperv1.NodePoolSpec{
					Config: []corev1.LocalObjectReference{
						{Name: "runc-config"},
					},
				},
			},
			configs: []client.Object{
				runcContainerRuntimeConfigMap("clusters", "runc-config"),
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "5.0.0"}},
			},
			expectedStream: "rhel-9",
		},
		{
			name: "When ContainerRuntimeConfig sets runc and explicit rhel-10 it should return error",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-np",
					Namespace: "clusters",
				},
				Spec: hyperv1.NodePoolSpec{
					OSImageStream: hyperv1.OSImageStreamReference{Name: "rhel-10"},
					Config: []corev1.LocalObjectReference{
						{Name: "runc-config"},
					},
				},
			},
			configs: []client.Object{
				runcContainerRuntimeConfigMap("clusters", "runc-config"),
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "5.0.0"}},
			},
			expectErr: true,
		},
		{
			name: "When ContainerRuntimeConfig sets crun and release is 5.0 it should return rhel-10",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-np",
					Namespace: "clusters",
				},
				Spec: hyperv1.NodePoolSpec{
					Config: []corev1.LocalObjectReference{
						{Name: "crun-config"},
					},
				},
			},
			configs: []client.Object{
				crunContainerRuntimeConfigMap("clusters", "crun-config"),
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "5.0.0"}},
			},
			expectedStream: "rhel-10",
		},
		{
			name: "When ContainerRuntimeConfig sets runc and release is 4.x it should return rhel-9",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-np",
					Namespace: "clusters",
				},
				Spec: hyperv1.NodePoolSpec{
					Config: []corev1.LocalObjectReference{
						{Name: "runc-config"},
					},
				},
			},
			configs: []client.Object{
				runcContainerRuntimeConfigMap("clusters", "runc-config"),
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "4.18.0"}},
			},
			expectedStream: "rhel-9",
		},
		{
			name: "When config ConfigMap does not exist it should not detect runc",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-np",
					Namespace: "clusters",
				},
				Spec: hyperv1.NodePoolSpec{
					Config: []corev1.LocalObjectReference{
						{Name: "missing-config"},
					},
				},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "5.0.0"}},
			},
			expectedStream: "rhel-10",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			objs := make([]client.Object, 0, len(tc.configs))
			objs = append(objs, tc.configs...)

			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objs...).Build()

			stream, err := getRHELStream(t.Context(), fakeClient, tc.nodePool, tc.releaseImage)
			if tc.expectErr {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(stream).To(Equal(tc.expectedStream))
		})
	}
}

func TestValidateOSImageStream(t *testing.T) {
	testCases := []struct {
		name         string
		nodePool     *hyperv1.NodePool
		releaseImage *releaseinfo.ReleaseImage
		configs      []client.Object
		expectErr    bool
	}{
		{
			name: "When osImageStream.Name is empty, it should succeed",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "5.0.0"}},
			},
		},
		{
			name: "When osImageStream.Name is rhel-9 and version is 4.x, it should succeed",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					OSImageStream: hyperv1.OSImageStreamReference{Name: "rhel-9"},
				},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "4.18.0"}},
			},
		},
		{
			name: "When osImageStream.Name is rhel-10 and version is 5.x, it should succeed",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					OSImageStream: hyperv1.OSImageStreamReference{Name: "rhel-10"},
				},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "5.0.0"}},
			},
		},
		{
			name: "When osImageStream.Name is rhel-10 and version is 4.x, it should return an error",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					OSImageStream: hyperv1.OSImageStreamReference{Name: "rhel-10"},
				},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "4.18.0"}},
			},
			expectErr: true,
		},
		{
			name: "When osImageStream.Name is invalid, it should return an error",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					OSImageStream: hyperv1.OSImageStreamReference{Name: "rhel-8"},
				},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "5.0.0"}},
			},
			expectErr: true,
		},
		{
			name: "When osImageStream.Name is rhel-10 and ContainerRuntimeConfig sets runc it should return an error",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-np",
					Namespace: "clusters",
				},
				Spec: hyperv1.NodePoolSpec{
					OSImageStream: hyperv1.OSImageStreamReference{Name: "rhel-10"},
					Config: []corev1.LocalObjectReference{
						{Name: "runc-config"},
					},
				},
			},
			configs: []client.Object{
				runcContainerRuntimeConfigMap("clusters", "runc-config"),
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "5.0.0"}},
			},
			expectErr: true,
		},
		{
			name: "When osImageStream.Name is rhel-9 and ContainerRuntimeConfig sets runc it should succeed",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-np",
					Namespace: "clusters",
				},
				Spec: hyperv1.NodePoolSpec{
					OSImageStream: hyperv1.OSImageStreamReference{Name: "rhel-9"},
					Config: []corev1.LocalObjectReference{
						{Name: "runc-config"},
					},
				},
			},
			configs: []client.Object{
				runcContainerRuntimeConfigMap("clusters", "runc-config"),
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "5.0.0"}},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			objs := make([]client.Object, 0, len(tc.configs))
			objs = append(objs, tc.configs...)

			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objs...).Build()

			err := validateOSImageStream(t.Context(), fakeClient, tc.nodePool, tc.releaseImage)
			if tc.expectErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}

func TestUsesRuncRuntime(t *testing.T) {
	testCases := []struct {
		name     string
		nodePool *hyperv1.NodePool
		configs  []client.Object
		expected bool
	}{
		{
			name: "When no configs it should return false",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{Name: "np", Namespace: "ns"},
			},
			expected: false,
		},
		{
			name: "When ContainerRuntimeConfig sets runc it should return true",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{Name: "np", Namespace: "ns"},
				Spec: hyperv1.NodePoolSpec{
					Config: []corev1.LocalObjectReference{{Name: "runc-cm"}},
				},
			},
			configs: []client.Object{
				runcContainerRuntimeConfigMap("ns", "runc-cm"),
			},
			expected: true,
		},
		{
			name: "When ContainerRuntimeConfig sets crun it should return false",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{Name: "np", Namespace: "ns"},
				Spec: hyperv1.NodePoolSpec{
					Config: []corev1.LocalObjectReference{{Name: "crun-cm"}},
				},
			},
			configs: []client.Object{
				crunContainerRuntimeConfigMap("ns", "crun-cm"),
			},
			expected: false,
		},
		{
			name: "When ContainerRuntimeConfig has empty defaultRuntime it should return false",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{Name: "np", Namespace: "ns"},
				Spec: hyperv1.NodePoolSpec{
					Config: []corev1.LocalObjectReference{{Name: "empty-cm"}},
				},
			},
			configs: []client.Object{
				emptyRuntimeContainerRuntimeConfigMap("ns", "empty-cm"),
			},
			expected: false,
		},
		{
			name: "When ConfigMap contains a MachineConfig instead of ContainerRuntimeConfig it should return false",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{Name: "np", Namespace: "ns"},
				Spec: hyperv1.NodePoolSpec{
					Config: []corev1.LocalObjectReference{{Name: "mc-cm"}},
				},
			},
			configs: []client.Object{
				machineConfigConfigMap("ns", "mc-cm"),
			},
			expected: false,
		},
		{
			name: "When ConfigMap does not exist it should return false",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{Name: "np", Namespace: "ns"},
				Spec: hyperv1.NodePoolSpec{
					Config: []corev1.LocalObjectReference{{Name: "missing"}},
				},
			},
			expected: false,
		},
		{
			name: "When multiple configs and one sets runc it should return true",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{Name: "np", Namespace: "ns"},
				Spec: hyperv1.NodePoolSpec{
					Config: []corev1.LocalObjectReference{
						{Name: "mc-cm"},
						{Name: "runc-cm"},
					},
				},
			},
			configs: []client.Object{
				machineConfigConfigMap("ns", "mc-cm"),
				runcContainerRuntimeConfigMap("ns", "runc-cm"),
			},
			expected: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			objs := make([]client.Object, 0, len(tc.configs))
			objs = append(objs, tc.configs...)

			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objs...).Build()

			result, err := usesRuncRuntime(t.Context(), fakeClient, tc.nodePool)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(tc.expected))
		})
	}
}

// runcContainerRuntimeConfigMap returns a ConfigMap containing a
// ContainerRuntimeConfig with defaultRuntime set to "runc".
func runcContainerRuntimeConfigMap(namespace, name string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: map[string]string{
			TokenSecretConfigKey: `apiVersion: machineconfiguration.openshift.io/v1
kind: ContainerRuntimeConfig
metadata:
  name: set-runc
spec:
  containerRuntimeConfig:
    defaultRuntime: runc
`,
		},
	}
}

// crunContainerRuntimeConfigMap returns a ConfigMap containing a
// ContainerRuntimeConfig with defaultRuntime set to "crun".
func crunContainerRuntimeConfigMap(namespace, name string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: map[string]string{
			TokenSecretConfigKey: `apiVersion: machineconfiguration.openshift.io/v1
kind: ContainerRuntimeConfig
metadata:
  name: set-crun
spec:
  containerRuntimeConfig:
    defaultRuntime: crun
`,
		},
	}
}

// emptyRuntimeContainerRuntimeConfigMap returns a ConfigMap containing a
// ContainerRuntimeConfig with no defaultRuntime set (empty).
func emptyRuntimeContainerRuntimeConfigMap(namespace, name string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: map[string]string{
			TokenSecretConfigKey: `apiVersion: machineconfiguration.openshift.io/v1
kind: ContainerRuntimeConfig
metadata:
  name: no-runtime
spec:
  containerRuntimeConfig:
    pidsLimit: 2048
`,
		},
	}
}

// machineConfigConfigMap returns a ConfigMap containing a MachineConfig
// (not a ContainerRuntimeConfig) to verify the scanner ignores non-CRC resources.
func machineConfigConfigMap(namespace, name string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: map[string]string{
			TokenSecretConfigKey: `apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  name: custom-mc
  labels:
    machineconfiguration.openshift.io/role: worker
spec:
  config:
    ignition:
      version: 3.2.0
`,
		},
	}
}
