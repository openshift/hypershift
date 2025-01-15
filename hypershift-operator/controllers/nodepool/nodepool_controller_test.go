package nodepool

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	supportutil "github.com/openshift/hypershift/support/util"
	"k8s.io/apimachinery/pkg/util/validation"
	k8sutilspointer "k8s.io/utils/pointer"

	"github.com/blang/semver"
	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/image/docker10"
	imagev1 "github.com/openshift/api/image/v1"
	performanceprofilev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	crconditionsv1 "github.com/openshift/custom-resource-status/conditions/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/ipnet"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	ignserver "github.com/openshift/hypershift/ignition-server/controllers"
	kvinfra "github.com/openshift/hypershift/kubevirtexternalinfra"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/releaseinfo"
	fakereleaseprovider "github.com/openshift/hypershift/support/releaseinfo/fake"
	"github.com/openshift/hypershift/support/supportedversion"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util/fakeimagemetadataprovider"
	"go.uber.org/zap/zaptest"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	testingclock "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"
	capiaws "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/yaml"
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
  baseOSExtensionsContainerImage: ""
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
  baseOSExtensionsContainerImage: ""
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
	machineConfig23 := `
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
--- # empty yamls should be ignored

---
apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  labels:
    machineconfiguration.openshift.io/role: master
  name: config-3
spec:
  config:
    ignition:
      version: 3.2.0
    storage:
      files:
      - contents:
        source: "[Service]\nType=oneshot\nExecStart=/usr/bin/echo Hello World 3\n\n[Install]\nWantedBy=multi-user.target"
        filesystem: root
        mode: 493
        path: /usr/local/bin/file3.sh
`
	machineConfig23Defaulted := `apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  creationTimestamp: null
  labels:
    machineconfiguration.openshift.io/role: worker
  name: config-2
spec:
  baseOSExtensionsContainerImage: ""
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

---
apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  creationTimestamp: null
  labels:
    machineconfiguration.openshift.io/role: worker
  name: config-3
spec:
  baseOSExtensionsContainerImage: ""
  config:
    ignition:
      version: 3.2.0
    storage:
      files:
      - contents: null
        filesystem: root
        mode: 493
        path: /usr/local/bin/file3.sh
        source: |-
          [Service]
          Type=oneshot
          ExecStart=/usr/bin/echo Hello World 3

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
  kubeletConfig:
    maxPods: 100
`
	kubeletConfig1Defaulted := `apiVersion: machineconfiguration.openshift.io/v1
kind: KubeletConfig
metadata:
  creationTimestamp: null
  name: set-max-pods
spec:
  kubeletConfig:
    maxPods: 100
  machineConfigPoolSelector:
    matchLabels:
      machineconfiguration.openshift.io/mco-built-in: ""
status:
  conditions: null
`
	kubeletConfig2 := `
apiVersion: machineconfiguration.openshift.io/v1
kind: KubeletConfig
metadata:
  name: set-max-pods-2
spec:
  kubeletConfig:
    maxPods: 200
`
	kubeletConfig2Defaulted := `apiVersion: machineconfiguration.openshift.io/v1
kind: KubeletConfig
metadata:
  creationTimestamp: null
  name: set-max-pods-2
spec:
  kubeletConfig:
    maxPods: 200
  machineConfigPoolSelector:
    matchLabels:
      machineconfiguration.openshift.io/mco-built-in: ""
status:
  conditions: null
`

	haproxyIgnititionConfig := `apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  creationTimestamp: null
  labels:
    machineconfiguration.openshift.io/role: worker
  name: 20-apiserver-haproxy
spec:
  baseOSExtensionsContainerImage: ""
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
	containerRuntimeConfig1 := `apiVersion: machineconfiguration.openshift.io/v1
kind: ContainerRuntimeConfig
metadata:
  name: set-pids-limit
spec:
  containerRuntimeConfig:
    logSizeMax: "0"
    overlaySize: "0"
    pidsLimit: 2048
`

	containerRuntimeConfig1Defaulted := `apiVersion: machineconfiguration.openshift.io/v1
kind: ContainerRuntimeConfig
metadata:
  creationTimestamp: null
  name: set-pids-limit
spec:
  containerRuntimeConfig:
    logSizeMax: "0"
    overlaySize: "0"
    pidsLimit: 2048
  machineConfigPoolSelector:
    matchLabels:
      machineconfiguration.openshift.io/mco-built-in: ""
status:
  conditions: null
`

	namespace := "test"
	testCases := []struct {
		name                        string
		nodePool                    *hyperv1.NodePool
		config                      []client.Object
		mirroredConfigs             []*MirrorConfig
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
			name: "gets three valid MachineConfig, two of them in a single config-map",
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
						TokenSecretConfigKey: machineConfig23,
					},
				},
			},
			expect: machineConfig1Defaulted + "\n---\n" + machineConfig23Defaulted,
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
			name: "gets a single valid ContainerRuntimeConfig",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
				},
				Spec: hyperv1.NodePoolSpec{
					Config: []corev1.LocalObjectReference{
						{
							Name: "containerRuntimeConfig-1",
						},
					},
				},
			},
			config: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "containerRuntimeConfig-1",
						Namespace: namespace,
					},
					Data: map[string]string{
						TokenSecretConfigKey: containerRuntimeConfig1,
					},
				},
			},
			mirroredConfigs: []*MirrorConfig{
				{
					ConfigMap: &corev1.ConfigMap{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "containerRuntimeConfig-1",
							Namespace: namespace,
						},
						Data: map[string]string{
							TokenSecretConfigKey: containerRuntimeConfig1,
						},
					},
					Labels: map[string]string{
						ContainerRuntimeConfigConfigMapLabel: "",
					},
				},
			},
			expect: containerRuntimeConfig1Defaulted,
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
						TokenSecretConfigKey: machineConfig1Defaulted,
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
		{
			name: "gets a single valid KubeletConfig",
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
				Status: hyperv1.NodePoolStatus{},
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
					BinaryData: nil,
				},
			},
			expect: kubeletConfig1Defaulted,
			error:  false,
		},
		{
			name: "gets two valid KubeletConfig",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
				},
				Spec: hyperv1.NodePoolSpec{
					Config: []corev1.LocalObjectReference{
						{
							Name: "kubeletconfig-1",
						},
						{
							Name: "kubeletconfig-2",
						},
					},
				},
				Status: hyperv1.NodePoolStatus{},
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
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kubeletconfig-2",
						Namespace: namespace,
					},
					Data: map[string]string{
						TokenSecretConfigKey: kubeletConfig2,
					},
				},
			},
			expect: kubeletConfig1Defaulted + "\n---\n" + kubeletConfig2Defaulted,
			error:  false,
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
				Spec: hyperv1.HostedClusterSpec{
					Networking: hyperv1.ClusterNetworking{
						ServiceNetwork: []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("192.168.1.0/24")}},
					},
				},
			}
			releaseImage := &releaseinfo.ReleaseImage{ImageStream: &imagev1.ImageStream{Spec: imagev1.ImageStreamSpec{Tags: []imagev1.TagReference{{
				Name: "haproxy-router",
				From: &corev1.ObjectReference{},
			}}}}}
			got, mirroredConfigs, missingConfigs, err := r.getConfig(context.Background(),
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
			if diff := cmp.Diff(mirroredConfigs, tc.mirroredConfigs, cmpopts.IgnoreFields(metav1.ObjectMeta{}, "ResourceVersion")); diff != "" {
				t.Errorf("actual mirrored configs differs from expected: %s", diff)
			}
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
	perfprofOne := `apiVersion: performance.openshift.io/v2
kind: PerformanceProfile
metadata:
    name: perfprofOne
spec:
    cpu:
        isolated: 1,3-39,41,43-79
        reserved: 0,2,40,42
    machineConfigPoolSelector:
        machineconfiguration.openshift.io/role: worker-cnf
    nodeSelector:
        node-role.kubernetes.io/worker-cnf: ""
    numa:
        topologyPolicy: restricted
    realTimeKernel:
        enabled: true
    workloadHints:
        highPowerConsumption: false
        realTime: true
`
	perfprofTwo := `apiVersion: performance.openshift.io/v2
kind: PerformanceProfile
metadata:
    name: perfprofTwo
spec:
    cpu:
        isolated: 1,3-39,41,43-79
        reserved: 0,2,40,42
    machineConfigPoolSelector:
        machineconfiguration.openshift.io/role: worker-cnf
    nodeSelector:
        node-role.kubernetes.io/worker-cnf: ""
    numa:
        topologyPolicy: restricted
    realTimeKernel:
        enabled: false
    workloadHints:
        highPowerConsumption: false
        realTime: true
`
	perfprofOneDefaulted := `apiVersion: performance.openshift.io/v2
kind: PerformanceProfile
metadata:
  creationTimestamp: null
  name: perfprofOne
spec:
  cpu:
    isolated: 1,3-39,41,43-79
    reserved: 0,2,40,42
  machineConfigPoolSelector:
    machineconfiguration.openshift.io/role: worker-cnf
  nodeSelector:
    node-role.kubernetes.io/worker-cnf: ""
  numa:
    topologyPolicy: restricted
  realTimeKernel:
    enabled: true
  workloadHints:
    highPowerConsumption: false
    realTime: true
