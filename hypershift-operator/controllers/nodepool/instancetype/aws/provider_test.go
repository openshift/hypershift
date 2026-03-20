package aws

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/instancetype"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
)

// mockEC2Client is a configurable mock for ec2iface.EC2API.
type mockEC2Client struct {
	ec2iface.EC2API
	describeInstanceTypesFunc func(input *ec2.DescribeInstanceTypesInput) (*ec2.DescribeInstanceTypesOutput, error)
}

func (m *mockEC2Client) DescribeInstanceTypesWithContext(ctx aws.Context, input *ec2.DescribeInstanceTypesInput, opts ...request.Option) (*ec2.DescribeInstanceTypesOutput, error) {
	if m.describeInstanceTypesFunc != nil {
		return m.describeInstanceTypesFunc(input)
	}
	return &ec2.DescribeInstanceTypesOutput{}, nil
}

// newMockEC2Client creates a mock client with customizable behavior.
func newMockEC2Client(instanceTypes []*ec2.InstanceTypeInfo) *mockEC2Client {
	return &mockEC2Client{
		describeInstanceTypesFunc: func(input *ec2.DescribeInstanceTypesInput) (*ec2.DescribeInstanceTypesOutput, error) {
			return &ec2.DescribeInstanceTypesOutput{
				InstanceTypes: instanceTypes,
			}, nil
		},
	}
}

// makeInstanceTypeInfo creates an ec2.InstanceTypeInfo for testing.
func makeInstanceTypeInfo(name, arch string, vcpu int64, memoryMb int64, gpuCount int64) *ec2.InstanceTypeInfo {
	info := &ec2.InstanceTypeInfo{
		InstanceType: aws.String(name),
		VCpuInfo: &ec2.VCpuInfo{
			DefaultVCpus: aws.Int64(vcpu),
		},
		MemoryInfo: &ec2.MemoryInfo{
			SizeInMiB: aws.Int64(memoryMb),
		},
	}
	if arch != "" {
		info.ProcessorInfo = &ec2.ProcessorInfo{
			SupportedArchitectures: []*string{aws.String(arch)},
		}
	}
	if gpuCount > 0 {
		info.GpuInfo = &ec2.GpuInfo{
			Gpus: []*ec2.GpuDeviceInfo{
				{Count: aws.Int64(gpuCount)},
			},
		}
	}
	return info
}

