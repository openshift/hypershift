package nodepool

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	. "github.com/onsi/gomega"
	"github.com/openshift/api/image/docker10"
	imagev1 "github.com/openshift/api/image/v1"
	api "github.com/openshift/hypershift/api"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util/fakeimagemetadataprovider"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	k8sutilspointer "k8s.io/utils/pointer"
	capiaws "sigs.k8s.io/cluster-api-provider-aws/api/v1beta1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestIsUpdatingConfig(t *testing.T) {
	testCases := []struct {
		name     string
		nodePool *hyperv1.NodePool
		target   string
		expect   bool
	}{
		{
			name: "it is not updating when strings match",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						nodePoolAnnotationCurrentConfig: "same",
					},
				},
			},
			target: "same",
			expect: false,
		},
		{
			name: "it is updating when strings does not match",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						nodePoolAnnotationCurrentConfig: "config1",
					},
				},
			},
			target: "config2",
			expect: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(isUpdatingConfig(tc.nodePool, tc.target)).To(Equal(tc.expect))
		})
	}
}

func TestIsUpdatingVersion(t *testing.T) {
	testCases := []struct {
		name     string
		nodePool *hyperv1.NodePool
		target   string
		expect   bool
	}{
		{
			name: "it is not updating when strings match",
			nodePool: &hyperv1.NodePool{
				Status: hyperv1.NodePoolStatus{
					Version: "same",
				},
			},
			target: "same",
			expect: false,
		},
		{
			name: "it is updating when strings does not match",
			nodePool: &hyperv1.NodePool{
				Status: hyperv1.NodePoolStatus{
					Version: "v1",
				},
			},
			target: "v2",
			expect: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(isUpdatingVersion(tc.nodePool, tc.target)).To(Equal(tc.expect))
		})
	}
}

func TestIsAutoscalingEnabled(t *testing.T) {
	testCases := []struct {
		name     string
		nodePool *hyperv1.NodePool
		expect   bool
	}{
		{
			name: "it is enabled when the struct is not nil and has no values",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					AutoScaling: &hyperv1.NodePoolAutoScaling{
						Min: 0,
						Max: 0,
					},
				},
			},
			expect: true,
		},
		{
			name: "it is enabled when the struct is not nil and has values",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					AutoScaling: &hyperv1.NodePoolAutoScaling{
						Min: 1,
						Max: 2,
					},
				},
			},
			expect: true,
		},
		{
			name: "it is not enabled when the struct is nil",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{},
			},
			expect: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(isAutoscalingEnabled(tc.nodePool)).To(Equal(tc.expect))
		})
	}
}

func TestValidateAutoscaling(t *testing.T) {
	testCases := []struct {
		name     string
		nodePool *hyperv1.NodePool
		error    bool
	}{
		{
			name: "fails when both nodeCount and autoscaling are set",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Replicas: k8sutilspointer.Int32Ptr(1),
					AutoScaling: &hyperv1.NodePoolAutoScaling{
						Min: 1,
						Max: 2,
					},
				},
			},
			error: true,
		},
		{
			name: "fails when min is zero",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					AutoScaling: &hyperv1.NodePoolAutoScaling{
						Min: 2,
						Max: 0,
					},
				},
			},
			error: true,
		},
		{
			name: "fails when max is zero",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					AutoScaling: &hyperv1.NodePoolAutoScaling{
						Min: 0,
						Max: 2,
					},
				},
			},
			error: true,
		},
		{
			name: "fails when max < min",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					AutoScaling: &hyperv1.NodePoolAutoScaling{
						Min: 3,
						Max: 2,
					},
				},
			},
			error: true,
		},
		{
			name: "passes when max > min > 0",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					AutoScaling: &hyperv1.NodePoolAutoScaling{
						Min: 1,
						Max: 2,
					},
				},
			},
			error: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			err := validateAutoscaling(tc.nodePool)
			if tc.error {
				g.Expect(err).Should(HaveOccurred())
				return
			}
			g.Expect(err).ShouldNot(HaveOccurred())
		})
	}
}

