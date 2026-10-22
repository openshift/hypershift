//go:build e2ev2

package tests

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/blang/semver"
	"github.com/onsi/gomega"
)

func TestConfigureAutoscalerScaleDown(t *testing.T) {
	originalDefault := gomega.Default
	gomega.Default = gomega.NewWithT(t)
	t.Cleanup(func() { gomega.Default = originalDefault })
	for _, testCase := range []struct {
		name, origin, wantError string
		wantUpdates             int
	}{
		{name: "When origin is 4.17 despite newer history, it should preserve basic scaling without new configuration", origin: "4.17.0"},
		{name: "When origin reaches 4.18, it should attempt the exact scale-down configuration", origin: "4.18.0", wantUpdates: 1, wantError: "synthetic update rejection"},
		{name: "When origin cannot be resolved, it should fail before mutation", wantError: "independent metadata unavailable"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("E2E_PREVIOUS_RELEASE_IMAGE", "previous")
			t.Setenv("E2E_LATEST_RELEASE_IMAGE", "")
			t.Setenv("E2E_CONDITION_DIAGNOSTIC_RELEASE_IMAGE", "")
			t.Setenv("E2E_CONDITION_DIAGNOSTIC_UPGRADE_TARGET", "")
			scheme := runtime.NewScheme()
			if err := hyperv1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			hc := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "clusters"},
				Status:     hyperv1.HostedClusterStatus{Version: &hyperv1.ClusterVersionStatus{History: []configv1.UpdateHistory{{Version: "4.23.0"}}}},
			}
			original := hc.Spec.Autoscaling.DeepCopy()
			updates := 0
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(hc).WithInterceptorFuncs(interceptor.Funcs{
				Update: func(_ context.Context, _ crclient.WithWatch, object crclient.Object, _ ...crclient.UpdateOption) error {
					updates++
					got := object.(*hyperv1.HostedCluster).Spec.Autoscaling
					if got.Scaling != hyperv1.ScaleUpAndScaleDown || got.ScaleDown == nil || ptr.Deref(got.ScaleDown.DelayAfterAddSeconds, 0) != 30 || ptr.Deref(got.ScaleDown.UnneededDurationSeconds, 0) != 60 || ptr.Deref(got.ScaleDown.UtilizationThresholdPercent, 0) != 50 {
						t.Errorf("incorrect scale-down configuration: %+v", got)
					}
					return errors.New("synthetic update rejection")
				},
			}).Build()
			tc := &internal.TestContext{
				Context: t.Context(), MgmtClient: client, ClusterName: hc.Name, ClusterNamespace: hc.Namespace,
				ConditionDiagnosticReleaseVersions: map[string]semver.Version{},
				ConditionDiagnosticReleaseErrors:   map[string]error{"previous": errors.New("independent metadata unavailable")},
			}
			if testCase.origin != "" {
				tc.ConditionDiagnosticReleaseVersions["previous"] = semver.MustParse(testCase.origin)
			}
			resetFailureConditionPhase(tc)
			err := gomega.InterceptGomegaFailure(func() { configureAutoscalerScaleDown(tc, hc) })
			if updates != testCase.wantUpdates || (err != nil) != (testCase.wantError != "") {
				t.Fatalf("updates=%d error=%v; want %d and %q", updates, err, testCase.wantUpdates, testCase.wantError)
			}
			if err != nil && !strings.Contains(err.Error(), testCase.wantError) {
				t.Fatalf("unexpected failure: %v", err)
			}
			if updates == 0 && !reflect.DeepEqual(hc.Spec.Autoscaling.DeepCopy(), original) {
				t.Fatal("unsupported or unresolved origin mutated autoscaling")
			}
		})
	}
}

func TestAutoscalerSettingsMatchDeploymentReadiness(t *testing.T) {
	ready := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Generation: 12},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To[int32](1),
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{
				Name: "cluster-autoscaler", Args: []string{"--scale-down-enabled=true"},
			}}}},
		},
		Status: appsv1.DeploymentStatus{ObservedGeneration: 12, Replicas: 1, UpdatedReplicas: 1, ReadyReplicas: 1, AvailableReplicas: 1},
	}
	for _, testCase := range []struct {
		name   string
		mutate func(*appsv1.Deployment)
		valid  bool
	}{
		{name: "When the new revision is fully available, it should accept the configured autoscaler", valid: true},
		{name: "When only the old pod is Ready and the new pod is unready, it should reject the mixed rollout", mutate: func(deployment *appsv1.Deployment) { deployment.Status.Replicas = 2 }},
		{name: "When the generation has not been observed, it should reject stale status", mutate: func(deployment *appsv1.Deployment) { deployment.Status.ObservedGeneration-- }},
		{name: "When the updated replica is Ready but unavailable, it should wait for availability", mutate: func(deployment *appsv1.Deployment) { deployment.Status.AvailableReplicas = 0 }},
		{name: "When the updated replica is unready, it should wait for readiness", mutate: func(deployment *appsv1.Deployment) {
			deployment.Status.ReadyReplicas = 0
			deployment.Status.AvailableReplicas = 0
		}},
		{name: "When the remaining pod belongs to the old revision, it should wait for the update", mutate: func(deployment *appsv1.Deployment) { deployment.Status.UpdatedReplicas = 0 }},
		{name: "When the autoscaler is disabled, it should reject zero desired replicas", mutate: func(deployment *appsv1.Deployment) {
			deployment.Spec.Replicas = ptr.To[int32](0)
			deployment.Status = appsv1.DeploymentStatus{ObservedGeneration: 12}
		}},
		{name: "When the template container is absent, it should reject missing settings", mutate: func(deployment *appsv1.Deployment) { deployment.Spec.Template.Spec.Containers = nil }},
		{name: "When a completed rollout has the wrong arguments, it should reject the configuration", mutate: func(deployment *appsv1.Deployment) {
			deployment.Spec.Template.Spec.Containers[0].Args = []string{"--scale-down-enabled=false"}
		}},
		{name: "When replicas are defaulted, it should accept a fully available singleton", mutate: func(deployment *appsv1.Deployment) { deployment.Spec.Replicas = nil }, valid: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			deployment := ready.DeepCopy()
			if testCase.mutate != nil {
				testCase.mutate(deployment)
			}
			if err := autoscalerSettingsReady(deployment, []string{"--scale-down-enabled=true"}); (err == nil) != testCase.valid {
				t.Fatalf("got error %v, want valid=%t", err, testCase.valid)
			}
		})
	}
}

