package nodepool

import (
	"context"
	"fmt"
	"strings"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/releaseinfo"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	capiaws "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/google/go-cmp/cmp"
)

const amiName = "ami"
const infraName = "test"

var volume = hyperv1.Volume{
	Size: 16,
	Type: "io1",
	IOPS: 5000,
}

func TestAWSMachineTemplate(t *testing.T) {
	infraName := "test"
	defaultSG := []hyperv1.AWSResourceReference{
		{
			ID: ptr.To("default"),
		},
	}
	testCases := []struct {
		name                string
		cluster             hyperv1.HostedClusterSpec
		clusterStatus       *hyperv1.HostedClusterStatus
		nodePool            hyperv1.NodePoolSpec
		nodePoolAnnotations map[string]string
		expected            *capiaws.AWSMachineTemplate
		checkError          func(*testing.T, error)
	}{
		{
			name: "ebs size",
			nodePool: hyperv1.NodePoolSpec{
				ClusterName: "",
				Replicas:    nil,
				Config:      nil,
				Management:  hyperv1.NodePoolManagement{},
				AutoScaling: nil,
				Platform: hyperv1.NodePoolPlatform{
					Type: hyperv1.AWSPlatform,
					AWS: &hyperv1.AWSNodePoolPlatform{
						RootVolume: &volume,
						AMI:        amiName,
					},
				},
				Release: hyperv1.Release{},
			},

			expected: defaultAWSMachineTemplate(withRootVolume(&volume)),
		},
		{
			name: "Tags from nodepool get copied",
			nodePool: hyperv1.NodePoolSpec{Platform: hyperv1.NodePoolPlatform{AWS: &hyperv1.AWSNodePoolPlatform{
				ResourceTags: []hyperv1.AWSResourceTag{
					{Key: "key", Value: "value"},
				},
				AMI: amiName,
			}}},

			expected: defaultAWSMachineTemplate(func(tmpl *capiaws.AWSMachineTemplate) {
				tmpl.Spec.Template.Spec.AdditionalTags["key"] = "value"
			}),
		},
		{
			name: "Tags from cluster get copied",
			cluster: hyperv1.HostedClusterSpec{Platform: hyperv1.PlatformSpec{AWS: &hyperv1.AWSPlatformSpec{
				ResourceTags: []hyperv1.AWSResourceTag{
					{Key: "key", Value: "value"},
				},
			}}},
			nodePool: hyperv1.NodePoolSpec{Platform: hyperv1.NodePoolPlatform{AWS: &hyperv1.AWSNodePoolPlatform{
				AMI: amiName,
			}}},

			expected: defaultAWSMachineTemplate(func(tmpl *capiaws.AWSMachineTemplate) {
				tmpl.Spec.Template.Spec.AdditionalTags["key"] = "value"
			}),
		},
		{
			name: "Cluster tags take precedence over nodepool tags",
			cluster: hyperv1.HostedClusterSpec{Platform: hyperv1.PlatformSpec{AWS: &hyperv1.AWSPlatformSpec{
				ResourceTags: []hyperv1.AWSResourceTag{
					{Key: "cluster-only", Value: "value"},
					{Key: "cluster-and-nodepool", Value: "cluster"},
				},
			}}},
			nodePool: hyperv1.NodePoolSpec{Platform: hyperv1.NodePoolPlatform{AWS: &hyperv1.AWSNodePoolPlatform{
				ResourceTags: []hyperv1.AWSResourceTag{
					{Key: "nodepool-only", Value: "value"},
					{Key: "cluster-and-nodepool", Value: "nodepool"},
				},
				AMI: amiName,
			}}},

			expected: defaultAWSMachineTemplate(func(tmpl *capiaws.AWSMachineTemplate) {
				tmpl.Spec.Template.Spec.AdditionalTags["cluster-only"] = "value"
				tmpl.Spec.Template.Spec.AdditionalTags["cluster-and-nodepool"] = "cluster"
				tmpl.Spec.Template.Spec.AdditionalTags["nodepool-only"] = "value"
			}),
		},
		{
			name:          "Cluster default sg is used when none specified",
			clusterStatus: &hyperv1.HostedClusterStatus{Platform: &hyperv1.PlatformStatus{AWS: &hyperv1.AWSPlatformStatus{DefaultWorkerSecurityGroupID: "cluster-default"}}},
			nodePool: hyperv1.NodePoolSpec{Platform: hyperv1.NodePoolPlatform{AWS: &hyperv1.AWSNodePoolPlatform{
				AMI: amiName,
			}}},
			expected: defaultAWSMachineTemplate(func(tmpl *capiaws.AWSMachineTemplate) {
				tmpl.Spec.Template.Spec.AdditionalSecurityGroups = []capiaws.AWSResourceReference{{ID: ptr.To("cluster-default")}}
			}),
		},
		{
			name: "NodePool sg is used in addition to cluster default",
			nodePool: hyperv1.NodePoolSpec{Platform: hyperv1.NodePoolPlatform{AWS: &hyperv1.AWSNodePoolPlatform{
				SecurityGroups: []hyperv1.AWSResourceReference{{ID: ptr.To("nodepool-specific")}},
				AMI:            amiName,
			}}},
			expected: defaultAWSMachineTemplate(func(tmpl *capiaws.AWSMachineTemplate) {
				tmpl.Spec.Template.Spec.AdditionalSecurityGroups = []capiaws.AWSResourceReference{{ID: ptr.To("nodepool-specific")}, {ID: defaultSG[0].ID}}
			}),
		},
		{
			name:          "NotReady error is returned if no sg specified and no cluster sg is available",
			clusterStatus: &hyperv1.HostedClusterStatus{Platform: &hyperv1.PlatformStatus{AWS: &hyperv1.AWSPlatformStatus{DefaultWorkerSecurityGroupID: ""}}},
			checkError: func(t *testing.T, err error) {
				_, isNotReady := err.(*NotReadyError)
				if err == nil || !isNotReady {
					t.Errorf("did not get expected NotReady error")
				}
			},
			nodePool: hyperv1.NodePoolSpec{Platform: hyperv1.NodePoolPlatform{AWS: &hyperv1.AWSNodePoolPlatform{
				AMI: amiName,
			}}},
		},
		{
			name: "NodePool has ec2-http-tokens annotation with 'required' as a value",
			nodePool: hyperv1.NodePoolSpec{Platform: hyperv1.NodePoolPlatform{AWS: &hyperv1.AWSNodePoolPlatform{
				AMI: amiName,
			}}},
			nodePoolAnnotations: map[string]string{
				ec2InstanceMetadataHTTPTokensAnnotation: "required",
			},
			expected: defaultAWSMachineTemplate(func(tmpl *capiaws.AWSMachineTemplate) {
				tmpl.Spec.Template.Spec.InstanceMetadataOptions.HTTPTokens = capiaws.HTTPTokensStateRequired
			}),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.cluster.Platform.AWS == nil {
				tc.cluster.Platform.AWS = &hyperv1.AWSPlatformSpec{}
			}
			if tc.nodePool.Platform.AWS == nil {
				tc.nodePool.Platform.AWS = &hyperv1.AWSNodePoolPlatform{}
			}
			clusterStatus := hyperv1.HostedClusterStatus{Platform: &hyperv1.PlatformStatus{AWS: &hyperv1.AWSPlatformStatus{DefaultWorkerSecurityGroupID: *defaultSG[0].ID}}}
			if tc.clusterStatus != nil {
				clusterStatus = *tc.clusterStatus
			}
			result, err := awsMachineTemplateSpec(infraName,
				&hyperv1.HostedCluster{Spec: tc.cluster, Status: clusterStatus},
				&hyperv1.NodePool{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: tc.nodePoolAnnotations,
					},
					Spec: tc.nodePool,
				},
				true,
				&releaseinfo.ReleaseImage{},
			)
			if tc.checkError != nil {
				tc.checkError(t, err)
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
			if tc.expected == nil {
				return
			}
			if !equality.Semantic.DeepEqual(&tc.expected.Spec, result) {
				t.Error(cmp.Diff(&tc.expected.Spec, result))
			}
		})
	}
}