func TestGetConfig(t *testing.T) {
	coreMachineConfig1 := `
apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  labels:
    machineconfiguration.openshift.io/role: master
  name: config-1
spec:
  config:
    ignition:
      version: 3.2.0
    storage:
      files:
      - contents:
        source: "[Service]\nType=oneshot\nExecStart=/usr/bin/echo Hello Core\n\n[Install]\nWantedBy=multi-user.target"
        filesystem: root
        mode: 493
        path: /usr/local/bin/core.sh
`
	coreMachineConfig1Defaulted := `apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  creationTimestamp: null
  labels:
    machineconfiguration.openshift.io/role: worker
  name: config-1
spec:
  config:
    ignition:
      version: 3.2.0
    storage:
      files:
      - contents: null
        filesystem: root
        mode: 493
        path: /usr/local/bin/core.sh
        source: |-
          [Service]
          Type=oneshot
          ExecStart=/usr/bin/echo Hello Core

          [Install]
          WantedBy=multi-user.target
  extensions: null
  fips: false
  kernelArguments: null
  kernelType: ""
  osImageURL: ""
`

	machineConfig1 := `
apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  labels:
    machineconfiguration.openshift.io/role: master
  name: config-1
spec:
  config:
    ignition:
      version: 3.2.0
    storage:
      files:
      - contents:
        source: "[Service]\nType=oneshot\nExecStart=/usr/bin/echo Hello World\n\n[Install]\nWantedBy=multi-user.target"
        filesystem: root
        mode: 493
        path: /usr/local/bin/file1.sh
`
	machineConfig1Defaulted := `apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  creationTimestamp: null
  labels:
    machineconfiguration.openshift.io/role: worker
  name: config-1
spec:
  config:
    ignition:
      version: 3.2.0
    storage:
      files:
      - contents: null
        filesystem: root
        mode: 493
        path: /usr/local/bin/file1.sh
        source: |-
          [Service]
          Type=oneshot
          ExecStart=/usr/bin/echo Hello World

          [Install]
          WantedBy=multi-user.target
  extensions: null
  fips: false
  kernelArguments: null
  kernelType: ""
  osImageURL: ""
`
	machineConfig2 := `
apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  labels:
    machineconfiguration.openshift.io/role: master
  name: config-2
spec:
  config:
    ignition:
      version: 3.2.0
    storage:
      files:
      - contents:
        source: "[Service]\nType=oneshot\nExecStart=/usr/bin/echo Hello World 2\n\n[Install]\nWantedBy=multi-user.target"
        filesystem: root
        mode: 493
        path: /usr/local/bin/file2.sh
`
	machineConfig2Defaulted := `apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  creationTimestamp: null
  labels:
    machineconfiguration.openshift.io/role: worker
  name: config-2
spec:
  config:
    ignition:
      version: 3.2.0
    storage:
      files:
      - contents: null
        filesystem: root
        mode: 493
        path: /usr/local/bin/file2.sh
        source: |-
          [Service]
          Type=oneshot
          ExecStart=/usr/bin/echo Hello World 2

          [Install]
          WantedBy=multi-user.target
  extensions: null
  fips: false
  kernelArguments: null
  kernelType: ""
  osImageURL: ""
`
	kubeletConfig1 := `
apiVersion: machineconfiguration.openshift.io/v1
kind: KubeletConfig
metadata:
  name: set-max-pods
spec:
  machineConfigPoolSelector:
    matchLabels:
      pools.operator.machineconfiguration.openshift.io/worker: ""
  kubeletConfig:
    maxPods: 100
`
	haproxyIgnititionConfig := `apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  creationTimestamp: null
  labels:
    machineconfiguration.openshift.io/role: worker
  name: 20-apiserver-haproxy
spec:
  config:
    ignition:
      version: 3.2.0
    storage:
      files:
      - contents:
          source: data:text/plain;charset=utf-8;base64,IyEvdXNyL2Jpbi9lbnYgYmFzaApzZXQgLXgKaXAgYWRkciBhZGQgMTcyLjIwLjAuMS8zMiBicmQgMTcyLjIwLjAuMSBzY29wZSBob3N0IGRldiBsbwppcCByb3V0ZSBhZGQgMTcyLjIwLjAuMS8zMiBkZXYgbG8gc2NvcGUgbGluayBzcmMgMTcyLjIwLjAuMQo=
        mode: 493
        overwrite: true
        path: /usr/local/bin/setup-apiserver-ip.sh
      - contents:
          source: data:text/plain;charset=utf-8;base64,IyEvdXNyL2Jpbi9lbnYgYmFzaApzZXQgLXgKaXAgYWRkciBkZWxldGUgMTcyLjIwLjAuMS8zMiBkZXYgbG8KaXAgcm91dGUgZGVsIDE3Mi4yMC4wLjEvMzIgZGV2IGxvIHNjb3BlIGxpbmsgc3JjIDE3Mi4yMC4wLjEK
        mode: 493
        overwrite: true
        path: /usr/local/bin/teardown-apiserver-ip.sh
      - contents:
          source: data:text/plain;charset=utf-8;base64,Z2xvYmFsCiAgbWF4Y29ubiA3MDAwCiAgbG9nIHN0ZG91dCBsb2NhbDAKICBsb2cgc3Rkb3V0IGxvY2FsMSBub3RpY2UKCmRlZmF1bHRzCiAgbW9kZSB0Y3AKICB0aW1lb3V0IGNsaWVudCAxMG0KICB0aW1lb3V0IHNlcnZlciAxMG0KICB0aW1lb3V0IGNvbm5lY3QgMTBzCiAgdGltZW91dCBjbGllbnQtZmluIDVzCiAgdGltZW91dCBzZXJ2ZXItZmluIDVzCiAgdGltZW91dCBxdWV1ZSA1cwogIHJldHJpZXMgMwoKZnJvbnRlbmQgbG9jYWxfYXBpc2VydmVyCiAgYmluZCAxNzIuMjAuMC4xOjY0NDMKICBsb2cgZ2xvYmFsCiAgbW9kZSB0Y3AKICBvcHRpb24gdGNwbG9nCiAgZGVmYXVsdF9iYWNrZW5kIHJlbW90ZV9hcGlzZXJ2ZXIKCmJhY2tlbmQgcmVtb3RlX2FwaXNlcnZlcgogIG1vZGUgdGNwCiAgbG9nIGdsb2JhbAogIG9wdGlvbiBodHRwY2hrIEdFVCAvdmVyc2lvbgogIG9wdGlvbiBsb2ctaGVhbHRoLWNoZWNrcwogIGRlZmF1bHQtc2VydmVyIGludGVyIDEwcyBmYWxsIDMgcmlzZSAzCiAgc2VydmVyIGNvbnRyb2xwbGFuZSBsb2NhbGhvc3Q6NjQ0Mwo=
        mode: 420
        overwrite: true
        path: /etc/kubernetes/apiserver-proxy-config/haproxy.cfg
      - contents:
          source: data:text/plain;charset=utf-8;base64,YXBpVmVyc2lvbjogdjEKa2luZDogUG9kCm1ldGFkYXRhOgogIGNyZWF0aW9uVGltZXN0YW1wOiBudWxsCiAgbGFiZWxzOgogICAgaHlwZXJzaGlmdC5vcGVuc2hpZnQuaW8vY29udHJvbC1wbGFuZS1jb21wb25lbnQ6IGt1YmUtYXBpc2VydmVyLXByb3h5CiAgICBrOHMtYXBwOiBrdWJlLWFwaXNlcnZlci1wcm94eQogIG5hbWU6IGt1YmUtYXBpc2VydmVyLXByb3h5CiAgbmFtZXNwYWNlOiBrdWJlLXN5c3RlbQpzcGVjOgogIGNvbnRhaW5lcnM6CiAgLSBjb21tYW5kOgogICAgLSBoYXByb3h5CiAgICAtIC1mCiAgICAtIC91c3IvbG9jYWwvZXRjL2hhcHJveHkKICAgIGxpdmVuZXNzUHJvYmU6CiAgICAgIGZhaWx1cmVUaHJlc2hvbGQ6IDMKICAgICAgaHR0cEdldDoKICAgICAgICBob3N0OiAxNzIuMjAuMC4xCiAgICAgICAgcGF0aDogL3ZlcnNpb24KICAgICAgICBwb3J0OiA2NDQzCiAgICAgICAgc2NoZW1lOiBIVFRQUwogICAgICBpbml0aWFsRGVsYXlTZWNvbmRzOiAxMjAKICAgICAgcGVyaW9kU2Vjb25kczogMTIwCiAgICAgIHN1Y2Nlc3NUaHJlc2hvbGQ6IDEKICAgIG5hbWU6IGhhcHJveHkKICAgIHBvcnRzOgogICAgLSBjb250YWluZXJQb3J0OiA2NDQzCiAgICAgIGhvc3RQb3J0OiA2NDQzCiAgICAgIG5hbWU6IGFwaXNlcnZlcgogICAgICBwcm90b2NvbDogVENQCiAgICByZXNvdXJjZXM6CiAgICAgIHJlcXVlc3RzOgogICAgICAgIGNwdTogMTNtCiAgICAgICAgbWVtb3J5OiAxNk1pCiAgICBzZWN1cml0eUNvbnRleHQ6CiAgICAgIHJ1bkFzVXNlcjogMTAwMQogICAgdm9sdW1lTW91bnRzOgogICAgLSBtb3VudFBhdGg6IC91c3IvbG9jYWwvZXRjL2hhcHJveHkKICAgICAgbmFtZTogY29uZmlnCiAgaG9zdE5ldHdvcms6IHRydWUKICBwcmlvcml0eUNsYXNzTmFtZTogc3lzdGVtLW5vZGUtY3JpdGljYWwKICB2b2x1bWVzOgogIC0gaG9zdFBhdGg6CiAgICAgIHBhdGg6IC9ldGMva3ViZXJuZXRlcy9hcGlzZXJ2ZXItcHJveHktY29uZmlnCiAgICBuYW1lOiBjb25maWcKc3RhdHVzOiB7fQo=
        mode: 420
        overwrite: true
        path: /etc/kubernetes/manifests/kube-apiserver-proxy.yaml
    systemd:
      units:
      - contents: |
          [Unit]
          Description=Sets up local IP to proxy API server requests
          Wants=network-online.target
          After=network-online.target

          [Service]
          Type=oneshot
          ExecStart=/usr/local/bin/setup-apiserver-ip.sh
          ExecStop=/usr/local/bin/teardown-apiserver-ip.sh
          RemainAfterExit=yes

          [Install]
          WantedBy=multi-user.target
        enabled: true
        name: apiserver-ip.service
  extensions: null
  fips: false
  kernelArguments: null
  kernelType: ""
  osImageURL: ""
`

	namespace := "test"
	testCases := []struct {
		name                        string
		nodePool                    *hyperv1.NodePool
		config                      []client.Object
		expectedCoreConfigResources int
		cpoImageMetadata            *dockerv1client.DockerImageConfig
		expect                      string
		missingConfigs              bool
		error                       bool
	}{
		{
			name: "gets a single valid MachineConfig",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
				},
				Spec: hyperv1.NodePoolSpec{
					Config: []corev1.LocalObjectReference{
						{
							Name: "machineconfig-1",
						},
					},
				},
				Status: hyperv1.NodePoolStatus{},
			},
			config: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "machineconfig-1",
						Namespace: namespace,
					},
					Data: map[string]string{
						TokenSecretConfigKey: machineConfig1,
					},
					BinaryData: nil,
				},
			},
			expect: machineConfig1Defaulted,
			error:  false,
		},
		{
			name: "gets two valid MachineConfig",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
				},
				Spec: hyperv1.NodePoolSpec{
					Config: []corev1.LocalObjectReference{
						{
							Name: "machineconfig-1",
						},
						{
							Name: "machineconfig-2",
						},
					},
				},
				Status: hyperv1.NodePoolStatus{},
			},
			config: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "machineconfig-1",
						Namespace: namespace,
					},
					Data: map[string]string{
						TokenSecretConfigKey: machineConfig1,
					},
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "machineconfig-2",
						Namespace: namespace,
					},
					Data: map[string]string{
						TokenSecretConfigKey: machineConfig2,
					},
				},
			},
			expect: machineConfig1Defaulted + "\n---\n" + machineConfig2Defaulted,
			error:  false,
		},
		{
			name: "fails if a non existent config is referenced",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
				},
				Spec: hyperv1.NodePoolSpec{
					Config: []corev1.LocalObjectReference{
						{
							Name: "does-not-exist",
						},
					},
				},
				Status: hyperv1.NodePoolStatus{},
			},
			config: []client.Object{},
			expect: "",
			error:  true,
		},
		{
			name: "fails if a non supported config kind",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
				},
				Spec: hyperv1.NodePoolSpec{
					Config: []corev1.LocalObjectReference{
						{
							Name: "kubeletconfig-1",
						},
					},
				},
			},
			config: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kubeletconfig-1",
						Namespace: namespace,
					},
					Data: map[string]string{
						TokenSecretConfigKey: kubeletConfig1,
					},
				},
			},
			expect: kubeletConfig1,
			error:  false,
		},
		{
			name: "gets a single valid MachineConfig with a core MachineConfig",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
				},
				Spec: hyperv1.NodePoolSpec{
					Config: []corev1.LocalObjectReference{
						{
							Name: "machineconfig-1",
						},
					},
				},
				Status: hyperv1.NodePoolStatus{},
			},
			config: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "machineconfig-1",
						Namespace: namespace,
					},
					Data: map[string]string{
						TokenSecretConfigKey: machineConfig1,
					},
					BinaryData: nil,
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "core-machineconfig",
						Namespace: namespace,
						Labels: map[string]string{
							nodePoolCoreIgnitionConfigLabel: "true",
						},
					},
					Data: map[string]string{
						TokenSecretConfigKey: coreMachineConfig1,
					},
				},
			},
			expectedCoreConfigResources: 1,
			expect:                      coreMachineConfig1Defaulted + "\n---\n" + machineConfig1Defaulted,
			error:                       false,
		},
		{
			name: "gets a single valid MachineConfig with a core MachineConfig and ignores independent namespace",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
				},
				Spec: hyperv1.NodePoolSpec{
					Config: []corev1.LocalObjectReference{
						{
							Name: "machineconfig-1",
						},
					},
				},
				Status: hyperv1.NodePoolStatus{},
			},
			config: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "machineconfig-1",
						Namespace: namespace,
					},
					Data: map[string]string{
						TokenSecretConfigKey: machineConfig1,
					},
					BinaryData: nil,
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "core-machineconfig",
						Namespace: namespace,
						Labels: map[string]string{
							nodePoolCoreIgnitionConfigLabel: "true",
						},
					},
					Data: map[string]string{
						TokenSecretConfigKey: coreMachineConfig1,
					},
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "core-machineconfig",
						Namespace: "separatenamespace",
						Labels: map[string]string{
							nodePoolCoreIgnitionConfigLabel: "true",
						},
					},
					Data: map[string]string{
						TokenSecretConfigKey: coreMachineConfig1,
					},
				},
			},
			expectedCoreConfigResources: 1,
			expect:                      coreMachineConfig1Defaulted + "\n---\n" + machineConfig1Defaulted,
			error:                       false,
		},
		{
			name: "No configs, missingConfigs is returned",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
				},
			},
			expectedCoreConfigResources: 1,
			missingConfigs:              true,
			error:                       false,
		},
		{
			name: "Nodepool controller generates HAProxy config",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
				},
			},
			config: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "machineconfig-1",
						Namespace: namespace,
						Labels: map[string]string{
							nodePoolCoreIgnitionConfigLabel: "true",
						},
					},
					Data: map[string]string{
						TokenSecretConfigKey: machineConfig1,
					},
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ignition-config-apiserver-haproxy",
						Namespace: namespace,
						Labels: map[string]string{
							nodePoolCoreIgnitionConfigLabel: "true",
						},
					},
					Data: map[string]string{
						TokenSecretConfigKey: machineConfig2Defaulted,
					},
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "core-machineconfig",
						Namespace: namespace,
						Labels: map[string]string{
							nodePoolCoreIgnitionConfigLabel: "true",
						},
					},
					Data: map[string]string{
						TokenSecretConfigKey: coreMachineConfig1,
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "kubeconfig"},
					Data: map[string][]byte{"kubeconfig": []byte(`apiVersion: v1
clusters:
- cluster:
    server: http://localhost:6443
  name: static-kas
contexts:
- context:
    cluster: static-kas
    user: ""
    namespace: default
  name: static-kas
current-context: static-kas
kind: Config`)},
				},
			},
			cpoImageMetadata: &dockerv1client.DockerImageConfig{Config: &docker10.DockerConfig{
				Labels: map[string]string{"io.openshift.hypershift.control-plane-operator-skips-haproxy": "true"},
			}},
			expectedCoreConfigResources: 3,
			expect:                      haproxyIgnititionConfig + "\n---\n" + coreMachineConfig1Defaulted + "\n---\n" + machineConfig1Defaulted,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			tc.config = append(tc.config, &corev1.Secret{
				Data: map[string][]byte{".dockerconfigjson": nil},
			})
			if tc.cpoImageMetadata == nil {
				tc.cpoImageMetadata = &dockerv1client.DockerImageConfig{}
			}

			r := NodePoolReconciler{
				Client:                fake.NewClientBuilder().WithObjects(tc.config...).Build(),
				ImageMetadataProvider: &fakeimagemetadataprovider.FakeImageMetadataProvider{Result: tc.cpoImageMetadata},
			}
			hc := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{"hypershift.openshift.io/control-plane-operator-image": "cpo-image"}},
				Status:     hyperv1.HostedClusterStatus{KubeConfig: &corev1.LocalObjectReference{Name: "kubeconfig"}},
			}
			releaseImage := &releaseinfo.ReleaseImage{ImageStream: &imagev1.ImageStream{Spec: imagev1.ImageStreamSpec{Tags: []imagev1.TagReference{{
				Name: "haproxy-router",
				From: &corev1.ObjectReference{},
			}}}}}
			got, missingConfigs, err := r.getConfig(context.Background(),
				tc.nodePool,
				tc.expectedCoreConfigResources,
				namespace,
				releaseImage,
				hc,
			)
			if tc.error {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(missingConfigs).To(Equal(tc.missingConfigs))
			g.Expect(err).ToNot(HaveOccurred())
			if diff := cmp.Diff(got, tc.expect); diff != "" {
				t.Errorf("actual config differs from expected: %s", diff)
			}
		})
	}
}

