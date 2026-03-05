package metrics

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"

	awsv2 "github.com/aws/aws-sdk-go-v2/aws"
	ec2v2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2typesv2 "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	pricingv2 "github.com/aws/aws-sdk-go-v2/service/pricing"
	"github.com/aws/smithy-go"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/clock"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

var (
	ignoreUnexportedDto = cmpopts.IgnoreUnexported(dto.MetricFamily{}, dto.Metric{}, dto.LabelPair{}, dto.Gauge{})
)

// awsError creates a smithy.APIError compatible error
type awsError struct {
	code    string
	message string
	fault   smithy.ErrorFault
}

func (e *awsError) Error() string {
	return fmt.Sprintf("%s: %s", e.code, e.message)
}

func (e *awsError) ErrorCode() string {
	return e.code
}

func (e *awsError) ErrorMessage() string {
	return e.message
}

func (e *awsError) ErrorFault() smithy.ErrorFault {
	return e.fault
}

func newAWSError(code, message string) error {
	return &awsError{
		code:    code,
		message: message,
		fault:   smithy.FaultUnknown,
	}
}

type nodePoolParams struct {
	availableNodesCount int32
	ec2InstanceType     string
}

type Ec2ClientMock struct {
	MockedDescribeInstanceTypesFunc func(ctx context.Context, input *ec2v2.DescribeInstanceTypesInput, optFns ...func(*ec2v2.Options)) (*ec2v2.DescribeInstanceTypesOutput, error)
}

func initDescribeInstanceTypesOutput(instanceTypeInfo []ec2typesv2.InstanceTypeInfo) *ec2v2.DescribeInstanceTypesOutput {
	return &ec2v2.DescribeInstanceTypesOutput{
		InstanceTypes: instanceTypeInfo,
	}
}

func initInstanceTypeInfo(instanceType string, vCpusCount int32) ec2typesv2.InstanceTypeInfo {
	return ec2typesv2.InstanceTypeInfo{
		InstanceType: ec2typesv2.InstanceType(instanceType),
		VCpuInfo: &ec2typesv2.VCpuInfo{
			DefaultVCpus: ptr.To[int32](vCpusCount),
		},
	}
}

func (c *Ec2ClientMock) DescribeInstanceTypes(ctx context.Context, input *ec2v2.DescribeInstanceTypesInput, optFns ...func(*ec2v2.Options)) (*ec2v2.DescribeInstanceTypesOutput, error) {
	return c.MockedDescribeInstanceTypesFunc(ctx, input, optFns...)
}

type PricingClientMock struct {
	MockedGetProductsFunc func(ctx context.Context, input *pricingv2.GetProductsInput, optFns ...func(*pricingv2.Options)) (*pricingv2.GetProductsOutput, error)
}

func (m *PricingClientMock) GetProducts(ctx context.Context, input *pricingv2.GetProductsInput, optFns ...func(*pricingv2.Options)) (*pricingv2.GetProductsOutput, error) {
	return m.MockedGetProductsFunc(ctx, input, optFns...)
}

