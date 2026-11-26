package dump

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/pkg/manifests"
	hyperapi "github.com/openshift/hypershift/support/api"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/utils/ptr"

	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	crclientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestRenderPodPlacement(t *testing.T) {
	node := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "ip-10-0-0-1.ec2.internal"},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4"),
				corev1.ResourceMemory: resource.MustParse("16Gi"),
			},
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}

	daemonSetPod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:       "openshift-monitoring",
			Name:            "node-exporter-abcde",
			OwnerReferences: []metav1.OwnerReference{{Kind: "DaemonSet", Name: "node-exporter"}},
		},
		Spec: corev1.PodSpec{
			NodeName: node.Name,
			Containers: []corev1.Container{{
				Name:      "node-exporter",
				Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("20m")}},
			}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}

	// A kube-system Deployment pod, as the kas-connection-checker is: it blocks
	// scale-down of its node unless a PodDisruptionBudget allows eviction.
	systemPod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:       "kube-system",
			Name:            "kas-connection-checker-5f7c9d8b4-abcde",
			OwnerReferences: []metav1.OwnerReference{{Kind: "ReplicaSet", Name: "kas-connection-checker-5f7c9d8b4"}},
		},
		Spec: corev1.PodSpec{
			NodeName: node.Name,
			Priority: ptr.To[int32](2000001000),
			Containers: []corev1.Container{{
				Name:      "connection-checker",
				Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("5m")}},
			}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}

	mirrorPod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:       "kube-system",
			Name:            "kube-apiserver-proxy-ip-10-0-0-1.ec2.internal",
			Annotations:     map[string]string{corev1.MirrorPodAnnotationKey: "abcdef"},
			OwnerReferences: []metav1.OwnerReference{{Kind: "Node", Name: node.Name}},
		},
		Spec:   corev1.PodSpec{NodeName: node.Name},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}

	pendingPod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:       "openshift-marketplace",
			Name:            "community-operators-catalog-7d9f-xyz",
			OwnerReferences: []metav1.OwnerReference{{Kind: "ReplicaSet", Name: "community-operators-catalog-7d9f"}},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:      "registry",
				Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("10m")}},
			}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodPending},
	}

	out := renderPodPlacement(
		[]corev1.Node{node},
		[]corev1.Pod{daemonSetPod, systemPod, mirrorPod, pendingPod},
	)

	for _, expected := range []string{
		"node ip-10-0-0-1.ec2.internal unschedulable=false ready=True",
		"nonDaemonSetPods=1 kubeSystemBlockingPods=1",
		"openshift-monitoring/node-exporter-abcde phase=Running ds=true mirror=false owner=DaemonSet/node-exporter",
		"kube-system/kas-connection-checker-5f7c9d8b4-abcde phase=Running ds=false mirror=false",
		"priority=2000001000 critical=true",
		"kube-system/kube-apiserver-proxy-ip-10-0-0-1.ec2.internal phase=Running ds=false mirror=true",
		"pending (not scheduled) pods: 1",
		"openshift-marketplace/community-operators-catalog-7d9f-xyz phase=Pending",
	} {
		if !strings.Contains(out, expected) {
			t.Errorf("expected the pod placement report to contain %q, got:\n%s", expected, out)
		}
	}
}

func TestRenderPodPlacementIgnoresSucceededPodsWithoutNode(t *testing.T) {
	succeeded := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "openshift-insights", Name: "periodic-gathering-abcde-xyz"},
		Status:     corev1.PodStatus{Phase: corev1.PodSucceeded},
	}

	out := renderPodPlacement(nil, []corev1.Pod{succeeded})
	if !strings.Contains(out, "pending (not scheduled) pods: 0") {
		t.Errorf("expected completed pods to be excluded from the pending list, got:\n%s", out)
	}
}