status: {}
`

	namespace := "test"
	testCases := []struct {
		name               string
		nodePool           *hyperv1.NodePool
		tuningConfig       []client.Object
		tunedExpect        string
		perfprofExpect     string
		perfProfNameExpect string
		error              bool
	}{
		{
			name: "gets a single valid TunedConfig",
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
			tunedExpect:    tuned1Defaulted,
			perfprofExpect: "",
			error:          false,
		},
		{
			name: "gets two valid TunedConfigs",
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
			tunedExpect:    tuned1Defaulted + "\n---\n" + tuned2Defaulted,
			perfprofExpect: "",
			error:          false,
		},
		{
			name: "fails if a non existent TunedConfig is referenced",
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
			tuningConfig:   []client.Object{},
			tunedExpect:    "",
			perfprofExpect: "",
			error:          true,
		},
		//-------------------------------------------------------------------------
		{
			name: "gets a single valid PerformanceProfileConfig",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
				},
				Spec: hyperv1.NodePoolSpec{
					TuningConfig: []corev1.LocalObjectReference{
						{
							Name: "perfprofOne",
						},
					},
				},
				Status: hyperv1.NodePoolStatus{},
			},
			tuningConfig: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "perfprofOne",
						Namespace: namespace,
					},
					Data: map[string]string{
						tuningConfigKey: perfprofOne,
					},
					BinaryData: nil,
				},
			},
			tunedExpect:        "",
			perfprofExpect:     perfprofOneDefaulted,
			perfProfNameExpect: "perfprofOne",
			error:              false,
		},
		{
			name: "Should be at most one PerformanceProfileConfig per NodePool",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
				},
				Spec: hyperv1.NodePoolSpec{
					TuningConfig: []corev1.LocalObjectReference{
						{
							Name: "perfprofOne",
						},
						{
							Name: "perfprofTwo",
						},
					},
				},
				Status: hyperv1.NodePoolStatus{},
			},
			tuningConfig: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "perfprofOne",
						Namespace: namespace,
					},
					Data: map[string]string{
						tuningConfigKey: perfprofOne,
					},
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "perfprofTwo",
						Namespace: namespace,
					},
					Data: map[string]string{
						tuningConfigKey: perfprofTwo,
					},
				},
			},
			tunedExpect:    "",
			perfprofExpect: "",
			error:          true,
		},
		{
			name: "fails if a non existent PerformanceProfile is referenced",
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
			tuningConfig:   []client.Object{},
			tunedExpect:    "",
			perfprofExpect: "",
			error:          true,
		},
		{
			name: "PerformanceProfiles and Tuned Configs could coexists",
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
						{
							Name: "perfprofOne",
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
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "perfprofOne",
						Namespace: namespace,
					},
					Data: map[string]string{
						tuningConfigKey: perfprofOne,
					},
				},
			},
			tunedExpect:        tuned1Defaulted + "\n---\n" + tuned2Defaulted,
			perfprofExpect:     perfprofOneDefaulted,
			perfProfNameExpect: "perfprofOne",
			error:              false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			r := NodePoolReconciler{
				Client: fake.NewClientBuilder().WithObjects(tc.tuningConfig...).Build(),
			}

			td, pp, ppName, err := r.getTuningConfig(context.Background(), tc.nodePool)

			if tc.error {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			if diff := cmp.Diff(td, tc.tunedExpect); diff != "" {
				t.Errorf("actual tuned config differs from expected: %s", diff)
				t.Logf("got: %s \n, expected: \n %s", td, tc.tunedExpect)
			}
			if diff := cmp.Diff(pp, tc.perfprofExpect); diff != "" {
				t.Errorf("actual Performance Profile config differs from expected: %s", diff)
				t.Logf("got:\n%s\n, expected:\n%s\n", pp, tc.perfprofExpect)
			}
			if diff := cmp.Diff(ppName, tc.perfProfNameExpect); diff != "" {
				t.Errorf("Performance Profile config name differ from expected: %s", diff)
				t.Logf("got:\n%s\n, expected:\n%s\n", ppName, tc.perfProfNameExpect)
			}
		})
	}
}

func TestReconcileMirroredConfigs(t *testing.T) {
	containerRuntimeConfig1 := `apiVersion: machineconfiguration.openshift.io/v1
	kind: ContainerRuntimeConfig
	metadata:
	 name: set-pids-limit
	spec:
	 containerRuntimeConfig:
	   pidsLimit: 2048
	`
	containerRuntimeConfig2 := `apiVersion: machineconfiguration.openshift.io/v1
	kind: ContainerRuntimeConfig
	metadata:
	 name: change-to-runc
	spec:
	 containerRuntimeConfig:
	   defaultRuntime: crun
	`
	hcpNamespace := "hostedcontrolplane-namespace"
	npNamespace := "nodepool-namespace"
	npName := "nodepool-test"
	np := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      npName,
			Namespace: npNamespace,
		},
	}
	testCases := []struct {
		name                    string
		nodePool                *hyperv1.NodePool
		controlPlaneNamespace   string
		configsToBeMirrored     []*MirrorConfig
		existingConfigsInHcpNs  []client.Object
		expectedMirroredConfigs []corev1.ConfigMap
		configsForDeletion      []corev1.ConfigMap
	}{
		{
			name:                  "with containerruntime",
			nodePool:              np,
			controlPlaneNamespace: hcpNamespace,
			configsToBeMirrored: []*MirrorConfig{
				{
					ConfigMap: &corev1.ConfigMap{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "foo",
							Namespace: npNamespace,
						},
						Data: map[string]string{
							TokenSecretConfigKey: containerRuntimeConfig1,
						},
					},
					Labels: map[string]string{
						ContainerRuntimeConfigConfigMapLabel: "",
					},
				},
			},
			existingConfigsInHcpNs: nil,
			expectedMirroredConfigs: []corev1.ConfigMap{
				{
					Immutable: k8sutilspointer.Bool(true),
					ObjectMeta: metav1.ObjectMeta{
						Name:      supportutil.ShortenName("foo", npName, validation.LabelValueMaxLength),
						Namespace: hcpNamespace,
						Labels: map[string]string{
							mirroredConfigLabel:                  "",
							nodePoolAnnotation:                   npName,
							ContainerRuntimeConfigConfigMapLabel: "",
						},
						Annotations: map[string]string{nodePoolAnnotation: npNamespace + "/" + npName},
					},
					Data: map[string]string{
						TokenSecretConfigKey: containerRuntimeConfig1,
					},
				},
			},
		},
		{
			name:                  "with configs that need to be deleted",
			nodePool:              np,
			controlPlaneNamespace: hcpNamespace,
			configsToBeMirrored: []*MirrorConfig{
				{
					ConfigMap: &corev1.ConfigMap{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "foo",
							Namespace: npNamespace,
						},
						Data: map[string]string{
							TokenSecretConfigKey: containerRuntimeConfig2,
						},
					},
					Labels: map[string]string{
						ContainerRuntimeConfigConfigMapLabel: "",
					},
				},
			},
			existingConfigsInHcpNs: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "bar",
						Namespace: npNamespace,
					},
					Data: map[string]string{
						TokenSecretConfigKey: containerRuntimeConfig1,
					},
				},
			},
			expectedMirroredConfigs: []corev1.ConfigMap{
				{
					Immutable: k8sutilspointer.Bool(true),
					ObjectMeta: metav1.ObjectMeta{
						Name:      supportutil.ShortenName("foo", npName, validation.LabelValueMaxLength),
						Namespace: hcpNamespace,
						Labels: map[string]string{
							mirroredConfigLabel:                  "",
							nodePoolAnnotation:                   npName,
							ContainerRuntimeConfigConfigMapLabel: "",
						},
						Annotations: map[string]string{nodePoolAnnotation: npNamespace + "/" + npName},
					},
					Data: map[string]string{
						TokenSecretConfigKey: containerRuntimeConfig2,
					},
				},
			},
			configsForDeletion: []corev1.ConfigMap{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      supportutil.ShortenName("bar", npName, validation.LabelValueMaxLength),
						Namespace: hcpNamespace,
					},
					Data: map[string]string{
						TokenSecretConfigKey: containerRuntimeConfig1,
					},
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			r := NodePoolReconciler{
				Client:                 fake.NewClientBuilder().WithObjects(tc.existingConfigsInHcpNs...).Build(),
				CreateOrUpdateProvider: upsert.New(true),
			}
			err := r.reconcileMirroredConfigs(context.Background(), logr.Discard(), tc.configsToBeMirrored, tc.controlPlaneNamespace, tc.nodePool)
			g.Expect(err).ToNot(HaveOccurred())
			for _, config := range tc.expectedMirroredConfigs {
				cm := &corev1.ConfigMap{}
				err := r.Get(context.Background(), client.ObjectKeyFromObject(&config), cm)
				g.Expect(err).ToNot(HaveOccurred())
				if diff := cmp.Diff(cm, &config, cmpopts.IgnoreFields(metav1.ObjectMeta{}, "ResourceVersion")); diff != "" {
					t.Errorf("actual mirrored config differs from expected: %s", diff)
					t.Logf("got:\n%+v\n, expected:\n%+v\n", cm, config)
				}
			}
			for _, config := range tc.configsForDeletion {
				cm := &corev1.ConfigMap{}
				err := r.Get(context.Background(), client.ObjectKeyFromObject(&config), cm)
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
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
					Replicas: ptr.To[int32](5),
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
					Replicas: ptr.To[int32](3),
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
					Replicas: ptr.To[int32](0),
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
					Replicas: ptr.To[int32](1),
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
					Replicas: ptr.To[int32](10),
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
					Subnet:          hyperv1.AWSResourceReference{ID: ptr.To("subnet-xyz")},
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
				Namespace: manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name),
			},
			Spec: capiaws.AWSMachineTemplateSpec{
				Template: capiaws.AWSMachineTemplateResource{
					Spec: capiaws.AWSMachineSpec{
						AMI: capiaws.AMIReference{
							ID: ptr.To(ami),
						},
						IAMInstanceProfile:   "test-worker-profile",
						Subnet:               &capiaws.AWSResourceReference{},
						UncompressedUserData: ptr.To(true),
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
			Namespace:   manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name),
			Annotations: map[string]string{nodePoolAnnotation: client.ObjectKeyFromObject(nodePool).String()},
		},
		Spec: capiaws.AWSMachineTemplateSpec{
			Template: capiaws.AWSMachineTemplateResource{
				Spec: capiaws.AWSMachineSpec{
					AMI: capiaws.AMIReference{
						ID: ptr.To(ami),
					},
					IAMInstanceProfile: "test-worker-profile",
					Subnet: &capiaws.AWSResourceReference{
						ID: ptr.To("subnet-xyz"),
					},
					UncompressedUserData: ptr.To(true),
					CloudInit: capiaws.CloudInit{
						InsecureSkipSecretsManager: true,
						SecureSecretsBackend:       "secrets-manager",
					},
					AdditionalTags: capiaws.Tags{
						awsClusterCloudProviderTagKey(infraID): infraLifecycleOwned,
					},
					AdditionalSecurityGroups: []capiaws.AWSResourceReference{
						{
							ID: ptr.To("default-sg"),
						},
					},
					RootVolume: &capiaws.Volume{
						Size: 16,
						Type: "io1",
						IOPS: 5000,
					},
					InstanceMetadataOptions: &capiaws.InstanceMetadataOptions{
						HTTPTokens:              capiaws.HTTPTokensStateOptional,
						HTTPPutResponseHopLimit: 2,
						HTTPEndpoint:            capiaws.InstanceMetadataEndpointStateEnabled,
						InstanceMetadataTags:    capiaws.InstanceMetadataEndpointStateDisabled,
					},
				},
			},
		},
	}
	expectedMachineTemplateSpecJSON, err := json.Marshal(expectedMachineTemplate.Spec)
	g.Expect(err).ToNot(HaveOccurred())

	expectedMachineTemplate.SetName(generateMachineTemplateName(nodePool, expectedMachineTemplateSpecJSON))

	template, mutateTemplate, machineTemplateSpecJSON, err := machineTemplateBuilders(hcluster, nodePool, infraID, ami, "", nil, true)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(machineTemplateSpecJSON).To(BeIdenticalTo(string(expectedMachineTemplateSpecJSON)))

	// Validate that template and mutateTemplate are able to produce an expected target template.
	_, err = r.CreateOrUpdate(context.Background(), r.Client, template, func() error {
		return mutateTemplate(template)
	})
	g.Expect(err).ToNot(HaveOccurred())

	gotMachineTemplate := &capiaws.AWSMachineTemplate{}
	g.Expect(r.Client.Get(context.Background(), client.ObjectKeyFromObject(expectedMachineTemplate), gotMachineTemplate)).To(Succeed())
	g.Expect(expectedMachineTemplate.Spec).To(BeEquivalentTo(gotMachineTemplate.Spec))
	g.Expect(expectedMachineTemplate.ObjectMeta.Annotations).To(BeEquivalentTo(gotMachineTemplate.ObjectMeta.Annotations))
}

func TestMachineTemplateBuilders(t *testing.T) {
	RunTestMachineTemplateBuilders(t, false)
}

func TestMachineTemplateBuildersPreexisting(t *testing.T) {
	RunTestMachineTemplateBuilders(t, true)
}

func TestCleanupMachineTemplates(t *testing.T) {
	g := NewWithT(t)

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

	template1 := &capiaws.AWSMachineTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "template1",
			Namespace:   "test",
			Annotations: map[string]string{nodePoolAnnotation: client.ObjectKeyFromObject(nodePool).String()},
		},
		Spec: capiaws.AWSMachineTemplateSpec{},
	}

	template2 := &capiaws.AWSMachineTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "template2",
			Namespace:   "test",
			Annotations: map[string]string{nodePoolAnnotation: client.ObjectKeyFromObject(nodePool).String()},
		},
		Spec: capiaws.AWSMachineTemplateSpec{},
	}

	gvk, err := apiutil.GVKForObject(template1, api.Scheme)
	g.Expect(err).ToNot(HaveOccurred())
	// machine set refrencing template1
	ms := &capiv1.MachineSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "machineSet",
			Namespace:   "test",
			Annotations: map[string]string{nodePoolAnnotation: client.ObjectKeyFromObject(nodePool).String()},
		},
		Spec: capiv1.MachineSetSpec{
			Template: capiv1.MachineTemplateSpec{
				Spec: capiv1.MachineSpec{
					InfrastructureRef: corev1.ObjectReference{
						Kind:       gvk.Kind,
						APIVersion: gvk.GroupVersion().String(),
						Name:       template1.Name,
						Namespace:  template1.Namespace,
					},
				},
			},
		},
	}

	c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(nodePool, template1, template2, ms).Build()
	r := &NodePoolReconciler{
		Client:                 c,
		CreateOrUpdateProvider: upsert.New(false),
	}

	err = r.cleanupMachineTemplates(context.Background(), logr.Discard(), nodePool, "test")
	g.Expect(err).ToNot(HaveOccurred())

	templates, err := r.listMachineTemplates(nodePool)
	g.Expect(err).ToNot(HaveOccurred())
	// check template2 has been deleted
	g.Expect(len(templates)).To(Equal(1))
	g.Expect(templates[0].GetName()).To(Equal("template1"))
}

func TestListMachineTemplatesAWS(t *testing.T) {
	g := NewWithT(t)
	capiaws.AddToScheme(api.Scheme)
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
	theTime, err := time.Parse(time.RFC3339Nano, "2006-01-02T15:04:05.999999999Z")
	if err != nil {
		t.Fatalf("could not parse time: %v", err)
	}
	fakeClock := testingclock.NewFakeClock(theTime)

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
			err := setExpirationTimestampOnToken(context.Background(), c, tc.inputSecret, fakeClock.Now)
			g.Expect(err).To(Not(HaveOccurred()))
			actualSecretData := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fakeName,
					Namespace: fakeNamespace,
				},
			}
			err = c.Get(context.Background(), client.ObjectKeyFromObject(actualSecretData), actualSecretData)
			g.Expect(err).To(Not(HaveOccurred()))
			g.Expect(actualSecretData.Annotations).To(testutil.MatchExpected(map[string]string{
				hyperv1.IgnitionServerTokenExpirationTimestampAnnotation: theTime.Add(2 * time.Hour).Format(time.RFC3339),
			}))
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

	r := &NodePoolReconciler{
		Client:               c,
		KubevirtInfraClients: newKVInfraMapMock([]client.Object{nodePool}),
	}
	if _, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(nodePool)}); err != nil {
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
					Replicas: ptr.To[int32](4),
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
					Replicas: ptr.To[int32](4),
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
					Replicas: ptr.To[int32](4),
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
					Replicas: ptr.To[int32](4),
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
					Replicas: ptr.To[int32](4),
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

func TestDefaultNodePoolAMI(t *testing.T) {
	testCases := []struct {
		name          string
		region        string
		specifiedArch string
		releaseImage  *releaseinfo.ReleaseImage
		image         string
		err           error
		expectedImage string
	}{
		{
			name:          "successfully pull amd64 AMI",
			region:        "us-east-1",
			specifiedArch: "amd64",
			expectedImage: "us-east-1-x86_64-image",
		},
		{
			name:          "successfully pull arm64 AMI",
			region:        "us-east-1",
			specifiedArch: "arm64",
			expectedImage: "us-east-1-aarch64-image",
		},
		{
			name:          "fail to pull amd64 AMI because region can't be found",
			region:        "us-east-2",
			specifiedArch: "amd64",
			expectedImage: "",
		},
		{
			name:          "fail to pull arm64 AMI because region can't be found",
			region:        "us-east-2",
			specifiedArch: "arm64",
			expectedImage: "",
		},
		{
			name:          "fail because architecture can't be found",
			region:        "us-east-2",
			specifiedArch: "arm644",
			expectedImage: "",
		},
		{
			name:          "fail because architecture can't be found",
			region:        "us-east-2",
			specifiedArch: "s390x",
			expectedImage: "",
		},
		{
			name:          "fail because no image data is defined",
			region:        "us-west-1",
			specifiedArch: "arm64",
			expectedImage: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			other := []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "pull-secret"},
					Data: map[string][]byte{
						corev1.DockerConfigJsonKey: nil,
					},
				},
			}

			client := fake.NewClientBuilder().WithObjects(other...).Build()
			releaseProvider := &fakereleaseprovider.FakeReleaseProvider{}
			hc := &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret",
					},
					Release: hyperv1.Release{
						Image: "image-4.12.0",
					},
				},
			}

			ctx := context.Background()
			tc.releaseImage = fakereleaseprovider.GetReleaseImage(ctx, hc, client, releaseProvider)

			tc.image, tc.err = defaultNodePoolAMI(tc.region, tc.specifiedArch, tc.releaseImage)
			if strings.Contains(tc.name, "successfully") {
				g.Expect(tc.image).To(Equal(tc.expectedImage))
				g.Expect(tc.err).To(BeNil())
			} else if strings.Contains(tc.name, "fail to pull") {
				g.Expect(tc.image).To(BeEmpty())
				g.Expect(tc.err.Error()).To(Equal("couldn't find AWS image for region \"" + tc.region + "\""))
			} else if strings.Contains(tc.name, "fail because architecture") {
				g.Expect(tc.image).To(BeEmpty())
				g.Expect(tc.err.Error()).To(Equal("couldn't find OS metadata for architecture \"" + tc.specifiedArch + "\""))
			} else {
				g.Expect(tc.image).To(BeEmpty())
				g.Expect(tc.err.Error()).To(Equal("release image metadata has no image for region \"" + tc.region + "\""))
			}
		})
	}
}

func TestGetHostedClusterVersion(t *testing.T) {
	testCases := []struct {
		name                string
		versionStatus       *hyperv1.ClusterVersionStatus
		releaseImageVersion string
		expectedVersion     string
	}{
		{
			name:                "version history status is empty, should return release image version",
			releaseImageVersion: "4.15.0",
			expectedVersion:     "4.15.0",
		},
		{
			name: "version history status has a completed entry, should return the completed version",
			versionStatus: &hyperv1.ClusterVersionStatus{
				History: []configv1.UpdateHistory{
					{
						Version:        "4.14.0",
						CompletionTime: ptr.To(metav1.Now()),
					},
				},
			},
			releaseImageVersion: "4.15.0",
			expectedVersion:     "4.14.0",
		},
		{
			name: "version history status has no completed entries, should return release image version",
			versionStatus: &hyperv1.ClusterVersionStatus{
				History: []configv1.UpdateHistory{
					{
						Version:        "4.14.0",
						CompletionTime: nil,
					},
				},
			},
			releaseImageVersion: "4.15.0",
			expectedVersion:     "4.15.0",
		},
		{
			name: "version history status has multiple entries, should return the first completed version",
			versionStatus: &hyperv1.ClusterVersionStatus{
				History: []configv1.UpdateHistory{
					{
						Version:        "4.16.0",
						CompletionTime: nil,
					},
					{
						Version:        "4.15.0",
						CompletionTime: ptr.To(metav1.Now()),
					},
				},
			},
			releaseImageVersion: "4.16.0",
			expectedVersion:     "4.15.0",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			releaseProvider := &fakereleaseprovider.FakeReleaseProvider{
				Version: tc.releaseImageVersion,
			}
			r := NodePoolReconciler{
				ReleaseProvider: releaseProvider,
			}
			hc := &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Release: hyperv1.Release{
						Image: "image",
					},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: tc.versionStatus,
				},
			}

			version, err := r.getHostedClusterVersion(context.Background(), hc, nil)
			g.Expect(err).ToNot(HaveOccurred())

			g.Expect(version).ToNot(BeNil())
			g.Expect(version.String()).To(Equal(tc.expectedVersion))
		})

	}
}

type testCondition struct {
	Status   corev1.ConditionStatus
	Reason   string
	Messages []string
}

func (t *testCondition) Compare(g Gomega, cond *hyperv1.NodePoolCondition) {
	if t == nil {
		return
	}

	g.Expect(cond.Status).To(Equal(t.Status))
	g.Expect(cond.Reason).To(Equal(t.Reason))

	for _, msg := range t.Messages {
		g.ExpectWithOffset(1, cond.Message).To(ContainSubstring(msg))
	}
}

func TestSetMachineAndNodeConditions(t *testing.T) {
	g := NewWithT(t)
	s := runtime.NewScheme()
	g.Expect(hyperv1.AddToScheme(s)).To(Succeed())
	g.Expect(capiv1.AddToScheme(s)).To(Succeed())

	for _, tc := range []struct {
		name                  string
		machinesGenerator     func() []client.Object
		expectedAllMachine    *testCondition
		expectedAllNodes      *testCondition
		expectedCIDRCollision *testCondition
	}{
		{
			name:              "no cluster-api machines",
			machinesGenerator: func() []client.Object { return nil },
			expectedAllMachine: &testCondition{
				Status:   corev1.ConditionFalse,
				Reason:   hyperv1.NodePoolNotFoundReason,
				Messages: []string{"No Machines are created"},
			},
			expectedAllNodes: &testCondition{
				Status: corev1.ConditionFalse,
				Reason: hyperv1.NodePoolNotFoundReason,
			},
		},
		{
			name: "good machines",
			machinesGenerator: func() []client.Object {
				return []client.Object{
					&capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "node1",
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []capiv1.Condition{
								{
									Type:   capiv1.ReadyCondition,
									Status: corev1.ConditionTrue,
								},
							},
						},
					},
					&capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "node2",
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []capiv1.Condition{
								{
									Type:   capiv1.ReadyCondition,
									Status: corev1.ConditionTrue,
								},
							},
						},
					},
				}
			},
			expectedAllMachine: &testCondition{
				Status:   corev1.ConditionTrue,
				Reason:   hyperv1.AsExpectedReason,
				Messages: []string{hyperv1.AllIsWellMessage},
			},
			expectedAllNodes: &testCondition{
				Status:   corev1.ConditionTrue,
				Reason:   hyperv1.AsExpectedReason,
				Messages: []string{hyperv1.AllIsWellMessage},
			},
		},
		{
			name: "no InfrastructureReady condition",
			machinesGenerator: func() []client.Object {
				return []client.Object{
					&capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "node1",
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []capiv1.Condition{
								{
									Type:    capiv1.ReadyCondition,
									Status:  corev1.ConditionFalse,
									Reason:  "TestReasonNode1",
									Message: "test message node 1",
								},
								{
									Type:    capiv1.MachineNodeHealthyCondition,
									Status:  corev1.ConditionFalse,
									Reason:  "TestReasonNode1",
									Message: "test message node 1",
								},
							},
						},
					},
					&capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "node2",
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []capiv1.Condition{
								{
									Type:    capiv1.ReadyCondition,
									Status:  corev1.ConditionFalse,
									Reason:  "TestReasonNode2",
									Message: "test message node 2",
								},
								{
									Type:    capiv1.MachineNodeHealthyCondition,
									Status:  corev1.ConditionFalse,
									Reason:  "TestReasonNode2",
									Message: "test message node 2",
								},
							},
						},
					},
				}
			},
			expectedAllMachine: &testCondition{
				Status:   corev1.ConditionFalse,
				Reason:   "TestReasonNode1,TestReasonNode2",
				Messages: []string{"2 of 2 machines are not ready", "Machine node1: TestReasonNode1", "Machine node2: TestReasonNode2"},
			},
			expectedAllNodes: &testCondition{
				Status:   corev1.ConditionFalse,
				Reason:   "TestReasonNode2",
				Messages: []string{"TestReasonNode1", "TestReasonNode2"},
			},
		},
		{
			name: "mix InfrastructureReady condition; setup counter first",
			machinesGenerator: func() []client.Object {
				return []client.Object{
					&capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "node1",
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
								"testDescription":  "message is setup counter",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []capiv1.Condition{
								{
									Type:    capiv1.ReadyCondition,
									Status:  corev1.ConditionFalse,
									Reason:  "TestReasonNode1",
									Message: "test message node 1",
								},
								{
									Type:    capiv1.InfrastructureReadyCondition,
									Status:  corev1.ConditionFalse,
									Reason:  "TestReasonNode1",
									Message: "12 of 34 completed",
								},
								{
									Type:    capiv1.MachineNodeHealthyCondition,
									Status:  corev1.ConditionFalse,
									Reason:  "TestReasonNode1",
									Message: "test message node 1",
								},
							},
						},
					},
					&capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "node2",
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
								"testDescription":  "message some error text",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []capiv1.Condition{
								{
									Type:    capiv1.ReadyCondition,
									Status:  corev1.ConditionFalse,
									Reason:  "TestReasonNode2",
									Message: "test message node 2",
								},
								{
									Type:    capiv1.InfrastructureReadyCondition,
									Status:  corev1.ConditionFalse,
									Reason:  "TestReasonNode2",
									Message: "some real failed message",
								},
								{
									Type:    capiv1.MachineNodeHealthyCondition,
									Status:  corev1.ConditionFalse,
									Reason:  "TestReasonNode2",
									Message: "test message node 2",
								},
							},
						},
					},
					&capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "node3",
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
								"testDescription":  "this machine is ready",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []capiv1.Condition{
								{
									Type:   capiv1.ReadyCondition,
									Status: corev1.ConditionTrue,
								},
								{
									Type:   capiv1.MachineNodeHealthyCondition,
									Status: corev1.ConditionTrue,
								},
							},
						},
					},
				}
			},
			expectedAllMachine: &testCondition{
				Status:   corev1.ConditionFalse,
				Reason:   "TestReasonNode1,TestReasonNode2",
				Messages: []string{"2 of 3 machines are not ready", "Machine node1: TestReasonNode1", "Machine node2: TestReasonNode2: some real failed message"},
			},
			expectedAllNodes: &testCondition{
				Status:   corev1.ConditionFalse,
				Reason:   "TestReasonNode2",
				Messages: []string{"TestReasonNode1", "TestReasonNode2"},
			},
		},
		{
			name: "mix InfrastructureReady condition; failure text first",
			machinesGenerator: func() []client.Object {
				return []client.Object{
					&capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "node1",
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
								"testDescription":  "message some error text",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []capiv1.Condition{
								{
									Type:    capiv1.ReadyCondition,
									Status:  corev1.ConditionFalse,
									Reason:  "TestReasonNode1",
									Message: "test message node 1",
								},
								{
									Type:    capiv1.InfrastructureReadyCondition,
									Status:  corev1.ConditionFalse,
									Reason:  "TestReasonNode1",
									Message: "some real failed message",
								},
								{
									Type:    capiv1.MachineNodeHealthyCondition,
									Status:  corev1.ConditionFalse,
									Reason:  "TestReasonNode1",
									Message: "test message node 1",
								},
							},
						},
					},
					&capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "node2",
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
								"testDescription":  "message is setup counter",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []capiv1.Condition{
								{
									Type:    capiv1.ReadyCondition,
									Status:  corev1.ConditionFalse,
									Reason:  "TestReasonNode2",
									Message: "test message node 2",
								},
								{
									Type:    capiv1.InfrastructureReadyCondition,
									Status:  corev1.ConditionFalse,
									Reason:  "TestReasonNode2",
									Message: "12 of 34 completed",
								},
								{
									Type:    capiv1.MachineNodeHealthyCondition,
									Status:  corev1.ConditionFalse,
									Reason:  "TestReasonNode2",
									Message: "test message node 2",
								},
							},
						},
					},
					&capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "node3",
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
								"testDescription":  "this machine is ready",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []capiv1.Condition{
								{
									Type:   capiv1.ReadyCondition,
									Status: corev1.ConditionTrue,
								},
								{
									Type:   capiv1.MachineNodeHealthyCondition,
									Status: corev1.ConditionTrue,
								},
							},
						},
					},
				}
			},
			expectedAllMachine: &testCondition{
				Status:   corev1.ConditionFalse,
				Reason:   "TestReasonNode1,TestReasonNode2",
				Messages: []string{"2 of 3 machines are not ready", "Machine node1: TestReasonNode1: some real failed message", "Machine node2: TestReasonNode2"},
			},
			expectedAllNodes: &testCondition{
				Status:   corev1.ConditionFalse,
				Reason:   "TestReasonNode2",
				Messages: []string{"TestReasonNode1", "TestReasonNode2"},
			},
		},
		{
			name: "too many not ready machines",
			machinesGenerator: func() []client.Object {
				longMessage := strings.Repeat("msg ", 50)

				machines := make([]client.Object, 15) // two reasons with 5 machine each (too long message), one reason with only 3 machines, and 2 ready machines

				i := 0

				for ; i < 5; i++ { // create 5 machine. should exceed max message length
					machines[i] = &capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      fmt.Sprintf("node%d", i),
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []capiv1.Condition{
								{
									Type:    capiv1.ReadyCondition,
									Status:  corev1.ConditionFalse,
									Reason:  "TestReasonNode1",
									Message: "not ready",
								},
								{
									Type:    capiv1.InfrastructureReadyCondition,
									Status:  corev1.ConditionFalse,
									Reason:  "TestReasonNode1",
									Message: longMessage,
								},
							},
						},
					}
				}
				for ; i < 8; i++ { // create 3 machine. should not exceed max message length
					machines[i] = &capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      fmt.Sprintf("node%d", i),
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []capiv1.Condition{
								{
									Type:    capiv1.ReadyCondition,
									Status:  corev1.ConditionFalse,
									Reason:  "TestReasonNode2",
									Message: "not ready",
								},
								{
									Type:    capiv1.InfrastructureReadyCondition,
									Status:  corev1.ConditionFalse,
									Reason:  "TestReasonNode2",
									Message: longMessage,
								},
							},
						},
					}
				}
				for ; i < 13; i++ { // create 5 machine. should exceed max message length
					machines[i] = &capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      fmt.Sprintf("node%d", i),
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []capiv1.Condition{
								{
									Type:    capiv1.ReadyCondition,
									Status:  corev1.ConditionFalse,
									Reason:  "TestReasonNode3",
									Message: "not ready",
								},
								{
									Type:    capiv1.InfrastructureReadyCondition,
									Status:  corev1.ConditionFalse,
									Reason:  "TestReasonNode3",
									Message: longMessage,
								},
							},
						},
					}
				}
				for ; i < 15; i++ { // 2 ready machines
					machines[i] = &capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      fmt.Sprintf("node%d", i),
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []capiv1.Condition{
								{
									Type:   capiv1.ReadyCondition,
									Status: corev1.ConditionTrue,
								},
							},
						},
					}
				}

				return machines
			},
			expectedAllMachine: &testCondition{
				Status:   corev1.ConditionFalse,
				Reason:   "TestReasonNode1,TestReasonNode2,TestReasonNode3",
				Messages: []string{"13 of 15 machines are not ready", endOfMessage},
			},
		},
		{
			name: "machine cidr collision",
			machinesGenerator: func() []client.Object {
				return []client.Object{
					&capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "node1",
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []capiv1.Condition{
								{
									Type:   capiv1.ReadyCondition,
									Status: corev1.ConditionTrue,
								},
							},
							Addresses: capiv1.MachineAddresses{
								{
									Type:    capiv1.MachineInternalIP,
									Address: "10.10.10.5",
								},
							},
						},
					},
					&capiv1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "node2",
							Namespace: "myns-cluster-name",
							Annotations: map[string]string{
								nodePoolAnnotation: "myns/np-name",
							},
						},
						Status: capiv1.MachineStatus{
							Conditions: []capiv1.Condition{
								{
									Type:   capiv1.ReadyCondition,
									Status: corev1.ConditionTrue,
								},
							},
							Addresses: capiv1.MachineAddresses{
								{
									Type:    capiv1.MachineInternalIP,
									Address: "10.10.10.6",
								},
							},
						},
					},
				}
			},
			expectedAllMachine: &testCondition{
				Status:   corev1.ConditionTrue,
				Reason:   hyperv1.AsExpectedReason,
				Messages: []string{hyperv1.AllIsWellMessage},
			},
			expectedAllNodes: &testCondition{
				Status:   corev1.ConditionTrue,
				Reason:   hyperv1.AsExpectedReason,
				Messages: []string{hyperv1.AllIsWellMessage},
			},
			expectedCIDRCollision: &testCondition{
				Status: corev1.ConditionTrue,
				Reason: hyperv1.InvalidConfigurationReason,
				Messages: []string{
					"machine [node1] with ip [10.10.10.5] collides with cluster-network cidr [10.10.10.0/14]",
					"machine [node2] with ip [10.10.10.6] collides with cluster-network cidr [10.10.10.0/14]",
				},
			},
		},
	} {
		t.Run(tc.name, func(tt *testing.T) {
			gg := NewWithT(tt)
			r := NodePoolReconciler{
				Client: fake.NewClientBuilder().WithScheme(s).WithObjects(tc.machinesGenerator()...).Build(),
			}

			np := &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{Name: "np-name", Namespace: "myns"},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: "cluster-name",
				},
			}

			hc := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster-name", Namespace: "myns"},
				Spec: hyperv1.HostedClusterSpec{
					Networking: hyperv1.ClusterNetworking{
						ClusterNetwork: []hyperv1.ClusterNetworkEntry{
							{
								CIDR: *ipnet.MustParseCIDR("10.10.10.0/14"),
							},
						},
					},
				},
			}

			gg.Expect(r.setMachineAndNodeConditions(context.Background(), np, hc)).To(Succeed())

			cond := FindStatusCondition(np.Status.Conditions, hyperv1.NodePoolAllMachinesReadyConditionType)
			gg.Expect(cond).ToNot(BeNil())
			tc.expectedAllMachine.Compare(gg, cond)

			cond = FindStatusCondition(np.Status.Conditions, hyperv1.NodePoolAllNodesHealthyConditionType)
			gg.Expect(cond).ToNot(BeNil())
			tc.expectedAllNodes.Compare(gg, cond)

			cond = FindStatusCondition(np.Status.Conditions, hyperv1.NodePoolClusterNetworkCIDRConflictType)
			if tc.expectedCIDRCollision == nil {
				gg.Expect(cond).To(BeNil())
			} else {
				gg.Expect(cond).ToNot(BeNil())
				tc.expectedCIDRCollision.Compare(gg, cond)
			}

		})
	}
}

func newKVInfraMapMock(objects []client.Object) kvinfra.KubevirtInfraClientMap {
	return kvinfra.NewMockKubevirtInfraClientMap(
		fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objects...).Build(),
		"",
		"")
}

func TestIsArchAndPlatformSupported(t *testing.T) {
	testCases := []struct {
		name     string
		nodePool *hyperv1.NodePool
		expect   bool
	}{
		{
			name: "supported arch and platform used",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AWSPlatform,
					},
					Arch: hyperv1.ArchitectureAMD64,
				},
			},
			expect: true,
		},
		{
			name: "supported platform with multiple arch baremetal - arm64",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AgentPlatform,
					},
					Arch: hyperv1.ArchitectureARM64,
				},
			},
			expect: true,
		},
		{
			name: "supported platform with multiple arch - amd64",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AgentPlatform,
					},
					Arch: hyperv1.ArchitectureAMD64,
				},
			},
			expect: true,
		},
		{
			name: "supported platform with multiple arch - ppc64le",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AgentPlatform,
					},
					Arch: hyperv1.ArchitecturePPC64LE,
				},
			},
			expect: true,
		},
		{
			name: "supported platform with multiple arch baremetal - arm64",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.NonePlatform,
					},
					Arch: hyperv1.ArchitectureARM64,
				},
			},
			expect: true,
		},
		{
			name: "supported platform with multiple arch baremetal - amd64",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.NonePlatform,
					},
					Arch: hyperv1.ArchitectureAMD64,
				},
			},
			expect: true,
		},
		{
			name: "unsupported arch and platform used",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AWSPlatform,
					},
					Arch: hyperv1.ArchitecturePPC64LE,
				},
			},
			expect: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(isArchAndPlatformSupported(tc.nodePool)).To(Equal(tc.expect))
		})
	}
}

func TestReconcileMachineHealthCheck(t *testing.T) {
	hostedcluster := func(opts ...func(client.Object)) *hyperv1.HostedCluster {
		hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "cluster"}}
		for _, o := range opts {
			o(hc)
		}
		return hc
	}

	nodepool := func(opts ...func(client.Object)) *hyperv1.NodePool {
		np := &hyperv1.NodePool{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "nodepool"}}
		np.Spec.ClusterName = "cluster"
		for _, o := range opts {
			o(np)
		}
		return np
	}

	defaultMaxUnhealthy := intstr.Parse("2")
	healthcheck := func(opts ...func(*capiv1.MachineHealthCheck)) *capiv1.MachineHealthCheck {
		mhc := &capiv1.MachineHealthCheck{ObjectMeta: metav1.ObjectMeta{Namespace: "ns-cluster", Name: "nodepool"}}
		resName := generateName("cluster", "cluster", "nodepool")
		mhc.Spec = capiv1.MachineHealthCheckSpec{
			ClusterName: "cluster",
			Selector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					resName: resName,
				},
			},
			UnhealthyConditions: []capiv1.UnhealthyCondition{
				{
					Type:   corev1.NodeReady,
					Status: corev1.ConditionFalse,
					Timeout: metav1.Duration{
						Duration: time.Duration(8 * time.Minute),
					},
				},
				{
					Type:   corev1.NodeReady,
					Status: corev1.ConditionUnknown,
					Timeout: metav1.Duration{
						Duration: time.Duration(8 * time.Minute),
					},
				},
			},
			MaxUnhealthy: &defaultMaxUnhealthy,
			NodeStartupTimeout: &metav1.Duration{
				Duration: 20 * time.Minute,
			},
		}
		for _, o := range opts {
			o(mhc)
		}
		return mhc
	}

	withTimeoutOverride := func(value string) func(client.Object) {
		return func(o client.Object) {
			a := o.GetAnnotations()
			if a == nil {
				a = map[string]string{}
			}
			a[hyperv1.MachineHealthCheckTimeoutAnnotation] = value
			o.SetAnnotations(a)
		}
	}

	withMaxUnhealthyOverride := func(value string) func(client.Object) {
		return func(o client.Object) {
			a := o.GetAnnotations()
			if a == nil {
				a = map[string]string{}
			}
			a[hyperv1.MachineHealthCheckMaxUnhealthyAnnotation] = value
			o.SetAnnotations(a)
		}
	}
	withMaxUnhealthy := func(value string) func(*capiv1.MachineHealthCheck) {
		return func(mhc *capiv1.MachineHealthCheck) {
			maxUnhealthy := intstr.Parse(value)
			mhc.Spec.MaxUnhealthy = &maxUnhealthy
		}
	}
	withTimeout := func(d time.Duration) func(*capiv1.MachineHealthCheck) {
		return func(mhc *capiv1.MachineHealthCheck) {
			for i := range mhc.Spec.UnhealthyConditions {
				mhc.Spec.UnhealthyConditions[i].Timeout = metav1.Duration{Duration: d}
			}
		}
	}

	tests := []struct {
		name     string
		hc       *hyperv1.HostedCluster
		np       *hyperv1.NodePool
		expected *capiv1.MachineHealthCheck
	}{
		{
			name:     "defaults",
			hc:       hostedcluster(),
			np:       nodepool(),
			expected: healthcheck(),
		},
		{
			name:     "timeout override in hc",
			hc:       hostedcluster(withTimeoutOverride("10m")),
			np:       nodepool(),
			expected: healthcheck(withTimeout(10 * time.Minute)),
		},
		{
			name:     "timeout override in np",
			hc:       hostedcluster(),
			np:       nodepool(withTimeoutOverride("40m")),
			expected: healthcheck(withTimeout(40 * time.Minute)),
		},
		{
			name:     "timeout override in both, np takes precedence",
			hc:       hostedcluster(withTimeoutOverride("10m")),
			np:       nodepool(withTimeoutOverride("40m")),
			expected: healthcheck(withTimeout(40 * time.Minute)),
		},
		{
			name:     "invalid timeout override, retains default",
			hc:       hostedcluster(withTimeoutOverride("foo")),
			np:       nodepool(),
			expected: healthcheck(),
		},
		{
			name:     "maxunhealthy override in hc",
			hc:       hostedcluster(withMaxUnhealthyOverride("10%")),
			np:       nodepool(),
			expected: healthcheck(withMaxUnhealthy("10%")),
		},
		{
			name:     "maxunhealthy override in np",
			hc:       hostedcluster(),
			np:       nodepool(withMaxUnhealthyOverride("5")),
			expected: healthcheck(withMaxUnhealthy("5")),
		},
		{
			name:     "maxunhealthy override in both, np takes precedence",
			hc:       hostedcluster(withMaxUnhealthyOverride("10%")),
			np:       nodepool(withMaxUnhealthyOverride("5")),
			expected: healthcheck(withMaxUnhealthy("5")),
		},
		{
			name:     "invalid maxunhealthy override value, default is preserved",
			hc:       hostedcluster(),
			np:       nodepool(withMaxUnhealthyOverride("foo")),
			expected: healthcheck(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			r := &NodePoolReconciler{}
			mhc := &capiv1.MachineHealthCheck{}
			r.reconcileMachineHealthCheck(context.Background(), mhc, tt.np, tt.hc, "cluster")
			g.Expect(mhc.Spec).To(testutil.MatchExpected(tt.expected.Spec))
		})
	}
}

func TestSecretJanitor_Reconcile(t *testing.T) {
	ctx := ctrl.LoggerInto(context.Background(), zapr.NewLogger(zaptest.NewLogger(t)))

	theTime, err := time.Parse(time.RFC3339Nano, "2006-01-02T15:04:05.999999999Z")
	if err != nil {
		t.Fatalf("could not parse time: %v", err)
	}
	fakeClock := testingclock.NewFakeClock(theTime)

	pullSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "pull-secret", Namespace: "myns"},
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: []byte("whatever"),
		},
	}

	hostedCluster := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-name", Namespace: "myns"},
		Spec: hyperv1.HostedClusterSpec{
			PullSecret: corev1.LocalObjectReference{Name: pullSecret.Name},
		},
	}

	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "nodepool-name", Namespace: "myns"},
		Spec: hyperv1.NodePoolSpec{
			ClusterName: hostedCluster.Name,
			Release:     hyperv1.Release{Image: "fake-release-image"},
			Config: []corev1.LocalObjectReference{
				{Name: "machineconfig-1"},
			},
		},
		Status: hyperv1.NodePoolStatus{Version: supportedversion.LatestSupportedVersion.String()},
	}

	coreMachineConfig := `
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

	machineConfig := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "machineconfig-1",
			Namespace: "myns",
		},
		Data: map[string]string{
			TokenSecretConfigKey: coreMachineConfig,
		},
	}

	ignitionConfig := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "core-machineconfig",
			Namespace: "myns-cluster-name",
			Labels: map[string]string{
				nodePoolCoreIgnitionConfigLabel: "true",
			},
		},
		Data: map[string]string{
			TokenSecretConfigKey: coreMachineConfig,
		},
	}
	ignitionConfig2 := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "core-machineconfig-2",
			Namespace: "myns-cluster-name",
			Labels: map[string]string{
				nodePoolCoreIgnitionConfigLabel: "true",
			},
		},
		Data: map[string]string{
			TokenSecretConfigKey: coreMachineConfig,
		},
	}
	ignitionConfig3 := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "core-machineconfig-3",
			Namespace: "myns-cluster-name",
			Labels: map[string]string{
				nodePoolCoreIgnitionConfigLabel: "true",
			},
		},
		Data: map[string]string{
			TokenSecretConfigKey: coreMachineConfig,
		},
	}

	c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(
		nodePool,
		hostedCluster,
		pullSecret,
		machineConfig,
		ignitionConfig,
		ignitionConfig2,
		ignitionConfig3,
	).Build()
	r := secretJanitor{
		NodePoolReconciler: &NodePoolReconciler{
			Client:          c,
			ReleaseProvider: &fakereleaseprovider.FakeReleaseProvider{Version: supportedversion.LatestSupportedVersion.String()},
			ImageMetadataProvider: &fakeimagemetadataprovider.FakeImageMetadataProvider{Result: &dockerv1client.DockerImageConfig{Config: &docker10.DockerConfig{
				Labels: map[string]string{},
			}}},
		},

		now: fakeClock.Now,
	}

	for _, testCase := range []struct {
		name     string
		input    *corev1.Secret
		expected *corev1.Secret
	}{
		{
			name: "unrelated secret untouched",
			input: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "whatever",
					Namespace: "myns",
				},
			},
			expected: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "whatever",
					Namespace: "myns",
				},
			},
		},
		{
			name: "related but not known secret untouched",
			input: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "related",
					Namespace: "myns",
					Annotations: map[string]string{
						nodePoolAnnotation: client.ObjectKeyFromObject(nodePool).String(),
					},
				},
			},
			expected: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "related",
					Namespace: "myns",
					Annotations: map[string]string{
						nodePoolAnnotation: client.ObjectKeyFromObject(nodePool).String(),
					},
				},
			},
		},
		{
			name: "related known secret with correct hash untouched",
			input: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "token-nodepool-name-da03707e",
					Namespace: "myns",
					Annotations: map[string]string{
						nodePoolAnnotation: client.ObjectKeyFromObject(nodePool).String(),
					},
				},
			},
			expected: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "token-nodepool-name-da03707e",
					Namespace: "myns",
					Annotations: map[string]string{
						nodePoolAnnotation: client.ObjectKeyFromObject(nodePool).String(),
					},
				},
			},
		},
		{
			name: "related token secret with incorrect hash set for expiry",
			input: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "token-nodepool-name-jsadfkjh23",
					Namespace: "myns",
					Annotations: map[string]string{
						nodePoolAnnotation: client.ObjectKeyFromObject(nodePool).String(),
					},
				},
			},
			expected: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "token-nodepool-name-jsadfkjh23",
					Namespace: "myns",
					Annotations: map[string]string{
						nodePoolAnnotation: client.ObjectKeyFromObject(nodePool).String(),
						"hypershift.openshift.io/ignition-token-expiration-timestamp": "2006-01-02T17:04:05Z",
					},
				},
			},
		},
		{
			name: "related ignition user data secret with incorrect hash deleted",
			input: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "user-data-nodepool-name-jsadfkjh23",
					Namespace: "myns",
					Annotations: map[string]string{
						nodePoolAnnotation: client.ObjectKeyFromObject(nodePool).String(),
					},
				},
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if err := c.Create(ctx, testCase.input); err != nil {
				t.Errorf("failed to create object: %v", err)
			}

			key := client.ObjectKeyFromObject(testCase.input)
			if _, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key}); err != nil {
				t.Errorf("failed to reconcile object: %v", err)
			}

			got := &corev1.Secret{}
			err := c.Get(ctx, client.ObjectKeyFromObject(testCase.input), got)
			if testCase.expected == nil {
				if !apierrors.IsNotFound(err) {
					t.Errorf("expected object to not exist, got error: %v", err)
				}
			} else {
				if err != nil {
					t.Errorf("failed to fetch object: %v", err)
				}
				if diff := cmp.Diff(got, testCase.expected, cmpopts.IgnoreFields(metav1.ObjectMeta{}, "ResourceVersion")); diff != "" {
					t.Errorf("got unexpected object after reconcile: %v", diff)
				}
			}
		})
	}
}