func withRootVolume(v *hyperv1.Volume) func(*capiaws.AWSMachineTemplate) {
	return func(template *capiaws.AWSMachineTemplate) {
		template.Spec.Template.Spec.RootVolume = &capiaws.Volume{
			Size: v.Size,
			Type: capiaws.VolumeType(v.Type),
			IOPS: v.IOPS,
		}
	}
}

func defaultAWSMachineTemplate(modify ...func(*capiaws.AWSMachineTemplate)) *capiaws.AWSMachineTemplate {
	template := &capiaws.AWSMachineTemplate{
		Spec: capiaws.AWSMachineTemplateSpec{
			Template: capiaws.AWSMachineTemplateResource{
				Spec: capiaws.AWSMachineSpec{
					AMI: capiaws.AMIReference{
						ID: ptr.To(amiName),
					},
					AdditionalTags: capiaws.Tags{
						awsClusterCloudProviderTagKey(infraName): infraLifecycleOwned,
					},
					IAMInstanceProfile:       infraName + "-worker-profile",
					AdditionalSecurityGroups: []capiaws.AWSResourceReference{{ID: ptr.To("default")}},
					Subnet:                   &capiaws.AWSResourceReference{},
					UncompressedUserData:     ptr.To(true),
					CloudInit: capiaws.CloudInit{
						InsecureSkipSecretsManager: true,
						SecureSecretsBackend:       "secrets-manager",
					},
					RootVolume: &capiaws.Volume{Size: 16},
					InstanceMetadataOptions: &capiaws.InstanceMetadataOptions{
						HTTPTokens:              capiaws.HTTPTokensStateOptional,
						HTTPPutResponseHopLimit: 2,
						HTTPEndpoint:            capiaws.InstanceMetadataEndpointStateEnabled,
						InstanceMetadataTags:    capiaws.InstanceMetadataEndpointStateDisabled,
					},
				},
			},
		},
	}

	for _, m := range modify {
		m(template)
	}

	return template
}

