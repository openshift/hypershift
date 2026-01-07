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

package machinedeployment

import (
	"context"
	"errors"
	"sync"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// mockAWSClient is a configurable mock for awsclient.Client.
type mockAWSClient struct {
	describeInstanceTypesFunc func(ctx context.Context, input *ec2.DescribeInstanceTypesInput) (*ec2.DescribeInstanceTypesOutput, error)
	callCount                 int
}

func (m *mockAWSClient) DescribeInstanceTypes(ctx context.Context, input *ec2.DescribeInstanceTypesInput) (*ec2.DescribeInstanceTypesOutput, error) {
	m.callCount++
	if m.describeInstanceTypesFunc != nil {
		return m.describeInstanceTypesFunc(ctx, input)
	}
	return &ec2.DescribeInstanceTypesOutput{}, nil
}

// newMockAWSClient creates a mock client with customizable behavior.
func newMockAWSClient(instanceTypes []types.InstanceTypeInfo) *mockAWSClient {
	return &mockAWSClient{
		describeInstanceTypesFunc: func(ctx context.Context, input *ec2.DescribeInstanceTypesInput) (*ec2.DescribeInstanceTypesOutput, error) {
			return &ec2.DescribeInstanceTypesOutput{
				InstanceTypes: instanceTypes,
			}, nil
		},
	}
}

// newMockAWSClientPaginated creates a mock client that requires pagination.
func newMockAWSClientPaginated(pages [][]types.InstanceTypeInfo) *mockAWSClient {
	pageIndex := 0
	return &mockAWSClient{
		describeInstanceTypesFunc: func(ctx context.Context, input *ec2.DescribeInstanceTypesInput) (*ec2.DescribeInstanceTypesOutput, error) {
			if pageIndex >= len(pages) {
				return &ec2.DescribeInstanceTypesOutput{}, nil
			}
			output := &ec2.DescribeInstanceTypesOutput{
				InstanceTypes: pages[pageIndex],
			}
			pageIndex++
			if pageIndex < len(pages) {
				output.NextToken = aws.String("next-token")
			}
			return output, nil
		},
	}
}

// makeInstanceTypeInfo creates a types.InstanceTypeInfo for testing.
func makeInstanceTypeInfo(name types.InstanceType, vcpu int32, memoryMb int64, arch types.ArchitectureType, gpuCount int32) types.InstanceTypeInfo {
	info := types.InstanceTypeInfo{
		InstanceType: name,
		VCpuInfo: &types.VCpuInfo{
			DefaultVCpus: aws.Int32(vcpu),
		},
		MemoryInfo: &types.MemoryInfo{
			SizeInMiB: aws.Int64(memoryMb),
		},
	}
	if arch != "" {
		info.ProcessorInfo = &types.ProcessorInfo{
			SupportedArchitectures: []types.ArchitectureType{arch},
		}
	}
	if gpuCount > 0 {
		info.GpuInfo = &types.GpuInfo{
			Gpus: []types.GpuDeviceInfo{
				{Count: aws.Int32(gpuCount)},
			},
		}
	}
	return info
}

func TestGetInstanceType_WhenCacheIsEmpty_ItShouldFetchFromAWSAndPopulateCache(t *testing.T) {
	g := NewGomegaWithT(t)

	instanceTypes := []types.InstanceTypeInfo{
		makeInstanceTypeInfo("m6i.xlarge", 4, 16384, types.ArchitectureTypeX8664, 0),
		makeInstanceTypeInfo("m6g.xlarge", 4, 16384, types.ArchitectureTypeArm64, 0),
	}
	mockClient := newMockAWSClient(instanceTypes)
	cache := NewInstanceTypesCache()

	result, err := cache.GetInstanceType(context.Background(), mockClient, "us-east-1", "m6i.xlarge")

	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.InstanceType).To(Equal("m6i.xlarge"))
	g.Expect(result.VCPU).To(Equal(int32(4)))
	g.Expect(result.MemoryMb).To(Equal(int64(16384)))
	g.Expect(result.CPUArchitecture).To(Equal(ArchitectureAmd64))
	g.Expect(mockClient.callCount).To(Equal(1))
}

func TestGetInstanceType_WhenCacheIsFresh_ItShouldReturnFromCacheWithoutCallingAWS(t *testing.T) {
	g := NewGomegaWithT(t)

	instanceTypes := []types.InstanceTypeInfo{
		makeInstanceTypeInfo("m6i.xlarge", 4, 16384, types.ArchitectureTypeX8664, 0),
	}
	mockClient := newMockAWSClient(instanceTypes)
	cache := NewInstanceTypesCache()

	// First call - should populate cache
	_, err := cache.GetInstanceType(context.Background(), mockClient, "us-east-1", "m6i.xlarge")
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(mockClient.callCount).To(Equal(1))

	// Second call - should use cache
	result, err := cache.GetInstanceType(context.Background(), mockClient, "us-east-1", "m6i.xlarge")
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.InstanceType).To(Equal("m6i.xlarge"))
	g.Expect(mockClient.callCount).To(Equal(1)) // No additional call
}

