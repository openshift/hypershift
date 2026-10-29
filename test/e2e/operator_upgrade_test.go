package e2e

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/onsi/gomega"
)

func TestLabelOperatorUpgradeHostedCluster(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name   string
		object crclient.Object
		want   map[string]string
	}{
		{name: "When a HostedCluster has no labels, it should add the upgrade label", object: &hyperv1.HostedCluster{}, want: map[string]string{hostedClusterUpgradeTestLabel: "true"}},
		{name: "When a HostedCluster has labels, it should preserve them and set the upgrade label", object: &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"existing": "value", hostedClusterUpgradeTestLabel: "false"}}}, want: map[string]string{"existing": "value", hostedClusterUpgradeTestLabel: "true"}},
		{name: "When the object is not a HostedCluster, it should leave labels unchanged", object: &hyperv1.NodePool{}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			labelOperatorUpgradeHostedCluster(testCase.object)
			if !reflect.DeepEqual(testCase.object.GetLabels(), testCase.want) {
				t.Errorf("got labels %v, want %v", testCase.object.GetLabels(), testCase.want)
			}
		})
	}
}

func TestInitialNodePoolConditionsComplete(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name      string
		mutate    func(*hyperv1.NodePool)
		wantError bool
	}{
		{name: "When all conditions have completed, it should accept the NodePool"},
		{name: "When conditions are missing, it should wait", mutate: func(pool *hyperv1.NodePool) { pool.Status.Conditions = nil }, wantError: true},
		{name: "When Ready is false, it should wait", mutate: func(pool *hyperv1.NodePool) { pool.Status.Conditions[0].Status = corev1.ConditionFalse }, wantError: true},
		{name: "When an updating condition is true, it should wait", mutate: func(pool *hyperv1.NodePool) { pool.Status.Conditions[1].Status = corev1.ConditionTrue }, wantError: true},
		{name: "When a condition is stale, it should wait", mutate: func(pool *hyperv1.NodePool) { pool.Status.Conditions[0].ObservedGeneration = 0 }, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			pool := &hyperv1.NodePool{ObjectMeta: metav1.ObjectMeta{Name: "workers", Generation: 1}}
			for _, conditionType := range []string{hyperv1.NodePoolReadyConditionType, hyperv1.NodePoolUpdatingVersionConditionType, hyperv1.NodePoolUpdatingConfigConditionType, hyperv1.NodePoolUpdatingPlatformMachineTemplateConditionType} {
				status := corev1.ConditionFalse
				if conditionType == hyperv1.NodePoolReadyConditionType {
					status = corev1.ConditionTrue
				}
				pool.Status.Conditions = append(pool.Status.Conditions, hyperv1.NodePoolCondition{Type: conditionType, Status: status, ObservedGeneration: 1})
			}
			if testCase.mutate != nil {
				testCase.mutate(pool)
			}
			if err := initialNodePoolConditionsComplete(pool); (err != nil) != testCase.wantError {
				t.Errorf("got error %v, wantError %t", err, testCase.wantError)
			}
		})
	}
}

