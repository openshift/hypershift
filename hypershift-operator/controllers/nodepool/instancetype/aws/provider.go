package aws

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/instancetype"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"

	"k8s.io/utils/ptr"
)

// Provider implements the instancetype.Provider interface for AWS.
// It queries EC2 DescribeInstanceTypes API to get instance type specifications.
type Provider struct {
	ec2Client ec2iface.EC2API
}

// NewProvider creates a new AWS instance type provider with the given EC2 client.
// The caller is responsible for creating the EC2 client with the correct credentials and region.
func NewProvider(ec2Client ec2iface.EC2API) *Provider {
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
		InstanceTypes: []*string{&instanceType},
	}

	rawInstanceTypes, err := p.ec2Client.DescribeInstanceTypesWithContext(ctx, input)
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
func transformInstanceTypeInfo(rawInstanceType *ec2.InstanceTypeInfo) (*instancetype.InstanceTypeInfo, error) {
	// Validate raw instance type data
	if rawInstanceType == nil {
		return nil, fmt.Errorf("rawInstanceType is nil")
	}

	instanceTypeName := ptr.Deref(rawInstanceType.InstanceType, "")
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
	vcpu := int32(*rawInstanceType.VCpuInfo.DefaultVCpus)
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
	if rawInstanceType.ProcessorInfo.SupportedArchitectures[0] == nil {
		return nil, fmt.Errorf("CPU architecture is nil for instance type %q", instanceTypeName)
	}
	architecture := *rawInstanceType.ProcessorInfo.SupportedArchitectures[0]
	switch architecture {
	case ec2.ArchitectureTypeX8664:
		info.CPUArchitecture = hyperv1.ArchitectureAMD64
	case ec2.ArchitectureTypeArm64:
		info.CPUArchitecture = hyperv1.ArchitectureARM64
	default:
		return nil, fmt.Errorf("unsupported CPU architecture %q for instance type %q", architecture, instanceTypeName)
	}

	return info, nil
}

// getGpuCount counts all the GPUs in GpuInfo.
// AWS instances can have multiple GPU devices, this sums them all.
// Returns 0 if gpuInfo is nil or contains no valid GPU entries.
func getGpuCount(gpuInfo *ec2.GpuInfo) int32 {
	if gpuInfo == nil || gpuInfo.Gpus == nil {
		return 0
	}

	gpuCountSum := int32(0)
	for _, gpu := range gpuInfo.Gpus {
		if gpu != nil && gpu.Count != nil {
			gpuCountSum += int32(*gpu.Count)
		}
	}
	return gpuCountSum
}