func TestSetMachineDeploymentReplicas(t *testing.T) {
	testCases := []struct {
		name                        string
		nodePool                    *hyperv1.NodePool
		machineDeployment           *capiv1.MachineDeployment
		expectReplicas              int32
		expectAutoscalerAnnotations map[string]string
	}{
		{
			name: "it sets replicas when autoscaling is disabled",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: hyperv1.NodePoolSpec{
					Replicas: k8sutilspointer.Int32Ptr(5),
				},
			},
			machineDeployment: &capiv1.MachineDeployment{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.Now(),
				},
			},
			expectReplicas: 5,
			expectAutoscalerAnnotations: map[string]string{
				autoscalerMinAnnotation: "0",
				autoscalerMaxAnnotation: "0",
			},
		},
		{
			name: "it keeps current replicas and set annotations when autoscaling is enabled",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: hyperv1.NodePoolSpec{
					AutoScaling: &hyperv1.NodePoolAutoScaling{
						Min: 1,
						Max: 5,
					},
				},
			},
			machineDeployment: &capiv1.MachineDeployment{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.Now(),
				},
				Spec: capiv1.MachineDeploymentSpec{
					Replicas: k8sutilspointer.Int32Ptr(3),
				},
			},
			expectReplicas: 3,
			expectAutoscalerAnnotations: map[string]string{
				autoscalerMinAnnotation: "1",
				autoscalerMaxAnnotation: "5",
			},
		},
		{
			name: "it sets current replicas to 1 and set annotations when autoscaling is enabled" +
				" and the MachineDeployment has not been created yet",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: hyperv1.NodePoolSpec{
					AutoScaling: &hyperv1.NodePoolAutoScaling{
						Min: 1,
						Max: 5,
					},
				},
			},
			machineDeployment: &capiv1.MachineDeployment{},
			expectReplicas:    1,
			expectAutoscalerAnnotations: map[string]string{
				autoscalerMinAnnotation: "1",
				autoscalerMaxAnnotation: "5",
			},
		},
		{
			name: "it sets current replicas to 1 and set annotations when autoscaling is enabled" +
				" and the MachineDeployment has 0 replicas",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: hyperv1.NodePoolSpec{
					AutoScaling: &hyperv1.NodePoolAutoScaling{
						Min: 1,
						Max: 5,
					},
				},
			},
			machineDeployment: &capiv1.MachineDeployment{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.Now(),
				},
				Spec: capiv1.MachineDeploymentSpec{
					Replicas: k8sutilspointer.Int32Ptr(0),
				},
			},
			expectReplicas: 1,
			expectAutoscalerAnnotations: map[string]string{
				autoscalerMinAnnotation: "1",
				autoscalerMaxAnnotation: "5",
			},
		},
		{
			name: "it sets current replicas to 1 and set annotations when autoscaling is enabled" +
				" and the MachineDeployment has nil replicas",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: hyperv1.NodePoolSpec{
					AutoScaling: &hyperv1.NodePoolAutoScaling{
						Min: 1,
						Max: 5,
					},
				},
			},
			machineDeployment: &capiv1.MachineDeployment{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.Now(),
				},
				Spec: capiv1.MachineDeploymentSpec{
					Replicas: nil,
				},
			},
			expectReplicas: 1,
			expectAutoscalerAnnotations: map[string]string{
				autoscalerMinAnnotation: "1",
				autoscalerMaxAnnotation: "5",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			setMachineDeploymentReplicas(tc.nodePool, tc.machineDeployment)
			g.Expect(*tc.machineDeployment.Spec.Replicas).To(Equal(tc.expectReplicas))
			g.Expect(tc.machineDeployment.Annotations).To(Equal(tc.expectAutoscalerAnnotations))
		})
	}
}

