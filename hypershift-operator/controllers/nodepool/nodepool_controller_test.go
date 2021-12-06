package nodepool

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
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
					NodeCount: pointer.Int32Ptr(1),
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

	namespace := "test"
	testCases := []struct {
		name                        string
		nodePool                    *hyperv1.NodePool
		config                      []client.Object
		expectedCoreConfigResources int
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
			expect: machineConfig1,
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
			expect: machineConfig1 + "\n---\n" + machineConfig2,
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
			expect: "",
			error:  true,
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
			expect:                      coreMachineConfig1 + "\n---\n" + machineConfig1,
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
			expect:                      coreMachineConfig1 + "\n---\n" + machineConfig1,
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
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			r := NodePoolReconciler{
				Client: fake.NewClientBuilder().WithObjects(tc.config...).Build(),
			}
			got, missingConfigs, err := r.getConfig(context.Background(), tc.nodePool, tc.expectedCoreConfigResources, namespace)
			if tc.error {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(missingConfigs).To(Equal(tc.missingConfigs))
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(got).To(Equal(tc.expect))
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
					NodeCount: pointer.Int32Ptr(5),
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
					Replicas: pointer.Int32Ptr(3),
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
					Replicas: pointer.Int32Ptr(0),
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
