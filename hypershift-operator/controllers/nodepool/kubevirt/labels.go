package kubevirt

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"sigs.k8s.io/controller-runtime/pkg/client"

	kubevirtv1 "kubevirt.io/api/core/v1"
	cdiv1beta1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
)

const (
	// ManagedHCLabelKeysAnnotation stores the JSON-encoded list of label keys
	// applied by HC label propagation, enabling clean removal when keys are
	// removed from hcluster.Spec.Labels.
	ManagedHCLabelKeysAnnotation = "hypershift.openshift.io/managed-hc-label-keys"
)

var reservedLabelKeys = map[string]bool{
	hyperv1.NodePoolNameLabel:              true,
	hyperv1.InfraIDLabel:                   true,
	hyperv1.IsKubeVirtRHCOSVolumeLabelName: true,
}

// PropagateLabelsToInfraResources patches user labels from hcluster.Spec.Labels
// onto KubeVirt VMs and DataVolumes on the infra cluster without modifying the
// KubevirtMachineTemplate (and thus without triggering rollouts).
func PropagateLabelsToInfraResources(
	ctx context.Context,
	infraClient client.Client,
	infraNamespace string,
	nodePoolName string,
	infraID string,
	desiredLabels map[string]string,
) error {
	vmList := &kubevirtv1.VirtualMachineList{}
	err := infraClient.List(ctx, vmList,
		client.InNamespace(infraNamespace),
		client.MatchingLabels{
			hyperv1.NodePoolNameLabel: nodePoolName,
			hyperv1.InfraIDLabel:      infraID,
		},
	)
	if err != nil {
		return fmt.Errorf("failed to list VMs: %w", err)
	}
	var errs []error
	for i := range vmList.Items {
		vm := &vmList.Items[i]
		if err := reconcileVMLabels(ctx, infraClient, vm, desiredLabels); err != nil {
			errs = append(errs, fmt.Errorf("failed to propagate labels to VM %s: %w", vm.Name, err))
		}
	}
	dvList := &cdiv1beta1.DataVolumeList{}
	err = infraClient.List(ctx, dvList,
		client.InNamespace(infraNamespace),
		client.MatchingLabels{
			hyperv1.IsKubeVirtRHCOSVolumeLabelName: "true",
		},
	)
	if err != nil {
		return utilerrors.NewAggregate(append(errs, fmt.Errorf("failed to list DataVolumes: %w", err)))
	}
	vmNames := make(map[string]bool, len(vmList.Items))
	for _, vm := range vmList.Items {
		vmNames[vm.Name] = true
	}
	for i := range dvList.Items {
		dv := &dvList.Items[i]
		if !isOwnedByVM(dv, vmNames) {
			continue
		}
		if err := reconcileObjectLabels(ctx, infraClient, dv, desiredLabels); err != nil {
			errs = append(errs, fmt.Errorf("failed to propagate labels to DataVolume %s: %w", dv.Name, err))
		}
	}
	return utilerrors.NewAggregate(errs)
}

func isOwnedByVM(dv *cdiv1beta1.DataVolume, vmNames map[string]bool) bool {
	for _, ref := range dv.OwnerReferences {
		if ref.Kind == "VirtualMachine" && vmNames[ref.Name] {
			return true
		}
	}
	return false
}

func reconcileVMLabels(ctx context.Context, cl client.Client, vm *kubevirtv1.VirtualMachine, desiredLabels map[string]string) error {
	patch := client.MergeFrom(vm.DeepCopy())
	previousKeys := managedKeysFromAnnotation(vm)
	filtered := filterReservedLabels(desiredLabels)
	changed := mergeLabels(vm.Labels, filtered, previousKeys)
	if vm.Spec.Template != nil {
		if vm.Spec.Template.ObjectMeta.Labels == nil {
			vm.Spec.Template.ObjectMeta.Labels = map[string]string{}
		}
		changed = mergeLabels(vm.Spec.Template.ObjectMeta.Labels, filtered, previousKeys) || changed
	}
	if !changed {
		return nil
	}
	setManagedKeysAnnotation(vm, sortedKeys(filtered))
	return cl.Patch(ctx, vm, patch)
}

func reconcileObjectLabels(ctx context.Context, cl client.Client, obj client.Object, desiredLabels map[string]string) error {
	patch := client.MergeFrom(obj.DeepCopyObject().(client.Object))
	previousKeys := managedKeysFromAnnotation(obj)
	filtered := filterReservedLabels(desiredLabels)
	if !mergeLabels(obj.GetLabels(), filtered, previousKeys) {
		return nil
	}
	setManagedKeysAnnotation(obj, sortedKeys(filtered))
	return cl.Patch(ctx, obj, patch)
}

func filterReservedLabels(desiredLabels map[string]string) map[string]string {
	filtered := make(map[string]string, len(desiredLabels))
	for k, v := range desiredLabels {
		if !reservedLabelKeys[k] {
			filtered[k] = v
		}
	}
	return filtered
}

// mergeLabels merges filtered into labels and removes keys that were
// previously managed (per previousKeys) but are no longer desired.
// Returns true if any change was made.
func mergeLabels(labels map[string]string, filtered map[string]string, previousKeys []string) bool {
	changed := false
	for _, key := range previousKeys {
		if _, stillDesired := filtered[key]; !stillDesired {
			if _, exists := labels[key]; exists {
				delete(labels, key)
				changed = true
			}
		}
	}
	for k, v := range filtered {
		if labels[k] != v {
			labels[k] = v
			changed = true
		}
	}
	return changed
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func managedKeysFromAnnotation(obj client.Object) []string {
	if obj == nil {
		return nil
	}
	annotations := obj.GetAnnotations()
	if annotations == nil {
		return nil
	}
	raw, ok := annotations[ManagedHCLabelKeysAnnotation]
	if !ok {
		return nil
	}
	var keys []string
	if err := json.Unmarshal([]byte(raw), &keys); err != nil {
		return nil
	}
	return keys
}

func setManagedKeysAnnotation(obj client.Object, keys []string) {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	if len(keys) == 0 {
		delete(annotations, ManagedHCLabelKeysAnnotation)
	} else {
		data, _ := json.Marshal(keys)
		annotations[ManagedHCLabelKeysAnnotation] = string(data)
	}
	obj.SetAnnotations(annotations)
}

// LabelsUpToDate returns true if the desired labels are already applied.
func LabelsUpToDate(currentLabels map[string]string, desiredLabels map[string]string) bool {
	for k, v := range desiredLabels {
		if reservedLabelKeys[k] {
			continue
		}
		if currentLabels[k] != v {
			return false
		}
	}
	return true
}
