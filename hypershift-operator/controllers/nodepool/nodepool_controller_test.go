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
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	ignserver "github.com/openshift/hypershift/ignition-server/controllers"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util/fakeimagemetadataprovider"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	k8sutilspointer "k8s.io/utils/pointer"
	capiaws "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
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
          source: data:text/plain;charset=utf-8;base64,YXBpVmVyc2lvbjogdjEKa2luZDogUG9kCm1ldGFkYXRhOgogIGNyZWF0aW9uVGltZXN0YW1wOiBudWxsCiAgbGFiZWxzOgogICAgazhzLWFwcDoga3ViZS1hcGlzZXJ2ZXItcHJveHkKICBuYW1lOiBrdWJlLWFwaXNlcnZlci1wcm94eQogIG5hbWVzcGFjZToga3ViZS1zeXN0ZW0Kc3BlYzoKICBjb250YWluZXJzOgogIC0gY29tbWFuZDoKICAgIC0gaGFwcm94eQogICAgLSAtZgogICAgLSAvdXNyL2xvY2FsL2V0Yy9oYXByb3h5CiAgICBsaXZlbmVzc1Byb2JlOgogICAgICBmYWlsdXJlVGhyZXNob2xkOiAzCiAgICAgIGh0dHBHZXQ6CiAgICAgICAgaG9zdDogMTcyLjIwLjAuMQogICAgICAgIHBhdGg6IC92ZXJzaW9uCiAgICAgICAgcG9ydDogNjQ0MwogICAgICAgIHNjaGVtZTogSFRUUFMKICAgICAgaW5pdGlhbERlbGF5U2Vjb25kczogMTIwCiAgICAgIHBlcmlvZFNlY29uZHM6IDEyMAogICAgICBzdWNjZXNzVGhyZXNob2xkOiAxCiAgICBuYW1lOiBoYXByb3h5CiAgICBwb3J0czoKICAgIC0gY29udGFpbmVyUG9ydDogNjQ0MwogICAgICBob3N0UG9ydDogNjQ0MwogICAgICBuYW1lOiBhcGlzZXJ2ZXIKICAgICAgcHJvdG9jb2w6IFRDUAogICAgcmVzb3VyY2VzOgogICAgICByZXF1ZXN0czoKICAgICAgICBjcHU6IDEzbQogICAgICAgIG1lbW9yeTogMTZNaQogICAgc2VjdXJpdHlDb250ZXh0OgogICAgICBydW5Bc1VzZXI6IDEwMDEKICAgIHZvbHVtZU1vdW50czoKICAgIC0gbW91bnRQYXRoOiAvdXNyL2xvY2FsL2V0Yy9oYXByb3h5CiAgICAgIG5hbWU6IGNvbmZpZwogIGhvc3ROZXR3b3JrOiB0cnVlCiAgcHJpb3JpdHlDbGFzc05hbWU6IHN5c3RlbS1ub2RlLWNyaXRpY2FsCiAgdm9sdW1lczoKICAtIGhvc3RQYXRoOgogICAgICBwYXRoOiAvZXRjL2t1YmVybmV0ZXMvYXBpc2VydmVyLXByb3h5LWNvbmZpZwogICAgbmFtZTogY29uZmlnCnN0YXR1czoge30K
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