func TestShouldKeepOldUserData(t *testing.T) {
	pullSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "pull-secret", Namespace: "test"},
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: []byte("whatever"),
		},
	}

	testCases := []struct {
		name            string
		hc              *hyperv1.HostedCluster
		releaseProvider releaseinfo.Provider
		expected        bool
	}{
		{
			name: "when hosted cluster is not aws it should NOT keep old user data",
			hc: &hyperv1.HostedCluster{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: pullSecret.Namespace,
				},
				Spec: hyperv1.HostedClusterSpec{
					PullSecret: corev1.LocalObjectReference{
						Name: pullSecret.Name,
					},
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AgentPlatform,
					},
					Release: hyperv1.Release{
						Image: "fake-release-image",
					},
				},
				Status: hyperv1.HostedClusterStatus{},
			},
			expected: false,
		},
		{
			name: "when hosted cluster is less than 4.16 it should keep user data",
			hc: &hyperv1.HostedCluster{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: pullSecret.Namespace,
				},
				Spec: hyperv1.HostedClusterSpec{
					PullSecret: corev1.LocalObjectReference{
						Name: pullSecret.Name,
					},
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
					},
					Release: hyperv1.Release{
						Image: "fake-release-image",
					},
				},
				Status: hyperv1.HostedClusterStatus{},
			},
			releaseProvider: &fakereleaseprovider.FakeReleaseProvider{Version: semver.MustParse("4.15.0").String()},
			expected:        true,
		},
		{
			name: "when hosted cluster is equal or greater than 4.16 it should NOT keep user data",
			hc: &hyperv1.HostedCluster{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: pullSecret.Namespace,
				},
				Spec: hyperv1.HostedClusterSpec{
					PullSecret: corev1.LocalObjectReference{
						Name: pullSecret.Name,
					},
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
					},
					Release: hyperv1.Release{
						Image: "fake-release-image",
					},
				},
			},
			releaseProvider: &fakereleaseprovider.FakeReleaseProvider{Version: semver.MustParse("4.16.0").String()},
			expected:        false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(
				pullSecret,
			).Build()

			r := &NodePoolReconciler{
				Client:          c,
				ReleaseProvider: tc.releaseProvider,
			}

			shouldKeepOldUserData, err := r.shouldKeepOldUserData(context.Background(), tc.hc)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(shouldKeepOldUserData).To(Equal(tc.expected))
		})
	}
}

