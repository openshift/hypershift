//go:build e2ev2

package tests

import (
	"context"
	"errors"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func controlPlaneHealthyTestPod() corev1.Pod {
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "component-0", Namespace: "control-plane", Labels: map[string]string{"app": "etcd"}},
		Spec: corev1.PodSpec{
			Containers:     []corev1.Container{{Name: "main", ImagePullPolicy: corev1.PullIfNotPresent, Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("10m"), corev1.ResourceMemory: resource.MustParse("10Mi")}}}},
			InitContainers: []corev1.Container{{Name: "init", ImagePullPolicy: corev1.PullIfNotPresent}},
		},
	}
}

func TestControlPlanePodStaticHealth(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		mutate    func(*corev1.Pod)
		wantError bool
	}{
		{name: "When all namespace requirements hold, it should accept the pod"},
		{name: "When init pull policy differs, it should reject the pod", mutate: func(pod *corev1.Pod) { pod.Spec.InitContainers[0].ImagePullPolicy = corev1.PullAlways }, wantError: true},
		{name: "When ordinary pull policy differs, it should reject the pod", mutate: func(pod *corev1.Pod) { pod.Spec.Containers[0].ImagePullPolicy = corev1.PullAlways }, wantError: true},
		{name: "When CPU request is missing, it should reject the pod", mutate: func(pod *corev1.Pod) { delete(pod.Spec.Containers[0].Resources.Requests, corev1.ResourceCPU) }, wantError: true},
		{name: "When memory request is missing, it should reject the pod", mutate: func(pod *corev1.Pod) { delete(pod.Spec.Containers[0].Resources.Requests, corev1.ResourceMemory) }, wantError: true},
		{name: "When catalog rollout lacks requests, it should preserve the exemption", mutate: func(pod *corev1.Pod) {
			pod.Name = "redhat-catalog-rollout-0"
			pod.Spec.Containers[0].Resources.Requests = nil
		}},
		{name: "When catalog rollout has wrong init pull policy, it should still reject it", mutate: func(pod *corev1.Pod) {
			pod.Name = "redhat-catalog-rollout-0"
			pod.Spec.InitContainers[0].ImagePullPolicy = corev1.PullAlways
		}, wantError: true},
		{name: "When priority equals etcd, it should accept it", mutate: func(pod *corev1.Pod) { pod.Spec.Priority = ptr.To(int32(100002000)) }},
		{name: "When priority exceeds etcd, it should reject it", mutate: func(pod *corev1.Pod) { pod.Spec.Priority = ptr.To(int32(100002001)) }, wantError: true},
		{name: "When multus has elevated priority, it should preserve the prefix exemption", mutate: func(pod *corev1.Pod) {
			pod.Name = "multus-admission-controller-0"
			pod.Spec.Priority = ptr.To(int32(100002001))
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			pod := controlPlaneHealthyTestPod()
			if testCase.mutate != nil {
				testCase.mutate(&pod)
			}
			if err := controlPlanePodStaticHealth(pod); (err != nil) != testCase.wantError {
				t.Fatalf("health error = %v, want error %v", err, testCase.wantError)
			}
		})
	}
}

func TestValidateControlPlaneNamespaceHealth(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := hyperv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name      string
		pod       *corev1.Pod
		listError bool
		wantError bool
	}{
		{name: "When namespace is empty, it should reject vacuous health", wantError: true},
		{name: "When listing fails, it should return the failure", listError: true, wantError: true},
		{name: "When only another namespace has pods, it should reject vacuous health", pod: func() *corev1.Pod { pod := controlPlaneHealthyTestPod(); pod.Namespace = "other"; return &pod }(), wantError: true},
		{name: "When namespace contains a healthy pod, it should pass", pod: ptr.To(controlPlaneHealthyTestPod())},
		{name: "When an audited component restarts within tolerance, it should pass without log access", pod: func() *corev1.Pod {
			pod := controlPlaneHealthyTestPod()
			pod.Labels["app"] = "cluster-version-operator"
			pod.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "main", RestartCount: 1}}
			return &pod
		}()},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			hostedCluster := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "clusters"}}
			hostedCluster.Spec.Platform.Type = hyperv1.AWSPlatform
			builder := fake.NewClientBuilder().WithScheme(scheme).WithObjects(hostedCluster)
			if testCase.pod != nil {
				builder = builder.WithObjects(testCase.pod)
			}
			if testCase.listError {
				builder = builder.WithInterceptorFuncs(interceptor.Funcs{List: func(context.Context, crclient.WithWatch, crclient.ObjectList, ...crclient.ListOption) error {
					return errors.New("list unavailable")
				}})
			}
			testContext := &internal.TestContext{Context: t.Context(), MgmtClient: builder.Build(), ControlPlaneNamespace: "control-plane", ClusterName: hostedCluster.Name, ClusterNamespace: hostedCluster.Namespace}
			if err := validateControlPlaneNamespaceHealth(testContext); (err != nil) != testCase.wantError {
				t.Fatalf("namespace health error = %v, want error %v", err, testCase.wantError)
			}
		})
	}
	if err := validateControlPlaneNamespaceHealth(nil); err == nil {
		t.Fatal("nil test context must fail")
	}
}

func TestControlPlanePodCrashTolerance(t *testing.T) {
	for _, testCase := range []struct {
		name            string
		platform        hyperv1.PlatformType
		external        bool
		azureManagement bool
		component       string
		want            int32
	}{
		{name: "When AWS has an unknown component, it should tolerate no restarts", platform: hyperv1.AWSPlatform},
		{name: "When local KubeVirt has an unknown component, it should tolerate no restarts", platform: hyperv1.KubevirtPlatform},
		{name: "When external KubeVirt has credentials, it should preserve one restart", platform: hyperv1.KubevirtPlatform, external: true, want: 1},
		{name: "When KubeVirt runs on Azure management, it should preserve one restart", platform: hyperv1.KubevirtPlatform, azureManagement: true, want: 1},
		{name: "When AWS has an Azure management annotation, it should not inherit KubeVirt tolerance", platform: hyperv1.AWSPlatform, azureManagement: true},
		{name: "When an audited component overrides the default, it should use its component tolerance", platform: hyperv1.KubevirtPlatform, external: true, component: "ingress-operator", want: 20},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			hostedCluster := &hyperv1.HostedCluster{}
			hostedCluster.Spec.Platform.Type = testCase.platform
			if testCase.platform == hyperv1.KubevirtPlatform {
				hostedCluster.Spec.Platform.Kubevirt = &hyperv1.KubevirtPlatformSpec{}
				if testCase.external {
					hostedCluster.Spec.Platform.Kubevirt.Credentials = &hyperv1.KubevirtPlatformCredentials{}
				}
			}
			if testCase.azureManagement {
				hostedCluster.Annotations = map[string]string{hyperv1.ManagementPlatformAnnotation: string(hyperv1.AzurePlatform)}
			}
			pod := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": testCase.component}}}
			if actual := controlPlanePodCrashTolerance(hostedCluster, pod); actual != testCase.want {
				t.Fatalf("crash tolerance = %d, want %d", actual, testCase.want)
			}
		})
	}
}
