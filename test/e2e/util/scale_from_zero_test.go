package util

import (
	"fmt"
	"maps"
	"math"
	"reflect"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	capiaws "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiazure "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	capiv1beta1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestPrepareScaleFromZeroUpgrade(t *testing.T) {
	for _, test := range []struct {
		name      string
		replicas  *int32
		wantError bool
	}{
		{name: "When framework validation uses two fixed workers, it should prepare autoscaling without changing the initial fixture", replicas: ptr.To[int32](2)},
		{name: "When the initial fixture has one fixed worker, it should retain that count as the minimum", replicas: ptr.To[int32](1)},
		{name: "When fixed replicas are absent, it should reject preparation before a usable baseline", wantError: true},
		{name: "When there are no initial workers, it should reject the upgrade fixture", replicas: ptr.To[int32](0), wantError: true},
		{name: "When the maximum replica count cannot grow, it should reject the upgrade fixture", replicas: ptr.To[int32](math.MaxInt32), wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			initial := &hyperv1.NodePool{Spec: hyperv1.NodePoolSpec{Replicas: test.replicas, NodeLabels: map[string]string{"keep": "label"}}}
			before := initial.DeepCopy()
			prepared, err := PrepareScaleFromZeroUpgrade(initial)
			if (err != nil) != test.wantError {
				t.Fatalf("PrepareScaleFromZeroUpgrade() error = %v, wantError %v", err, test.wantError)
			}
			if !reflect.DeepEqual(initial, before) {
				t.Fatal("preparation changed the fixed-replica fixture required by initial framework validation")
			}
			if test.wantError {
				return
			}
			if prepared.Spec.Replicas != nil || *prepared.Spec.AutoScaling.Min != *test.replicas || prepared.Spec.AutoScaling.Max != *test.replicas+1 {
				t.Fatal("prepared pool must preserve the ready worker count and leave room for autoscaling")
			}
			prepared.Spec.Replicas, prepared.Spec.AutoScaling = initial.Spec.Replicas, initial.Spec.AutoScaling
			if !reflect.DeepEqual(prepared, initial) {
				t.Fatal("preparation changed unrelated NodePool fields")
			}
		})
	}
}