func TestSetPerformanceProfileStatus(t *testing.T) {
	controlPlaneNamespace := "clusters-hostedcluster01"
	userClustersNamespace := "clusters"
	nodePoolName := "hostedcluster01"

	testCases := []struct {
		name                         string
		PerformanceProfileStatusCM   *corev1.ConfigMap
		wantConditions               map[string]hyperv1.NodePoolCondition
		hasPerformanceProfileApplied bool
	}{

		{
			name:                         "No Performance profile applied",
			PerformanceProfileStatusCM:   &corev1.ConfigMap{},
			wantConditions:               map[string]hyperv1.NodePoolCondition{},
			hasPerformanceProfileApplied: false,
		},

		{
			name: "Performance profile is available",
			PerformanceProfileStatusCM: &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ConfigMap",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "perfprofile-" + nodePoolName + "-status",
					Namespace: controlPlaneNamespace,
					Labels: map[string]string{
						"hypershift.openshift.io/nto-generated-performance-profile-status": "true",
						"hypershift.openshift.io/nodePool":                                 nodePoolName,
						"hypershift.openshift.io/performanceProfileName":                   nodePoolName,
					},
					Annotations: map[string]string{
						"hypershift.openshift.io/nodePool": nodePoolName,
					},
				},
				Data: map[string]string{
					"status": makePerformanceProfileStatusAsString(
						withCondition(crconditionsv1.Condition{
							Type:   crconditionsv1.ConditionAvailable,
							Status: corev1.ConditionTrue,
						}),
						withCondition(crconditionsv1.Condition{
							Type:   crconditionsv1.ConditionUpgradeable,
							Status: corev1.ConditionTrue,
						})),
				},
			},
			wantConditions: map[string]hyperv1.NodePoolCondition{
				hyperv1.NodePoolPerformanceProfileTuningAvailableConditionType: {
					Type:    hyperv1.NodePoolPerformanceProfileTuningAvailableConditionType,
					Status:  corev1.ConditionTrue,
					Message: "",
					Reason:  "",
				},
				hyperv1.NodePoolPerformanceProfileTuningProgressingConditionType: {
					Type:    hyperv1.NodePoolPerformanceProfileTuningProgressingConditionType,
					Status:  corev1.ConditionFalse,
					Message: "",
					Reason:  "",
				},
				hyperv1.NodePoolPerformanceProfileTuningUpgradeableConditionType: {
					Type:    hyperv1.NodePoolPerformanceProfileTuningUpgradeableConditionType,
					Status:  corev1.ConditionTrue,
					Message: "",
					Reason:  "",
				},
				hyperv1.NodePoolPerformanceProfileTuningDegradedConditionType: {
					Type:    hyperv1.NodePoolPerformanceProfileTuningDegradedConditionType,
					Status:  corev1.ConditionFalse,
					Message: "",
					Reason:  "",
				},
			},
			hasPerformanceProfileApplied: true,
		},
		{
			name: "Performance profile is progressing",
			PerformanceProfileStatusCM: &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ConfigMap",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "perfprofile-" + nodePoolName + "-status",
					Namespace: controlPlaneNamespace,
					Labels: map[string]string{
						"hypershift.openshift.io/nto-generated-performance-profile-status": "true",
						"hypershift.openshift.io/nodePool":                                 nodePoolName,
						"hypershift.openshift.io/performanceProfileName":                   nodePoolName,
					},
					Annotations: map[string]string{
						"hypershift.openshift.io/nodePool": nodePoolName,
					},
				},
				Data: map[string]string{
					"status": makePerformanceProfileStatusAsString(
						withCondition(crconditionsv1.Condition{
							Type:    crconditionsv1.ConditionProgressing,
							Status:  corev1.ConditionTrue,
							Reason:  "DeploymentStarting",
							Message: "Deployment is starting",
						})),
				},
			},
			wantConditions: map[string]hyperv1.NodePoolCondition{
				hyperv1.NodePoolPerformanceProfileTuningAvailableConditionType: {
					Type:    hyperv1.NodePoolPerformanceProfileTuningAvailableConditionType,
					Status:  corev1.ConditionFalse,
					Message: "",
					Reason:  "",
				},
				hyperv1.NodePoolPerformanceProfileTuningProgressingConditionType: {
					Type:    hyperv1.NodePoolPerformanceProfileTuningProgressingConditionType,
					Status:  corev1.ConditionTrue,
					Reason:  "DeploymentStarting",
					Message: "Deployment is starting",
				},
				hyperv1.NodePoolPerformanceProfileTuningUpgradeableConditionType: {
					Type:    hyperv1.NodePoolPerformanceProfileTuningUpgradeableConditionType,
					Status:  corev1.ConditionFalse,
					Message: "",
					Reason:  "",
				},
				hyperv1.NodePoolPerformanceProfileTuningDegradedConditionType: {
					Type:    hyperv1.NodePoolPerformanceProfileTuningDegradedConditionType,
					Status:  corev1.ConditionFalse,
					Message: "",
					Reason:  "",
				},
			},
			hasPerformanceProfileApplied: true,
		},
		{
			name: "Performance profile is degraded",
			PerformanceProfileStatusCM: &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ConfigMap",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "perfprofile-" + nodePoolName + "-status",
					Namespace: controlPlaneNamespace,
					Labels: map[string]string{
						"hypershift.openshift.io/nto-generated-performance-profile-status": "true",
						"hypershift.openshift.io/nodePool":                                 nodePoolName,
						"hypershift.openshift.io/performanceProfileName":                   nodePoolName,
					},
					Annotations: map[string]string{
						"hypershift.openshift.io/nodePool": nodePoolName,
					},
				},
				Data: map[string]string{
					"status": makePerformanceProfileStatusAsString(
						withCondition(crconditionsv1.Condition{
							Type:    crconditionsv1.ConditionDegraded,
							Status:  corev1.ConditionTrue,
							Message: "Cannot list Tuned Profiles to match with profile perfprofile-hostedcluster01",
							Reason:  "GettingTunedStatusFailed",
						})),
				},
			},
			wantConditions: map[string]hyperv1.NodePoolCondition{
				hyperv1.NodePoolPerformanceProfileTuningAvailableConditionType: {
					Type:    hyperv1.NodePoolPerformanceProfileTuningAvailableConditionType,
					Status:  corev1.ConditionFalse,
					Message: "",
					Reason:  "",
				},
				hyperv1.NodePoolPerformanceProfileTuningProgressingConditionType: {
					Type:    hyperv1.NodePoolPerformanceProfileTuningProgressingConditionType,
					Status:  corev1.ConditionFalse,
					Message: "",
					Reason:  "",
				},
				hyperv1.NodePoolPerformanceProfileTuningUpgradeableConditionType: {
					Type:    hyperv1.NodePoolPerformanceProfileTuningUpgradeableConditionType,
					Status:  corev1.ConditionFalse,
					Message: "",
					Reason:  "",
				},
				hyperv1.NodePoolPerformanceProfileTuningDegradedConditionType: {
					Type:    hyperv1.NodePoolPerformanceProfileTuningDegradedConditionType,
					Status:  corev1.ConditionTrue,
					Message: "Cannot list Tuned Profiles to match with profile perfprofile-hostedcluster01",
					Reason:  "GettingTunedStatusFailed",
				},
			},
			hasPerformanceProfileApplied: true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			r := NodePoolReconciler{
				Client: fake.NewClientBuilder().Build(),
			}
			nodePool := &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{Name: nodePoolName, Namespace: userClustersNamespace},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: nodePoolName,
				},
			}
			performanceProfileConditions := []string{
				hyperv1.NodePoolPerformanceProfileTuningAvailableConditionType,
				hyperv1.NodePoolPerformanceProfileTuningProgressingConditionType,
				hyperv1.NodePoolPerformanceProfileTuningUpgradeableConditionType,
				hyperv1.NodePoolPerformanceProfileTuningDegradedConditionType,
			}

			ctx := context.Background()

			// In case performance profile is applied, a config map holding the performance profile status is generated
			// by NTO should exist on the hosted control plane namespace.
			if tc.hasPerformanceProfileApplied {
				r.Create(ctx, tc.PerformanceProfileStatusCM)
			}
			err := r.SetPerformanceProfileConditions(ctx, logr.Discard(), nodePool, controlPlaneNamespace, false)
			g.Expect(err).ToNot(HaveOccurred())

			// In case there is no performance profile applied, no configmap with status is expected.
			// Therefore, we expect the nodepool conditions to have no performance profile conditions.
			if !tc.hasPerformanceProfileApplied {
				for _, NodePoolCondition := range performanceProfileConditions {
					cond := FindStatusCondition(nodePool.Status.Conditions, NodePoolCondition)
					g.Expect(cond).To(BeNil())
				}
				return
			}

			for _, NodePoolCondition := range performanceProfileConditions {
				gotCondition := FindStatusCondition(nodePool.Status.Conditions, NodePoolCondition)
				wantCondition := tc.wantConditions[NodePoolCondition]
				g.Expect(gotCondition).ToNot(BeNil(), "expected condition %s to be found under the NodePool status", NodePoolCondition)
				g.Expect(gotCondition.Status).To(Equal(wantCondition.Status), "got condition %s status equals to %s, want %s", gotCondition.Type, gotCondition.Status, wantCondition.Status)
				g.Expect(gotCondition.Message).To(Equal(wantCondition.Message), "got condition %s message equals to %s, want %s", gotCondition.Type, gotCondition.Message, wantCondition.Message)
				g.Expect(gotCondition.Reason).To(Equal(wantCondition.Reason), "got condition %s reason equals to %s, want %s", gotCondition.Type, gotCondition.Reason, wantCondition.Reason)
			}
		})
	}
}