func TestValidateManagement(t *testing.T) {
	intstrPointer1 := intstr.FromInt(1)
	testCases := []struct {
		name     string
		nodePool *hyperv1.NodePool
		error    bool
	}{
		{
			name: "it fails with bad upgradeType",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: hyperv1.NodePoolSpec{
					Management: hyperv1.NodePoolManagement{
						UpgradeType: "bad",
						Replace: &hyperv1.ReplaceUpgrade{
							Strategy:      hyperv1.UpgradeStrategyRollingUpdate,
							RollingUpdate: nil,
						},
					},
				},
			},
			error: true,
		},
		{
			name: "it fails with Replace type and no Replace settings",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: hyperv1.NodePoolSpec{
					Management: hyperv1.NodePoolManagement{
						UpgradeType: hyperv1.UpgradeTypeReplace,
					},
				},
			},
			error: true,
		},
		{
			name: "it fails with Replace type and bad strategy",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: hyperv1.NodePoolSpec{
					Management: hyperv1.NodePoolManagement{
						UpgradeType: hyperv1.UpgradeTypeReplace,
						Replace: &hyperv1.ReplaceUpgrade{
							Strategy: "bad",
							RollingUpdate: &hyperv1.RollingUpdate{
								MaxUnavailable: &intstrPointer1,
								MaxSurge:       &intstrPointer1,
							},
						},
					},
				},
			},
			error: true,
		},
		{
			name: "it fails with Replace type, RollingUpdate strategy and no rollingUpdate settings",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: hyperv1.NodePoolSpec{
					Management: hyperv1.NodePoolManagement{
						UpgradeType: hyperv1.UpgradeTypeReplace,
						Replace: &hyperv1.ReplaceUpgrade{
							Strategy:      hyperv1.UpgradeStrategyRollingUpdate,
							RollingUpdate: nil,
						},
					},
				},
			},
			error: true,
		},
		{
			name: "it passes with Replace type, RollingUpdate strategy and RollingUpdate settings",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: hyperv1.NodePoolSpec{
					Management: hyperv1.NodePoolManagement{
						UpgradeType: hyperv1.UpgradeTypeReplace,
						Replace: &hyperv1.ReplaceUpgrade{
							Strategy: hyperv1.UpgradeStrategyRollingUpdate,
							RollingUpdate: &hyperv1.RollingUpdate{
								MaxUnavailable: &intstrPointer1,
								MaxSurge:       &intstrPointer1,
							},
						},
					},
				},
			},
			error: false,
		},
		{
			name: "it passes with Replace type and OnDelete strategy",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: hyperv1.NodePoolSpec{
					Management: hyperv1.NodePoolManagement{
						UpgradeType: hyperv1.UpgradeTypeReplace,
						Replace: &hyperv1.ReplaceUpgrade{
							Strategy: hyperv1.UpgradeStrategyOnDelete,
						},
					},
				},
			},
			error: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			err := validateManagement(tc.nodePool)
			if tc.error {
				g.Expect(err).Should(HaveOccurred())
				return
			}
			g.Expect(err).ShouldNot(HaveOccurred())
		})
	}
}

