package e2e

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	hostedClusterUpgradeTestLabel          = "hypershift.openshift.io/upgrade-test"
	nodePoolAnnotationCurrentConfig        = "hypershift.openshift.io/nodePoolCurrentConfig"
	nodePoolAnnotationCurrentConfigVersion = "hypershift.openshift.io/nodePoolCurrentConfigVersion"
	machineDeploymentRevisionAnnotation    = "machinedeployment.clusters.x-k8s.io/revision"
)

func labelOperatorUpgradeHostedCluster(object crclient.Object) {
	switch cluster := object.(type) {
	case *hyperv1.HostedCluster:
		if cluster.Labels == nil {
			cluster.Labels = make(map[string]string)
		}
		cluster.Labels[hostedClusterUpgradeTestLabel] = "true"
	}
}

func listOperatorUpgradeWorkloads(ctx context.Context, client crclient.Client, namespace string, useV1Beta1 bool) (map[string]*unstructured.Unstructured, error) {
	version := "v1beta2"
	if useV1Beta1 {
		version = "v1beta1"
	}
	workloads := map[string]*unstructured.Unstructured{}
	for _, kind := range []string{"MachineDeployment", "MachineSet", "Machine"} {
		list := &unstructured.UnstructuredList{}
		list.SetGroupVersionKind(schema.GroupVersionKind{Group: "cluster.x-k8s.io", Version: version, Kind: kind + "List"})
		if err := client.List(ctx, list, crclient.InNamespace(namespace)); err != nil {
			return nil, fmt.Errorf("listing %s: %w", kind, err)
		}
		if len(list.Items) == 0 {
			return nil, fmt.Errorf("no %s found in namespace %s", kind, namespace)
		}
		for index := range list.Items {
			workloads[kind+"/"+list.Items[index].GetName()] = list.Items[index].DeepCopy()
		}
	}
	return workloads, nil
}

func initialNodePoolComplete(nodePool *hyperv1.NodePool, workloads map[string]*unstructured.Unstructured, useV1Beta1 bool) error {
	deployment := workloads["MachineDeployment/"+nodePool.Name]
	if deployment == nil {
		return fmt.Errorf("NodePool %s has no MachineDeployment", nodePool.Name)
	}
	for _, annotation := range []string{nodePoolAnnotationCurrentConfig, nodePoolAnnotationCurrentConfigVersion} {
		if nodePool.Annotations[annotation] == "" {
			return fmt.Errorf("NodePool %s has no %s", nodePool.Name, annotation)
		}
	}
	if err := initialNodePoolConditionsComplete(nodePool); err != nil {
		return err
	}
	version, _, _ := unstructured.NestedString(deployment.Object, "spec", "template", "spec", "version")
	bootstrap, _, _ := unstructured.NestedString(deployment.Object, "spec", "template", "spec", "bootstrap", "dataSecretName")
	if version == "" || nodePool.Status.Version != version || !strings.HasSuffix(bootstrap, "-"+nodePool.Annotations[nodePoolAnnotationCurrentConfigVersion]) {
		return fmt.Errorf("NodePool %s completion does not match its deployed version and bootstrap config", nodePool.Name)
	}
	desired, found, _ := unstructured.NestedInt64(deployment.Object, "spec", "replicas")
	if !found || nodePool.Spec.Replicas == nil || desired != int64(*nodePool.Spec.Replicas) {
		return fmt.Errorf("NodePool %s desired replicas have not propagated", nodePool.Name)
	}
	updatedField := "upToDateReplicas"
	if useV1Beta1 {
		updatedField = "updatedReplicas"
	}
	for _, field := range []string{"replicas", "availableReplicas", updatedField} {
		actual, found, _ := unstructured.NestedInt64(deployment.Object, "status", field)
		if !found || actual != desired {
			return fmt.Errorf("MachineDeployment %s status %s is not complete", deployment.GetName(), field)
		}
	}
	observed, _, _ := unstructured.NestedInt64(deployment.Object, "status", "observedGeneration")
	if observed < deployment.GetGeneration() || deployment.GetAnnotations()[machineDeploymentRevisionAnnotation] == "" {
		return fmt.Errorf("MachineDeployment %s has not observed its current generation", deployment.GetName())
	}
	ownedSets := 0
	for _, workload := range workloads {
		if workload.GetDeletionTimestamp() != nil {
			return fmt.Errorf("%s %s is terminating", workload.GetKind(), workload.GetName())
		}
		if workload.GetKind() != "MachineSet" {
			continue
		}
		for _, owner := range workload.GetOwnerReferences() {
			if owner.UID != deployment.GetUID() {
				continue
			}
			ownedSets++
			replicas, _, _ := unstructured.NestedInt64(workload.Object, "status", "replicas")
			specReplicas, _, _ := unstructured.NestedInt64(workload.Object, "spec", "replicas")
			setObserved, _, _ := unstructured.NestedInt64(workload.Object, "status", "observedGeneration")
			if workload.GetAnnotations()[machineDeploymentRevisionAnnotation] != deployment.GetAnnotations()[machineDeploymentRevisionAnnotation] ||
				replicas != desired || specReplicas != desired || setObserved < workload.GetGeneration() {
				return fmt.Errorf("MachineSet %s has not converged to MachineDeployment %s", workload.GetName(), deployment.GetName())
			}
		}
	}
	if ownedSets != 1 {
		return fmt.Errorf("MachineDeployment %s should have exactly one initial MachineSet, got %d", deployment.GetName(), ownedSets)
	}
	return nil
}

