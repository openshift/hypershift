package metrics

import (
	"strconv"
	"testing"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/openshift/hypershift/api"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/clock"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
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
			var coresCount *int64

			switch *instanceType {
			case "m5.xlarge":
				coresCount = pointer.Int64(2)
			case "m5.2xlarge":
				coresCount = pointer.Int64(4)
			}

			instanceTypesInfo = append(instanceTypesInfo, &ec2.InstanceTypeInfo{
				InstanceType: instanceType,
				VCpuInfo: &ec2.VCpuInfo{
					DefaultCores: coresCount,
				},
			})
		}

	}

	return &ec2.DescribeInstanceTypesOutput{InstanceTypes: instanceTypesInfo}, nil
}

func TestReportCoresCountByHCluster(t *testing.T) {
	testCases := []struct {
		name               string
		npsParams          []nodePoolParams
		expectedCoresCount float64
	}{
		{
			name:               "When there is no nodePool, the total number of worker cores is 0",
			npsParams:          []nodePoolParams{},
			expectedCoresCount: 0,
		},
		{
			name: "When there is one nodePool with no m5.xlarge nodes available, the total number of worker cores is 0",
			npsParams: []nodePoolParams{
				{availableNodesCount: 0, ec2InstanceType: "m5.xlarge"},
			},
			expectedCoresCount: 0,
		},
		{
			name: "When there is one nodePool with 2 m5.xlarge nodes available, the total number of worker cores is 4",
			npsParams: []nodePoolParams{
				{availableNodesCount: 2, ec2InstanceType: "m5.xlarge"},
			},
			expectedCoresCount: 4,
		},
		{
			name: "When there is two nodePools with 2 m5.2xlarge nodes available each, the total number of worker cores is 16",
			npsParams: []nodePoolParams{
				{availableNodesCount: 2, ec2InstanceType: "m5.2xlarge"},
				{availableNodesCount: 2, ec2InstanceType: "m5.2xlarge"},
			},
			expectedCoresCount: 16,
		},
		{
			name: "When the nodePool EC2 instance type is invalid, the total number of worker cores is -1",
			npsParams: []nodePoolParams{
				{availableNodesCount: 2, ec2InstanceType: "hello_world"},
			},
			expectedCoresCount: -1,
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

			expectedMetricValue := &dto.MetricFamily{
				Name: pointer.String(CoresCountByHClusterMetricName),
				Help: pointer.String(CoresCountByHClusterMetricHelp),
				Type: func() *dto.MetricType { v := dto.MetricType(1); return &v }(),
				Metric: []*dto.Metric{{
					Label: []*dto.LabelPair{
						{
							Name: pointer.String("_id"), Value: pointer.String("id"),
						},
						{
							Name: pointer.String("name"), Value: pointer.String("hc"),
						},
						{
							Name: pointer.String("namespace"), Value: pointer.String("any"),
						},
						{
							Name: pointer.String("platform"), Value: pointer.String(string(hyperv1.AWSPlatform)),
						},
					},
					Gauge: &dto.Gauge{Value: pointer.Float64(tc.expectedCoresCount)},
				}},
			}

			reg := prometheus.NewPedanticRegistry()
			reg.MustRegister(createNodePoolsMetricsCollector(clientBuilder.Build(), &Ec2ClientMock{}, clock.RealClock{}))

			allMetricsValues, err := reg.Gather()
			if err != nil {
				t.Fatalf("gathering metrics failed: %v", err)
			}

			var metricValue *dto.MetricFamily

			for _, currentMetricValue := range allMetricsValues {
				if currentMetricValue != nil && currentMetricValue.Name != nil && *currentMetricValue.Name == CoresCountByHClusterMetricName {
					metricValue = currentMetricValue
				}
			}

			if diff := cmp.Diff(metricValue, expectedMetricValue, ignoreUnexportedDto); diff != "" {
				t.Errorf("result differs from actual: %s", diff)
			}
		})
	}
}
