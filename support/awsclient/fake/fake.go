package fake

import (
	"context"

	"github.com/openshift/hypershift/support/awsclient"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

type awsClient struct {
}

func (c *awsClient) DescribeInstanceTypes(ctx context.Context, input *ec2.DescribeInstanceTypesInput) (*ec2.DescribeInstanceTypesOutput, error) {
	return &ec2.DescribeInstanceTypesOutput{
		InstanceTypes: []types.InstanceTypeInfo{
			{
				InstanceType: types.InstanceTypeA12xlarge,
				MemoryInfo: &types.MemoryInfo{
					SizeInMiB: aws.Int64(16384),
				},
				VCpuInfo: &types.VCpuInfo{
					DefaultVCpus: aws.Int32(8),
				},
				ProcessorInfo: &types.ProcessorInfo{
					SupportedArchitectures: []types.ArchitectureType{
						types.ArchitectureTypeX8664,
					},
				},
			},
			{
				InstanceType: types.InstanceTypeP216xlarge,
				MemoryInfo: &types.MemoryInfo{
					SizeInMiB: aws.Int64(749568),
				},
				VCpuInfo: &types.VCpuInfo{
					DefaultVCpus: aws.Int32(64),
				},
				GpuInfo: &types.GpuInfo{
					Gpus: []types.GpuDeviceInfo{
						{
							Name:         aws.String("K80"),
							Manufacturer: aws.String("NVIDIA"),
							Count:        aws.Int32(16),
							MemoryInfo: &types.GpuDeviceMemoryInfo{
								SizeInMiB: aws.Int32(12288),
							},
						},
					},
					TotalGpuMemoryInMiB: aws.Int32(196608),
				},
				ProcessorInfo: &types.ProcessorInfo{
					SupportedArchitectures: []types.ArchitectureType{
						types.ArchitectureTypeX8664,
					},
				},
			},
			{
				InstanceType: types.InstanceTypeM6g4xlarge,
				MemoryInfo: &types.MemoryInfo{
					SizeInMiB: aws.Int64(65536),
				},
				VCpuInfo: &types.VCpuInfo{
					DefaultVCpus: aws.Int32(16),
				},
				ProcessorInfo: &types.ProcessorInfo{
					SupportedArchitectures: []types.ArchitectureType{
						types.ArchitectureTypeArm64,
					},
				},
			},
			{
				// This instance type misses the specification of the CPU Architecture.
				InstanceType: types.InstanceTypeM6i8xlarge,
				MemoryInfo: &types.MemoryInfo{
					SizeInMiB: aws.Int64(131072),
				},
				VCpuInfo: &types.VCpuInfo{
					DefaultVCpus: aws.Int32(32),
				},
			},
		},
	}, nil
}

// NewClient creates a fake AWS client for testing.
func NewClient() awsclient.Client {
	return &awsClient{}
}