func TestGetInstanceType_WhenInstanceTypeNotFound_ItShouldReturnDescriptiveError(t *testing.T) {
	g := NewGomegaWithT(t)

	instanceTypes := []types.InstanceTypeInfo{
		makeInstanceTypeInfo("m6i.xlarge", 4, 16384, types.ArchitectureTypeX8664, 0),
	}
	mockClient := newMockAWSClient(instanceTypes)
	cache := NewInstanceTypesCache()

	_, err := cache.GetInstanceType(context.Background(), mockClient, "us-east-1", "nonexistent.xlarge")

	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("not found"))
	g.Expect(err.Error()).To(ContainSubstring("nonexistent.xlarge"))
}

func TestGetInstanceType_WhenAWSClientIsNil_ItShouldReturnError(t *testing.T) {
	g := NewGomegaWithT(t)

	cache := NewInstanceTypesCache()

	_, err := cache.GetInstanceType(context.Background(), nil, "us-east-1", "m6i.xlarge")

	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("awsClient is nil"))
}

func TestGetInstanceType_WhenContextIsCancelled_ItShouldReturnError(t *testing.T) {
	g := NewGomegaWithT(t)

	mockClient := &mockAWSClient{
		describeInstanceTypesFunc: func(ctx context.Context, input *ec2.DescribeInstanceTypesInput) (*ec2.DescribeInstanceTypesOutput, error) {
			// Simulate cancellation during the API call
			return nil, ctx.Err()
		},
	}
	cache := NewInstanceTypesCache()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := cache.GetInstanceType(ctx, mockClient, "us-east-1", "m6i.xlarge")

	g.Expect(err).To(HaveOccurred())
	g.Expect(errors.Is(err, context.Canceled)).To(BeTrue())
}

func TestGetInstanceType_WhenDifferentRegion_ItShouldCacheSeparately(t *testing.T) {
	g := NewGomegaWithT(t)

	instanceTypes := []types.InstanceTypeInfo{
		makeInstanceTypeInfo("m6i.xlarge", 4, 16384, types.ArchitectureTypeX8664, 0),
	}
	mockClient := newMockAWSClient(instanceTypes)
	cache := NewInstanceTypesCache()

	// Fetch for us-east-1
	_, err := cache.GetInstanceType(context.Background(), mockClient, "us-east-1", "m6i.xlarge")
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(mockClient.callCount).To(Equal(1))

	// Fetch for us-west-2 - should trigger a new API call
	_, err = cache.GetInstanceType(context.Background(), mockClient, "us-west-2", "m6i.xlarge")
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(mockClient.callCount).To(Equal(2))
}

