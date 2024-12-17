package metrics

import (
	"strconv"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"

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

type nodePoolParams struct {
	availableNodesCount int32
	ec2InstanceType     string
}

type Ec2ClientMock struct {
	ec2iface.EC2API
}

func (c *Ec2ClientMock) DescribeInstanceTypes(input *ec2.DescribeInstanceTypesInput) (*ec2.DescribeInstanceTypesOutput, error) {
	var instanceTypesInfo []*ec2.InstanceTypeInfo

	for _, instanceType := range input.InstanceTypes {
		if instanceType != nil {
			var vCpusCount *int64

			switch *instanceType {
			case "m5.xlarge":
				vCpusCount = ptr.To[int64](4)
			case "m5.2xlarge":
				vCpusCount = ptr.To[int64](8)
			}

			instanceTypesInfo = append(instanceTypesInfo, &ec2.InstanceTypeInfo{
				InstanceType: instanceType,
				VCpuInfo: &ec2.VCpuInfo{
					DefaultVCpus: vCpusCount,
				},
			})
		}

	}

	return &ec2.DescribeInstanceTypesOutput{InstanceTypes: instanceTypesInfo}, nil
}

func TestReportVCpusCountByHCluster(t *testing.T) {
	testCases := []struct {
		name                          string
		npsParams                     []nodePoolParams
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
			expectedVCpusCount: 0,
		},
		{
			name: "When there is one nodePool with 2 m5.xlarge nodes available, the total number of worker vCpus is 4",
			npsParams: []nodePoolParams{
				{availableNodesCount: 2, ec2InstanceType: "m5.xlarge"},
			},
			expectedVCpusCount: 8,
		},
		{
			name: "When there is two nodePools with 2 m5.2xlarge nodes available each, the total number of worker vCpus is 16",
			npsParams: []nodePoolParams{
				{availableNodesCount: 2, ec2InstanceType: "m5.2xlarge"},
				{availableNodesCount: 2, ec2InstanceType: "m5.2xlarge"},
			},
			expectedVCpusCount: 32,
		},
		{
			name: "When the nodePool EC2 instance type is invalid, the total number of worker vCpus is -1",
			npsParams: []nodePoolParams{
				{availableNodesCount: 2, ec2InstanceType: "hello_world"},
			},
			expectedVCpusCount:            -1,
			expectedVCpusCountErrorReason: "unexpected AWS output",
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
			reg.MustRegister(createNodePoolsMetricsCollector(clientBuilder.Build(), &Ec2ClientMock{}, clock.RealClock{}))

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
				t.Errorf("result differs from actual: %s", diff)
			}
		})
	}
}