func initialNodePoolConditionsComplete(nodePool *hyperv1.NodePool) error {
	for _, conditionType := range []string{hyperv1.NodePoolReadyConditionType, hyperv1.NodePoolUpdatingVersionConditionType, hyperv1.NodePoolUpdatingConfigConditionType, hyperv1.NodePoolUpdatingPlatformMachineTemplateConditionType} {
		found := false
		for _, condition := range nodePool.Status.Conditions {
			if condition.Type != conditionType {
				continue
			}
			want := "False"
			if conditionType == hyperv1.NodePoolReadyConditionType {
				want = "True"
			}
			if string(condition.Status) != want || condition.ObservedGeneration < nodePool.Generation {
				return fmt.Errorf("NodePool %s condition %s has not completed generation %d", nodePool.Name, conditionType, nodePool.Generation)
			}
			found = true
		}
		if !found {
			return fmt.Errorf("NodePool %s has no %s condition", nodePool.Name, conditionType)
		}
	}
	return nil
}

func operatorUpgradeWorkloadUnchanged(before, after *unstructured.Unstructured) error {
	if before.GetDeletionTimestamp() != nil || after.GetDeletionTimestamp() != nil {
		return fmt.Errorf("%s %s is terminating", before.GetKind(), before.GetName())
	}
	if before.GetUID() == "" || before.GetUID() != after.GetUID() {
		return fmt.Errorf("%s %s identity changed", before.GetKind(), before.GetName())
	}
	if before.GetAnnotations()[machineDeploymentRevisionAnnotation] != after.GetAnnotations()[machineDeploymentRevisionAnnotation] {
		return fmt.Errorf("%s %s revision changed", before.GetKind(), before.GetName())
	}
	expected := before.DeepCopy()
	allowedRemoval := false
	if before.GetKind() == "MachineDeployment" || before.GetKind() == "MachineSet" {
		annotations, _, _ := unstructured.NestedStringMap(expected.Object, "spec", "template", "metadata", "annotations")
		postAnnotations, _, _ := unstructured.NestedStringMap(after.Object, "spec", "template", "metadata", "annotations")
		_, existed := annotations[hyperv1.NodePoolReleaseVersionAnnotation]
		_, remains := postAnnotations[hyperv1.NodePoolReleaseVersionAnnotation]
		if existed && !remains {
			delete(annotations, hyperv1.NodePoolReleaseVersionAnnotation)
			if err := unstructured.SetNestedStringMap(expected.Object, annotations, "spec", "template", "metadata", "annotations"); err != nil {
				return err
			}
			allowedRemoval = true
		}
	}
	if !reflect.DeepEqual(expected.Object["spec"], after.Object["spec"]) {
		return fmt.Errorf("%s %s spec changed beyond release-version annotation removal", before.GetKind(), before.GetName())
	}
	wantGeneration := before.GetGeneration()
	if allowedRemoval {
		wantGeneration++
	}
	if after.GetGeneration() != wantGeneration {
		return fmt.Errorf("%s %s generation changed from %d to %d, expected %d", before.GetKind(), before.GetName(), before.GetGeneration(), after.GetGeneration(), wantGeneration)
	}
	return nil
}
