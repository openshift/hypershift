package aws

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/instancetype"
	"github.com/openshift/hypershift/support/awsapi"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// Provider implements the instancetype.Provider interface for AWS.
// It queries EC2 DescribeInstanceTypes API to get instance type specifications.
type Provider struct {
	ec2Client awsapi.EC2API
}

// NewProvider creates a new AWS instance type provider with the given EC2 client.
// The caller is responsible for creating the EC2 client with the correct credentials and region.
func NewProvider(ec2Client awsapi.EC2API) *Provider {
	return &Provider{
		ec2Client: ec2Client,
	}
}

// GetInstanceTypeInfo queries EC2 API for instance type specifications.
// This information is used to populate cluster autoscaler capacity annotations
// for scaling from zero replicas.
func (p *Provider) GetInstanceTypeInfo(ctx context.Context, instanceType string) (*instancetype.InstanceTypeInfo, error) {
	// Query EC2 for the specific instance type
	input := &ec2.DescribeInstanceTypesInput{
		InstanceTypes: []ec2types.InstanceType{ec2types.InstanceType(instanceType)},
	}

	rawInstanceTypes, err := p.ec2Client.DescribeInstanceTypes(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("describeInstanceTypes request failed for %q: %w", instanceType, err)
	}

	if len(rawInstanceTypes.InstanceTypes) == 0 {
		return nil, fmt.Errorf("instance type %q not found", instanceType)
	}

	// Transform and validate AWS EC2 response
	result, err := transformInstanceTypeInfo(rawInstanceTypes.InstanceTypes[0])
	if err != nil {
		return nil, err
	}

	return result, nil
}

// transformInstanceTypeInfo converts EC2 InstanceTypeInfo to our common InstanceTypeInfo structure.
// It extracts and validates CPU, memory, GPU, and architecture information from the AWS response.
func transformInstanceTypeInfo(rawInstanceType ec2types.InstanceTypeInfo) (*instancetype.InstanceTypeInfo, error) {
	instanceTypeName := string(rawInstanceType.InstanceType)
	if instanceTypeName == "" {
		return nil, fmt.Errorf("instance type name is missing or empty")
	}
	info := &instancetype.InstanceTypeInfo{
		InstanceType: instanceTypeName,
	}

	// Extract and validate vCPU information (required)
	if rawInstanceType.VCpuInfo == nil || rawInstanceType.VCpuInfo.DefaultVCpus == nil {
		return nil, fmt.Errorf("missing vCPU information for instance type %q", instanceTypeName)
	}
	vcpu := *rawInstanceType.VCpuInfo.DefaultVCpus
	if vcpu <= 0 {
		return nil, fmt.Errorf("invalid vCPU count %d for instance type %q", vcpu, instanceTypeName)
	}
	info.VCPU = vcpu

	// Extract and validate memory information (required)
	if rawInstanceType.MemoryInfo == nil || rawInstanceType.MemoryInfo.SizeInMiB == nil {
		return nil, fmt.Errorf("missing memory information for instance type %q", instanceTypeName)
	}
	memoryMb := *rawInstanceType.MemoryInfo.SizeInMiB
	if memoryMb <= 0 {
		return nil, fmt.Errorf("invalid memory size %d for instance type %q", memoryMb, instanceTypeName)
	}
	info.MemoryMb = memoryMb

	// Extract GPU information (optional, defaults to 0)
	info.GPU = getGpuCount(rawInstanceType.GpuInfo)

	// Extract and normalize CPU architecture
	if rawInstanceType.ProcessorInfo == nil || len(rawInstanceType.ProcessorInfo.SupportedArchitectures) == 0 {
		return nil, fmt.Errorf("missing CPU architecture information for instance type %q", instanceTypeName)
	}
	for _, arch := range rawInstanceType.ProcessorInfo.SupportedArchitectures {
		switch arch {
		case ec2types.ArchitectureTypeX8664:
			info.CPUArchitecture = hyperv1.ArchitectureAMD64
			return info, nil
		case ec2types.ArchitectureTypeArm64:
			info.CPUArchitecture = hyperv1.ArchitectureARM64
			return info, nil
		}
	}

	return nil, fmt.Errorf("unsupported CPU architecture for instance type %q, supported: %v", instanceTypeName, rawInstanceType.ProcessorInfo.SupportedArchitectures)
}

// getGpuCount counts all the GPUs in GpuInfo.
// AWS instances can have multiple GPU devices, this sums them all.
// Returns 0 if gpuInfo is nil or contains no valid GPU entries.
func getGpuCount(gpuInfo *ec2types.GpuInfo) int32 {
	if gpuInfo == nil {
		return 0
	}

	gpuCountSum := int32(0)
	for _, gpu := range gpuInfo.Gpus {
		if gpu.Count != nil {
			gpuCountSum += *gpu.Count
		}
	}
	return gpuCountSum
}
