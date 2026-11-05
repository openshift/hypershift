package metrics

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	npmetrics "github.com/openshift/hypershift/pkg/metrics/nodepool"
	"github.com/openshift/hypershift/support/api"

	ec2v2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2typesv2 "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/clock"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
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

func TestReportVCpusCountByHCluster(t *testing.T) {
	testCases := []struct {
		name      string
		platform  hyperv1.PlatformType
		npsParams []nodePoolParams
		configMap *corev1.ConfigMap

		MockedEC2DescribeInstanceTypesFunc func(ctx context.Context, input *ec2v2.DescribeInstanceTypesInput, optFns ...func(*ec2v2.Options)) (*ec2v2.DescribeInstanceTypesOutput, error)

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
			name: "When EC2 API returns unexpected output and no ConfigMap exists, it should report ConfigMap not found",
			npsParams: []nodePoolParams{
				{availableNodesCount: 2, ec2InstanceType: "hello_world"},
			},
			MockedEC2DescribeInstanceTypesFunc: func(ctx context.Context, input *ec2v2.DescribeInstanceTypesInput, optFns ...func(*ec2v2.Options)) (*ec2v2.DescribeInstanceTypesOutput, error) {
				return &ec2v2.DescribeInstanceTypesOutput{}, nil
			},
			expectedVCpusCount:            -1,
			expectedVCpusCountErrorReason: errRosaCPUsInstanceTypesConfigNotFound.Error(),
		},
		{
			name: "When EC2 API fails and no ConfigMap exists, it should report ConfigMap not found",
			npsParams: []nodePoolParams{
				{availableNodesCount: 2, ec2InstanceType: "dream-instance.xlarge"},
			},
			MockedEC2DescribeInstanceTypesFunc: func(ctx context.Context, input *ec2v2.DescribeInstanceTypesInput, optFns ...func(*ec2v2.Options)) (*ec2v2.DescribeInstanceTypesOutput, error) {
				return nil, newAWSError("InvalidInstanceType", "the instance type is not recognized")
			},
			expectedVCpusCount:            -1,
			expectedVCpusCountErrorReason: errRosaCPUsInstanceTypesConfigNotFound.Error(),
		},
		{
			name: "When EC2 API fails but ConfigMap contains the instance type, it should resolve vCPUs from ConfigMap",
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
		{
			name:     "When nodePool platform is KubeVirt, it should return -1 with unsupported platform error",
			platform: hyperv1.KubevirtPlatform,
			npsParams: []nodePoolParams{
				{availableNodesCount: 2},
			},
			expectedVCpusCount:            -1,
			expectedVCpusCountErrorReason: "unsupported platform",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			platform := tc.platform
			if platform == "" {
				platform = hyperv1.AWSPlatform
			}

			hcluster := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hc",
					Namespace: "any",
				},
				Spec: hyperv1.HostedClusterSpec{
					ClusterID: "id",
					Platform: hyperv1.PlatformSpec{
						Type: platform,
					},
				},
			}

			clientBuilder := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(hcluster)
			if tc.configMap != nil {
				clientBuilder = clientBuilder.WithObjects(tc.configMap)
			}

			ec2MockedClient := &Ec2ClientMock{}
			ec2MockedClient.MockedDescribeInstanceTypesFunc = tc.MockedEC2DescribeInstanceTypesFunc

			for k, npParam := range tc.npsParams {
				nodePool := &hyperv1.NodePool{
					ObjectMeta: metav1.ObjectMeta{
						Name:      strconv.Itoa(k),
						Namespace: "any",
					},
					Spec: hyperv1.NodePoolSpec{
						ClusterName: "hc",
						Platform: hyperv1.NodePoolPlatform{
							Type: platform,
						},
					},
					Status: hyperv1.NodePoolStatus{
						Replicas: npParam.availableNodesCount,
					},
				}

				if platform == hyperv1.AWSPlatform {
					nodePool.Spec.Platform.AWS = &hyperv1.AWSNodePoolPlatform{
						InstanceType: npParam.ec2InstanceType,
					}
				}

				clientBuilder = clientBuilder.WithObjects(nodePool)
			}

			reg := prometheus.NewPedanticRegistry()
			reg.MustRegister(createNodePoolsMetricsCollector(clientBuilder.Build(), ec2MockedClient, clock.RealClock{}))

			allMetricsValues, err := reg.Gather()
			g.Expect(err).ToNot(HaveOccurred())

			var vCpusCountMetricValue *dto.MetricFamily
			var vCpusComputationErrorMetricValue *dto.MetricFamily
			var expectedVCpusComputationErrorMetricValue *dto.MetricFamily

			for _, metricValue := range allMetricsValues {
				if metricValue != nil && metricValue.Name != nil {
					switch *metricValue.Name {
					case npmetrics.VCpusCountByHClusterMetricName:
						vCpusCountMetricValue = metricValue
					case npmetrics.VCpusComputationErrorByHClusterMetricName:
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
					Name: ptr.To("platform"), Value: ptr.To(string(platform)),
				},
			}

			expectedVCpusCountMetricValue := &dto.MetricFamily{
				Name: ptr.To(npmetrics.VCpusCountByHClusterMetricName),
				Help: ptr.To(VCpusCountByHClusterMetricHelp),
				Type: func() *dto.MetricType { v := dto.MetricType(1); return &v }(),
				Metric: []*dto.Metric{{
					Label: expectedBaseLabels,
					Gauge: &dto.Gauge{Value: ptr.To(tc.expectedVCpusCount)},
				}},
			}

			if tc.expectedVCpusCountErrorReason != "" {
				expectedVCpusComputationErrorMetricValue = &dto.MetricFamily{
					Name: ptr.To(npmetrics.VCpusComputationErrorByHClusterMetricName),
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

			g.Expect(cmp.Diff(vCpusCountMetricValue, expectedVCpusCountMetricValue, ignoreUnexportedDto)).To(BeEmpty())
			g.Expect(cmp.Diff(vCpusComputationErrorMetricValue, expectedVCpusComputationErrorMetricValue, ignoreUnexportedDto)).To(BeEmpty())
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

	collector := createNodePoolsMetricsCollector(fakeClient, ec2Mock, clock.RealClock{})

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

func TestCollect(t *testing.T) {
	t.Run("When a cache read is blocked, it should stop collecting after the cache read timeout", func(t *testing.T) {
		g := NewWithT(t)
		var deadlineSeen bool
		var listErr error

		blockedClient := fake.NewClientBuilder().
			WithScheme(api.Scheme).
			WithInterceptorFuncs(interceptor.Funcs{
				List: func(ctx context.Context, _ crclient.WithWatch, _ crclient.ObjectList, _ ...crclient.ListOption) error {
					_, deadlineSeen = ctx.Deadline()
					<-ctx.Done()
					listErr = ctx.Err()
					return listErr
				},
			}).
			Build()

		collector := createNodePoolsMetricsCollector(blockedClient, nil, clock.RealClock{}).(*nodePoolsMetricsCollector)
		collector.cacheReadTimeout = 10 * time.Millisecond

		ch := make(chan prometheus.Metric)
		done := make(chan struct{})
		go func() {
			for range ch {
			}
			close(done)
		}()

		start := time.Now()
		collector.Collect(ch)
		close(ch)
		<-done

		g.Expect(deadlineSeen).To(BeTrue())
		g.Expect(listErr).To(MatchError(context.DeadlineExceeded))
		g.Expect(time.Since(start)).To(BeNumerically("<", time.Second))
	})

	newFixtures := func() (*hyperv1.HostedCluster, *hyperv1.NodePool) {
		hc := &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "rosa-cluster", Namespace: "clusters"},
			Spec: hyperv1.HostedClusterSpec{
				ClusterID: "d1b8d336-52c5-4a4d-9d9f-81e947c6ea7b",
				Platform:  hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform},
			},
		}
		np := &hyperv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{Name: "workers", Namespace: hc.Namespace},
			Spec: hyperv1.NodePoolSpec{
				ClusterName: hc.Name,
				Replicas:    ptr.To[int32](10),
				Platform: hyperv1.NodePoolPlatform{
					Type: hyperv1.AWSPlatform,
					AWS:  &hyperv1.AWSNodePoolPlatform{InstanceType: "m5.xlarge"},
				},
			},
			Status: hyperv1.NodePoolStatus{Replicas: 2},
		}
		return hc, np
	}
	gaugeHeader := func(name, help string) string {
		return fmt.Sprintf("# HELP %s %s\n# TYPE %s gauge\n", name, help, name)
	}
	poolSample := func(hc *hyperv1.HostedCluster, np *hyperv1.NodePool, workloadType string, vcpus float64) string {
		return fmt.Sprintf("%s{_id=%q,cluster_name=%q,name=%q,namespace=%q,platform=%q,workload_type=%q} %g\n",
			npmetrics.VCpusCountByNodePoolMetricName, hc.Spec.ClusterID, hc.Name, np.Name, np.Namespace,
			string(np.Spec.Platform.Type), workloadType, vcpus)
	}

	for _, tc := range []struct {
		name                 string
		configure            func(*hyperv1.HostedCluster, *hyperv1.NodePool)
		ec2Err               error
		configMapData        map[string]string
		omitHostedCluster    bool
		expectedPoolVCpus    *float64
		expectedWorkloadType string
		expectedClusterVCpus float64
		expectedError        error
	}{
		{
			name:                 "When a NodePool has no workload label, it should report available vCPUs with an empty workload type",
			expectedPoolVCpus:    ptr.To(8.0),
			expectedClusterVCpus: 8,
		},
		{
			name: "When a NodePool is labeled for virt, it should report its workload type and instance capacity",
			configure: func(_ *hyperv1.HostedCluster, np *hyperv1.NodePool) {
				np.Spec.NodeLabels = map[string]string{nodePoolWorkloadTypeLabel: "virt"}
				np.Spec.Platform.AWS.InstanceType = "m5.metal"
				np.Status.Replicas = 3
			},
			expectedPoolVCpus:    ptr.To(288.0),
			expectedWorkloadType: "virt",
			expectedClusterVCpus: 288,
		},
		{
			name: "When workload labels conflict, it should pass through the node label without interpreting its value",
			configure: func(_ *hyperv1.HostedCluster, np *hyperv1.NodePool) {
				np.Labels = map[string]string{nodePoolWorkloadTypeLabel: "virt"}
				np.Spec.NodeLabels = map[string]string{nodePoolWorkloadTypeLabel: "isv"}
			},
			expectedPoolVCpus:    ptr.To(8.0),
			expectedWorkloadType: "isv",
			expectedClusterVCpus: 8,
		},
		{
			name: "When only metadata has a workload label, it should report an empty workload type",
			configure: func(_ *hyperv1.HostedCluster, np *hyperv1.NodePool) {
				np.Labels = map[string]string{nodePoolWorkloadTypeLabel: "virt"}
			},
			expectedPoolVCpus:    ptr.To(8.0),
			expectedClusterVCpus: 8,
		},
		{
			name: "When a NodePool has zero available replicas, it should report zero without resolving the instance type",
			configure: func(_ *hyperv1.HostedCluster, np *hyperv1.NodePool) {
				np.Status.Replicas = 0
			},
			expectedPoolVCpus: ptr.To(0.0),
		},
		{
			name: "When a NodePool is autoscaled, it should use available replicas without a desired replica count",
			configure: func(_ *hyperv1.HostedCluster, np *hyperv1.NodePool) {
				np.Spec.Replicas = nil
			},
			expectedPoolVCpus:    ptr.To(8.0),
			expectedClusterVCpus: 8,
		},
		{
			name:                 "When EC2 fails but the ConfigMap has capacity, it should use the fallback for both metrics",
			ec2Err:               newAWSError("InvalidInstanceType", "the instance type is not recognized"),
			configMapData:        map[string]string{"m5.xlarge": "8"},
			expectedPoolVCpus:    ptr.To(16.0),
			expectedClusterVCpus: 16,
		},
		{
			name: "When capacity resolution fails, it should emit -1 for the pool and preserve the cluster error",
			configure: func(_ *hyperv1.HostedCluster, np *hyperv1.NodePool) {
				np.Spec.NodeLabels = map[string]string{nodePoolWorkloadTypeLabel: "virt"}
				np.Status.Replicas = 3
			},
			ec2Err:               fmt.Errorf("EC2 is unavailable"),
			expectedPoolVCpus:    ptr.To(-1.0),
			expectedWorkloadType: "virt",
			expectedClusterVCpus: -1,
			expectedError:        errRosaCPUsInstanceTypesConfigNotFound,
		},
		{
			name: "When the platform is not AWS, it should omit pool usage and preserve unsupported platform reporting",
			configure: func(hc *hyperv1.HostedCluster, np *hyperv1.NodePool) {
				hc.Spec.Platform.Type = hyperv1.KubevirtPlatform
				np.Spec.Platform = hyperv1.NodePoolPlatform{Type: hyperv1.KubevirtPlatform}
			},
			expectedClusterVCpus: -1,
			expectedError:        errUnsupportedPlatform,
		},
		{
			name: "When the AWS platform spec is missing, it should emit -1 for the pool and preserve the cluster error",
			configure: func(_ *hyperv1.HostedCluster, np *hyperv1.NodePool) {
				np.Spec.Platform.AWS = nil
			},
			expectedPoolVCpus:    ptr.To(-1.0),
			expectedClusterVCpus: -1,
			expectedError:        errMissingAWSPlatformSpec,
		},
		{
			name: "When a non-AWS NodePool has zero replicas, it should omit pool usage without resolving capacity",
			configure: func(hc *hyperv1.HostedCluster, np *hyperv1.NodePool) {
				hc.Spec.Platform.Type = hyperv1.KubevirtPlatform
				np.Spec.Platform = hyperv1.NodePoolPlatform{Type: hyperv1.KubevirtPlatform}
				np.Status.Replicas = 0
			},
		},
		{
			name:              "When the HostedCluster is missing, it should omit unattributable pool usage without resolving capacity",
			omitHostedCluster: true,
		},
		{
			name: "When the HostedCluster ID is empty, it should omit pool usage without changing the cluster metric",
			configure: func(hc *hyperv1.HostedCluster, _ *hyperv1.NodePool) {
				hc.Spec.ClusterID = ""
			},
			expectedClusterVCpus: 8,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			hc, np := newFixtures()
			if tc.configure != nil {
				tc.configure(hc, np)
			}
			builder := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(np)
			if !tc.omitHostedCluster {
				builder = builder.WithObjects(hc)
			}
			if tc.configMapData != nil {
				builder = builder.WithObjects(&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Name: rosaCPUsInstanceTypeConfigMapName, Namespace: rosaCPUInstanceTypeConfigMapNamespace},
					Data:       tc.configMapData,
				})
			}
			ec2Calls := 0
			ec2Mock := &Ec2ClientMock{
				MockedDescribeInstanceTypesFunc: func(_ context.Context, input *ec2v2.DescribeInstanceTypesInput, _ ...func(*ec2v2.Options)) (*ec2v2.DescribeInstanceTypesOutput, error) {
					ec2Calls++
					if tc.ec2Err != nil {
						return nil, tc.ec2Err
					}
					instanceType := string(input.InstanceTypes[0])
					vcpus := map[string]int32{"m5.xlarge": 4, "m5.metal": 96}[instanceType]
					return initDescribeInstanceTypesOutput([]ec2typesv2.InstanceTypeInfo{initInstanceTypeInfo(instanceType, vcpus)}), nil
				},
			}
			reg := prometheus.NewRegistry()
			reg.MustRegister(createNodePoolsMetricsCollector(builder.Build(), ec2Mock, clock.RealClock{}))

			var expected string
			if tc.expectedPoolVCpus != nil {
				expected += gaugeHeader(npmetrics.VCpusCountByNodePoolMetricName, vCpusCountByNodePoolMetricHelp)
				expected += poolSample(hc, np, tc.expectedWorkloadType, *tc.expectedPoolVCpus)
			}
			if !tc.omitHostedCluster {
				expected += gaugeHeader(npmetrics.VCpusCountByHClusterMetricName, VCpusCountByHClusterMetricHelp)
				expected += fmt.Sprintf("%s{_id=%q,name=%q,namespace=%q,platform=%q} %g\n",
					npmetrics.VCpusCountByHClusterMetricName, hc.Spec.ClusterID, hc.Name, hc.Namespace, string(hc.Spec.Platform.Type), tc.expectedClusterVCpus)
			}
			if tc.expectedError != nil {
				expected += gaugeHeader(npmetrics.VCpusComputationErrorByHClusterMetricName, VCpusComputationErrorByHClusterMetricHelp)
				expected += fmt.Sprintf("%s{_id=%q,name=%q,namespace=%q,platform=%q,reason=%q} 1\n",
					npmetrics.VCpusComputationErrorByHClusterMetricName, hc.Spec.ClusterID, hc.Name, hc.Namespace, string(hc.Spec.Platform.Type), tc.expectedError.Error())
			}
			g.Expect(testutil.GatherAndCompare(reg, strings.NewReader(expected),
				npmetrics.VCpusCountByNodePoolMetricName, npmetrics.VCpusCountByHClusterMetricName, npmetrics.VCpusComputationErrorByHClusterMetricName)).To(Succeed())
			if np.Status.Replicas == 0 {
				g.Expect(ec2Calls).To(BeZero(), "zero-replica NodePool %s/%s should not resolve instance capacity", np.Namespace, np.Name)
			}
		})
	}

	t.Run("When some pools fail capacity resolution, it should report healthy pools independently and retain the first cluster error", func(t *testing.T) {
		g := NewWithT(t)
		hc, invalid := newFixtures()
		hc.Status.AutoNode.VCPUs = ptr.To[int32](20)
		invalid.Name = "a-invalid"
		invalid.Spec.Platform.AWS.InstanceType = "unknown.metal"
		virt := invalid.DeepCopy()
		virt.Name = "b-virt"
		virt.Spec.NodeLabels = map[string]string{nodePoolWorkloadTypeLabel: "virt"}
		virt.Spec.Platform.AWS.InstanceType = "m5.metal"
		virt.Status.Replicas = 3
		secondInvalid := invalid.DeepCopy()
		secondInvalid.Name = "c-invalid"
		secondInvalid.Spec.Platform.AWS.InstanceType = "invalid.xlarge"
		secondInvalid.Status.Replicas = 5
		app := invalid.DeepCopy()
		app.Name = "d-app"
		app.Spec.Platform.AWS.InstanceType = "m5.xlarge"
		app.Status.Replicas = 10
		fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).
			WithObjects(hc, invalid, virt, secondInvalid, app, &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: rosaCPUsInstanceTypeConfigMapName, Namespace: rosaCPUInstanceTypeConfigMapNamespace},
				Data:       map[string]string{"invalid.xlarge": "not-a-number"},
			}).Build()
		ec2Mock := &Ec2ClientMock{
			MockedDescribeInstanceTypesFunc: func(_ context.Context, input *ec2v2.DescribeInstanceTypesInput, _ ...func(*ec2v2.Options)) (*ec2v2.DescribeInstanceTypesOutput, error) {
				instanceType := string(input.InstanceTypes[0])
				vcpus, ok := map[string]int32{"m5.xlarge": 4, "m5.metal": 96}[instanceType]
				if !ok {
					return nil, newAWSError("InvalidInstanceType", "the instance type is not recognized")
				}
				return initDescribeInstanceTypesOutput([]ec2typesv2.InstanceTypeInfo{initInstanceTypeInfo(instanceType, vcpus)}), nil
			},
		}
		reg := prometheus.NewRegistry()
		reg.MustRegister(createNodePoolsMetricsCollector(fakeClient, ec2Mock, clock.RealClock{}))
		expected := gaugeHeader(npmetrics.VCpusCountByNodePoolMetricName, vCpusCountByNodePoolMetricHelp)
		expected += poolSample(hc, invalid, "", -1) + poolSample(hc, virt, "virt", 288)
		expected += poolSample(hc, secondInvalid, "", -1) + poolSample(hc, app, "", 40)
		expected += gaugeHeader(npmetrics.VCpusCountByHClusterMetricName, VCpusCountByHClusterMetricHelp)
		expected += fmt.Sprintf("%s{_id=%q,name=%q,namespace=%q,platform=\"AWS\"} -1\n",
			npmetrics.VCpusCountByHClusterMetricName, hc.Spec.ClusterID, hc.Name, hc.Namespace)
		expected += gaugeHeader(npmetrics.VCpusComputationErrorByHClusterMetricName, VCpusComputationErrorByHClusterMetricHelp)
		expected += fmt.Sprintf("%s{_id=%q,name=%q,namespace=%q,platform=\"AWS\",reason=%q} 1\n",
			npmetrics.VCpusComputationErrorByHClusterMetricName, hc.Spec.ClusterID, hc.Name, hc.Namespace, errUnknownInstanceType.Error())
		g.Expect(testutil.GatherAndCompare(reg, strings.NewReader(expected),
			npmetrics.VCpusCountByNodePoolMetricName, npmetrics.VCpusCountByHClusterMetricName, npmetrics.VCpusComputationErrorByHClusterMetricName)).To(Succeed())
	})

	t.Run("When pools or workload labels change after registration, it should emit only current series", func(t *testing.T) {
		g := NewWithT(t)
		fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
		ec2Mock := &Ec2ClientMock{
			MockedDescribeInstanceTypesFunc: func(_ context.Context, input *ec2v2.DescribeInstanceTypesInput, _ ...func(*ec2v2.Options)) (*ec2v2.DescribeInstanceTypesOutput, error) {
				if input.InstanceTypes[0] != ec2typesv2.InstanceTypeM5Xlarge {
					return nil, newAWSError("InvalidInstanceType", "the instance type is not recognized")
				}
				return initDescribeInstanceTypesOutput([]ec2typesv2.InstanceTypeInfo{initInstanceTypeInfo("m5.xlarge", 4)}), nil
			},
		}
		reg := prometheus.NewRegistry()
		reg.MustRegister(createNodePoolsMetricsCollector(fakeClient, ec2Mock, clock.RealClock{}))
		g.Expect(testutil.GatherAndCompare(reg, strings.NewReader(""), npmetrics.VCpusCountByNodePoolMetricName)).To(Succeed())
		hc, np := newFixtures()
		g.Expect(fakeClient.Create(context.Background(), hc)).To(Succeed())
		g.Expect(fakeClient.Create(context.Background(), np)).To(Succeed())
		for _, workloadType := range []string{"virt", "isv", ""} {
			np.Spec.NodeLabels = map[string]string{nodePoolWorkloadTypeLabel: workloadType}
			if workloadType == "" {
				np.Spec.NodeLabels = nil
			}
			g.Expect(fakeClient.Update(context.Background(), np)).To(Succeed())
			expected := gaugeHeader(npmetrics.VCpusCountByNodePoolMetricName, vCpusCountByNodePoolMetricHelp) + poolSample(hc, np, workloadType, 8)
			g.Expect(testutil.GatherAndCompare(reg, strings.NewReader(expected), npmetrics.VCpusCountByNodePoolMetricName)).To(Succeed())
		}
		np.Spec.Platform.AWS.InstanceType = "unknown.metal"
		g.Expect(fakeClient.Update(context.Background(), np)).To(Succeed())
		expected := gaugeHeader(npmetrics.VCpusCountByNodePoolMetricName, vCpusCountByNodePoolMetricHelp) + poolSample(hc, np, "", -1)
		expected += gaugeHeader(npmetrics.VCpusCountByHClusterMetricName, VCpusCountByHClusterMetricHelp)
		expected += fmt.Sprintf("%s{_id=%q,name=%q,namespace=%q,platform=\"AWS\"} -1\n",
			npmetrics.VCpusCountByHClusterMetricName, hc.Spec.ClusterID, hc.Name, hc.Namespace)
		expected += gaugeHeader(npmetrics.VCpusComputationErrorByHClusterMetricName, VCpusComputationErrorByHClusterMetricHelp)
		expected += fmt.Sprintf("%s{_id=%q,name=%q,namespace=%q,platform=\"AWS\",reason=%q} 1\n",
			npmetrics.VCpusComputationErrorByHClusterMetricName, hc.Spec.ClusterID, hc.Name, hc.Namespace, errRosaCPUsInstanceTypesConfigNotFound.Error())
		metricNames := []string{npmetrics.VCpusCountByNodePoolMetricName, npmetrics.VCpusCountByHClusterMetricName, npmetrics.VCpusComputationErrorByHClusterMetricName}
		g.Expect(testutil.GatherAndCompare(reg, strings.NewReader(expected), metricNames...)).To(Succeed())
		g.Expect(fakeClient.Create(context.Background(), &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: rosaCPUsInstanceTypeConfigMapName, Namespace: rosaCPUInstanceTypeConfigMapNamespace},
			Data:       map[string]string{"unknown.metal": "4"},
		})).To(Succeed())
		expected = gaugeHeader(npmetrics.VCpusCountByNodePoolMetricName, vCpusCountByNodePoolMetricHelp) + poolSample(hc, np, "", 8)
		expected += gaugeHeader(npmetrics.VCpusCountByHClusterMetricName, VCpusCountByHClusterMetricHelp)
		expected += fmt.Sprintf("%s{_id=%q,name=%q,namespace=%q,platform=\"AWS\"} 8\n",
			npmetrics.VCpusCountByHClusterMetricName, hc.Spec.ClusterID, hc.Name, hc.Namespace)
		g.Expect(testutil.GatherAndCompare(reg, strings.NewReader(expected), metricNames...)).To(Succeed())
		g.Expect(fakeClient.Delete(context.Background(), np)).To(Succeed())
		g.Expect(testutil.GatherAndCompare(reg, strings.NewReader(""), npmetrics.VCpusCountByNodePoolMetricName)).To(Succeed())
	})
}