func TestAutoscalerSettingsMatch(t *testing.T) {
	for _, test := range []struct {
		name                string
		arguments, required []string
		wantError           bool
	}{
		{name: "When a required argument matches exactly, it should accept it", arguments: []string{"--max-nodes-total=6", "--balance-similar-node-groups=true"}, required: []string{"--max-nodes-total=6"}},
		{name: "When the argument is absent, it should reject it", arguments: []string{"--balance-similar-node-groups=true"}, required: []string{"--max-nodes-total=6"}, wantError: true},
		{name: "When a numeric value only shares the expected prefix, it should reject it", arguments: []string{"--max-nodes-total=60"}, required: []string{"--max-nodes-total=6"}, wantError: true},
		{name: "When a label value only shares the expected prefix, it should reject it", arguments: []string{"--balancing-ignore-label=e2e-balance-ignore-extra"}, required: []string{"--balancing-ignore-label=e2e-balance-ignore"}, wantError: true},
		{name: "When one of multiple required arguments is absent, it should reject the configuration", arguments: []string{"--max-nodes-total=6"}, required: []string{"--max-nodes-total=6", "--balance-similar-node-groups=true"}, wantError: true},
		{name: "When no arguments are required, it should accept an empty configuration"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := autoscalerSettingsMatch(test.arguments, test.required)
			if (err != nil) != test.wantError {
				t.Fatalf("unexpected argument validation result: %v", err)
			}
		})
	}
}

func TestAutoscalingCapacity(t *testing.T) {
	testCases := []struct {
		name        string
		native      corev1.ResourceList
		annotations map[string]string
		wantErr     bool
	}{
		{name: "When native CPU and memory are present, it should accept capacity without legacy annotations", native: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4"), corev1.ResourceMemory: resource.MustParse("16Gi")}},
		{name: "When native memory is missing, it should reject incomplete capacity", native: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4")}, wantErr: true},
		{name: "When native CPU is zero, it should reject unusable capacity", native: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("0"), corev1.ResourceMemory: resource.MustParse("16Gi")}, wantErr: true},
		{name: "When legacy capacity and architecture are valid, it should accept the fallback", annotations: map[string]string{"machine.openshift.io/vCPU": "4", "machine.openshift.io/memoryMb": "16384", "capacity.cluster-autoscaler.kubernetes.io/labels": "kubernetes.io/os=linux,kubernetes.io/arch=amd64"}},
		{name: "When no capacity source is available, it should reject the missing metadata", wantErr: true},
		{name: "When a legacy capacity number is malformed, it should reject the annotation", annotations: map[string]string{"machine.openshift.io/vCPU": "invalid", "machine.openshift.io/memoryMb": "16384", "capacity.cluster-autoscaler.kubernetes.io/labels": "kubernetes.io/arch=amd64"}, wantErr: true},
		{name: "When legacy memory is negative, it should reject the annotation", annotations: map[string]string{"machine.openshift.io/vCPU": "4", "machine.openshift.io/memoryMb": "-1", "capacity.cluster-autoscaler.kubernetes.io/labels": "kubernetes.io/arch=amd64"}, wantErr: true},
		{name: "When architecture is absent, it should reject otherwise valid legacy capacity", annotations: map[string]string{"machine.openshift.io/vCPU": "4", "machine.openshift.io/memoryMb": "16384"}, wantErr: true},
		{name: "When architecture differs from the NodePool, it should reject the mismatch", annotations: map[string]string{"machine.openshift.io/vCPU": "4", "machine.openshift.io/memoryMb": "16384", "capacity.cluster-autoscaler.kubernetes.io/labels": "kubernetes.io/arch=arm64"}, wantErr: true},
		{name: "When architecture only shares a prefix, it should require an exact value", annotations: map[string]string{"machine.openshift.io/vCPU": "4", "machine.openshift.io/memoryMb": "16384", "capacity.cluster-autoscaler.kubernetes.io/labels": "kubernetes.io/arch=amd64extra"}, wantErr: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := autoscalingCapacity(testCase.native, testCase.annotations, "amd64")
			if (err != nil) != testCase.wantErr {
				t.Fatalf("got error %v, want error=%t", err, testCase.wantErr)
			}
		})
	}
}