func makePerformanceProfileStatusAsString(opts ...func(*performanceprofilev2.PerformanceProfileStatus)) string {
	status := &performanceprofilev2.PerformanceProfileStatus{
		Conditions: []crconditionsv1.Condition{
			{
				Type:   "Available",
				Status: "False",
			},
			{
				Type:   "Upgradeable",
				Status: "False",
			},
			{
				Type:   "Progressing",
				Status: "False",
			},
			{
				Type:   "Degraded",
				Status: "False",
			},
		},
		Tuned:        k8sutilspointer.String("openshift-cluster-node-tuning-operator/openshift-node-performance-performance"),
		RuntimeClass: k8sutilspointer.String("performance-performance"),
	}

	for _, f := range opts {
		f(status)
	}
	// A test code we fully control of,
	// hence no error should be return
	b, _ := yaml.Marshal(status)
	return string(b)
}

func withCondition(condition crconditionsv1.Condition) func(*performanceprofilev2.PerformanceProfileStatus) {
	return func(status *performanceprofilev2.PerformanceProfileStatus) {
		for i := range status.Conditions {
			if status.Conditions[i].Type == condition.Type {
				status.Conditions[i] = condition
			}
		}
	}
}

func Test_validateHCPayloadSupportsNodePoolCPUArch(t *testing.T) {
	testCases := []struct {
		name        string
		hc          *hyperv1.HostedCluster
		np          *hyperv1.NodePool
		expectedErr bool
	}{
		{
			name: "payload is multi",
			hc: &hyperv1.HostedCluster{
				Status: hyperv1.HostedClusterStatus{
					PayloadArch: hyperv1.Multi,
				},
			},
			expectedErr: false,
		},
		{
			name: "payload is amd64; np is amd64",
			hc: &hyperv1.HostedCluster{
				Status: hyperv1.HostedClusterStatus{
					PayloadArch: hyperv1.AMD64,
				},
			},
			np: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Arch: hyperv1.ArchitectureAMD64,
				},
			},
			expectedErr: false,
		},
		{
			name: "payload is amd64; np is arm64",
			hc: &hyperv1.HostedCluster{
				Status: hyperv1.HostedClusterStatus{
					PayloadArch: hyperv1.AMD64,
				},
			},
			np: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Arch: hyperv1.ArchitectureARM64,
				},
			},
			expectedErr: true,
		},
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			err := validateHCPayloadSupportsNodePoolCPUArch(tt.hc, tt.np)
			if tt.expectedErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err).To(Not(BeNil()))
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
		})
	}
}