func TestDumpScaleDownDiagnostics(t *testing.T) {
	ctx := t.Context()

	hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "hc", Namespace: "clusters"}}
	hcpNamespace := manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name)

	managementClient := crclientfake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(
		&hyperv1.NodePool{ObjectMeta: metav1.ObjectMeta{Name: "np", Namespace: hc.Namespace}},
		&capiv1.MachineDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: "np", Namespace: hcpNamespace},
			Spec:       capiv1.MachineDeploymentSpec{Replicas: ptr.To[int32](2)},
		},
		&capiv1.MachineSet{ObjectMeta: metav1.ObjectMeta{Name: "np-abcde", Namespace: hcpNamespace}},
		&capiv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "np-abcde-xyz", Namespace: hcpNamespace}},
	).Build()

	guestClient := crclientfake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "ip-10-0-0-1.ec2.internal"}},
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Namespace: "kube-system", Name: clusterAutoscalerStatusConfigMap},
			Data:       map[string]string{"status": "Cluster-autoscaler status"},
		},
		&policyv1.PodDisruptionBudget{ObjectMeta: metav1.ObjectMeta{Namespace: "openshift-ingress", Name: "router-default"}},
	).Build()

	casPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-autoscaler-5f7c9d8b4-abcde",
			Namespace: hcpNamespace,
			Labels:    map[string]string{"app": "cluster-autoscaler"},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "cluster-autoscaler"}}},
	}
	casDeployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "cluster-autoscaler", Namespace: hcpNamespace}}

	kubeClient := k8sfake.NewSimpleClientset(casPod, casDeployment)
	kubeClient.PrependReactor("get", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() != "log" {
			return false, nil, nil
		}
		return true, &runtime.Unknown{Raw: []byte("scaled down node ip-10-0-0-2.ec2.internal\n")}, nil
	})

	artifactDir := t.TempDir()
	if err := DumpScaleDownDiagnostics(ctx, artifactDir, hc, managementClient, kubeClient, guestClient); err != nil {
		t.Fatalf("expected diagnostics to be collected, got: %v", err)
	}

	for _, name := range []string{
		"cluster-autoscaler/cluster-autoscaler-5f7c9d8b4-abcde-cluster-autoscaler.log",
		"cluster-autoscaler-pods.yaml",
		"cluster-autoscaler-deployment.yaml",
		"cluster-autoscaler-status.yaml",
		"nodes.yaml",
		"pods.yaml",
		"pods-by-node.txt",
		"poddisruptionbudgets.yaml",
		"nodepools.yaml",
		"machinedeployments.yaml",
		"machinesets.yaml",
		"machines.yaml",
	} {
		if _, err := os.Stat(filepath.Join(artifactDir, name)); err != nil {
			t.Errorf("expected %s to be written: %v", name, err)
		}
	}

	logs, err := os.ReadFile(filepath.Join(artifactDir, "cluster-autoscaler", "cluster-autoscaler-5f7c9d8b4-abcde-cluster-autoscaler.log"))
	if err != nil {
		t.Fatalf("failed to read the autoscaler logs: %v", err)
	}
	if !strings.Contains(string(logs), "scaled down node") {
		t.Errorf("expected the autoscaler logs to be captured, got: %q", string(logs))
	}
}

func TestDumpScaleDownDiagnosticsKeepsGuestArtifactsWhenPodsFail(t *testing.T) {
	ctx := t.Context()
	hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "hc", Namespace: "clusters"}}

	guestClient := crclientfake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "ip-10-0-0-1.ec2.internal"}},
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Namespace: "kube-system", Name: clusterAutoscalerStatusConfigMap},
			Data:       map[string]string{"status": "Cluster-autoscaler status"},
		},
	).WithInterceptorFuncs(interceptor.Funcs{
		List: func(ctx context.Context, client crclient.WithWatch, list crclient.ObjectList, opts ...crclient.ListOption) error {
			if _, ok := list.(*corev1.PodList); ok {
				return errors.New("listing pods is forbidden")
			}
			return client.List(ctx, list, opts...)
		},
	}).Build()

	artifactDir := t.TempDir()
	err := DumpScaleDownDiagnostics(ctx, artifactDir, hc, nil, nil, guestClient)
	if err == nil {
		t.Fatal("expected the pod list failure to be reported")
	}

	for _, name := range []string{"nodes.yaml", "cluster-autoscaler-status.yaml"} {
		if _, err := os.Stat(filepath.Join(artifactDir, name)); err != nil {
			t.Errorf("expected %s to be written despite the pod list failure: %v", name, err)
		}
	}
	for _, name := range []string{"pods.yaml", "pods-by-node.txt"} {
		if _, err := os.Stat(filepath.Join(artifactDir, name)); err == nil {
			t.Errorf("expected %s not to be written without a pod list", name)
		}
	}
}

func TestDumpScaleDownDiagnosticsReportsUnexpectedPreviousLogsError(t *testing.T) {
	ctx := t.Context()
	hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "hc", Namespace: "clusters"}}
	hcpNamespace := manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name)

	kubeClient := k8sfake.NewSimpleClientset(
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cluster-autoscaler-5f7c9d8b4-abcde",
				Namespace: hcpNamespace,
				Labels:    map[string]string{"app": "cluster-autoscaler"},
			},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "cluster-autoscaler"}}},
		},
		// Seed the Deployment so a NotFound error cannot satisfy the assertion below.
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "cluster-autoscaler", Namespace: hcpNamespace}},
	)
	kubeClient.PrependReactor("get", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() != "log" {
			return false, nil, nil
		}
		genericAction, ok := action.(k8stesting.GenericAction)
		if !ok {
			return false, nil, nil
		}
		if options, ok := genericAction.GetValue().(*corev1.PodLogOptions); ok && options.Previous {
			return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "cluster-autoscaler", errors.New("previous logs are forbidden"))
		}
		return true, &runtime.Unknown{Raw: []byte("current logs\n")}, nil
	})

	artifactDir := t.TempDir()
	err := DumpScaleDownDiagnostics(ctx, artifactDir, hc, nil, kubeClient, nil)
	if err == nil {
		t.Fatal("expected the unexpected previous log error to be reported")
	}
	if !strings.Contains(err.Error(), "previous logs are forbidden") {
		t.Errorf("expected the injected previous log error to be reported, got: %v", err)
	}

	logFile := filepath.Join(artifactDir, "cluster-autoscaler", "cluster-autoscaler-5f7c9d8b4-abcde-cluster-autoscaler.log")
	logs, readErr := os.ReadFile(logFile)
	if readErr != nil {
		t.Fatalf("expected the current logs to be written despite the previous logs error: %v", readErr)
	}
	if !strings.Contains(string(logs), "current logs") {
		t.Errorf("expected the current logs to be captured, got: %q", string(logs))
	}
}