// It returns a expected machineTemplateSpecJSON
// and a template and mutateTemplate able to produce an expected target template.
func RunTestMachineTemplateBuilders(t *testing.T, preCreateMachineTemplate bool) {
	g := NewWithT(t)
	c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects().Build()
	r := &NodePoolReconciler{
		Client:                 c,
		CreateOrUpdateProvider: upsert.New(false),
	}

	infraID := "test"
	ami := "test"
	hcluster := &hyperv1.HostedCluster{
		TypeMeta:   metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{},
		Spec: hyperv1.HostedClusterSpec{
			Release: hyperv1.Release{},
			InfraID: infraID,
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AWSPlatform,
				AWS: &hyperv1.AWSPlatformSpec{
					ResourceTags: nil,
				},
			},
		},
		Status: hyperv1.HostedClusterStatus{},
	}
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "test",
		},
		Spec: hyperv1.NodePoolSpec{
			Platform: hyperv1.NodePoolPlatform{

				Type: hyperv1.AWSPlatform,
				AWS: &hyperv1.AWSNodePoolPlatform{
					InstanceType:    "",
					InstanceProfile: "",
					Subnet:          nil,
					AMI:             ami,
					RootVolume: &hyperv1.Volume{
						Size: 16,
						Type: "io1",
						IOPS: 5000,
					},
					ResourceTags: nil,
				},
			},
		},
	}

	if preCreateMachineTemplate {
		preCreatedMachineTemplate := &capiaws.AWSMachineTemplate{
			ObjectMeta: metav1.ObjectMeta{
				Name:      nodePool.GetName(),
				Namespace: manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name).Name,
			},
			Spec: capiaws.AWSMachineTemplateSpec{
				Template: capiaws.AWSMachineTemplateResource{
					Spec: capiaws.AWSMachineSpec{
						AMI: capiaws.AMIReference{
							ID: k8sutilspointer.StringPtr(ami),
						},
						IAMInstanceProfile:   "test-worker-profile",
						Subnet:               &capiaws.AWSResourceReference{},
						UncompressedUserData: k8sutilspointer.BoolPtr(true),
					},
				},
			},
		}
		err := r.Create(context.Background(), preCreatedMachineTemplate)
		g.Expect(err).ToNot(HaveOccurred())
	}

	expectedMachineTemplate := &capiaws.AWSMachineTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:        nodePool.GetName(),
			Namespace:   manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name).Name,
			Annotations: map[string]string{nodePoolAnnotation: client.ObjectKeyFromObject(nodePool).String()},
		},
		Spec: capiaws.AWSMachineTemplateSpec{
			Template: capiaws.AWSMachineTemplateResource{
				Spec: capiaws.AWSMachineSpec{
					AMI: capiaws.AMIReference{
						ID: k8sutilspointer.StringPtr(ami),
					},
					IAMInstanceProfile:   "test-worker-profile",
					Subnet:               &capiaws.AWSResourceReference{},
					UncompressedUserData: k8sutilspointer.BoolPtr(true),
					CloudInit: capiaws.CloudInit{
						InsecureSkipSecretsManager: true,
						SecureSecretsBackend:       "secrets-manager",
					},
					AdditionalTags: capiaws.Tags{
						awsClusterCloudProviderTagKey(infraID): infraLifecycleOwned,
					},
					RootVolume: &capiaws.Volume{
						Size: 16,
						Type: "io1",
						IOPS: 5000,
					},
				},
			},
		},
	}
	expectedMachineTemplateSpecJSON, err := json.Marshal(expectedMachineTemplate.Spec)
	g.Expect(err).ToNot(HaveOccurred())

	template, mutateTemplate, machineTemplateSpecJSON, err := machineTemplateBuilders(hcluster, nodePool, infraID, ami, "", "")
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(machineTemplateSpecJSON).To(BeIdenticalTo(string(expectedMachineTemplateSpecJSON)))

	// Validate that template and mutateTemplate are able to produce an expected target template.
	_, err = r.CreateOrUpdate(context.Background(), r.Client, template, func() error {
		return mutateTemplate(template)
	})
	g.Expect(err).ToNot(HaveOccurred())

	gotMachineTemplate := &capiaws.AWSMachineTemplate{}
	r.Client.Get(context.Background(), client.ObjectKeyFromObject(expectedMachineTemplate), gotMachineTemplate)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(expectedMachineTemplate.Spec).To(BeEquivalentTo(gotMachineTemplate.Spec))
	g.Expect(expectedMachineTemplate.ObjectMeta.Annotations).To(BeEquivalentTo(gotMachineTemplate.ObjectMeta.Annotations))
}