// drainVCpuValue reads all metrics from a closed channel and returns the
// hypershift_cluster_vcpus gauge value, or -999 if not found.
func drainVCpuValue(ch <-chan prometheus.Metric) float64 {
	for m := range ch {
		if m.Desc() == vCpusCountByHClusterMetricDesc {
			var metric dto.Metric
			_ = m.Write(&metric)
			return metric.GetGauge().GetValue()
		}
	}
	return -999
}

func TestErrorCache(t *testing.T) {
	t.Run("When EC2 API reports unknown instance type, it should cache the error and skip API calls on subsequent collections", func(t *testing.T) {
		g := NewWithT(t)

		var ec2CallCount int

		hcluster := &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "hc", Namespace: "any"},
			Spec: hyperv1.HostedClusterSpec{
				ClusterID: "id",
				Platform:  hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform},
			},
		}

		nodePool := &hyperv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{Name: "np-0", Namespace: "any"},
			Spec: hyperv1.NodePoolSpec{
				ClusterName: "hc",
				Platform: hyperv1.NodePoolPlatform{
					Type: hyperv1.AWSPlatform,
					AWS:  &hyperv1.AWSNodePoolPlatform{InstanceType: "unknown-type.xlarge"},
				},
			},
			Status: hyperv1.NodePoolStatus{Replicas: 2},
		}

		configMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      rosaCPUsInstanceTypeConfigMapName,
				Namespace: rosaCPUInstanceTypeConfigMapNamespace,
			},
			Data: map[string]string{"unknown-type.xlarge": "8"},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(api.Scheme).
			WithObjects(hcluster, nodePool, configMap).
			Build()

		ec2Mock := &Ec2ClientMock{
			MockedDescribeInstanceTypesFunc: func(ctx context.Context, input *ec2v2.DescribeInstanceTypesInput, optFns ...func(*ec2v2.Options)) (*ec2v2.DescribeInstanceTypesOutput, error) {
				ec2CallCount++
				return nil, newAWSError("InvalidInstanceType", "the instance type is not recognized")
			},
		}

		collector := createNodePoolsMetricsCollector(fakeClient, ec2Mock, clock.RealClock{}).(*nodePoolsMetricsCollector)

		// First collect: EC2 API called, error cached, ConfigMap resolves vCPUs.
		ch := make(chan prometheus.Metric, 200)
		collector.Collect(ch)
		close(ch)
		g.Expect(ec2CallCount).To(Equal(1))
		g.Expect(drainVCpuValue(ch)).To(BeNumerically("==", 16)) // 2 replicas * 8 vCPUs

		// Second collect: error cache hit, EC2 API skipped, ConfigMap used directly.
		ch = make(chan prometheus.Metric, 200)
		collector.Collect(ch)
		close(ch)
		g.Expect(ec2CallCount).To(Equal(1), "EC2 API should not be called again due to error cache")
		g.Expect(drainVCpuValue(ch)).To(BeNumerically("==", 16))
	})

	t.Run("When EC2 API returns a transient error, it should not cache and should retry on subsequent collections", func(t *testing.T) {
		g := NewWithT(t)

		var ec2CallCount int

		hcluster := &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "hc", Namespace: "any"},
			Spec: hyperv1.HostedClusterSpec{
				ClusterID: "id",
				Platform:  hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform},
			},
		}

		nodePool := &hyperv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{Name: "np-0", Namespace: "any"},
			Spec: hyperv1.NodePoolSpec{
				ClusterName: "hc",
				Platform: hyperv1.NodePoolPlatform{
					Type: hyperv1.AWSPlatform,
					AWS:  &hyperv1.AWSNodePoolPlatform{InstanceType: "transient-fail.xlarge"},
				},
			},
			Status: hyperv1.NodePoolStatus{Replicas: 2},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(api.Scheme).
			WithObjects(hcluster, nodePool).
			Build()

		ec2Mock := &Ec2ClientMock{
			MockedDescribeInstanceTypesFunc: func(ctx context.Context, input *ec2v2.DescribeInstanceTypesInput, optFns ...func(*ec2v2.Options)) (*ec2v2.DescribeInstanceTypesOutput, error) {
				ec2CallCount++
				return nil, fmt.Errorf("network timeout")
			},
		}

		collector := createNodePoolsMetricsCollector(fakeClient, ec2Mock, clock.RealClock{}).(*nodePoolsMetricsCollector)

		// First collect: EC2 API called, fails with transient error, no ConfigMap -> vCPUs = -1.
		ch := make(chan prometheus.Metric, 200)
		collector.Collect(ch)
		close(ch)
		g.Expect(ec2CallCount).To(Equal(1))

		// Second collect: error NOT cached (transient error), EC2 API retried.
		ch = make(chan prometheus.Metric, 200)
		collector.Collect(ch)
		close(ch)
		g.Expect(ec2CallCount).To(Equal(2), "EC2 API should be retried when its error was transient")
	})
}

