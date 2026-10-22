//go:build e2ev2

package main

import (
	"fmt"
	"sync"

	"github.com/openshift/hypershift/test/e2e/v2/lifecycle"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func configureExpectedNodePoolRecorders(specs []namedSpec, manifest *lifecycle.ClusterManifest, directory string) {
	var mutex sync.Mutex
	for index := range specs {
		specs[index].recordExpectedNodePools = func(pools map[string]int32) error {
			mutex.Lock()
			defer mutex.Unlock()
			manifest.Clusters[index].ExpectedNodePools = pools
			if err := lifecycle.WriteManifest(directory, manifest); err != nil {
				return err
			}
			specs[index].expectedNodePools = pools
			return nil
		}
	}
}

func initialWorkersConfigured(cfg envConfig, spec namedSpec) bool {
	if spec.expectedNodePools == nil {
		return initialNodeCount(cfg, spec) > 0
	}
	for _, replicas := range spec.expectedNodePools {
		if replicas > 0 {
			return true
		}
	}
	return false
}

func recordAWSExpectedNodePools(namespace string, spec namedSpec, objects []*unstructured.Unstructured) error {
	pools := map[string]int32{}
	for _, object := range objects {
		if object.GetKind() != "NodePool" || object.GetAPIVersion() != "hypershift.openshift.io/v1beta1" {
			continue
		}
		cluster, _, err := unstructured.NestedString(object.Object, "spec", "clusterName")
		if err != nil || cluster != spec.name || object.GetNamespace() != namespace {
			return fmt.Errorf("rendered NodePool %q has unexpected cluster identity", object.GetName())
		}
		replicas, found, err := unstructured.NestedInt64(object.Object, "spec", "replicas")
		if err != nil || !found || replicas < 0 || replicas > 2147483647 {
			return fmt.Errorf("rendered NodePool %q has invalid configured replicas", object.GetName())
		}
		if _, exists := pools[object.GetName()]; exists {
			return fmt.Errorf("duplicate rendered NodePool %q", object.GetName())
		}
		pools[object.GetName()] = int32(replicas)
	}
	if err := lifecycle.ValidateExpectedNodePools(pools); err != nil {
		return err
	}
	if spec.recordExpectedNodePools == nil {
		return fmt.Errorf("missing rendered NodePool intent recorder")
	}
	return spec.recordExpectedNodePools(pools)
}