func TestGetTuningConfig(t *testing.T) {
	tuned1 := `
apiVersion: tuned.openshift.io/v1
kind: Tuned
metadata:
  name: tuned-1
  namespace: openshift-cluster-node-tuning-operator
spec:
  profile:
  - data: |
      [main]
      summary=Custom OpenShift profile
      include=openshift-node

      [sysctl]
      vm.dirty_ratio="55"
    name: tuned-1-profile
  recommend:
  - match:
    - label: tuned-1-node-label
    priority: 20
    profile: tuned-1-profile
`
	tuned1Defaulted := `apiVersion: tuned.openshift.io/v1
kind: Tuned
metadata:
  creationTimestamp: null
  name: tuned-1
  namespace: openshift-cluster-node-tuning-operator
spec:
  profile:
  - data: |
      [main]
      summary=Custom OpenShift profile
      include=openshift-node

      [sysctl]
      vm.dirty_ratio="55"
    name: tuned-1-profile
  recommend:
  - match:
    - label: tuned-1-node-label
    operand:
      tunedConfig:
        reapply_sysctl: null
    priority: 20
    profile: tuned-1-profile
status: {}
`
	tuned2 := `
apiVersion: tuned.openshift.io/v1
kind: Tuned
metadata:
  name: tuned-2
  namespace: openshift-cluster-node-tuning-operator
spec:
  profile:
  - data: |
      [main]
      summary=Custom OpenShift profile
      include=openshift-node

      [sysctl]
      vm.dirty_background_ratio="25"
    name: tuned-2-profile
  recommend:
  - match:
    - label: tuned-2-node-label
    priority: 10
    profile: tuned-2-profile
`
	tuned2Defaulted := `apiVersion: tuned.openshift.io/v1
kind: Tuned
metadata:
  creationTimestamp: null
  name: tuned-2
  namespace: openshift-cluster-node-tuning-operator
spec:
  profile:
  - data: |
      [main]
      summary=Custom OpenShift profile
      include=openshift-node

      [sysctl]
      vm.dirty_background_ratio="25"
    name: tuned-2-profile
  recommend:
  - match:
    - label: tuned-2-node-label
    operand:
      tunedConfig:
        reapply_sysctl: null
    priority: 10
    profile: tuned-2-profile
status: {}
`

	namespace := "test"
	testCases := []struct {
		name         string
		nodePool     *hyperv1.NodePool
		tuningConfig []client.Object
		expect       string
		error        bool
	}{
		{
			name: "gets a single valid TuningConfig",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
				},
				Spec: hyperv1.NodePoolSpec{
					TuningConfig: []corev1.LocalObjectReference{
						{
							Name: "tuned-1",
						},
					},
				},
				Status: hyperv1.NodePoolStatus{},
			},
			tuningConfig: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "tuned-1",
						Namespace: namespace,
					},
					Data: map[string]string{
						tuningConfigKey: tuned1,
					},
					BinaryData: nil,
				},
			},
			expect: tuned1Defaulted,
			error:  false,
		},
		{
			name: "gets two valid TuningConfigs",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
				},
				Spec: hyperv1.NodePoolSpec{
					TuningConfig: []corev1.LocalObjectReference{
						{
							Name: "tuned-1",
						},
						{
							Name: "tuned-2",
						},
					},
				},
				Status: hyperv1.NodePoolStatus{},
			},
			tuningConfig: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "tuned-1",
						Namespace: namespace,
					},
					Data: map[string]string{
						tuningConfigKey: tuned1,
					},
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "tuned-2",
						Namespace: namespace,
					},
					Data: map[string]string{
						tuningConfigKey: tuned2,
					},
				},
			},
			expect: tuned1Defaulted + "\n---\n" + tuned2Defaulted,
			error:  false,
		},
		{
			name: "fails if a non existent TuningConfig is referenced",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
				},
				Spec: hyperv1.NodePoolSpec{
					TuningConfig: []corev1.LocalObjectReference{
						{
							Name: "does-not-exist",
						},
					},
				},
				Status: hyperv1.NodePoolStatus{},
			},
			tuningConfig: []client.Object{},
			expect:       "",
			error:        true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			r := NodePoolReconciler{
				Client: fake.NewClientBuilder().WithObjects(tc.tuningConfig...).Build(),
			}

			got, err := r.getTuningConfig(context.Background(), tc.nodePool)

			if tc.error {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			if diff := cmp.Diff(got, tc.expect); diff != "" {
				t.Errorf("actual config differs from expected: %s", diff)
				t.Logf("got: %s \n, expected: \n %s", got, tc.expect)
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
		{
			name: "it does not set current replicas but set annotations when autoscaling is enabled" +
				" and the MachineDeployment has nil replicas",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: hyperv1.NodePoolSpec{
					AutoScaling: &hyperv1.NodePoolAutoScaling{
						Min: 2,
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
			expectReplicas: 2,
			expectAutoscalerAnnotations: map[string]string{
				autoscalerMinAnnotation: "2",
				autoscalerMaxAnnotation: "5",
			},
		},
		{
			name: "it sets current replicas to autoScaling.min and set annotations when autoscaling is enabled" +
				" and the MachineDeployment has replicas < autoScaling.min",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: hyperv1.NodePoolSpec{
					AutoScaling: &hyperv1.NodePoolAutoScaling{
						Min: 2,
						Max: 5,
					},
				},
			},
			machineDeployment: &capiv1.MachineDeployment{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.Now(),
				},
				Spec: capiv1.MachineDeploymentSpec{
					Replicas: k8sutilspointer.Int32Ptr(1),
				},
			},
			expectReplicas: 2,
			expectAutoscalerAnnotations: map[string]string{
				autoscalerMinAnnotation: "2",
				autoscalerMaxAnnotation: "5",
			},
		},
		{
			name: "it sets current replicas to autoScaling.max and set annotations when autoscaling is enabled" +
				" and the MachineDeployment has replicas > autoScaling.max",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: hyperv1.NodePoolSpec{
					AutoScaling: &hyperv1.NodePoolAutoScaling{
						Min: 2,
						Max: 5,
					},
				},
			},
			machineDeployment: &capiv1.MachineDeployment{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.Now(),
				},
				Spec: capiv1.MachineDeploymentSpec{
					Replicas: k8sutilspointer.Int32Ptr(10),
				},
			},
			expectReplicas: 5,
			expectAutoscalerAnnotations: map[string]string{
				autoscalerMinAnnotation: "2",
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
		Status: hyperv1.HostedClusterStatus{
			Platform: &hyperv1.PlatformStatus{
				AWS: &hyperv1.AWSPlatformStatus{
					DefaultWorkerSecurityGroupID: "default-sg",
				},
			},
		},
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
					AdditionalSecurityGroups: []capiaws.AWSResourceReference{
						{
							ID: k8sutilspointer.String("default-sg"),
						},
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

	template, mutateTemplate, machineTemplateSpecJSON, err := machineTemplateBuilders(hcluster, nodePool, infraID, ami, "", "", true)
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

func TestGetNodePoolNamespacedName(t *testing.T) {
	testControlPlaneNamespace := "control-plane-ns"
	testNodePoolNamespace := "clusters"
	testNodePoolName := "nodepool-1"
	testCases := []struct {
		name                  string
		nodePoolName          string
		controlPlaneNamespace string
		hostedControlPlane    *hyperv1.HostedControlPlane
		expect                string
		error                 bool
	}{
		{
			name:                  "gets correct NodePool namespaced name",
			nodePoolName:          testNodePoolName,
			controlPlaneNamespace: testControlPlaneNamespace,
			hostedControlPlane: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: testControlPlaneNamespace,
					Annotations: map[string]string{
						hostedcluster.HostedClusterAnnotation: types.NamespacedName{Name: "hosted-cluster-1", Namespace: testNodePoolNamespace}.String(),
					},
				},
			},
			expect: types.NamespacedName{Name: testNodePoolName, Namespace: testNodePoolNamespace}.String(),
			error:  false,
		},
		{
			name:                  "fails if HostedControlPlane missing HostedClusterAnnotation",
			nodePoolName:          testNodePoolName,
			controlPlaneNamespace: testControlPlaneNamespace,
			hostedControlPlane: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: testControlPlaneNamespace,
				},
			},
			expect: "",
			error:  true,
		},
		{
			name:                  "fails if HostedControlPlane does not exist",
			nodePoolName:          testNodePoolName,
			controlPlaneNamespace: testControlPlaneNamespace,
			hostedControlPlane:    nil,
			expect:                "",
			error:                 true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			var r NodePoolReconciler
			if tc.hostedControlPlane == nil {
				r = NodePoolReconciler{
					Client: fake.NewClientBuilder().WithObjects().Build(),
				}
			} else {
				r = NodePoolReconciler{
					Client: fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(tc.hostedControlPlane).Build(),
				}
			}

			got, err := r.getNodePoolNamespacedName(testNodePoolName, testControlPlaneNamespace)

			if tc.error {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			if diff := cmp.Diff(got.String(), tc.expect); diff != "" {
				t.Errorf("actual NodePool namespaced name differs from expected: %s", diff)
				t.Logf("got: %s \n, expected: \n %s", got, tc.expect)
			}
		})
	}
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