func TestReportVCpusWithKarpenterAutoNode(t *testing.T) {
	testCases := []struct {
		name      string
		autoVCPUs *int32
		npsParams []nodePoolParams

		MockedEC2DescribeInstanceTypesFunc func(ctx context.Context, input *ec2v2.DescribeInstanceTypesInput, optFns ...func(*ec2v2.Options)) (*ec2v2.DescribeInstanceTypesOutput, error)

		expectedVCpusCount            float64
		expectedVCpusCountErrorReason string
	}{
		{
			name:               "When cluster has only Karpenter nodes, it should report AutoNode vCPUs",
			autoVCPUs:          ptr.To[int32](12),
			npsParams:          []nodePoolParams{},
			expectedVCpusCount: 12,
		},
		{
			name:      "When cluster has both Karpenter and native nodes, it should sum both",
			autoVCPUs: ptr.To[int32](20),
			npsParams: []nodePoolParams{
				{availableNodesCount: 2, ec2InstanceType: "m5.xlarge"},
			},
			MockedEC2DescribeInstanceTypesFunc: func(ctx context.Context, input *ec2v2.DescribeInstanceTypesInput, optFns ...func(*ec2v2.Options)) (*ec2v2.DescribeInstanceTypesOutput, error) {
				return initDescribeInstanceTypesOutput([]ec2typesv2.InstanceTypeInfo{
					initInstanceTypeInfo("m5.xlarge", 4)}), nil
			},
			expectedVCpusCount: 28, // 20 Karpenter + 2*4 native
		},
		{
			name:      "When AutoNode.VCPUs is nil, it should report only native vCPUs",
			autoVCPUs: nil,
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
			name:      "When native lookup fails with Karpenter vCPUs, it should report -1",
			autoVCPUs: ptr.To[int32](20),
			npsParams: []nodePoolParams{
				{availableNodesCount: 2, ec2InstanceType: "unknown-type"},
			},
			MockedEC2DescribeInstanceTypesFunc: func(ctx context.Context, input *ec2v2.DescribeInstanceTypesInput, optFns ...func(*ec2v2.Options)) (*ec2v2.DescribeInstanceTypesOutput, error) {
				return &ec2v2.DescribeInstanceTypesOutput{}, nil
			},
			expectedVCpusCount:            -1,
			expectedVCpusCountErrorReason: errRosaCPUsInstanceTypesConfigNotFound.Error(),
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
				Status: hyperv1.HostedClusterStatus{
					AutoNode: hyperv1.AutoNodeStatus{
						VCPUs: tc.autoVCPUs,
					},
				},
			}

			clientBuilder := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(hcluster)

			ec2MockedClient := &Ec2ClientMock{}
			if tc.MockedEC2DescribeInstanceTypesFunc != nil {
				ec2MockedClient.MockedDescribeInstanceTypesFunc = tc.MockedEC2DescribeInstanceTypesFunc
			}

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
			reg.MustRegister(createNodePoolsMetricsCollector(clientBuilder.Build(), ec2MockedClient, clock.RealClock{}))

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
					case npmetrics.VCpusCountByHClusterMetricName:
						vCpusCountMetricValue = metricValue
					case npmetrics.VCpusComputationErrorByHClusterMetricName:
						vCpusComputationErrorMetricValue = metricValue
					}
				}
			}

			expectedBaseLabels := []*dto.LabelPair{
				{Name: ptr.To("_id"), Value: ptr.To("id")},
				{Name: ptr.To("name"), Value: ptr.To("hc")},
				{Name: ptr.To("namespace"), Value: ptr.To("any")},
				{Name: ptr.To("platform"), Value: ptr.To(string(hyperv1.AWSPlatform))},
			}

			expectedVCpusCountMetricValue := &dto.MetricFamily{
				Name: ptr.To(npmetrics.VCpusCountByHClusterMetricName),
				Help: ptr.To(VCpusCountByHClusterMetricHelp),
				Type: func() *dto.MetricType { v := dto.MetricType(1); return &v }(),
				Metric: []*dto.Metric{{
					Label: expectedBaseLabels,
					Gauge: &dto.Gauge{Value: ptr.To(tc.expectedVCpusCount)},
				}},
			}

			if tc.expectedVCpusCountErrorReason != "" {
				expectedVCpusComputationErrorMetricValue = &dto.MetricFamily{
					Name: ptr.To(npmetrics.VCpusComputationErrorByHClusterMetricName),
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
				t.Errorf("vCpus count differs from expected: %s", diff)
			}

			if diff := cmp.Diff(vCpusComputationErrorMetricValue, expectedVCpusComputationErrorMetricValue, ignoreUnexportedDto); diff != "" {
				t.Errorf("vCpus error metric differs from expected: %s", diff)
			}
		})
	}
}
