/*
Copyright The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package utils

import (
	"context"
	"fmt"

	"k8s.io/klog/v2"

	infrav1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// RegionAnnotation is the fallback annotation for AWS region
	RegionAnnotation = "capa.infrastructure.cluster.x-k8s.io/region"
)

// ResolveAWSMachineTemplate fetches the AWSMachineTemplate referenced by the MachineDeployment
func ResolveAWSMachineTemplate(ctx context.Context, c client.Client, machineDeployment *clusterv1.MachineDeployment) (*infrav1.AWSMachineTemplate, error) {
	// Extract infrastructureRef
	infraRef := machineDeployment.Spec.Template.Spec.InfrastructureRef
	if infraRef.Name == "" {
		return nil, fmt.Errorf("infrastructureRef.name is empty")
	}

	// Validate it's an AWSMachineTemplate
	if infraRef.Kind != "AWSMachineTemplate" {
		return nil, fmt.Errorf("expected AWSMachineTemplate, got %s", infraRef.Kind)
	}

	// Fetch the template
	template := &infrav1.AWSMachineTemplate{}
	key := client.ObjectKey{
		Name:      infraRef.Name,
		Namespace: infraRef.Namespace,
	}
	// Use same namespace as MachineDeployment if not specified
	if key.Namespace == "" {
		key.Namespace = machineDeployment.Namespace
	}

	if err := c.Get(ctx, key, template); err != nil {
		return nil, fmt.Errorf("failed to fetch AWSMachineTemplate %s/%s: %w", key.Namespace, key.Name, err)
	}

	klog.V(3).Infof("Resolved AWSMachineTemplate %s/%s for MachineDeployment %s", key.Namespace, key.Name, machineDeployment.Name)
	return template, nil
}

// ExtractInstanceType gets the instance type from AWSMachineTemplate
func ExtractInstanceType(template *infrav1.AWSMachineTemplate) (string, error) {
	if template == nil {
		return "", fmt.Errorf("AWSMachineTemplate is nil")
	}
	if template.Spec.Template.Spec.InstanceType == "" {
		return "", fmt.Errorf("instanceType is empty in AWSMachineTemplate")
	}
	return template.Spec.Template.Spec.InstanceType, nil
}

// ResolveRegion attempts to get AWS region from AWSCluster, falls back to annotation
func ResolveRegion(ctx context.Context, c client.Client, machineDeployment *clusterv1.MachineDeployment) (string, error) {
	// Try to get region from AWSCluster
	if machineDeployment.Spec.ClusterName != "" {
		region, err := getRegionFromAWSCluster(ctx, c, machineDeployment)
		if err == nil {
			return region, nil
		}
		klog.V(3).Infof("Failed to get region from AWSCluster: %v, trying annotation fallback", err)
	}

	// Fallback to annotation
	if region, ok := machineDeployment.Annotations[RegionAnnotation]; ok && region != "" {
		klog.V(3).Infof("Using region %s from annotation %s", region, RegionAnnotation)
		return region, nil
	}

	return "", fmt.Errorf("unable to determine AWS region from AWSCluster or annotation %s", RegionAnnotation)
}

// getRegionFromAWSCluster fetches region from the AWSCluster resource
func getRegionFromAWSCluster(ctx context.Context, c client.Client, machineDeployment *clusterv1.MachineDeployment) (string, error) {
	// Fetch the Cluster resource
	cluster := &clusterv1.Cluster{}
	clusterKey := client.ObjectKey{
		Name:      machineDeployment.Spec.ClusterName,
		Namespace: machineDeployment.Namespace,
	}

	if err := c.Get(ctx, clusterKey, cluster); err != nil {
		return "", fmt.Errorf("failed to fetch Cluster %s/%s: %w", clusterKey.Namespace, clusterKey.Name, err)
	}

	// Fetch AWSCluster
	if cluster.Spec.InfrastructureRef == nil {
		return "", fmt.Errorf("cluster %s has nil infrastructureRef", cluster.Name)
	}
	if cluster.Spec.InfrastructureRef.Name == "" {
		return "", fmt.Errorf("cluster %s has empty infrastructureRef.Name", cluster.Name)
	}

	awsCluster := &infrav1.AWSCluster{}
	awsClusterKey := client.ObjectKey{
		Name:      cluster.Spec.InfrastructureRef.Name,
		Namespace: cluster.Spec.InfrastructureRef.Namespace,
	}
	if awsClusterKey.Namespace == "" {
		awsClusterKey.Namespace = cluster.Namespace
	}

	if err := c.Get(ctx, awsClusterKey, awsCluster); err != nil {
		return "", fmt.Errorf("failed to fetch AWSCluster %s/%s: %w", awsClusterKey.Namespace, awsClusterKey.Name, err)
	}

	if awsCluster.Spec.Region == "" {
		return "", fmt.Errorf("AWSCluster %s has empty region", awsCluster.Name)
	}

	klog.V(3).Infof("Resolved region %s from AWSCluster %s", awsCluster.Spec.Region, awsClusterKey.Name)
	return awsCluster.Spec.Region, nil
}