func TestReportVCpusCountByHCluster(t *testing.T) {
	testCases := []struct {
		name      string
		npsParams []nodePoolParams
		configMap *corev1.ConfigMap

		MockedEC2DescribeInstanceTypesFunc func(ctx context.Context, input *ec2v2.DescribeInstanceTypesInput, optFns ...func(*ec2v2.Options)) (*ec2v2.DescribeInstanceTypesOutput, error)
		MockedGetProductsFunc              func(ctx context.Context, input *pricingv2.GetProductsInput, optFns ...func(*pricingv2.Options)) (*pricingv2.GetProductsOutput, error)

		// expected results
		expectedVCpusCount            float64
		expectedVCpusCountErrorReason string
	}{
		{
			name:               "When there is no nodePool, the total number of worker vCpus is 0",
			npsParams:          []nodePoolParams{},
			expectedVCpusCount: 0,
		},
		{
			name: "When there is one nodePool with no m5.xlarge nodes available, the total number of worker vCpus is 0",
			npsParams: []nodePoolParams{
				{availableNodesCount: 0, ec2InstanceType: "m5.xlarge"},
			},
			MockedEC2DescribeInstanceTypesFunc: func(ctx context.Context, input *ec2v2.DescribeInstanceTypesInput, optFns ...func(*ec2v2.Options)) (*ec2v2.DescribeInstanceTypesOutput, error) {
				return initDescribeInstanceTypesOutput([]ec2typesv2.InstanceTypeInfo{}), nil
			},
			expectedVCpusCount: 0,
		},
		{
			name: "When there is one nodePool with 2 m5.xlarge nodes available, the total number of worker vCPUs is 8",
			npsParams: []nodePoolParams{
				{availableNodesCount: 2, ec2InstanceType: "m5.xlarge"},
			},
			MockedEC2DescribeInstanceTypesFunc: func(ctx context.Context, input *ec2v2.DescribeInstanceTypesInput, optFns ...func(*ec2v2.Options)) (*ec2v2.DescribeInstanceTypesOutput, error) {
				return initDescribeInstanceTypesOutput([]ec2typesv2.InstanceTypeInfo{
					initInstanceTypeInfo("m5.xlarge", 4)}), nil
			},
			expectedVCpusCount: 8,
		},
		{
			name: "When there are two nodePools with 2 m5.2xlarge nodes available each, the total number of worker vCPUs is 32",
			npsParams: []nodePoolParams{
				{availableNodesCount: 2, ec2InstanceType: "m5.2xlarge"},
				{availableNodesCount: 2, ec2InstanceType: "m5.2xlarge"},
			},
			MockedEC2DescribeInstanceTypesFunc: func(ctx context.Context, input *ec2v2.DescribeInstanceTypesInput, optFns ...func(*ec2v2.Options)) (*ec2v2.DescribeInstanceTypesOutput, error) {
				return initDescribeInstanceTypesOutput([]ec2typesv2.InstanceTypeInfo{
					initInstanceTypeInfo("m5.2xlarge", 8)}), nil
			},
			expectedVCpusCount: 32,
		},

		{
			name: "When the nodePool EC2 instance type is not valid, we fetch value from pricing APIs",
			npsParams: []nodePoolParams{
				{availableNodesCount: 2, ec2InstanceType: "hello_world"},
			},
			MockedEC2DescribeInstanceTypesFunc: func(ctx context.Context, input *ec2v2.DescribeInstanceTypesInput, optFns ...func(*ec2v2.Options)) (*ec2v2.DescribeInstanceTypesOutput, error) {
				return &ec2v2.DescribeInstanceTypesOutput{}, nil
			},
			MockedGetProductsFunc: func(ctx context.Context, input *pricingv2.GetProductsInput, optFns ...func(*pricingv2.Options)) (*pricingv2.GetProductsOutput, error) {
				return &pricingv2.GetProductsOutput{}, fmt.Errorf("pricing API unavailable")
			},
			expectedVCpusCount:            -1,
			expectedVCpusCountErrorReason: string(failedToCallAWSErrorReason),
		},
		{
			name: "When EC2 and pricing API fail and configMap fails too, we return the actual error reason",
			npsParams: []nodePoolParams{
				{availableNodesCount: 2, ec2InstanceType: "dream-instance.xlarge"},
			},
			MockedEC2DescribeInstanceTypesFunc: func(ctx context.Context, input *ec2v2.DescribeInstanceTypesInput, optFns ...func(*ec2v2.Options)) (*ec2v2.DescribeInstanceTypesOutput, error) {
				return initDescribeInstanceTypesOutput([]ec2typesv2.InstanceTypeInfo{
					initInstanceTypeInfo("dream-instance.xlarge", 4)}), newAWSError("InvalidInstanceType", "the instance type is not recognized")
			},
			MockedGetProductsFunc: func(ctx context.Context, input *pricingv2.GetProductsInput, optFns ...func(*pricingv2.Options)) (*pricingv2.GetProductsOutput, error) {
				return nil, fmt.Errorf("pricing API request failed")
			},

			expectedVCpusCount:            -1,
			expectedVCpusCountErrorReason: string(failedToCallAWSErrorReason),
		},

		{
			name: "When EC2 DescribeInstanceTypes return InvalidInstanceType error, pricing.GetProducts return valid but empty data",
			npsParams: []nodePoolParams{
				{availableNodesCount: 2, ec2InstanceType: "dream-instance.xlarge"},
			},
			MockedEC2DescribeInstanceTypesFunc: func(ctx context.Context, input *ec2v2.DescribeInstanceTypesInput, optFns ...func(*ec2v2.Options)) (*ec2v2.DescribeInstanceTypesOutput, error) {
				return initDescribeInstanceTypesOutput([]ec2typesv2.InstanceTypeInfo{
					initInstanceTypeInfo("dream-instance.xlarge", 4)}), newAWSError("InvalidInstanceType", "the instance type is not recognized")
			},
			MockedGetProductsFunc: func(ctx context.Context, input *pricingv2.GetProductsInput, optFns ...func(*pricingv2.Options)) (*pricingv2.GetProductsOutput, error) {
				return &pricingv2.GetProductsOutput{
					PriceList: []string{
						`{"bad": "data"}`,
					},
				}, nil
			},
			expectedVCpusCount:            -1,
			expectedVCpusCountErrorReason: string(unknownInstanceTypeErrorReason),
		},
		{
			name: "When EC2 DescribeInstanceTypes return InvalidInstanceType error, pricing.GetProducts return bad data",
			npsParams: []nodePoolParams{
				{availableNodesCount: 2, ec2InstanceType: "dream-instance.xlarge"},
			},
			MockedEC2DescribeInstanceTypesFunc: func(ctx context.Context, input *ec2v2.DescribeInstanceTypesInput, optFns ...func(*ec2v2.Options)) (*ec2v2.DescribeInstanceTypesOutput, error) {
				return initDescribeInstanceTypesOutput([]ec2typesv2.InstanceTypeInfo{
					initInstanceTypeInfo("dream-instance.xlarge", 4)}), newAWSError("InvalidInstanceType", "the instance type is not recognized")
			},
			MockedGetProductsFunc: func(ctx context.Context, input *pricingv2.GetProductsInput, optFns ...func(*pricingv2.Options)) (*pricingv2.GetProductsOutput, error) {
				return &pricingv2.GetProductsOutput{
					PriceList: []string{
						`{"product": {"attributes": "foo"}}`,
					},
				}, nil
			},
			expectedVCpusCount:            -1,
			expectedVCpusCountErrorReason: string(unknownInstanceTypeErrorReason),
		},
		{
			name: "When EC2 DescribeInstanceTypes return InvalidInstanceType error, pricing.GetProducts return nil",
			npsParams: []nodePoolParams{
				{availableNodesCount: 2, ec2InstanceType: "dream-instance.xlarge"},
			},
			MockedEC2DescribeInstanceTypesFunc: func(ctx context.Context, input *ec2v2.DescribeInstanceTypesInput, optFns ...func(*ec2v2.Options)) (*ec2v2.DescribeInstanceTypesOutput, error) {
				return initDescribeInstanceTypesOutput([]ec2typesv2.InstanceTypeInfo{
					initInstanceTypeInfo("dream-instance.xlarge", 4)}), newAWSError("InvalidInstanceType", "the instance type is not recognized")
			},
			MockedGetProductsFunc: func(ctx context.Context, input *pricingv2.GetProductsInput, optFns ...func(*pricingv2.Options)) (*pricingv2.GetProductsOutput, error) {
				return &pricingv2.GetProductsOutput{
					PriceList: nil,
				}, nil
			},
			expectedVCpusCount:            -1,
			expectedVCpusCountErrorReason: string(unexpectedAWSOutputErrorReason),
		},
		{
			name: "When EC2 DescribeInstanceTypes return InvalidInstanceType error, pricing.GetProducts return bad CPU value",
			npsParams: []nodePoolParams{
				{availableNodesCount: 2, ec2InstanceType: "i3.metal"},
			},
			MockedEC2DescribeInstanceTypesFunc: func(ctx context.Context, input *ec2v2.DescribeInstanceTypesInput, optFns ...func(*ec2v2.Options)) (*ec2v2.DescribeInstanceTypesOutput, error) {
				return nil, newAWSError("InvalidInstanceType", "the instance type is not recognized")
			},
			MockedGetProductsFunc: func(ctx context.Context, input *pricingv2.GetProductsInput, optFns ...func(*pricingv2.Options)) (*pricingv2.GetProductsOutput, error) {
				return &pricingv2.GetProductsOutput{
					FormatVersion: awsv2.String("aws_v1"),
					NextToken:     awsv2.String("AAMA-TOKEN"),
					PriceList: []string{
						`{"product": {"attributes": {"instanceType": "i3.metal", "vcpu": "should be a number"}}}`,
					}}, nil
			},
			expectedVCpusCount:            -1,
			expectedVCpusCountErrorReason: string(unknownInstanceTypeErrorReason),
		},
		{
			name: "When EC2 DescribeInstanceTypes return InvalidInstanceType error, pricing.GetProducts return valid data",
			npsParams: []nodePoolParams{
				{availableNodesCount: 2, ec2InstanceType: "i3.metal"},
			},
			MockedEC2DescribeInstanceTypesFunc: func(ctx context.Context, input *ec2v2.DescribeInstanceTypesInput, optFns ...func(*ec2v2.Options)) (*ec2v2.DescribeInstanceTypesOutput, error) {
				return nil, newAWSError("InvalidInstanceType", "the instance type is not recognized")
			},
			MockedGetProductsFunc: func(ctx context.Context, input *pricingv2.GetProductsInput, optFns ...func(*pricingv2.Options)) (*pricingv2.GetProductsOutput, error) {
				return &pricingv2.GetProductsOutput{
					FormatVersion: awsv2.String("aws_v1"),
					NextToken:     awsv2.String("AAMA-TOKEN"),
					PriceList: []string{
						`{"product": {"attributes": {"instanceType": "i3.metal", "vcpu": "72"}}}`,
					}}, nil
			},
			expectedVCpusCount:            144,
			expectedVCpusCountErrorReason: "",
		},
		{
			name: "When EC2 and Pricing APIs fail but ConfigMap contains the instance type, it should resolve vCPUs from ConfigMap",
			npsParams: []nodePoolParams{
				{availableNodesCount: 2, ec2InstanceType: "dream-instance.xlarge"},
			},
			configMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      rosaCPUsInstanceTypeConfigMapName,
					Namespace: rosaCPUInstanceTypeConfigMapNamespace,
				},
				Data: map[string]string{
					"dream-instance.xlarge": "12",
				},
			},
			MockedEC2DescribeInstanceTypesFunc: func(ctx context.Context, input *ec2v2.DescribeInstanceTypesInput, optFns ...func(*ec2v2.Options)) (*ec2v2.DescribeInstanceTypesOutput, error) {
				return nil, newAWSError("InvalidInstanceType", "the instance type is not recognized")
			},
			MockedGetProductsFunc: func(ctx context.Context, input *pricingv2.GetProductsInput, optFns ...func(*pricingv2.Options)) (*pricingv2.GetProductsOutput, error) {
				return nil, fmt.Errorf("pricing API unavailable")
			},

			expectedVCpusCount:            24,
			expectedVCpusCountErrorReason: "",
		},
		{
			name: "When EC2 API succeeds and ConfigMap also has a value, it should use the EC2 API vCPU count",
			npsParams: []nodePoolParams{
				{availableNodesCount: 3, ec2InstanceType: "m5.xlarge"},
			},
			configMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      rosaCPUsInstanceTypeConfigMapName,
					Namespace: rosaCPUInstanceTypeConfigMapNamespace,
				},
				Data: map[string]string{
					"m5.xlarge": "16",
				},
			},
			// EC2 API returns 4 vCPUs for m5.xlarge and ConfigMap says 16.
			// EC2 API has priority; result should be 3 nodes * 4 vCPUs = 12.
			MockedEC2DescribeInstanceTypesFunc: func(ctx context.Context, input *ec2v2.DescribeInstanceTypesInput, optFns ...func(*ec2v2.Options)) (*ec2v2.DescribeInstanceTypesOutput, error) {
				return initDescribeInstanceTypesOutput([]ec2typesv2.InstanceTypeInfo{
					initInstanceTypeInfo("m5.xlarge", 4)}), nil
			},
			expectedVCpusCount:            12,
			expectedVCpusCountErrorReason: "",
		},
		{
			name: "When EC2 API succeeds, it should use EC2 value even if ConfigMap exists with other instance types",
			npsParams: []nodePoolParams{
				{availableNodesCount: 2, ec2InstanceType: "m5.2xlarge"},
			},
			configMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      rosaCPUsInstanceTypeConfigMapName,
					Namespace: rosaCPUInstanceTypeConfigMapNamespace,
				},
				Data: map[string]string{
					"other-instance.xlarge": "99",
				},
			},
			MockedEC2DescribeInstanceTypesFunc: func(ctx context.Context, input *ec2v2.DescribeInstanceTypesInput, optFns ...func(*ec2v2.Options)) (*ec2v2.DescribeInstanceTypesOutput, error) {
				return initDescribeInstanceTypesOutput([]ec2typesv2.InstanceTypeInfo{
					initInstanceTypeInfo("m5.2xlarge", 8)}), nil
			},
			expectedVCpusCount:            16,
			expectedVCpusCountErrorReason: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			hcluster := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hc",
					Namespace: "any",
				},
				Spec: hyperv1.HostedClusterSpec{
					ClusterID: "id",
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
					},
				},
			}

			clientBuilder := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(hcluster)
			if tc.configMap != nil {
				clientBuilder = clientBuilder.WithObjects(tc.configMap)
			}

			ec2MockedClient := &Ec2ClientMock{}
			ec2MockedClient.MockedDescribeInstanceTypesFunc = tc.MockedEC2DescribeInstanceTypesFunc

			pricingMockedClient := &PricingClientMock{}
			pricingMockedClient.MockedGetProductsFunc = tc.MockedGetProductsFunc

			for k, npParam := range tc.npsParams {
				nodePool := &hyperv1.NodePool{
					ObjectMeta: metav1.ObjectMeta{
						Name:      strconv.Itoa(k),
						Namespace: "any",
					},
					Spec: hyperv1.NodePoolSpec{
						ClusterName: "hc",
						Platform: hyperv1.NodePoolPlatform{
							Type: hyperv1.AWSPlatform,
							AWS: &hyperv1.AWSNodePoolPlatform{
								InstanceType: npParam.ec2InstanceType,
							},
						},
					},
					Status: hyperv1.NodePoolStatus{
						Replicas: npParam.availableNodesCount,
					},
				}

				clientBuilder = clientBuilder.WithObjects(nodePool)
			}

			reg := prometheus.NewPedanticRegistry()
			reg.MustRegister(createNodePoolsMetricsCollector(clientBuilder.Build(), ec2MockedClient, pricingMockedClient, clock.RealClock{}))

			allMetricsValues, err := reg.Gather()
			if err != nil {
				t.Fatalf("gathering metrics failed: %v", err)
			}

			var vCpusCountMetricValue *dto.MetricFamily
			var vCpusComputationErrorMetricValue *dto.MetricFamily
			var expectedVCpusComputationErrorMetricValue *dto.MetricFamily

			for _, metricValue := range allMetricsValues {
				if metricValue != nil && metricValue.Name != nil {
					switch *metricValue.Name {
					case VCpusCountByHClusterMetricName:
						vCpusCountMetricValue = metricValue
					case VCpusComputationErrorByHClusterMetricName:
						vCpusComputationErrorMetricValue = metricValue
					}
				}
			}

			expectedBaseLabels := []*dto.LabelPair{
				{
					Name: ptr.To("_id"), Value: ptr.To("id"),
				},
				{
					Name: ptr.To("name"), Value: ptr.To("hc"),
				},
				{
					Name: ptr.To("namespace"), Value: ptr.To("any"),
				},
				{
					Name: ptr.To("platform"), Value: ptr.To(string(hyperv1.AWSPlatform)),
				},
			}

			expectedVCpusCountMetricValue := &dto.MetricFamily{
				Name: ptr.To(VCpusCountByHClusterMetricName),
				Help: ptr.To(VCpusCountByHClusterMetricHelp),
				Type: func() *dto.MetricType { v := dto.MetricType(1); return &v }(),
				Metric: []*dto.Metric{{
					Label: expectedBaseLabels,
					Gauge: &dto.Gauge{Value: ptr.To(tc.expectedVCpusCount)},
				}},
			}

			if tc.expectedVCpusCountErrorReason != "" {
				expectedVCpusComputationErrorMetricValue = &dto.MetricFamily{
					Name: ptr.To(VCpusComputationErrorByHClusterMetricName),
					Help: ptr.To(VCpusComputationErrorByHClusterMetricHelp),
					Type: func() *dto.MetricType { v := dto.MetricType(1); return &v }(),
					Metric: []*dto.Metric{{
						Label: append(expectedBaseLabels, &dto.LabelPair{
							Name: ptr.To("reason"), Value: ptr.To(tc.expectedVCpusCountErrorReason),
						}),
						Gauge: &dto.Gauge{Value: ptr.To[float64](1.0)},
					}},
				}
			}

			if diff := cmp.Diff(vCpusCountMetricValue, expectedVCpusCountMetricValue, ignoreUnexportedDto); diff != "" {
				t.Errorf("result differs from actual: %s", diff)
			}

			if diff := cmp.Diff(vCpusComputationErrorMetricValue, expectedVCpusComputationErrorMetricValue, ignoreUnexportedDto); diff != "" {
				t.Errorf("result differs from actual: %s, expected %+v, got %+v", diff,
					expectedVCpusComputationErrorMetricValue,
					vCpusComputationErrorMetricValue)
			}
		})
	}
}