func TestMachineTemplateBuilders(t *testing.T) {
	RunTestMachineTemplateBuilders(t, false)
}

func TestMachineTemplateBuildersPreexisting(t *testing.T) {
	RunTestMachineTemplateBuilders(t, true)
}

func TestListMachineTemplatesAWS(t *testing.T) {
	g := NewWithT(t)
	c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects().Build()
	r := &NodePoolReconciler{
		Client:                 c,
		CreateOrUpdateProvider: upsert.New(false),
	}
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "test",
		},
		Spec: hyperv1.NodePoolSpec{
			Platform: hyperv1.NodePoolPlatform{
				Type: hyperv1.AWSPlatform,
			},
		},
	}
	g.Expect(r.Client.Create(context.Background(), nodePool)).To(BeNil())

	// MachineTemplate with the expected annotation
	template1 := &capiaws.AWSMachineTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "template1",
			Namespace:   "test",
			Annotations: map[string]string{nodePoolAnnotation: client.ObjectKeyFromObject(nodePool).String()},
		},
		Spec: capiaws.AWSMachineTemplateSpec{},
	}
	g.Expect(r.Client.Create(context.Background(), template1)).To(BeNil())

	// MachineTemplate without the expected annoation
	template2 := &capiaws.AWSMachineTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "template2",
			Namespace: "test",
		},
		Spec: capiaws.AWSMachineTemplateSpec{},
	}
	g.Expect(r.Client.Create(context.Background(), template2)).To(BeNil())

	templates, err := r.listMachineTemplates(nodePool)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(len(templates)).To(Equal(1))
	g.Expect(templates[0].GetName()).To(Equal("template1"))
}