func TestInitialNodePoolComplete(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name       string
		mutate     func(*hyperv1.NodePool, *unstructured.Unstructured)
		useV1Beta1 bool
		wantError  bool
	}{
		{name: "When initial reconciliation is complete, it should accept the baseline"},
		{name: "When using v1beta1 counters, it should accept the completed baseline", useV1Beta1: true},
		{name: "When current config is empty, it should wait", mutate: func(pool *hyperv1.NodePool, deployment *unstructured.Unstructured) {
			delete(pool.Annotations, nodePoolAnnotationCurrentConfig)
		}, wantError: true},
		{name: "When current config version is empty, it should wait", mutate: func(pool *hyperv1.NodePool, deployment *unstructured.Unstructured) {
			delete(pool.Annotations, nodePoolAnnotationCurrentConfigVersion)
		}, wantError: true},
		{name: "When a condition is stale, it should wait despite populated annotations", mutate: func(pool *hyperv1.NodePool, deployment *unstructured.Unstructured) {
			pool.Status.Conditions[0].ObservedGeneration = 0
		}, wantError: true},
		{name: "When config is updating, it should wait despite populated annotations", mutate: func(pool *hyperv1.NodePool, deployment *unstructured.Unstructured) {
			pool.Status.Conditions[2].Status = corev1.ConditionTrue
		}, wantError: true},
		{name: "When a completion condition is absent, it should wait", mutate: func(pool *hyperv1.NodePool, deployment *unstructured.Unstructured) {
			pool.Status.Conditions = pool.Status.Conditions[:1]
		}, wantError: true},
		{name: "When the completed config differs from bootstrap, it should wait", mutate: func(pool *hyperv1.NodePool, deployment *unstructured.Unstructured) {
			pool.Annotations[nodePoolAnnotationCurrentConfigVersion] = "stale"
		}, wantError: true},
		{name: "When the completed version differs from the workload, it should wait", mutate: func(pool *hyperv1.NodePool, deployment *unstructured.Unstructured) { pool.Status.Version = "4.20.0" }, wantError: true},
		{name: "When desired replicas have not propagated, it should wait", mutate: func(pool *hyperv1.NodePool, deployment *unstructured.Unstructured) {
			pool.Spec.Replicas = ptr.To[int32](2)
		}, wantError: true},
		{name: "When workload counters are incomplete, it should wait", mutate: func(pool *hyperv1.NodePool, deployment *unstructured.Unstructured) {
			deployment.Object["status"].(map[string]interface{})["upToDateReplicas"] = int64(0)
		}, wantError: true},
		{name: "When v1beta1 updated replicas are incomplete, it should wait", useV1Beta1: true, mutate: func(pool *hyperv1.NodePool, deployment *unstructured.Unstructured) {
			deployment.Object["status"].(map[string]interface{})["updatedReplicas"] = int64(0)
		}, wantError: true},
		{name: "When a deployment is terminating, it should wait", mutate: func(pool *hyperv1.NodePool, deployment *unstructured.Unstructured) {
			now := metav1.Now()
			deployment.SetDeletionTimestamp(&now)
		}, wantError: true},
		{name: "When workload generation is unobserved, it should wait", mutate: func(pool *hyperv1.NodePool, deployment *unstructured.Unstructured) { deployment.SetGeneration(2) }, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			deployment := operatorUpgradeTestDeployment()
			if testCase.useV1Beta1 {
				deployment.SetAPIVersion("cluster.x-k8s.io/v1beta1")
				unstructured.RemoveNestedField(deployment.Object, "status", "upToDateReplicas")
			} else {
				unstructured.RemoveNestedField(deployment.Object, "status", "updatedReplicas")
			}
			pool := &hyperv1.NodePool{ObjectMeta: metav1.ObjectMeta{Name: "workers", Generation: 1, Annotations: map[string]string{nodePoolAnnotationCurrentConfig: "config", nodePoolAnnotationCurrentConfigVersion: "payload"}}, Spec: hyperv1.NodePoolSpec{Replicas: ptr.To[int32](1)}, Status: hyperv1.NodePoolStatus{Version: "4.21.0"}}
			for _, conditionType := range []string{hyperv1.NodePoolReadyConditionType, hyperv1.NodePoolUpdatingVersionConditionType, hyperv1.NodePoolUpdatingConfigConditionType, hyperv1.NodePoolUpdatingPlatformMachineTemplateConditionType} {
				status := corev1.ConditionFalse
				if conditionType == hyperv1.NodePoolReadyConditionType {
					status = corev1.ConditionTrue
				}
				pool.Status.Conditions = append(pool.Status.Conditions, hyperv1.NodePoolCondition{Type: conditionType, Status: status, ObservedGeneration: 1})
			}
			if testCase.mutate != nil {
				testCase.mutate(pool, deployment)
			}
			machineSet := deployment.DeepCopy()
			machineSet.SetKind("MachineSet")
			machineSet.SetName("workers-set")
			machineSet.SetOwnerReferences([]metav1.OwnerReference{{UID: deployment.GetUID()}})
			workloads := map[string]*unstructured.Unstructured{"MachineDeployment/workers": deployment, "MachineSet/workers-set": machineSet}
			if err := initialNodePoolComplete(pool, workloads, testCase.useV1Beta1); (err != nil) != testCase.wantError {
				t.Fatalf("unexpected completion result: %v", err)
			}
			if !testCase.wantError {
				now := metav1.Now()
				machineSet.SetDeletionTimestamp(&now)
				if err := initialNodePoolComplete(pool, workloads, testCase.useV1Beta1); err == nil {
					t.Fatal("terminating workloads should block baseline capture")
				}
				machineSet.SetDeletionTimestamp(nil)
				machineSet.SetAnnotations(map[string]string{machineDeploymentRevisionAnnotation: "old"})
				if err := initialNodePoolComplete(pool, workloads, testCase.useV1Beta1); err == nil {
					t.Fatal("stale MachineSet should block capture despite completed MD counters")
				}
			}
			if err := initialNodePoolComplete(pool, nil, testCase.useV1Beta1); err == nil {
				t.Fatal("missing workload should not be complete")
			}
		})
	}
}