func TestInPlaceUpgradeMaxUnavailable(t *testing.T) {
	intPointer1 := intstr.FromInt(1)
	intPointer2 := intstr.FromInt(2)
	strPointer10 := intstr.FromString("10%")
	strPointer75 := intstr.FromString("75%")
	testCases := []struct {
		name     string
		nodePool *hyperv1.NodePool
		expect   int
	}{
		{
			name: "defaults to 1 when no maxUnavailable specified",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Management: hyperv1.NodePoolManagement{
						InPlace: &hyperv1.InPlaceUpgrade{},
					},
					Replicas: k8sutilspointer.Int32Ptr(4),
				},
			},
			expect: 1,
		},
		{
			name: "can handle default value of 1",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Management: hyperv1.NodePoolManagement{
						InPlace: &hyperv1.InPlaceUpgrade{
							MaxUnavailable: &intPointer1,
						},
					},
					Replicas: k8sutilspointer.Int32Ptr(4),
				},
			},
			expect: 1,
		},
		{
			name: "can handle other values",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Management: hyperv1.NodePoolManagement{
						InPlace: &hyperv1.InPlaceUpgrade{
							MaxUnavailable: &intPointer2,
						},
					},
					Replicas: k8sutilspointer.Int32Ptr(4),
				},
			},
			expect: 2,
		},
		{
			name: "can handle percent values",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Management: hyperv1.NodePoolManagement{
						InPlace: &hyperv1.InPlaceUpgrade{
							MaxUnavailable: &strPointer75,
						},
					},
					Replicas: k8sutilspointer.Int32Ptr(4),
				},
			},
			expect: 3,
		},
		{
			name: "can handle roundable values",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Management: hyperv1.NodePoolManagement{
						InPlace: &hyperv1.InPlaceUpgrade{
							MaxUnavailable: &strPointer10,
						},
					},
					Replicas: k8sutilspointer.Int32Ptr(4),
				},
			},
			expect: 1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			maxUnavailable, err := getInPlaceMaxUnavailable(tc.nodePool)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(maxUnavailable).To(Equal(tc.expect))
		})
	}
}