func TestListMachineTemplatesIBMCloud(t *testing.T) {
	g := NewWithT(t)
	c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects().Build()
	r := &NodePoolReconciler{
		Client:                 c,
		CreateOrUpdateProvider: upsert.New(false),
	}
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "test",
		},
		Spec: hyperv1.NodePoolSpec{
			Platform: hyperv1.NodePoolPlatform{
				Type: hyperv1.IBMCloudPlatform,
			},
		},
	}
	g.Expect(r.Client.Create(context.Background(), nodePool)).To(BeNil())

	templates, err := r.listMachineTemplates(nodePool)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(len(templates)).To(Equal(0))
}

func TestValidateInfraID(t *testing.T) {
	g := NewWithT(t)
	err := validateInfraID("")
	g.Expect(err).To(HaveOccurred())

	err = validateInfraID("123")
	g.Expect(err).ToNot(HaveOccurred())
}

func TestGetName(t *testing.T) {
	g := NewWithT(t)

	alphaNumeric := regexp.MustCompile(`^[a-z0-9]*$`)
	base := "infraID-clusterName" // length 19
	suffix := "nodePoolName"      // length 12
	length := len(base) + len(suffix)

	// When maxLength == base+suffix
	name := getName(base, suffix, length)
	g.Expect(alphaNumeric.MatchString(string(name[0]))).To(BeTrue())

	// When maxLength < base+suffix
	name = getName(base, suffix, length-1)
	g.Expect(alphaNumeric.MatchString(string(name[0]))).To(BeTrue())

	// When maxLength > base+suffix
	name = getName(base, suffix, length+1)
	g.Expect(alphaNumeric.MatchString(string(name[0]))).To(BeTrue())
}