func TestOperatorUpgradeWorkloadUnchanged(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"MachineDeployment", "MachineSet", "Machine"} {
		for _, testCase := range []struct {
			name      string
			mutate    func(*unstructured.Unstructured)
			wantError bool
		}{
			{name: "When nothing changes, it should preserve the invariant"},
			{name: "When only release version is removed, it should permit one template generation", mutate: func(after *unstructured.Unstructured) {
				unstructured.RemoveNestedField(after.Object, "spec", "template", "metadata", "annotations", hyperv1.NodePoolReleaseVersionAnnotation)
				after.SetGeneration(2)
			}},
			{name: "When release version changes value, it should reject the change", mutate: func(after *unstructured.Unstructured) {
				_ = unstructured.SetNestedField(after.Object, "4.22.0", "spec", "template", "metadata", "annotations", hyperv1.NodePoolReleaseVersionAnnotation)
			}, wantError: true},
			{name: "When another annotation is removed, it should reject the change", mutate: func(after *unstructured.Unstructured) {
				unstructured.RemoveNestedField(after.Object, "spec", "template", "metadata", "annotations", "hypershift.openshift.io/nodePool")
			}, wantError: true},
			{name: "When bootstrap changes, it should reject the rollout", mutate: func(after *unstructured.Unstructured) {
				_ = unstructured.SetNestedField(after.Object, "user-data-workers-new", "spec", "template", "spec", "bootstrap", "dataSecretName")
			}, wantError: true},
			{name: "When the object is replaced, it should reject the new identity", mutate: func(after *unstructured.Unstructured) { after.SetUID("replacement") }, wantError: true},
			{name: "When deletion starts without an identity change, it should reject the rollout", mutate: func(after *unstructured.Unstructured) {
				now := metav1.Now()
				after.SetDeletionTimestamp(&now)
			}, wantError: true},
			{name: "When revision changes, it should reject the rollout", mutate: func(after *unstructured.Unstructured) {
				after.SetAnnotations(map[string]string{machineDeploymentRevisionAnnotation: "2"})
			}, wantError: true},
			{name: "When generation changes without the allowed removal, it should reject the change", mutate: func(after *unstructured.Unstructured) { after.SetGeneration(2) }, wantError: true},
			{name: "When removal accompanies another spec change, it should reject the change", mutate: func(after *unstructured.Unstructured) {
				unstructured.RemoveNestedField(after.Object, "spec", "template", "metadata", "annotations", hyperv1.NodePoolReleaseVersionAnnotation)
				_ = unstructured.SetNestedField(after.Object, int64(2), "spec", "replicas")
				after.SetGeneration(2)
			}, wantError: true},
		} {
			t.Run(kind+"/"+testCase.name, func(t *testing.T) {
				t.Parallel()
				before := operatorUpgradeTestDeployment()
				before.SetKind(kind)
				after := before.DeepCopy()
				if testCase.mutate != nil {
					testCase.mutate(after)
				}
				wantError := testCase.wantError || (kind == "Machine" && testCase.name == "When only release version is removed, it should permit one template generation")
				if err := operatorUpgradeWorkloadUnchanged(before, after); (err != nil) != wantError {
					t.Fatalf("unexpected invariant result: %v", err)
				}
			})
		}
	}
}

