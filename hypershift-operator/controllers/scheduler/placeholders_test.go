package scheduler

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	schedulingv1alpha1 "github.com/openshift/hypershift/api/scheduling/v1alpha1"
	"go.uber.org/zap/zapcore"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	appsv1applyconfigurations "k8s.io/client-go/applyconfigurations/apps/v1"
	metav1applyconfigurations "k8s.io/client-go/applyconfigurations/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func TestDeploymentName(t *testing.T) {
	want := 3
	got, err := parseIndex("small", deploymentName("small", want))
	if err != nil {
		t.Fatalf("expected no error but got one: %v", err)
	}
	if got != want {
		t.Fatalf("incorrect index returned, wanted %d, got %d", want, got)
	}
}

func TestPlaceholderCreator_Reconcile(t *testing.T) {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true), zap.JSONEncoder(func(o *zapcore.EncoderConfig) {
		o.EncodeTime = zapcore.RFC3339TimeEncoder
	})))
	ctx := ctrl.LoggerInto(context.Background(), ctrl.Log)

	validConfigStatus := schedulingv1alpha1.ClusterSizingConfigurationStatus{
		Conditions: []metav1.Condition{{Type: schedulingv1alpha1.ClusterSizingConfigurationValidType, Status: metav1.ConditionTrue}},
	}

	for _, testCase := range []struct {
		name   string
		config *schedulingv1alpha1.ClusterSizingConfiguration

		listDeployments func(context.Context, ...client.ListOption) (*appsv1.DeploymentList, error)
		listConfigMaps  func(context.Context, ...client.ListOption) (*corev1.ConfigMapList, error)

		expected    *appsv1applyconfigurations.DeploymentApplyConfiguration
		expectedErr bool
	}{
		{
			name: "invalid config, do nothing",
			config: &schedulingv1alpha1.ClusterSizingConfiguration{
				Status: schedulingv1alpha1.ClusterSizingConfigurationStatus{
					Conditions: []metav1.Condition{{Type: schedulingv1alpha1.ClusterSizingConfigurationValidType, Status: metav1.ConditionFalse}},
				},
			},
		},
		{
			name: "no placeholders necessary, do nothing",
			config: &schedulingv1alpha1.ClusterSizingConfiguration{
				Spec: schedulingv1alpha1.ClusterSizingConfigurationSpec{
					Sizes: []schedulingv1alpha1.SizeConfiguration{
						{Name: "small", Management: &schedulingv1alpha1.Management{Placeholders: 0}},
						{Name: "medium"},
					},
				},
				Status: validConfigStatus,
			},
		},
		{
			name: "some placeholders necessary, none exist, create first",
			config: &schedulingv1alpha1.ClusterSizingConfiguration{
				Spec: schedulingv1alpha1.ClusterSizingConfigurationSpec{
					Sizes: []schedulingv1alpha1.SizeConfiguration{
						{Name: "small", Management: &schedulingv1alpha1.Management{Placeholders: 2}},
					},
				},
				Status: validConfigStatus,
			},
			listDeployments: func(_ context.Context, _ ...client.ListOption) (*appsv1.DeploymentList, error) {
				return &appsv1.DeploymentList{Items: []appsv1.Deployment{}}, nil
			},
			listConfigMaps: func(_ context.Context, _ ...client.ListOption) (*corev1.ConfigMapList, error) {
				return &corev1.ConfigMapList{Items: []corev1.ConfigMap{}}, nil
			},
			expected: newDeployment(placeholderNamespace, "small", 0, []string{}),
		},
		{
			name: "some placeholders necessary, some exist, create next",
			config: &schedulingv1alpha1.ClusterSizingConfiguration{
				Spec: schedulingv1alpha1.ClusterSizingConfigurationSpec{
					Sizes: []schedulingv1alpha1.SizeConfiguration{
						{Name: "small", Management: &schedulingv1alpha1.Management{Placeholders: 2}},
					},
				},
				Status: validConfigStatus,
			},
			listDeployments: func(_ context.Context, _ ...client.ListOption) (*appsv1.DeploymentList, error) {
				return &appsv1.DeploymentList{Items: []appsv1.Deployment{
					{ObjectMeta: metav1.ObjectMeta{Name: "placeholder-small-0", Labels: map[string]string{"hypershift.openshift.io/hosted-cluster-size": "small"}}},
				}}, nil
			},
			listConfigMaps: func(_ context.Context, _ ...client.ListOption) (*corev1.ConfigMapList, error) {
				return &corev1.ConfigMapList{Items: []corev1.ConfigMap{}}, nil
			},
			expected: newDeployment(placeholderNamespace, "small", 1, []string{}),
		},
		{
			name: "some placeholders necessary, some exist, create missing",
			config: &schedulingv1alpha1.ClusterSizingConfiguration{
				Spec: schedulingv1alpha1.ClusterSizingConfigurationSpec{
					Sizes: []schedulingv1alpha1.SizeConfiguration{
						{Name: "small", Management: &schedulingv1alpha1.Management{Placeholders: 2}},
					},
				},
				Status: validConfigStatus,
			},
			listDeployments: func(_ context.Context, _ ...client.ListOption) (*appsv1.DeploymentList, error) {
				return &appsv1.DeploymentList{Items: []appsv1.Deployment{
					{ObjectMeta: metav1.ObjectMeta{Name: "placeholder-small-1", Labels: map[string]string{"hypershift.openshift.io/hosted-cluster-size": "small"}}},
				}}, nil
			},
			listConfigMaps: func(_ context.Context, _ ...client.ListOption) (*corev1.ConfigMapList, error) {
				return &corev1.ConfigMapList{Items: []corev1.ConfigMap{}}, nil
			},
			expected: newDeployment(placeholderNamespace, "small", 0, []string{}),
		},
		{
			name: "some placeholders necessary, all exist, do nothing",
			config: &schedulingv1alpha1.ClusterSizingConfiguration{
				Spec: schedulingv1alpha1.ClusterSizingConfigurationSpec{
					Sizes: []schedulingv1alpha1.SizeConfiguration{
						{Name: "small", Management: &schedulingv1alpha1.Management{Placeholders: 2}},
					},
				},
				Status: validConfigStatus,
			},
			listDeployments: func(_ context.Context, _ ...client.ListOption) (*appsv1.DeploymentList, error) {
				return &appsv1.DeploymentList{Items: []appsv1.Deployment{
					{ObjectMeta: metav1.ObjectMeta{Name: "placeholder-small-0", Labels: map[string]string{"hypershift.openshift.io/hosted-cluster-size": "small"}}},
					{ObjectMeta: metav1.ObjectMeta{Name: "placeholder-small-1", Labels: map[string]string{"hypershift.openshift.io/hosted-cluster-size": "small"}}},
				}}, nil
			},
			listConfigMaps: func(_ context.Context, _ ...client.ListOption) (*corev1.ConfigMapList, error) {
				return &corev1.ConfigMapList{Items: []corev1.ConfigMap{}}, nil
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			r := &placeholderCreator{
				placeholderLister: &placeholderLister{
					listDeployments: testCase.listDeployments,
					listConfigMaps:  testCase.listConfigMaps,
				},
			}
			action, err := r.reconcile(ctx, testCase.config)
			if err == nil && testCase.expectedErr {
				t.Fatalf("expected an error but got none")
			}
			if err != nil && !testCase.expectedErr {
				t.Fatalf("expected no error but got one: %v", err)
			}
			if diff := cmp.Diff(action, testCase.expected, compareDeploymentApplyConfiguration()...); diff != "" {
				t.Fatalf("got incorrect action: %v", diff)
			}
		})
	}
}

