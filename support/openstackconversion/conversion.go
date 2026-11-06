package openstackconversion

//go:generate go run ../../hack/copy-capo-conversions -output .

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	capov1beta1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
	capov1beta2 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/webhook/conversion"
)

const dataAnnotation = "cluster.x-k8s.io/conversion-data"

var conditionReasonPattern = regexp.MustCompile(`^[A-Za-z]([A-Za-z0-9_,:]*[A-Za-z0-9_])?$`)

type Converter struct{}

func Register(registry conversion.Registry) error {
	for _, kind := range []string{"OpenStackCluster", "OpenStackClusterTemplate", "OpenStackMachine", "OpenStackMachineTemplate"} {
		if err := registry.RegisterConverter(schema.GroupKind{Group: capov1beta1.SchemeGroupVersion.Group, Kind: kind}, Converter{}); err != nil {
			return fmt.Errorf("register CAPO conversion for %s: %w", kind, err)
		}
	}
	return nil
}

func (Converter) ConvertObject(ctx context.Context, source, destination runtime.Object) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if source == nil || destination == nil || reflect.ValueOf(source).IsNil() || reflect.ValueOf(destination).IsNil() {
		return fmt.Errorf("CAPO conversion requires non-nil objects")
	}
	source = source.DeepCopyObject()
	destinationGVK := destination.GetObjectKind().GroupVersionKind()
	if err := convertFields(source, destination); err != nil {
		return err
	}
	sourceMeta, ok := source.(metav1.Object)
	if !ok {
		return fmt.Errorf("CAPO source %T has no metadata", source)
	}
	destinationMeta := destination.(metav1.Object)
	if data, found := sourceMeta.GetAnnotations()[dataAnnotation]; found {
		original := reflect.New(reflect.TypeOf(destination).Elem()).Interface().(runtime.Object)
		if err := json.Unmarshal([]byte(data), original); err != nil {
			return fmt.Errorf("read CAPO preserved data: %w", err)
		}
		intermediate := source.DeepCopyObject()
		if err := convertFields(original, intermediate); err != nil {
			return err
		}
		baseline := destination.DeepCopyObject()
		if err := convertFields(intermediate, baseline); err != nil {
			return err
		}
		currentData, err := runtime.DefaultUnstructuredConverter.ToUnstructured(destination)
		if err != nil {
			return err
		}
		originalData, err := runtime.DefaultUnstructuredConverter.ToUnstructured(original)
		if err != nil {
			return err
		}
		baselineData, err := runtime.DefaultUnstructuredConverter.ToUnstructured(baseline)
		if err != nil {
			return err
		}
		for _, field := range []string{"spec", "status"} {
			if restored, found := restoreUnchanged(currentData[field], originalData[field], baselineData[field]); found {
				currentData[field] = restored
			} else {
				delete(currentData, field)
			}
		}
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(currentData, destination); err != nil {
			return err
		}
	}
	preserved, err := runtime.DefaultUnstructuredConverter.ToUnstructured(source)
	if err != nil {
		return err
	}
	delete(preserved, "metadata")
	data, err := json.Marshal(preserved)
	if err != nil {
		return err
	}
	annotations := destinationMeta.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[dataAnnotation] = string(data)
	destinationMeta.SetAnnotations(annotations)
	destination.GetObjectKind().SetGroupVersionKind(destinationGVK)
	return nil
}

func restoreUnchanged(current, original, baseline any) (any, bool) {
	if reflect.DeepEqual(current, baseline) {
		return original, original != nil
	}
	currentMap, currentOK := current.(map[string]any)
	originalMap, originalOK := original.(map[string]any)
	baselineMap, baselineOK := baseline.(map[string]any)
	if currentOK && baselineOK && (originalOK || original == nil) {
		fields := map[string]bool{}
		for field := range originalMap {
			fields[field] = true
		}
		for field := range baselineMap {
			fields[field] = true
		}
		for field := range fields {
			if restored, found := restoreUnchanged(currentMap[field], originalMap[field], baselineMap[field]); found {
				currentMap[field] = restored
			} else {
				delete(currentMap, field)
			}
		}
	}
	return current, current != nil
}

func normalizeConditions(conditions []metav1.Condition, generation int64) {
	for index := range conditions {
		conditions[index].ObservedGeneration = generation
		if !conditionReasonPattern.MatchString(conditions[index].Reason) {
			conditions[index].Reason = "Unknown"
		}
	}
}

func convertFields(source, destination runtime.Object) error {
	switch typed := source.(type) {
	case *capov1beta1.OpenStackCluster:
		if target, ok := destination.(*capov1beta2.OpenStackCluster); ok {
			*target = capov1beta2.OpenStackCluster{}
			if err := Convert_v1beta1_OpenStackCluster_To_v1beta2_OpenStackCluster(typed, target, nil); err != nil {
				return err
			}
			normalizeConditions(target.Status.Conditions, typed.Generation)
			return nil
		}
	case *capov1beta2.OpenStackCluster:
		if target, ok := destination.(*capov1beta1.OpenStackCluster); ok {
			*target = capov1beta1.OpenStackCluster{}
			return Convert_v1beta2_OpenStackCluster_To_v1beta1_OpenStackCluster(typed, target, nil)
		}
	case *capov1beta1.OpenStackMachine:
		if target, ok := destination.(*capov1beta2.OpenStackMachine); ok {
			*target = capov1beta2.OpenStackMachine{}
			if err := Convert_v1beta1_OpenStackMachine_To_v1beta2_OpenStackMachine(typed, target, nil); err != nil {
				return err
			}
			normalizeConditions(target.Status.Conditions, typed.Generation)
			return nil
		}
	case *capov1beta2.OpenStackMachine:
		if target, ok := destination.(*capov1beta1.OpenStackMachine); ok {
			*target = capov1beta1.OpenStackMachine{}
			return Convert_v1beta2_OpenStackMachine_To_v1beta1_OpenStackMachine(typed, target, nil)
		}
	case *capov1beta1.OpenStackClusterTemplate:
		if target, ok := destination.(*capov1beta2.OpenStackClusterTemplate); ok {
			*target = capov1beta2.OpenStackClusterTemplate{}
			return Convert_v1beta1_OpenStackClusterTemplate_To_v1beta2_OpenStackClusterTemplate(typed, target, nil)
		}
	case *capov1beta2.OpenStackClusterTemplate:
		if target, ok := destination.(*capov1beta1.OpenStackClusterTemplate); ok {
			*target = capov1beta1.OpenStackClusterTemplate{}
			return Convert_v1beta2_OpenStackClusterTemplate_To_v1beta1_OpenStackClusterTemplate(typed, target, nil)
		}
	case *capov1beta1.OpenStackMachineTemplate:
		if target, ok := destination.(*capov1beta2.OpenStackMachineTemplate); ok {
			*target = capov1beta2.OpenStackMachineTemplate{}
			return Convert_v1beta1_OpenStackMachineTemplate_To_v1beta2_OpenStackMachineTemplate(typed, target, nil)
		}
	case *capov1beta2.OpenStackMachineTemplate:
		if target, ok := destination.(*capov1beta1.OpenStackMachineTemplate); ok {
			*target = capov1beta1.OpenStackMachineTemplate{}
			return Convert_v1beta2_OpenStackMachineTemplate_To_v1beta1_OpenStackMachineTemplate(typed, target, nil)
		}
	}
	return fmt.Errorf("unsupported CAPO conversion from %T to %T", source, destination)
}