func TestValidateScaleFromZeroUpgradeReplicas(t *testing.T) {
	for _, version := range []string{"v1beta1", "v1beta2"} {
		for _, kind := range []string{"MachineDeployment", "MachineSet"} {
			for _, test := range []struct {
				name       string
				mutate     func(*unstructured.Unstructured, *hyperv1.NodePool)
				noObject   bool
				noNodePool bool
				wantError  bool
			}{
				{name: "When prepared bounds and minimum replicas are persisted, it should accept the baseline"},
				{name: "When replicas equal the prepared maximum, it should accept the baseline", mutate: func(object *unstructured.Unstructured, _ *hyperv1.NodePool) {
					object.Object["spec"] = map[string]interface{}{"replicas": int64(3)}
				}},
				{name: "When fixed replica bounds have not transitioned, it should keep waiting", wantError: true, mutate: func(object *unstructured.Unstructured, _ *hyperv1.NodePool) {
					object.SetAnnotations(map[string]string{"cluster.x-k8s.io/cluster-api-autoscaler-node-group-min-size": "0", "cluster.x-k8s.io/cluster-api-autoscaler-node-group-max-size": "0"})
				}},
				{name: "When autoscaling bounds are absent, it should keep waiting", wantError: true, mutate: func(object *unstructured.Unstructured, _ *hyperv1.NodePool) { object.SetAnnotations(nil) }},
				{name: "When only the minimum matches, it should keep waiting", wantError: true, mutate: func(object *unstructured.Unstructured, _ *hyperv1.NodePool) {
					object.SetAnnotations(map[string]string{"cluster.x-k8s.io/cluster-api-autoscaler-node-group-min-size": "2", "cluster.x-k8s.io/cluster-api-autoscaler-node-group-max-size": "4"})
				}},
				{name: "When replicas are below the prepared minimum, it should keep waiting", wantError: true, mutate: func(object *unstructured.Unstructured, _ *hyperv1.NodePool) {
					object.Object["spec"] = map[string]interface{}{"replicas": int64(1)}
				}},
				{name: "When replicas exceed the prepared maximum, it should keep waiting", wantError: true, mutate: func(object *unstructured.Unstructured, _ *hyperv1.NodePool) {
					object.Object["spec"] = map[string]interface{}{"replicas": int64(4)}
				}},
				{name: "When replicas are missing, it should keep waiting", wantError: true, mutate: func(object *unstructured.Unstructured, _ *hyperv1.NodePool) { delete(object.Object, "spec") }},
				{name: "When replicas are malformed, it should reject the baseline", wantError: true, mutate: func(object *unstructured.Unstructured, _ *hyperv1.NodePool) {
					object.Object["spec"] = map[string]interface{}{"replicas": "2"}
				}},
				{name: "When the scalable object is missing, it should reject the baseline", noObject: true, wantError: true},
				{name: "When the NodePool is missing, it should reject the baseline", noNodePool: true, wantError: true},
				{name: "When autoscaling is disabled, it should reject the baseline", wantError: true, mutate: func(_ *unstructured.Unstructured, nodePool *hyperv1.NodePool) { nodePool.Spec.AutoScaling = nil }},
				{name: "When the prepared minimum is missing, it should reject the baseline", wantError: true, mutate: func(_ *unstructured.Unstructured, nodePool *hyperv1.NodePool) { nodePool.Spec.AutoScaling.Min = nil }},
				{name: "When the prepared minimum has no existing workers, it should reject the baseline", wantError: true, mutate: func(_ *unstructured.Unstructured, nodePool *hyperv1.NodePool) {
					nodePool.Spec.AutoScaling.Min = ptr.To[int32](0)
				}},
				{name: "When the prepared maximum is below the minimum, it should reject the baseline", wantError: true, mutate: func(_ *unstructured.Unstructured, nodePool *hyperv1.NodePool) { nodePool.Spec.AutoScaling.Max = 1 }},
			} {
				t.Run(fmt.Sprintf("%s/%s/%s", test.name, version, kind), func(t *testing.T) {
					object := &unstructured.Unstructured{Object: map[string]interface{}{
						"apiVersion": "cluster.x-k8s.io/" + version, "kind": kind,
						"spec": map[string]interface{}{"replicas": int64(2)},
					}}
					object.SetAnnotations(map[string]string{"cluster.x-k8s.io/cluster-api-autoscaler-node-group-min-size": "2", "cluster.x-k8s.io/cluster-api-autoscaler-node-group-max-size": "3"})
					nodePool := &hyperv1.NodePool{Spec: hyperv1.NodePoolSpec{AutoScaling: &hyperv1.NodePoolAutoScaling{Min: ptr.To[int32](2), Max: 3}}}
					if test.mutate != nil {
						test.mutate(object, nodePool)
					}
					var scalable client.Object = object
					if test.noObject {
						scalable = nil
					}
					if test.noNodePool {
						nodePool = nil
					}
					if err := ValidateScaleFromZeroUpgradeReplicas(scalable, nodePool); (err != nil) != test.wantError {
						t.Fatalf("ValidateScaleFromZeroUpgradeReplicas() error = %v, wantError %v", err, test.wantError)
					}
				})
			}
		}
	}
}