func TestSetExpirationTimestampOnToken(t *testing.T) {
	fakeName := "test-token"
	fakeNamespace := "master-cluster1"
	fakeCurrentTokenVal := "tokenval1"

	testCases := []struct {
		name        string
		inputSecret *corev1.Secret
	}{
		{
			name: "when set expiration timestamp on token is called on a secret then the expiration timestamp is set",
			inputSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fakeName,
					Namespace: fakeNamespace,
				},
				Data: map[string][]byte{
					TokenSecretTokenKey: []byte(fakeCurrentTokenVal),
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			c := fake.NewClientBuilder().WithObjects(tc.inputSecret).Build()
			err := setExpirationTimestampOnToken(context.Background(), c, tc.inputSecret)
			g.Expect(err).To(Not(HaveOccurred()))
			actualSecretData := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fakeName,
					Namespace: fakeNamespace,
				},
			}
			err = c.Get(context.Background(), client.ObjectKeyFromObject(actualSecretData), actualSecretData)
			g.Expect(err).To(Not(HaveOccurred()))
			rawExpirationTimestamp, ok := actualSecretData.Annotations[hyperv1.IgnitionServerTokenExpirationTimestampAnnotation]
			g.Expect(ok).To(BeTrue())
			expirationTimestamp, err := time.Parse(time.RFC3339, rawExpirationTimestamp)
			g.Expect(err).To(Not(HaveOccurred()))
			// ensures the 2 hour expiration is active. 119 minutes is one minute less than 2 hours. gives time for
			// test to run.
			g.Expect(time.Now().Add(119 * time.Minute).Before(expirationTimestamp)).To(BeTrue())
		})
	}
}

func TestNodepoolDeletionDoesntRequireHCluster(t *testing.T) {
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "some-nodepool",
			Namespace:  "clusters",
			Finalizers: []string{finalizer},
		},
	}

	ctx := context.Background()
	c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(nodePool).Build()
	if err := c.Delete(ctx, nodePool); err != nil {
		t.Fatalf("failed to delete nodepool: %v", err)
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(nodePool), nodePool); err != nil {
		t.Errorf("expected to be able to get nodepool after deletion because of finalizer, but got err: %v", err)
	}

	if _, err := (&NodePoolReconciler{Client: c}).Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(nodePool)}); err != nil {
		t.Fatalf("reconciliation failed: %v", err)
	}

	if err := c.Get(ctx, client.ObjectKeyFromObject(nodePool), nodePool); !apierrors.IsNotFound(err) {
		t.Errorf("expected to get NotFound after deleted nodePool was reconciled, got %v", err)
	}
}