func TestPlaceholderUpdater_Reconcile(t *testing.T) {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true), zap.JSONEncoder(func(o *zapcore.EncoderConfig) {
		o.EncodeTime = zapcore.RFC3339TimeEncoder
	})))
	ctx := ctrl.LoggerInto(context.Background(), ctrl.Log)

	validConfigStatus := schedulingv1alpha1.ClusterSizingConfigurationStatus{
		Conditions: []metav1.Condition{{Type: schedulingv1alpha1.ClusterSizingConfigurationValidType, Status: metav1.ConditionTrue}},
	}

	for _, testCase := range []struct {
		name       string
		config     *schedulingv1alpha1.ClusterSizingConfiguration
		deployment *appsv1.Deployment

		listConfigMaps func(context.Context, ...client.ListOption) (*corev1.ConfigMapList, error)

		delete      bool
		expected    *appsv1applyconfigurations.DeploymentApplyConfiguration
		expectedErr bool
	}{
		{
			name: "non-placeholder deployment, do nothing",
			deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						hypershiftv1beta1.HostedClusterSizeLabel: "small",
					},
				},
			},
		},
		{
			name: "placeholder deployment without size, do nothing",
			deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						PlaceholderLabel: "true",
					},
				},
			},
		},
		{
			name: "invalid config, do nothing",
			deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						PlaceholderLabel:                         "true",
						hypershiftv1beta1.HostedClusterSizeLabel: "small",
					},
				},
			},
			config: &schedulingv1alpha1.ClusterSizingConfiguration{
				Status: schedulingv1alpha1.ClusterSizingConfigurationStatus{
					Conditions: []metav1.Condition{{Type: schedulingv1alpha1.ClusterSizingConfigurationValidType, Status: metav1.ConditionFalse}},
				},
			},
		},
		{
			name: "invalid deployment name, do nothing",
			deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name: "whatever",
					Labels: map[string]string{
						PlaceholderLabel:                         "true",
						hypershiftv1beta1.HostedClusterSizeLabel: "small",
					},
				},
			},
			config: &schedulingv1alpha1.ClusterSizingConfiguration{
				Status: schedulingv1alpha1.ClusterSizingConfigurationStatus{
					Conditions: []metav1.Condition{{Type: schedulingv1alpha1.ClusterSizingConfigurationValidType, Status: metav1.ConditionFalse}},
				},
			},
		},
		{
			name: "too-large placeholder deployment, delete",
			deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name: "placeholder-small-123",
					Labels: map[string]string{
						PlaceholderLabel:                         "true",
						hypershiftv1beta1.HostedClusterSizeLabel: "small",
					},
				},
			},
			config: &schedulingv1alpha1.ClusterSizingConfiguration{
				Spec: schedulingv1alpha1.ClusterSizingConfigurationSpec{
					Sizes: []schedulingv1alpha1.SizeConfiguration{
						{Name: "small", Management: &schedulingv1alpha1.Management{Placeholders: 2}},
					},
				},
				Status: validConfigStatus,
			},
			delete: true,
		},
		{
			name: "too-large placeholder deployment edge-case, delete",
			deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name: "placeholder-small-2",
					Labels: map[string]string{
						PlaceholderLabel:                         "true",
						hypershiftv1beta1.HostedClusterSizeLabel: "small",
					},
				},
			},
			config: &schedulingv1alpha1.ClusterSizingConfiguration{
				Spec: schedulingv1alpha1.ClusterSizingConfigurationSpec{
					Sizes: []schedulingv1alpha1.SizeConfiguration{
						{Name: "small", Management: &schedulingv1alpha1.Management{Placeholders: 2}},
					},
				},
				Status: validConfigStatus,
			},
			delete: true,
		},
		{
			name: "placeholder deployment paired nodes missing, update",
			deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name: "placeholder-small-1",
					Labels: map[string]string{
						PlaceholderLabel:                         "true",
						hypershiftv1beta1.HostedClusterSizeLabel: "small",
					},
				},
				Spec: appsv1.DeploymentSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Affinity: &corev1.Affinity{
								NodeAffinity: &corev1.NodeAffinity{
									RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
										NodeSelectorTerms: []corev1.NodeSelectorTerm{{
											MatchExpressions: []corev1.NodeSelectorRequirement{{
												Key:      OSDFleetManagerPairedNodesLabel,
												Operator: corev1.NodeSelectorOpNotIn,
												Values:   []string{},
											}},
										}},
									},
								},
							},
						},
					},
				},
			},
			config: &schedulingv1alpha1.ClusterSizingConfiguration{
				Spec: schedulingv1alpha1.ClusterSizingConfigurationSpec{
					Sizes: []schedulingv1alpha1.SizeConfiguration{
						{Name: "small", Management: &schedulingv1alpha1.Management{Placeholders: 2}},
					},
				},
				Status: validConfigStatus,
			},
			listConfigMaps: func(_ context.Context, _ ...client.ListOption) (*corev1.ConfigMapList, error) {
				return &corev1.ConfigMapList{Items: []corev1.ConfigMap{
					{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{pairLabelKey: "first"}}},
					{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{pairLabelKey: "first"}}},
					{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{pairLabelKey: "second"}}},
					{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{pairLabelKey: "second"}}},
				}}, nil
			},
			expected: newDeployment(placeholderNamespace, "small", 1, []string{"first", "second"}),
		},
		{
			name: "placeholder deployment paired nodes out-of-date, update",
			deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name: "placeholder-small-1",
					Labels: map[string]string{
						PlaceholderLabel:                         "true",
						hypershiftv1beta1.HostedClusterSizeLabel: "small",
					},
				},
				Spec: appsv1.DeploymentSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Affinity: &corev1.Affinity{
								NodeAffinity: &corev1.NodeAffinity{
									RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
										NodeSelectorTerms: []corev1.NodeSelectorTerm{{
											MatchExpressions: []corev1.NodeSelectorRequirement{{
												Key:      OSDFleetManagerPairedNodesLabel,
												Operator: corev1.NodeSelectorOpNotIn,
												Values:   []string{"first"},
											}},
										}},
									},
								},
							},
						},
					},
				},
			},
			config: &schedulingv1alpha1.ClusterSizingConfiguration{
				Spec: schedulingv1alpha1.ClusterSizingConfigurationSpec{
					Sizes: []schedulingv1alpha1.SizeConfiguration{
						{Name: "small", Management: &schedulingv1alpha1.Management{Placeholders: 2}},
					},
				},
				Status: validConfigStatus,
			},
			listConfigMaps: func(_ context.Context, _ ...client.ListOption) (*corev1.ConfigMapList, error) {
				return &corev1.ConfigMapList{Items: []corev1.ConfigMap{
					{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{pairLabelKey: "first"}}},
					{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{pairLabelKey: "first"}}},
					{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{pairLabelKey: "second"}}},
					{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{pairLabelKey: "second"}}},
				}}, nil
			},
			expected: newDeployment(placeholderNamespace, "small", 1, []string{"first", "second"}),
		},
		{
			name: "placeholder deployment correct, no-op",
			deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name: "placeholder-small-1",
					Labels: map[string]string{
						PlaceholderLabel:                         "true",
						hypershiftv1beta1.HostedClusterSizeLabel: "small",
					},
				},
				Spec: appsv1.DeploymentSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Affinity: &corev1.Affinity{
								NodeAffinity: &corev1.NodeAffinity{
									RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
										NodeSelectorTerms: []corev1.NodeSelectorTerm{{
											MatchExpressions: []corev1.NodeSelectorRequirement{{
												Key:      OSDFleetManagerPairedNodesLabel,
												Operator: corev1.NodeSelectorOpNotIn,
												Values:   []string{"first", "second"},
											}},
										}},
									},
								},
							},
						},
					},
				},
			},
			config: &schedulingv1alpha1.ClusterSizingConfiguration{
				Spec: schedulingv1alpha1.ClusterSizingConfigurationSpec{
					Sizes: []schedulingv1alpha1.SizeConfiguration{
						{Name: "small", Management: &schedulingv1alpha1.Management{Placeholders: 2}},
					},
				},
				Status: validConfigStatus,
			},
			listConfigMaps: func(_ context.Context, _ ...client.ListOption) (*corev1.ConfigMapList, error) {
				return &corev1.ConfigMapList{Items: []corev1.ConfigMap{
					{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{pairLabelKey: "first"}}},
					{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{pairLabelKey: "first"}}},
					{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{pairLabelKey: "second"}}},
					{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{pairLabelKey: "second"}}},
				}}, nil
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			r := &placeholderUpdater{
				placeholderLister: &placeholderLister{
					listConfigMaps: testCase.listConfigMaps,
				},
			}
			shouldDelete, action, err := r.reconcile(ctx, testCase.deployment, testCase.config)
			if err == nil && testCase.expectedErr {
				t.Fatalf("expected an error but got none")
			}
			if err != nil && !testCase.expectedErr {
				t.Fatalf("expected no error but got one: %v", err)
			}
			if shouldDelete != testCase.delete {
				t.Fatalf("wanted delete=%v, got delete=%v", testCase.delete, shouldDelete)
			}
			if diff := cmp.Diff(action, testCase.expected, compareDeploymentApplyConfiguration()...); diff != "" {
				t.Fatalf("got incorrect action: %v", diff)
			}
		})
	}
}

func compareDeploymentApplyConfiguration() []cmp.Option {
	return []cmp.Option{
		cmpopts.IgnoreTypes(
			metav1applyconfigurations.TypeMetaApplyConfiguration{}, // these are entirely set by generated code
		),
		cmpopts.IgnoreFields(metav1applyconfigurations.ConditionApplyConfiguration{}, "ObservedGeneration"),
	}
}