func TestGetGpuCount(t *testing.T) {
	tests := []struct {
		name     string
		gpuInfo  *ec2.GpuInfo
		expected int32
	}{
		{
			name: "When single GPU type it should return that count",
			gpuInfo: &ec2.GpuInfo{
				Gpus: []*ec2.GpuDeviceInfo{
					{Count: aws.Int64(4)},
				},
			},
			expected: 4,
		},
		{
			name: "When multiple GPU types it should sum all counts",
			gpuInfo: &ec2.GpuInfo{
				Gpus: []*ec2.GpuDeviceInfo{
					{Count: aws.Int64(4)},
					{Count: aws.Int64(2)},
					{Count: aws.Int64(8)},
				},
			},
			expected: 14,
		},
		{
			name: "When GPU has nil count it should skip it",
			gpuInfo: &ec2.GpuInfo{
				Gpus: []*ec2.GpuDeviceInfo{
					{Count: aws.Int64(4)},
					{Count: nil},
					{Count: aws.Int64(2)},
				},
			},
			expected: 6,
		},
		{
			name: "When gpuInfo has empty Gpus slice it should return 0",
			gpuInfo: &ec2.GpuInfo{
				Gpus: []*ec2.GpuDeviceInfo{},
			},
			expected: 0,
		},
		{
			name:     "When gpuInfo is nil it should return 0",
			gpuInfo:  nil,
			expected: 0,
		},
		{
			name: "When gpuInfo.Gpus is nil it should return 0",
			gpuInfo: &ec2.GpuInfo{
				Gpus: nil,
			},
			expected: 0,
		},
		{
			name: "When Gpus array contains nil entries it should skip them",
			gpuInfo: &ec2.GpuInfo{
				Gpus: []*ec2.GpuDeviceInfo{
					{Count: aws.Int64(4)},
					nil,
					{Count: aws.Int64(2)},
				},
			},
			expected: 6,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			result := getGpuCount(tt.gpuInfo)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestTransformInstanceTypeInfo_WhenMissingRequiredFields_ItShouldReturnError(t *testing.T) {
	tests := []struct {
		name          string
		input         *ec2.InstanceTypeInfo
		expectedError string
	}{
		{
			name:          "When input is nil it should return error",
			input:         nil,
			expectedError: "rawInstanceType is nil",
		},
		{
			name: "When InstanceType name is nil it should return error",
			input: &ec2.InstanceTypeInfo{
				VCpuInfo: &ec2.VCpuInfo{
					DefaultVCpus: aws.Int64(4),
				},
				MemoryInfo: &ec2.MemoryInfo{
					SizeInMiB: aws.Int64(16384),
				},
			},
			expectedError: "instance type name is missing",
		},
		{
			name: "When VCpuInfo is nil it should return error",
			input: &ec2.InstanceTypeInfo{
				InstanceType: aws.String("test.xlarge"),
				MemoryInfo: &ec2.MemoryInfo{
					SizeInMiB: aws.Int64(16384),
				},
			},
			expectedError: "missing vCPU information",
		},
		{
			name: "When DefaultVCpus is nil it should return error",
			input: &ec2.InstanceTypeInfo{
				InstanceType: aws.String("test.xlarge"),
				VCpuInfo:     &ec2.VCpuInfo{},
				MemoryInfo: &ec2.MemoryInfo{
					SizeInMiB: aws.Int64(16384),
				},
			},
			expectedError: "missing vCPU information",
		},
		{
			name: "When vCPU count is zero it should return error",
			input: &ec2.InstanceTypeInfo{
				InstanceType: aws.String("test.xlarge"),
				VCpuInfo: &ec2.VCpuInfo{
					DefaultVCpus: aws.Int64(0),
				},
				MemoryInfo: &ec2.MemoryInfo{
					SizeInMiB: aws.Int64(16384),
				},
			},
			expectedError: "invalid vCPU count",
		},
		{
			name: "When MemoryInfo is nil it should return error",
			input: &ec2.InstanceTypeInfo{
				InstanceType: aws.String("test.xlarge"),
				VCpuInfo: &ec2.VCpuInfo{
					DefaultVCpus: aws.Int64(4),
				},
			},
			expectedError: "missing memory information",
		},
		{
			name: "When SizeInMiB is nil it should return error",
			input: &ec2.InstanceTypeInfo{
				InstanceType: aws.String("test.xlarge"),
				VCpuInfo: &ec2.VCpuInfo{
					DefaultVCpus: aws.Int64(4),
				},
				MemoryInfo: &ec2.MemoryInfo{},
			},
			expectedError: "missing memory information",
		},
		{
			name: "When memory size is zero it should return error",
			input: &ec2.InstanceTypeInfo{
				InstanceType: aws.String("test.xlarge"),
				VCpuInfo: &ec2.VCpuInfo{
					DefaultVCpus: aws.Int64(4),
				},
				MemoryInfo: &ec2.MemoryInfo{
					SizeInMiB: aws.Int64(0),
				},
			},
			expectedError: "invalid memory size",
		},
		{
			name: "When ProcessorInfo is nil it should return error",
			input: &ec2.InstanceTypeInfo{
				InstanceType: aws.String("test.xlarge"),
				VCpuInfo: &ec2.VCpuInfo{
					DefaultVCpus: aws.Int64(4),
				},
				MemoryInfo: &ec2.MemoryInfo{
					SizeInMiB: aws.Int64(16384),
				},
			},
			expectedError: "missing CPU architecture information",
		},
		{
			name:          "When architecture is unsupported it should return error",
			input:         makeInstanceTypeInfo("t2.micro", ec2.ArchitectureTypeI386, 1, 1024, 0),
			expectedError: "unsupported CPU architecture",
		},
		{
			name: "When SupportedArchitectures[0] is nil it should return error",
			input: &ec2.InstanceTypeInfo{
				InstanceType: aws.String("test.xlarge"),
				VCpuInfo: &ec2.VCpuInfo{
					DefaultVCpus: aws.Int64(4),
				},
				MemoryInfo: &ec2.MemoryInfo{
					SizeInMiB: aws.Int64(16384),
				},
				ProcessorInfo: &ec2.ProcessorInfo{
					SupportedArchitectures: []*string{nil},
				},
			},
			expectedError: "CPU architecture is nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			_, err := transformInstanceTypeInfo(tt.input)
			g.Expect(err).To(HaveOccurred())
			g.Expect(err.Error()).To(ContainSubstring(tt.expectedError))
		})
	}
}

func TestTransformInstanceTypeInfo_WhenValidInput_ItShouldTransformCorrectly(t *testing.T) {
	tests := []struct {
		name     string
		input    *ec2.InstanceTypeInfo
		expected *instancetype.InstanceTypeInfo
	}{
		{
			name: "When all fields are present it should transform correctly",
			input: &ec2.InstanceTypeInfo{
				InstanceType: aws.String("m6i.xlarge"),
				VCpuInfo: &ec2.VCpuInfo{
					DefaultVCpus: aws.Int64(4),
				},
				MemoryInfo: &ec2.MemoryInfo{
					SizeInMiB: aws.Int64(16384),
				},
				ProcessorInfo: &ec2.ProcessorInfo{
					SupportedArchitectures: []*string{aws.String(ec2.ArchitectureTypeX8664)},
				},
			},
			expected: &instancetype.InstanceTypeInfo{
				InstanceType:    "m6i.xlarge",
				VCPU:            4,
				MemoryMb:        16384,
				GPU:             0,
				CPUArchitecture: hyperv1.ArchitectureAMD64,
			},
		},
		{
			name:  "When instance has GPU it should set GPU count",
			input: makeInstanceTypeInfo("p3.2xlarge", ec2.ArchitectureTypeX8664, 8, 61440, 1),
			expected: &instancetype.InstanceTypeInfo{
				InstanceType:    "p3.2xlarge",
				VCPU:            8,
				MemoryMb:        61440,
				GPU:             1,
				CPUArchitecture: hyperv1.ArchitectureAMD64,
			},
		},
		{
			name:  "When instance is ARM it should set correct architecture",
			input: makeInstanceTypeInfo("m6g.xlarge", ec2.ArchitectureTypeArm64, 4, 16384, 0),
			expected: &instancetype.InstanceTypeInfo{
				InstanceType:    "m6g.xlarge",
				VCPU:            4,
				MemoryMb:        16384,
				GPU:             0,
				CPUArchitecture: hyperv1.ArchitectureARM64,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			result, err := transformInstanceTypeInfo(tt.input)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestGetInstanceTypeInfo(t *testing.T) {
	tests := []struct {
		name          string
		mockClient    *mockEC2Client
		instanceType  string
		expected      *instancetype.InstanceTypeInfo
		expectedError string
	}{
		{
			name: "When instance type exists it should return info",
			mockClient: newMockEC2Client([]*ec2.InstanceTypeInfo{
				makeInstanceTypeInfo("m6i.xlarge", ec2.ArchitectureTypeX8664, 4, 16384, 0),
			}),
			instanceType: "m6i.xlarge",
			expected: &instancetype.InstanceTypeInfo{
				InstanceType:    "m6i.xlarge",
				VCPU:            4,
				MemoryMb:        16384,
				GPU:             0,
				CPUArchitecture: hyperv1.ArchitectureAMD64,
			},
		},
		{
			name:          "When instance type not found it should return error",
			mockClient:    newMockEC2Client([]*ec2.InstanceTypeInfo{}),
			instanceType:  "nonexistent.xlarge",
			expectedError: "not found",
		},
		{
			name: "When API returns error it should propagate error",
			mockClient: &mockEC2Client{
				describeInstanceTypesFunc: func(input *ec2.DescribeInstanceTypesInput) (*ec2.DescribeInstanceTypesOutput, error) {
					return nil, fmt.Errorf("API error: throttling")
				},
			},
			instanceType:  "m6i.xlarge",
			expectedError: "describeInstanceTypes request failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			provider := NewProvider(tt.mockClient)
			result, err := provider.GetInstanceTypeInfo(context.Background(), tt.instanceType)

			if tt.expectedError != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.expectedError))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(result).To(Equal(tt.expected))
			}
		})
	}
}
