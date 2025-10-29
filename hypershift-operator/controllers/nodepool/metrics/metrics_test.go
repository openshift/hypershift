package metrics

import (
	"fmt"
	"strconv"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
	"github.com/aws/aws-sdk-go/service/pricing"
	"github.com/aws/aws-sdk-go/service/pricing/pricingiface"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/clock"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/davecgh/go-spew/spew"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

var (
	ignoreUnexportedDto = cmpopts.IgnoreUnexported(dto.MetricFamily{}, dto.Metric{}, dto.LabelPair{}, dto.Gauge{})
)

type nodePoolParams struct {
	availableNodesCount int32
	ec2InstanceType     string
}

type Ec2ClientMock struct {
	ec2iface.EC2API

	MockedDescribeInstanceTypesFunc func(input *ec2.DescribeInstanceTypesInput) (*ec2.DescribeInstanceTypesOutput, error)
	ResponseInstanceTypes           []*ec2.InstanceTypeInfo
	ResponseError                   error
}

func initDescribeInstanceTypesOutput(instanceTypeInfo []*ec2.InstanceTypeInfo) *ec2.DescribeInstanceTypesOutput {
	return &ec2.DescribeInstanceTypesOutput{
		InstanceTypes: instanceTypeInfo,
	}
}

func initInstanceTypeInfo(instanceType string, vCpusCount int64) *ec2.InstanceTypeInfo {
	return &ec2.InstanceTypeInfo{
		InstanceType: ptr.To[string](instanceType),
		VCpuInfo: &ec2.VCpuInfo{
			DefaultVCpus: ptr.To[int64](vCpusCount),
		},
	}
}

func (c *Ec2ClientMock) DescribeInstanceTypes(input *ec2.DescribeInstanceTypesInput) (*ec2.DescribeInstanceTypesOutput, error) {
	return c.MockedDescribeInstanceTypesFunc(input)
}

type PricingClientMock struct {
	pricingiface.PricingAPI

	MockedGetProductsWithContextFunc func(input *pricing.GetProductsInput) (*pricing.GetProductsOutput, error)
}

func (m *PricingClientMock) GetProductsWithContext(context aws.Context, input *pricing.GetProductsInput, options ...request.Option) (*pricing.GetProductsOutput, error) {
	return m.MockedGetProductsWithContextFunc(input)
}

func TestReportVCpusCountByHCluster(t *testing.T) {
	testCases := []struct {
		name      string
		npsParams []nodePoolParams
		configMap *corev1.ConfigMap

		MockedEC2DescribeInstanceTypesFunc      func(input *ec2.DescribeInstanceTypesInput) (*ec2.DescribeInstanceTypesOutput, error)
		MockedPricingGetProductsWithContextFunc func(*pricing.GetProductsInput) (*pricing.GetProductsOutput, error)

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
			MockedEC2DescribeInstanceTypesFunc: func(input *ec2.DescribeInstanceTypesInput) (*ec2.DescribeInstanceTypesOutput, error) {
				return initDescribeInstanceTypesOutput([]*ec2.InstanceTypeInfo{}), nil
			},
			expectedVCpusCount: 0,
		},
		{
			name: "When there is one nodePool with 2 m5.xlarge nodes available, the total number of worker vCpus is 8",
			npsParams: []nodePoolParams{
				{availableNodesCount: 2, ec2InstanceType: "m5.xlarge"},
			},
			MockedEC2DescribeInstanceTypesFunc: func(input *ec2.DescribeInstanceTypesInput) (*ec2.DescribeInstanceTypesOutput, error) {
				return initDescribeInstanceTypesOutput([]*ec2.InstanceTypeInfo{
					initInstanceTypeInfo("m5.xlarge", 4)}), nil
			},
			expectedVCpusCount: 8,
		},
		{
			name: "When there is two nodePools with 2 m5.2xlarge nodes available each, the total number of worker vCpus is 32",
			npsParams: []nodePoolParams{
				{availableNodesCount: 2, ec2InstanceType: "m5.2xlarge"},
				{availableNodesCount: 2, ec2InstanceType: "m5.2xlarge"},
			},
			MockedEC2DescribeInstanceTypesFunc: func(input *ec2.DescribeInstanceTypesInput) (*ec2.DescribeInstanceTypesOutput, error) {
				return initDescribeInstanceTypesOutput([]*ec2.InstanceTypeInfo{
					initInstanceTypeInfo("m5.2xlarge", 8)}), nil
			},
			expectedVCpusCount: 32,
		},

		{
			name: "When the nodePool EC2 instance type is not valid, we fetch value from pricing APIs",
			npsParams: []nodePoolParams{
				{availableNodesCount: 2, ec2InstanceType: "hello_world"},
			},
			MockedEC2DescribeInstanceTypesFunc: func(input *ec2.DescribeInstanceTypesInput) (*ec2.DescribeInstanceTypesOutput, error) {
				return &ec2.DescribeInstanceTypesOutput{}, nil
			},
			MockedPricingGetProductsWithContextFunc: func(*pricing.GetProductsInput) (*pricing.GetProductsOutput, error) {
				return &pricing.GetProductsOutput{}, fmt.Errorf("an error dude")
			},
			expectedVCpusCount:            -1,
			expectedVCpusCountErrorReason: unknownInstanceTypeErrorReason,
		},
		{
			name: "When EC2 and pricing API fail and configMap fails too, we return unknown instance type error -1",
			npsParams: []nodePoolParams{
				{availableNodesCount: 2, ec2InstanceType: "dream-instance.xlarge"},
			},
			MockedEC2DescribeInstanceTypesFunc: func(input *ec2.DescribeInstanceTypesInput) (*ec2.DescribeInstanceTypesOutput, error) {
				return initDescribeInstanceTypesOutput([]*ec2.InstanceTypeInfo{
					initInstanceTypeInfo("dream-instance.xlarge", 4)}), awserr.New("InvalidInstanceType", "don't know the instance", nil)
			},
			MockedPricingGetProductsWithContextFunc: func(*pricing.GetProductsInput) (*pricing.GetProductsOutput, error) {
				return nil, fmt.Errorf("No man I don't know it")
			},

			expectedVCpusCount:            -1,
			expectedVCpusCountErrorReason: unknownInstanceTypeErrorReason,
		},

		{
			name: "When EC2 DescribeInstanceTypes return InvalidInstanceType error, pricing.GetProducts return valid but empty data",
			npsParams: []nodePoolParams{
				{availableNodesCount: 2, ec2InstanceType: "dream-instance.xlarge"},
			},
			MockedEC2DescribeInstanceTypesFunc: func(input *ec2.DescribeInstanceTypesInput) (*ec2.DescribeInstanceTypesOutput, error) {
				return initDescribeInstanceTypesOutput([]*ec2.InstanceTypeInfo{
					initInstanceTypeInfo("dream-instance.xlarge", 4)}), awserr.New("InvalidInstanceType", "don't know the instance", nil)
			},
			MockedPricingGetProductsWithContextFunc: func(*pricing.GetProductsInput) (*pricing.GetProductsOutput, error) {
				return &pricing.GetProductsOutput{
					PriceList: []aws.JSONValue{
						{
							"bad": "data",
						},
					},
				}, nil
			},
			expectedVCpusCount:            -1,
			expectedVCpusCountErrorReason: unknownInstanceTypeErrorReason,
		},
		{
			name: "When EC2 DescribeInstanceTypes return InvalidInstanceType error, pricing.GetProducts return bad data",
			npsParams: []nodePoolParams{
				{availableNodesCount: 2, ec2InstanceType: "dream-instance.xlarge"},
			},
			MockedEC2DescribeInstanceTypesFunc: func(input *ec2.DescribeInstanceTypesInput) (*ec2.DescribeInstanceTypesOutput, error) {
				return initDescribeInstanceTypesOutput([]*ec2.InstanceTypeInfo{
					initInstanceTypeInfo("dream-instance.xlarge", 4)}), awserr.New("InvalidInstanceType", "don't know the instance", nil)
			},
			MockedPricingGetProductsWithContextFunc: func(*pricing.GetProductsInput) (*pricing.GetProductsOutput, error) {
				return &pricing.GetProductsOutput{
					PriceList: []aws.JSONValue{
						{
							"product": aws.JSONValue{
								"attributes": "foo",
							},
						},
					},
				}, nil
			},
			expectedVCpusCount:            -1,
			expectedVCpusCountErrorReason: unknownInstanceTypeErrorReason,
		},
		{
			name: "When EC2 DescribeInstanceTypes return InvalidInstanceType error, pricing.GetProducts return nil",
			npsParams: []nodePoolParams{
				{availableNodesCount: 2, ec2InstanceType: "dream-instance.xlarge"},
			},
			MockedEC2DescribeInstanceTypesFunc: func(input *ec2.DescribeInstanceTypesInput) (*ec2.DescribeInstanceTypesOutput, error) {
				return initDescribeInstanceTypesOutput([]*ec2.InstanceTypeInfo{
					initInstanceTypeInfo("dream-instance.xlarge", 4)}), awserr.New("InvalidInstanceType", "don't know the instance", nil)
			},
			MockedPricingGetProductsWithContextFunc: func(*pricing.GetProductsInput) (*pricing.GetProductsOutput, error) {
				return &pricing.GetProductsOutput{
					PriceList: nil,
				}, nil
			},
			expectedVCpusCount:            -1,
			expectedVCpusCountErrorReason: unknownInstanceTypeErrorReason,
		},
		{
			name: "When EC2 DescribeInstanceTypes return InvalidInstanceType error, pricing.GetProducts return bad CPU value",
			npsParams: []nodePoolParams{
				{availableNodesCount: 2, ec2InstanceType: "i3.metal"},
			},
			MockedEC2DescribeInstanceTypesFunc: func(input *ec2.DescribeInstanceTypesInput) (*ec2.DescribeInstanceTypesOutput, error) {
				return nil, awserr.New("InvalidInstanceType", "don't know the instance", nil)
			},
			MockedPricingGetProductsWithContextFunc: func(*pricing.GetProductsInput) (*pricing.GetProductsOutput, error) {
				return &pricing.GetProductsOutput{
					FormatVersion: ptr.To("aws_v1"),
					NextToken:     ptr.To("AAMA-TOKEN"),
					PriceList: []aws.JSONValue{
						{
							"product": aws.JSONValue{
								"attributes": aws.JSONValue{
									"instanceType": "i3.metal",
									"vcpu":         "should be a number",
								},
							},
						},
					}}, nil
			},
			expectedVCpusCount:            -1,
			expectedVCpusCountErrorReason: unknownInstanceTypeErrorReason,
		},
		{
			name: "When EC2 DescribeInstanceTypes return InvalidInstanceType error, pricing.GetProducts return valid  data",
			npsParams: []nodePoolParams{
				{availableNodesCount: 2, ec2InstanceType: "i3.metal"},
			},
			MockedEC2DescribeInstanceTypesFunc: func(input *ec2.DescribeInstanceTypesInput) (*ec2.DescribeInstanceTypesOutput, error) {
				return nil, awserr.New("InvalidInstanceType", "don't know the instance", nil)
			},
			MockedPricingGetProductsWithContextFunc: func(*pricing.GetProductsInput) (*pricing.GetProductsOutput, error) {
				return &pricing.GetProductsOutput{
					FormatVersion: ptr.To("aws_v1"),
					NextToken:     ptr.To("AAMA-TOKEN"),
					PriceList: []aws.JSONValue{
						{
							"product": aws.JSONValue{
								"attributes": aws.JSONValue{
									"instanceType": "i3.metal",
									"vcpu":         "72",
								},
							},
						},
					}}, nil
			},
			expectedVCpusCount:            144,
			expectedVCpusCountErrorReason: "",
		},
		{
			name: "When EC2 and pricing API fail, we get cpus from configMap",
			npsParams: []nodePoolParams{
				{availableNodesCount: 2, ec2InstanceType: "dream-instance.xlarge"},
			},
			MockedEC2DescribeInstanceTypesFunc: func(input *ec2.DescribeInstanceTypesInput) (*ec2.DescribeInstanceTypesOutput, error) {
				return initDescribeInstanceTypesOutput([]*ec2.InstanceTypeInfo{
					initInstanceTypeInfo("dream-instance.xlarge", 4)}), awserr.New("InvalidInstanceType", "don't know the instance", nil)
			},
			MockedPricingGetProductsWithContextFunc: func(*pricing.GetProductsInput) (*pricing.GetProductsOutput, error) {
				return nil, fmt.Errorf("Nice try, mate!")
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

			expectedVCpusCount:            24,
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
			pricingMockedClient.MockedGetProductsWithContextFunc = tc.MockedPricingGetProductsWithContextFunc

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
				t.Errorf("result differs from actual: %s, expected %s, got %s", diff,
					spew.Sdump(expectedVCpusComputationErrorMetricValue),
					spew.Sdump(vCpusComputationErrorMetricValue))
			}
		})
	}
}