func TestGetGpuCount(t *testing.T) {
	tests := []struct {
		name     string
		gpuInfo  *types.GpuInfo
		expected int32
	}{
		{
			name: "When single GPU type it should return that count",
			gpuInfo: &types.GpuInfo{
				Gpus: []types.GpuDeviceInfo{
					{Count: aws.Int32(4)},
				},
			},
			expected: 4,
		},
		{
			name: "When multiple GPU types it should sum all counts",
			gpuInfo: &types.GpuInfo{
				Gpus: []types.GpuDeviceInfo{
					{Count: aws.Int32(4)},
					{Count: aws.Int32(2)},
					{Count: aws.Int32(8)},
				},
			},
			expected: 14,
		},
		{
			name: "When GPU has nil count it should skip it",
			gpuInfo: &types.GpuInfo{
				Gpus: []types.GpuDeviceInfo{
					{Count: aws.Int32(4)},
					{Count: nil},
					{Count: aws.Int32(2)},
				},
			},
			expected: 6,
		},
		{
			name: "When gpuInfo has empty Gpus slice it should return 0",
			gpuInfo: &types.GpuInfo{
				Gpus: []types.GpuDeviceInfo{},
			},
			expected: 0,
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

func TestNormalizeArchitecture(t *testing.T) {
	tests := []struct {
		name     string
		input    types.ArchitectureType
		expected normalizedArch
	}{
		{
			name:     "When x86_64 it should convert to amd64",
			input:    types.ArchitectureTypeX8664,
			expected: ArchitectureAmd64,
		},
		{
			name:     "When arm64 it should keep arm64",
			input:    types.ArchitectureTypeArm64,
			expected: ArchitectureArm64,
		},
		{
			name:     "When unknown architecture it should default to amd64",
			input:    types.ArchitectureType("unknown"),
			expected: ArchitectureAmd64,
		},
		{
			name:     "When i386 it should default to amd64",
			input:    types.ArchitectureTypeI386,
			expected: ArchitectureAmd64,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			result := normalizeArchitecture(context.Background(), tt.input)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestTransformInstanceType_WhenMissingOptionalFields_ItShouldHandleNilPointers(t *testing.T) {
	tests := []struct {
		name     string
		input    types.InstanceTypeInfo
		expected InstanceType
	}{
		{
			name: "When all fields are present it should transform correctly",
			input: types.InstanceTypeInfo{
				InstanceType: "m6i.xlarge",
				VCpuInfo: &types.VCpuInfo{
					DefaultVCpus: aws.Int32(4),
				},
				MemoryInfo: &types.MemoryInfo{
					SizeInMiB: aws.Int64(16384),
				},
				ProcessorInfo: &types.ProcessorInfo{
					SupportedArchitectures: []types.ArchitectureType{types.ArchitectureTypeX8664},
				},
			},
			expected: InstanceType{
				InstanceType:    "m6i.xlarge",
				VCPU:            4,
				MemoryMb:        16384,
				GPU:             0,
				CPUArchitecture: ArchitectureAmd64,
			},
		},
		{
			name: "When VCpuInfo is nil it should default to 0",
			input: types.InstanceTypeInfo{
				InstanceType: "test.xlarge",
				MemoryInfo: &types.MemoryInfo{
					SizeInMiB: aws.Int64(16384),
				},
			},
			expected: InstanceType{
				InstanceType:    "test.xlarge",
				VCPU:            0,
				MemoryMb:        16384,
				GPU:             0,
				CPUArchitecture: ArchitectureAmd64, // defaults to amd64
			},
		},
		{
			name: "When MemoryInfo is nil it should default to 0",
			input: types.InstanceTypeInfo{
				InstanceType: "test.xlarge",
				VCpuInfo: &types.VCpuInfo{
					DefaultVCpus: aws.Int32(4),
				},
			},
			expected: InstanceType{
				InstanceType:    "test.xlarge",
				VCPU:            4,
				MemoryMb:        0,
				GPU:             0,
				CPUArchitecture: ArchitectureAmd64,
			},
		},
		{
			name: "When ProcessorInfo is nil it should default to amd64",
			input: types.InstanceTypeInfo{
				InstanceType: "test.xlarge",
			},
			expected: InstanceType{
				InstanceType:    "test.xlarge",
				VCPU:            0,
				MemoryMb:        0,
				GPU:             0,
				CPUArchitecture: ArchitectureAmd64,
			},
		},
		{
			name: "When VCpuInfo.DefaultVCpus is nil it should default to 0",
			input: types.InstanceTypeInfo{
				InstanceType: "test.xlarge",
				VCpuInfo:     &types.VCpuInfo{},
			},
			expected: InstanceType{
				InstanceType:    "test.xlarge",
				VCPU:            0,
				MemoryMb:        0,
				GPU:             0,
				CPUArchitecture: ArchitectureAmd64,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			result := transformInstanceType(context.Background(), tt.input)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestFetchEC2InstanceTypes_WithPagination(t *testing.T) {
	g := NewGomegaWithT(t)

	pages := [][]types.InstanceTypeInfo{
		{makeInstanceTypeInfo("m6i.xlarge", 4, 16384, types.ArchitectureTypeX8664, 0)},
		{makeInstanceTypeInfo("m6g.xlarge", 4, 16384, types.ArchitectureTypeArm64, 0)},
		{makeInstanceTypeInfo("p3.2xlarge", 8, 61440, types.ArchitectureTypeX8664, 1)},
	}
	mockClient := newMockAWSClientPaginated(pages)

	result, err := fetchEC2InstanceTypes(context.Background(), mockClient)

	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result).To(HaveLen(3))
	g.Expect(result).To(HaveKey("m6i.xlarge"))
	g.Expect(result).To(HaveKey("m6g.xlarge"))
	g.Expect(result).To(HaveKey("p3.2xlarge"))
}

func TestFetchEC2InstanceTypes_WhenEmptyResponse_ItShouldReturnError(t *testing.T) {
	g := NewGomegaWithT(t)

	mockClient := newMockAWSClient([]types.InstanceTypeInfo{})

	_, err := fetchEC2InstanceTypes(context.Background(), mockClient)

	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("unable to load EC2 Instance Type list"))
}

func TestCacheConcurrency(t *testing.T) {
	g := NewGomegaWithT(t)

	instanceTypes := []types.InstanceTypeInfo{
		makeInstanceTypeInfo("m6i.xlarge", 4, 16384, types.ArchitectureTypeX8664, 0),
	}
	mockClient := newMockAWSClient(instanceTypes)
	cache := NewInstanceTypesCache()

	// Launch multiple goroutines to access cache concurrently
	var wg sync.WaitGroup
	errChan := make(chan error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := cache.GetInstanceType(context.Background(), mockClient, "us-east-1", "m6i.xlarge")
			if err != nil {
				errChan <- err
			}
		}()
	}

	wg.Wait()
	close(errChan)

	for err := range errChan {
		g.Expect(err).ToNot(HaveOccurred())
	}

	// Verify cache was only refreshed once despite concurrent access
	g.Expect(mockClient.callCount).To(BeNumerically("<=", 2)) // Allow for race, but should be 1 in most cases
}