// TestCollectConcurrency verifies that concurrent Collect calls do not race on
// shared mutable state (ec2InstanceTypeToVCpusCount map and lastCollectTime).
func TestCollectConcurrency(t *testing.T) {
	hcluster := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hc",
			Namespace: "any",
		},
		Spec: hyperv1.HostedClusterSpec{
			ClusterID: "id",
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AWSPlatform,
			},
		},
	}

	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "np-0",
			Namespace: "any",
		},
		Spec: hyperv1.NodePoolSpec{
			ClusterName: "hc",
			Platform: hyperv1.NodePoolPlatform{
				Type: hyperv1.AWSPlatform,
				AWS: &hyperv1.AWSNodePoolPlatform{
					InstanceType: "m5.xlarge",
				},
			},
		},
		Status: hyperv1.NodePoolStatus{
			Replicas: 2,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(api.Scheme).
		WithObjects(hcluster, nodePool).
		Build()

	ec2Mock := &Ec2ClientMock{
		MockedDescribeInstanceTypesFunc: func(ctx context.Context, input *ec2v2.DescribeInstanceTypesInput, optFns ...func(*ec2v2.Options)) (*ec2v2.DescribeInstanceTypesOutput, error) {
			// Add a small delay to widen the race window
			time.Sleep(time.Millisecond)
			return initDescribeInstanceTypesOutput([]ec2typesv2.InstanceTypeInfo{
				initInstanceTypeInfo("m5.xlarge", 4),
			}), nil
		},
	}

	pricingMock := &PricingClientMock{
		MockedGetProductsFunc: func(ctx context.Context, input *pricingv2.GetProductsInput, optFns ...func(*pricingv2.Options)) (*pricingv2.GetProductsOutput, error) {
			return &pricingv2.GetProductsOutput{}, nil
		},
	}

	collector := createNodePoolsMetricsCollector(fakeClient, ec2Mock, pricingMock, clock.RealClock{})

	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			ch := make(chan prometheus.Metric, 100)
			go func() {
				for range ch {
				}
			}()
			collector.(*nodePoolsMetricsCollector).Collect(ch)
			close(ch)
		}()
	}
	wg.Wait()
}