func TestCreateValidGeneratedPayloadCondition(t *testing.T) {
	testCases := []struct {
		name                    string
		tokenSecret             *corev1.Secret
		tokenSecretDoesNotExist bool
		expectedCondition       *hyperv1.NodePoolCondition
	}{
		{
			name: "when token secret is not found it should report it in the condition",
			tokenSecret: &corev1.Secret{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
				},
			},
			tokenSecretDoesNotExist: true,
			expectedCondition: &hyperv1.NodePoolCondition{
				Type:               hyperv1.NodePoolValidGeneratedPayloadConditionType,
				Status:             corev1.ConditionFalse,
				Severity:           "",
				LastTransitionTime: metav1.Time{},
				Reason:             hyperv1.NodePoolNotFoundReason,
				Message:            "secrets \"test\" not found",
				ObservedGeneration: 1,
			},
		},
		{
			name: "when token secret has data it should report it in the condition",
			tokenSecret: &corev1.Secret{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
				},
				Data: map[string][]byte{
					ignserver.TokenSecretReasonKey:  []byte(hyperv1.AsExpectedReason),
					ignserver.TokenSecretMessageKey: []byte("Payload generated successfully"),
				},
			},
			expectedCondition: &hyperv1.NodePoolCondition{
				Type:               hyperv1.NodePoolValidGeneratedPayloadConditionType,
				Status:             corev1.ConditionTrue,
				Severity:           "",
				LastTransitionTime: metav1.Time{},
				Reason:             hyperv1.AsExpectedReason,
				Message:            "Payload generated successfully",
				ObservedGeneration: 1,
			},
		},
		{
			name: "when token secret has no data it should report unknown in the condition",
			tokenSecret: &corev1.Secret{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
				},
				Data: map[string][]byte{},
			},
			expectedCondition: &hyperv1.NodePoolCondition{
				Type:               hyperv1.NodePoolValidGeneratedPayloadConditionType,
				Status:             corev1.ConditionUnknown,
				Severity:           "",
				Reason:             "",
				Message:            "Unable to get status data from token secret",
				LastTransitionTime: metav1.Time{},
				ObservedGeneration: 1,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			var client client.Client
			if tc.tokenSecretDoesNotExist {
				client = fake.NewClientBuilder().WithObjects().Build()
			} else {
				client = fake.NewClientBuilder().WithObjects(tc.tokenSecret).Build()
			}

			r := NodePoolReconciler{
				Client: client,
			}

			got, err := r.createValidGeneratedPayloadCondition(context.Background(), tc.tokenSecret, 1)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(got).To(BeEquivalentTo(tc.expectedCondition))
		})
	}
}

func TestTaintsToJSON(t *testing.T) {
	testCases := []struct {
		name     string
		taints   []hyperv1.Taint
		expected string
	}{
		{
			name:     "",
			taints:   []hyperv1.Taint{},
			expected: "[]",
		},
		{
			name: "",
			taints: []hyperv1.Taint{
				{
					Key:    "foo",
					Value:  "bar",
					Effect: "any",
				},
				{
					Key:    "foo2",
					Value:  "bar2",
					Effect: "any",
				},
			},
			expected: `[{"key":"foo","value":"bar","effect":"any"},{"key":"foo2","value":"bar2","effect":"any"}]`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			taints, err := taintsToJSON(tc.taints)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(taints).To(BeEquivalentTo(tc.expected))

			// validate decoding.
			var coreTaints []corev1.Taint
			err = json.Unmarshal([]byte(taints), &coreTaints)
			g.Expect(err).ToNot(HaveOccurred())
			node := &corev1.Node{}
			node.Spec.Taints = append(node.Spec.Taints, coreTaints...)
			g.Expect(node.Spec.Taints).To(ContainElements(coreTaints))
		})
	}
}