func TestListOperatorUpgradeWorkloads(t *testing.T) {
	t.Parallel()
	for _, version := range []string{"v1beta1", "v1beta2"} {
		t.Run("When using "+version+", it should capture each workload kind", func(t *testing.T) {
			t.Parallel()
			scheme := runtime.NewScheme()
			client := fake.NewClientBuilder().WithScheme(scheme).Build()
			for _, kind := range []string{"MachineDeployment", "MachineSet", "Machine"} {
				scheme.AddKnownTypeWithName(schema.GroupVersionKind{Group: "cluster.x-k8s.io", Version: version, Kind: kind + "List"}, &unstructured.UnstructuredList{})
				object := operatorUpgradeTestDeployment()
				object.SetAPIVersion("cluster.x-k8s.io/" + version)
				object.SetKind(kind)
				if err := client.Create(context.Background(), object); err != nil {
					t.Fatal(err)
				}
			}
			workloads, err := listOperatorUpgradeWorkloads(context.Background(), client, "clusters", version == "v1beta1")
			if err != nil || len(workloads) != 3 {
				t.Fatalf("unexpected capture: %d workloads, %v", len(workloads), err)
			}
			if _, err := listOperatorUpgradeWorkloads(context.Background(), client, "empty", version == "v1beta1"); err == nil {
				t.Fatal("empty namespace should fail capture")
			}
		})
	}
}