type fakeEC2Client struct {
	ec2iface.EC2API

	capacityReservation    *ec2.CapacityReservation
	SubnetAvailabilityZone string
}

func (fake *fakeEC2Client) DescribeCapacityReservationsWithContext(aws.Context, *ec2.DescribeCapacityReservationsInput, ...request.Option) (*ec2.DescribeCapacityReservationsOutput, error) {
	return &ec2.DescribeCapacityReservationsOutput{
		CapacityReservations: []*ec2.CapacityReservation{
			fake.capacityReservation,
		},
	}, nil
}

func (fake *fakeEC2Client) DescribeSubnetsWithContext(aws.Context, *ec2.DescribeSubnetsInput, ...request.Option) (*ec2.DescribeSubnetsOutput, error) {
	return &ec2.DescribeSubnetsOutput{
		Subnets: []*ec2.Subnet{
			{
				AvailabilityZone: ptr.To(fake.SubnetAvailabilityZone),
			},
		},
	}, nil
}

func fakePullSecret(hc *hyperv1.HostedCluster) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      hc.Spec.PullSecret.Name,
			Namespace: hc.Namespace,
		},
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: []byte("fake"),
		},
	}
}
func TestValidateAWSPlatformConfig(t *testing.T) {
	hostedcluster := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test",
		},
		Spec: hyperv1.HostedClusterSpec{
			PullSecret: corev1.LocalObjectReference{Name: "fake-pullsecret"},
		},
	}

	capacityReservationID := "cr-fakeID"

	pullSecret := fakePullSecret(hostedcluster)
	fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(pullSecret).Build()

	type NodePoolOptions struct {
		instanceType           string
		replicas               *int32
		autoScaling            *hyperv1.NodePoolAutoScaling
		subnetAvailabilityZone string
	}

	testCases := []struct {
		name                 string
		hostedClusterVersion string
		nodePoolOpts         NodePoolOptions
		capacityReservation  *ec2.CapacityReservation
		oldCondition         *hyperv1.NodePoolCondition
		expectedError        string
	}{
		{
			name: "If capacityReservation was already reported expired/cancelled, skip checks and return old condition message",
			oldCondition: &hyperv1.NodePoolCondition{
				Status:  corev1.ConditionFalse,
				Message: fmt.Sprintf("capacityReservation %s is expired", capacityReservationID),
			},
			expectedError: fmt.Sprintf("capacityReservation %s is expired", capacityReservationID),
		},
		{
			name:                 "If hostedCluster < 4.19 it should fail",
			hostedClusterVersion: "4.18.0",
			expectedError:        "capacityReservation is only supported on 4.19+ clusters",
		},
		{
			name:                 "If capacityReservation is cancelled or expired it should fail",
			hostedClusterVersion: "4.19.0",
			capacityReservation: &ec2.CapacityReservation{
				State: ptr.To("expired"),
			},
			expectedError: "expired",
		},
		{
			name:                 "If nodePool autoScaling max replicas is greater than capacityReservation total instance count it should fail",
			hostedClusterVersion: "4.19.0",
			nodePoolOpts: NodePoolOptions{
				autoScaling: &hyperv1.NodePoolAutoScaling{
					Max: 4,
				},
			},
			capacityReservation: &ec2.CapacityReservation{
				State:              ptr.To("active"),
				TotalInstanceCount: ptr.To[int64](2),
			},
			expectedError: "nodePool.Spec.AutoScaling.Max '4' is greater than the capacityReservation total instance count '2'",
		},
		{
			name:                 "If nodePool replicas is greater than capacityReservation total instance count it should fail",
			hostedClusterVersion: "4.19.0",
			nodePoolOpts: NodePoolOptions{
				replicas: ptr.To[int32](4),
			},
			capacityReservation: &ec2.CapacityReservation{
				State:              ptr.To("active"),
				TotalInstanceCount: ptr.To[int64](2),
			},
			expectedError: "nodePool.Spec.Replicas '4' is greater than the capacityReservation total instance count '2'",
		},
		{
			name:                 "If nodePool instanceType doesn't match the capacityReservation instanceType it should fail",
			hostedClusterVersion: "4.19.0",
			nodePoolOpts: NodePoolOptions{
				replicas:     ptr.To[int32](2),
				instanceType: "m5.large",
			},
			capacityReservation: &ec2.CapacityReservation{
				State:              ptr.To("active"),
				TotalInstanceCount: ptr.To[int64](2),
				InstanceType:       ptr.To("m1.medium"),
			},
			expectedError: "nodePool.Spec.Platform.AWS.InstanceType 'm5.large' doesn't match the capacityReservation instance type 'm1.medium'",
		},
		{
			name:                 "If nodePool subnet availabilityZone doesn't match the capacityReservation availabilityZone it should fail",
			hostedClusterVersion: "4.19.0",
			nodePoolOpts: NodePoolOptions{
				replicas:               ptr.To[int32](2),
				instanceType:           "m5.large",
				subnetAvailabilityZone: "eu-west-1a",
			},
			capacityReservation: &ec2.CapacityReservation{
				State:              ptr.To("active"),
				TotalInstanceCount: ptr.To[int64](2),
				InstanceType:       ptr.To("m5.large"),
				AvailabilityZone:   ptr.To("us-east-2a"),
			},
			expectedError: "nodePool availabilityZone 'eu-west-1a' doesn't match the capacityReservation availabilityZone 'us-east-2a'",
		},
		{
			name:                 "If nodePool and capacityReservation options match it should successed",
			hostedClusterVersion: "4.19.0",
			nodePoolOpts: NodePoolOptions{
				replicas:               ptr.To[int32](2),
				instanceType:           "m5.large",
				subnetAvailabilityZone: "us-east-2a",
			},
			capacityReservation: &ec2.CapacityReservation{
				State:              ptr.To("active"),
				TotalInstanceCount: ptr.To[int64](2),
				InstanceType:       ptr.To("m5.large"),
				AvailabilityZone:   ptr.To("us-east-2a"),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			hostedcluster.Status.Version = &hyperv1.ClusterVersionStatus{
				History: []configv1.UpdateHistory{
					{
						CompletionTime: &metav1.Time{},
						Version:        tc.hostedClusterVersion,
					},
				},
			}

			nodePool := &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Replicas:    tc.nodePoolOpts.replicas,
					AutoScaling: tc.nodePoolOpts.autoScaling,
					Platform: hyperv1.NodePoolPlatform{
						AWS: &hyperv1.AWSNodePoolPlatform{
							InstanceType: tc.nodePoolOpts.instanceType,
							Placement: &hyperv1.PlacementOptions{
								CapacityReservation: &hyperv1.CapacityReservationOptions{
									ID: capacityReservationID,
								},
							},
						},
					},
				},
			}

			fakeEC2Client := &fakeEC2Client{
				capacityReservation:    tc.capacityReservation,
				SubnetAvailabilityZone: tc.nodePoolOpts.subnetAvailabilityZone,
			}

			reconciler := &NodePoolReconciler{
				Client:    fakeClient,
				EC2Client: fakeEC2Client,
			}
			err := reconciler.validateAWSPlatformConfig(context.Background(), nodePool, hostedcluster, tc.oldCondition)
			if tc.expectedError == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}

			if err == nil {
				t.Fatalf("expected an error, got nothing")
			}

			if !strings.Contains(err.Error(), tc.expectedError) {
				t.Fatalf("expected error to contain %s, got %v", tc.expectedError, err)
			}
		})
	}
}