func TestSnapshotScaleFromZeroRollout(t *testing.T) {
	for _, version := range []string{"v1beta1", "v1beta2"} {
		for _, platform := range []hyperv1.PlatformType{hyperv1.AWSPlatform, hyperv1.AzurePlatform} {
			for _, state := range []string{"ready", "no workers", "not ready"} {
				t.Run(fmt.Sprintf("When %s %s workers are %s, it should capture stable rollout inputs or reject the fixture", version, platform, state), func(t *testing.T) {
					metadata := metav1.ObjectMeta{Name: "workers", Namespace: "clusters-guest", UID: types.UID("existing-workers")}
					machineDeployment := &capiv1.MachineDeployment{ObjectMeta: metadata, Spec: capiv1.MachineDeploymentSpec{Template: capiv1.MachineTemplateSpec{Spec: capiv1.MachineSpec{Version: "4.22.15"}}}}
					machineSet := &capiv1.MachineSet{ObjectMeta: metadata, Spec: capiv1.MachineSetSpec{Template: machineDeployment.Spec.Template}}
					machine := &capiv1.Machine{ObjectMeta: metadata, Spec: machineDeployment.Spec.Template.Spec}
					templateMetadata := metav1.ObjectMeta{Name: "workers-template", Namespace: metadata.Namespace, UID: types.UID("existing-template")}
					var template client.Object = &capiaws.AWSMachineTemplate{ObjectMeta: templateMetadata, Spec: capiaws.AWSMachineTemplateSpec{Template: capiaws.AWSMachineTemplateResource{Spec: capiaws.AWSMachineSpec{InstanceType: "m5.large"}}}}
					if platform == hyperv1.AzurePlatform {
						template = &capiazure.AzureMachineTemplate{ObjectMeta: templateMetadata, Spec: capiazure.AzureMachineTemplateSpec{Template: capiazure.AzureMachineTemplateResource{Spec: capiazure.AzureMachineSpec{VMSize: "Standard_D4s_v5"}}}}
					}
					var scalable client.Object = machineDeployment
					objects := []client.Object{machineDeployment, machineSet, machine, template}
					if version == "v1beta1" {
						oldTemplate := capiv1beta1.MachineTemplateSpec{Spec: capiv1beta1.MachineSpec{Version: ptr.To("4.22.15")}}
						scalable = &capiv1beta1.MachineDeployment{ObjectMeta: metadata, Spec: capiv1beta1.MachineDeploymentSpec{Template: oldTemplate}}
						objects = []client.Object{scalable, &capiv1beta1.MachineSet{ObjectMeta: metadata, Spec: capiv1beta1.MachineSetSpec{Template: oldTemplate}}, &capiv1beta1.Machine{ObjectMeta: metadata, Spec: oldTemplate.Spec}, template}
					}
					managementClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objects...).Build()
					node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker", UID: types.UID("existing-node")}, Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}}}
					if state == "not ready" {
						node.Status.Conditions[0].Status = corev1.ConditionFalse
					}
					guestObjects := []client.Object{}
					if state != "no workers" {
						guestObjects = append(guestObjects, node)
					}
					guestClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(guestObjects...).Build()
					before, err := SnapshotScaleFromZeroRollout(t.Context(), managementClient, guestClient, metadata.Namespace, version, platform)
					if state != "ready" {
						if err == nil {
							t.Fatal("expected an unusable-worker-fixture error")
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					if len(before) != 5 {
						t.Fatalf("snapshot has %d objects, want MD, MS, Machine, template and Node", len(before))
					}
					scalable.SetAnnotations(map[string]string{"capacity.cluster-autoscaler.kubernetes.io/cpu": "4"})
					if err := managementClient.Update(t.Context(), scalable); err != nil {
						t.Fatal(err)
					}
					after, err := SnapshotScaleFromZeroRollout(t.Context(), managementClient, guestClient, metadata.Namespace, version, platform)
					if err != nil || !reflect.DeepEqual(before, after) {
						t.Fatalf("metadata-only migration changed snapshot: %v", err)
					}
					switch scalable := scalable.(type) {
					case *capiv1.MachineDeployment:
						scalable.Spec.Template.Spec.Version = "4.22.16"
					case *capiv1beta1.MachineDeployment:
						scalable.Spec.Template.Spec.Version = ptr.To("4.22.16")
					}
					if err := managementClient.Update(t.Context(), scalable); err != nil {
						t.Fatal(err)
					}
					after, err = SnapshotScaleFromZeroRollout(t.Context(), managementClient, guestClient, metadata.Namespace, version, platform)
					if err != nil || reflect.DeepEqual(before, after) {
						t.Fatalf("snapshot missed a rollout-causing template change: %v", err)
					}
				})
			}
		}
	}
}

func TestValidateScaleFromZeroCapacity(t *testing.T) {
	const prefix = "capacity.cluster-autoscaler.kubernetes.io/"
	valid := map[string]string{
		prefix + "cpu": "4", prefix + "memory": "16384Mi", prefix + "labels": "kubernetes.io/arch=amd64",
		"machine.openshift.io/vCPU": "4", "machine.openshift.io/memoryMb": "16384",
	}
	for _, test := range []struct {
		name      string
		source    string
		native    corev1.ResourceList
		updates   map[string]string
		remove    []string
		wantError bool
	}{
		{name: "When annotations describe four cores and sixteen GiB, it should accept both consumer contracts", source: "annotations"},
		{name: "When CAPI memory is expressed in Gi, it should compare equivalent quantities", source: "annotations", updates: map[string]string{prefix + "memory": "16Gi"}},
		{name: "When GPU count and type match, it should accept NVIDIA capacity", source: "annotations", updates: map[string]string{prefix + "gpu-count": "1", prefix + "gpu-type": "nvidia.com/gpu", "machine.openshift.io/GPU": "1"}},
		{name: "When native capacity is present in an annotation fixture, it should reject accidental native coverage", source: "annotations", native: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4")}, wantError: true},
		{name: "When CAPI CPU is missing, it should reject legacy-only coverage", source: "annotations", remove: []string{prefix + "cpu"}, wantError: true},
		{name: "When CAPI CPU is malformed, it should reject it", source: "annotations", updates: map[string]string{prefix + "cpu": "invalid"}, wantError: true},
		{name: "When CAPI CPU is zero, it should reject it", source: "annotations", updates: map[string]string{prefix + "cpu": "0"}, wantError: true},
		{name: "When CAPI CPU is negative, it should reject it", source: "annotations", updates: map[string]string{prefix + "cpu": "-4"}, wantError: true},
		{name: "When legacy CPU is malformed, it should reject it", source: "annotations", updates: map[string]string{"machine.openshift.io/vCPU": "invalid"}, wantError: true},
		{name: "When CAPI CPU differs from legacy, it should reject it", source: "annotations", updates: map[string]string{prefix + "cpu": "8"}, wantError: true},
		{name: "When CAPI memory is a bare byte count, it should reject unit mismatch", source: "annotations", updates: map[string]string{prefix + "memory": "16384"}, wantError: true},
		{name: "When CAPI memory is decimal MB, it should reject unit mismatch", source: "annotations", updates: map[string]string{prefix + "memory": "16384M"}, wantError: true},
		{name: "When legacy memory is missing, it should reject it", source: "annotations", remove: []string{"machine.openshift.io/memoryMb"}, wantError: true},
		{name: "When legacy memory is zero, it should reject it", source: "annotations", updates: map[string]string{"machine.openshift.io/memoryMb": "0"}, wantError: true},
		{name: "When GPU type has no count, it should reject stale metadata", source: "annotations", updates: map[string]string{prefix + "gpu-type": "nvidia.com/gpu"}, wantError: true},
		{name: "When GPU count has no type, it should reject incomplete capacity", source: "annotations", updates: map[string]string{prefix + "gpu-count": "1", "machine.openshift.io/GPU": "1"}, wantError: true},
		{name: "When GPU counts differ, it should reject incompatible consumers", source: "annotations", updates: map[string]string{prefix + "gpu-count": "2", prefix + "gpu-type": "nvidia.com/gpu", "machine.openshift.io/GPU": "1"}, wantError: true},
		{name: "When architecture is absent, it should reject unusable scheduling metadata", source: "annotations", remove: []string{prefix + "labels"}, wantError: true},
		{name: "When architecture is unsupported, it should reject unusable scheduling metadata", source: "annotations", updates: map[string]string{prefix + "labels": "kubernetes.io/arch=not-an-architecture"}, wantError: true},
		{name: "When capacity source is unknown, it should reject fixture configuration", source: "auto", wantError: true},
		{name: "When native capacity has stale annotations, it should reject overrides", source: "native", native: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4"), corev1.ResourceMemory: resource.MustParse("16Gi")}, wantError: true},
		{name: "When native CPU and memory are positive, it should accept the native control", source: "native", native: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4"), corev1.ResourceMemory: resource.MustParse("16Gi")}, remove: []string{prefix + "cpu", prefix + "memory", "machine.openshift.io/vCPU", "machine.openshift.io/memoryMb"}},
		{name: "When native CPU is missing, it should reject incomplete native capacity", source: "native", native: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("16Gi")}, remove: []string{prefix + "cpu", prefix + "memory", "machine.openshift.io/vCPU", "machine.openshift.io/memoryMb"}, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			annotations := maps.Clone(valid)
			maps.Copy(annotations, test.updates)
			for _, key := range test.remove {
				delete(annotations, key)
			}
			if err := ValidateScaleFromZeroCapacity(annotations, test.native, test.source); (err != nil) != test.wantError {
				t.Fatalf("ValidateScaleFromZeroCapacity() error = %v, wantError %v", err, test.wantError)
			}
		})
	}
}

func TestGetScaleFromZeroCapacity(t *testing.T) {
	for _, version := range []string{"v1beta1", "v1beta2"} {
		for _, platform := range []hyperv1.PlatformType{hyperv1.AWSPlatform, hyperv1.AzurePlatform} {
			for _, upgradeType := range []hyperv1.UpgradeType{hyperv1.UpgradeTypeReplace, hyperv1.UpgradeTypeInPlace} {
				for _, state := range []string{"present", "missing scalable", "missing reference", "missing template"} {
					t.Run(fmt.Sprintf("When %s %s uses %s and resources are %s, it should read capacity or report the missing input", version, platform, upgradeType, state), func(t *testing.T) {
						nodePool := &hyperv1.NodePool{ObjectMeta: metav1.ObjectMeta{Name: "workers"}, Spec: hyperv1.NodePoolSpec{
							Platform: hyperv1.NodePoolPlatform{Type: platform}, Management: hyperv1.NodePoolManagement{UpgradeType: upgradeType},
						}}
						metadata := metav1.ObjectMeta{Name: "workers", Namespace: "clusters-guest"}
						templateName := "workers-template"
						if state == "missing reference" {
							templateName = ""
						}
						machineTemplate := capiv1.MachineTemplateSpec{Spec: capiv1.MachineSpec{InfrastructureRef: capiv1.ContractVersionedObjectReference{Name: templateName}}}
						var object client.Object = &capiv1.MachineDeployment{ObjectMeta: metadata, Spec: capiv1.MachineDeploymentSpec{Template: machineTemplate}}
						if upgradeType == hyperv1.UpgradeTypeInPlace {
							object = &capiv1.MachineSet{ObjectMeta: metadata, Spec: capiv1.MachineSetSpec{Template: machineTemplate}}
						}
						if version == "v1beta1" {
							oldTemplate := capiv1beta1.MachineTemplateSpec{Spec: capiv1beta1.MachineSpec{InfrastructureRef: corev1.ObjectReference{Name: templateName}}}
							object = &capiv1beta1.MachineDeployment{ObjectMeta: metadata, Spec: capiv1beta1.MachineDeploymentSpec{Template: oldTemplate}}
							if upgradeType == hyperv1.UpgradeTypeInPlace {
								object = &capiv1beta1.MachineSet{ObjectMeta: metadata, Spec: capiv1beta1.MachineSetSpec{Template: oldTemplate}}
							}
						}
						capacity := corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4"), corev1.ResourceMemory: resource.MustParse("16Gi")}
						templateMetadata := metav1.ObjectMeta{Name: "workers-template", Namespace: metadata.Namespace}
						var template client.Object = &capiaws.AWSMachineTemplate{ObjectMeta: templateMetadata, Status: capiaws.AWSMachineTemplateStatus{Capacity: capacity}}
						if platform == hyperv1.AzurePlatform {
							template = &capiazure.AzureMachineTemplate{ObjectMeta: templateMetadata, Status: capiazure.AzureMachineTemplateStatus{Capacity: capacity}}
						}
						objects := []client.Object{}
						if state != "missing scalable" {
							objects = append(objects, object)
						}
						if state != "missing template" {
							objects = append(objects, template)
						}
						managementClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objects...).Build()
						actual, actualCapacity, err := GetScaleFromZeroCapacity(t.Context(), managementClient, metadata.Namespace, nodePool, version)
						if state != "present" {
							if err == nil {
								t.Fatal("expected a missing-resource error")
							}
							return
						}
						if err != nil || client.ObjectKeyFromObject(actual) != client.ObjectKeyFromObject(object) {
							t.Fatalf("GetScaleFromZeroCapacity() object = %v, error = %v", actual, err)
						}
						for name, quantity := range capacity {
							if observed := actualCapacity[name]; observed.Cmp(quantity) != 0 {
								t.Fatalf("%s capacity = %s, want %s", name, observed.String(), quantity.String())
							}
						}
					})
				}
			}
		}
	}
}