func TestCaptureInitialOperatorUpgradeState(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name       string
		useV1Beta1 bool
		mutation   string
		wantError  bool
	}{
		{name: "When v1beta2 workloads converge, it should capture their validated state"},
		{name: "When v1beta1 workloads converge, it should capture their validated state", useV1Beta1: true},
		{name: "When no NodePools exist, it should reject capture", mutation: "missing", wantError: true},
		{name: "When listing fails, it should reject capture", mutation: "list error", wantError: true},
		{name: "When a workload is missing, it should reject capture", mutation: "missing machine", wantError: true},
		{name: "When extra workloads exist, it should reject capture", mutation: "extra machine", wantError: true},
		{name: "When config is stale, it should retry and retain only the successful polling state", mutation: "retry"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			scheme := runtime.NewScheme()
			if err := hyperv1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			pool := &hyperv1.NodePool{ObjectMeta: metav1.ObjectMeta{Name: "workers", Namespace: "clusters", Generation: 1, Annotations: map[string]string{nodePoolAnnotationCurrentConfig: "config", nodePoolAnnotationCurrentConfigVersion: "payload"}}, Spec: hyperv1.NodePoolSpec{Replicas: ptr.To[int32](1)}, Status: hyperv1.NodePoolStatus{Version: "4.21.0"}}
			for _, conditionType := range []string{hyperv1.NodePoolReadyConditionType, hyperv1.NodePoolUpdatingVersionConditionType, hyperv1.NodePoolUpdatingConfigConditionType, hyperv1.NodePoolUpdatingPlatformMachineTemplateConditionType} {
				status := corev1.ConditionFalse
				if conditionType == hyperv1.NodePoolReadyConditionType {
					status = corev1.ConditionTrue
				}
				pool.Status.Conditions = append(pool.Status.Conditions, hyperv1.NodePoolCondition{Type: conditionType, Status: status, ObservedGeneration: 1})
			}
			version := "v1beta2"
			if testCase.useV1Beta1 {
				version = "v1beta1"
			}
			objects := []crclient.Object{}
			if testCase.mutation != "missing" {
				objects = append(objects, pool)
			}
			for _, kind := range []string{"MachineDeployment", "MachineSet", "Machine"} {
				scheme.AddKnownTypeWithName(schema.GroupVersionKind{Group: "cluster.x-k8s.io", Version: version, Kind: kind + "List"}, &unstructured.UnstructuredList{})
				object := operatorUpgradeTestDeployment()
				object.SetAPIVersion("cluster.x-k8s.io/" + version)
				object.SetKind(kind)
				if kind == "MachineSet" {
					object.SetOwnerReferences([]metav1.OwnerReference{{UID: "original"}})
				}
				if kind != "Machine" || testCase.mutation != "missing machine" {
					objects = append(objects, object)
				}
				if kind == "Machine" && testCase.mutation == "extra machine" {
					extra := object.DeepCopy()
					extra.SetName("extra")
					objects = append(objects, extra)
				}
			}
			poolLists := 0
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithInterceptorFuncs(interceptor.Funcs{
				List: func(ctx context.Context, client crclient.WithWatch, list crclient.ObjectList, options ...crclient.ListOption) error {
					if testCase.mutation == "list error" {
						return fmt.Errorf("list unavailable")
					}
					if err := client.List(ctx, list, options...); err != nil {
						return err
					}
					if pools, ok := list.(*hyperv1.NodePoolList); ok {
						poolLists++
						if testCase.mutation == "retry" && poolLists != 2 {
							pools.Items[0].Annotations[nodePoolAnnotationCurrentConfigVersion] = "unconverged"
						}
					}
					return nil
				},
			}).Build()
			var baseline map[string]*hyperv1.NodePool
			var workloads map[string]*unstructured.Unstructured
			capture := func() error {
				var err error
				baseline, workloads, err = captureInitialOperatorUpgradeState(ctx, client, "clusters", "clusters", testCase.useV1Beta1)
				return err
			}
			if testCase.wantError {
				if err := capture(); err == nil || baseline != nil || workloads != nil {
					t.Fatalf("failed capture should not return a baseline: %v", err)
				}
				return
			}
			gomega.NewWithT(t).Eventually(capture, time.Second, time.Millisecond).Should(gomega.Succeed())
			if len(baseline) != 1 || len(workloads) != 3 || baseline["workers"].Annotations[nodePoolAnnotationCurrentConfigVersion] != "payload" {
				t.Fatal("capture should retain exactly the validated state")
			}
			if testCase.mutation == "retry" && poolLists != 2 {
				t.Fatalf("capture should stop listing after the successful poll, got %d lists", poolLists)
			}
		})
	}
}

func operatorUpgradeTestDeployment() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "cluster.x-k8s.io/v1beta2", "kind": "MachineDeployment",
		"metadata": map[string]interface{}{"name": "workers", "namespace": "clusters", "uid": "original", "generation": int64(1), "annotations": map[string]interface{}{machineDeploymentRevisionAnnotation: "1"}},
		"spec":     map[string]interface{}{"replicas": int64(1), "template": map[string]interface{}{"metadata": map[string]interface{}{"annotations": map[string]interface{}{hyperv1.NodePoolReleaseVersionAnnotation: "4.21.0", "hypershift.openshift.io/nodePool": "clusters/workers"}}, "spec": map[string]interface{}{"version": "4.21.0", "bootstrap": map[string]interface{}{"dataSecretName": "user-data-workers-payload"}}}},
		"status":   map[string]interface{}{"observedGeneration": int64(1), "replicas": int64(1), "availableReplicas": int64(1), "upToDateReplicas": int64(1), "updatedReplicas": int64(1)},
	}}
}
