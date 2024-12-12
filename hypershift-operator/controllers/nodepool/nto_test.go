package nodepool

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	. "github.com/onsi/gomega"
	performanceprofilev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	crconditionsv1 "github.com/openshift/custom-resource-status/conditions/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/upsert"
	supportutil "github.com/openshift/hypershift/support/util"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/yaml"
)

func withCondition(condition crconditionsv1.Condition) func(*performanceprofilev2.PerformanceProfileStatus) {
	return func(status *performanceprofilev2.PerformanceProfileStatus) {
		for i := range status.Conditions {
			if status.Conditions[i].Type == condition.Type {
				status.Conditions[i] = condition
			}
		}
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

	kubeletConfig1 := `
    apiVersion: machineconfiguration.openshift.io/v1
    kind: KubeletConfig
    metadata:
      name: set-max-pods
    spec:
      kubeletConfig:
        maxPods: 100
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
		expectedError           bool
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
					Immutable: ptr.To(true),
					ObjectMeta: metav1.ObjectMeta{
						Name:      supportutil.ShortenName("foo", npName, validation.LabelValueMaxLength),
						Namespace: hcpNamespace,
						Labels: map[string]string{
							NTOMirroredConfigLabel:               "true",
							nodePoolAnnotation:                   npName,
							ContainerRuntimeConfigConfigMapLabel: "",
						},
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
					Immutable: ptr.To(true),
					ObjectMeta: metav1.ObjectMeta{
						Name:      supportutil.ShortenName("foo", npName, validation.LabelValueMaxLength),
						Namespace: hcpNamespace,
						Labels: map[string]string{
							NTOMirroredConfigLabel:               "true",
							nodePoolAnnotation:                   npName,
							ContainerRuntimeConfigConfigMapLabel: "",
						},
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
		{
			name:                  "with kubeletconfig objects",
			nodePool:              np,
			controlPlaneNamespace: hcpNamespace,
			configsToBeMirrored: []*MirrorConfig{
				{
					ConfigMap: &corev1.ConfigMap{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "bar",
							Namespace: npNamespace,
						},
						Data: map[string]string{
							TokenSecretConfigKey: kubeletConfig1,
						},
					},
					Labels: map[string]string{
						KubeletConfigConfigMapLabel: "true",
					},
				},
			},
			existingConfigsInHcpNs: nil,
			expectedMirroredConfigs: []corev1.ConfigMap{
				{
					Immutable: ptr.To(true),
					ObjectMeta: metav1.ObjectMeta{
						Name:      supportutil.ShortenName("bar", npName, validation.LabelValueMaxLength),
						Namespace: hcpNamespace,
						Labels: map[string]string{
							NTOMirroredConfigLabel:      "true",
							nodePoolAnnotation:          npName,
							KubeletConfigConfigMapLabel: "true",
						},
					},
					Data: map[string]string{
						TokenSecretConfigKey: kubeletConfig1,
					},
				},
			},
		},
		{
			name:                  "negative: with multiple kubeletconfig objects expect validation error",
			nodePool:              np,
			controlPlaneNamespace: hcpNamespace,
			configsToBeMirrored: []*MirrorConfig{
				{
					ConfigMap: &corev1.ConfigMap{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "bar",
							Namespace: npNamespace,
						},
						Data: map[string]string{
							TokenSecretConfigKey: kubeletConfig1,
						},
					},
					Labels: map[string]string{
						KubeletConfigConfigMapLabel: "true",
					},
				},
			},
			existingConfigsInHcpNs: []client.Object{
				&corev1.ConfigMap{
					Immutable: ptr.To(true),
					ObjectMeta: metav1.ObjectMeta{
						Name:      supportutil.ShortenName("bar-2", npName, validation.LabelValueMaxLength),
						Namespace: hcpNamespace,
						Labels: map[string]string{
							nodeTuningGeneratedConfigLabel: "true",
							nodePoolAnnotation:             npName,
							KubeletConfigConfigMapLabel:    "true",
						},
					},
					Data: map[string]string{
						TokenSecretConfigKey: kubeletConfig1,
					},
				},
			},
			expectedError: true,
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
			if tc.expectedError {
				g.Expect(err).To(HaveOccurred())
				return
			}
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
		Tuned:        ptr.To("openshift-cluster-node-tuning-operator/openshift-node-performance-performance"),
		RuntimeClass: ptr.To("performance-performance"),
	}

	for _, f := range opts {
		f(status)
	}
	// A test code we fully control of,
	// hence no error should be return
	b, _ := yaml.Marshal(status)
	return string(b)
}

func TestGetMirrorConfigForManifest(t *testing.T) {
	machineConfig := `
apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  labels:
    machineconfiguration.openshift.io/role: worker
  name: valid-machineconfig
spec:
  config:
    ignition:
      version: 2.2.0
    storage:
      files:
        - contents:
            source: data:text/plain;base64,dGhyb3dhd2F5Cg==
          filesystem: root
          mode: 493
          path: /some/path
`

	containerRuntimeConfig := `
apiVersion: machineconfiguration.openshift.io/v1
kind: ContainerRuntimeConfig
metadata:
  name: valid-containerruntimeconfig
spec:
  containerRuntimeConfig:
    defaultRuntime: crun
`

	kubeletConfig := `
apiVersion: machineconfiguration.openshift.io/v1
kind: KubeletConfig
metadata:
  name: valid-kubeletconfig
spec:
  kubeletConfig:
    maxPods: 100
`

	imageDigestMirrorSet := `
apiVersion: config.openshift.io/v1
kind: ImageDigestMirrorSet
metadata:
  name: valid-idms
spec:
  imageDigestMirrors:
    - mirrorSourcePolicy: AllowContactingSource
      mirrors:
        - some.registry.io/registry-redhat-io
      source: registry.redhat.io
`

	testCases := []struct {
		name  string
		input []byte
	}{
		{
			name:  "Valid MachineConfig",
			input: []byte(machineConfig),
		},
		{
			name:  "Valid ContainerRuntimeConfig",
			input: []byte(containerRuntimeConfig),
		},
		{
			name:  "Valid KubeletConfig",
			input: []byte(kubeletConfig),
		},
		{
			name:  "Valid ImageDigestMirrorSet",
			input: []byte(imageDigestMirrorSet),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			_, err := getMirrorConfigForManifest(tc.input)
			g.Expect(err).To(BeNil())
		})
	}
}
