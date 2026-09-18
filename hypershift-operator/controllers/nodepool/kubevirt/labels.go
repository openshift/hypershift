package kubevirt

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	kubevirtv1 "kubevirt.io/api/core/v1"
	cdiv1beta1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
	log := ctrl.LoggerFrom(ctx)

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
	for i := range vmList.Items {
		vm := &vmList.Items[i]
		if err := reconcileVMLabels(ctx, infraClient, vm, desiredLabels); err != nil {
			log.Error(err, "Failed to propagate labels to VM", "vm", vm.Name)
			continue
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
		return fmt.Errorf("failed to list DataVolumes: %w", err)
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
			log.Error(err, "Failed to propagate labels to DataVolume", "dv", dv.Name)
			continue
		}
	}
	return nil
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
	changed := applyLabels(vm.Labels, desiredLabels, vm)
	changed = applyLabels(vm.Spec.Template.ObjectMeta.Labels, desiredLabels, nil) || changed
	if !changed {
		return nil
	}
	return cl.Patch(ctx, vm, patch)
}

func reconcileObjectLabels(ctx context.Context, cl client.Client, obj client.Object, desiredLabels map[string]string) error {
	patch := client.MergeFrom(obj.DeepCopyObject().(client.Object))
	if !applyLabels(obj.GetLabels(), desiredLabels, obj) {
		return nil
	}
	return cl.Patch(ctx, obj, patch)
}

// applyLabels merges desiredLabels into labels, removes stale managed keys,
// and updates the managed-keys annotation on annotationHolder.
// Returns true if any change was made.
func applyLabels(labels map[string]string, desiredLabels map[string]string, annotationHolder client.Object) bool {
	previousKeys := managedKeysFromAnnotation(annotationHolder)
	filtered := make(map[string]string, len(desiredLabels))
	for k, v := range desiredLabels {
		if !reservedLabelKeys[k] {
			filtered[k] = v
		}
	}
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
	if annotationHolder != nil && changed {
		newKeys := make([]string, 0, len(filtered))
		for k := range filtered {
			newKeys = append(newKeys, k)
		}
		sort.Strings(newKeys)
		setManagedKeysAnnotation(annotationHolder, newKeys)
	}
	return changed
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
