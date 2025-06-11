package nodepool

import (
	"context"
	"strings"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/releaseinfo"

	configv1 "github.com/openshift/api/config/v1"

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

	testCases := []struct {
		name                 string
		hostedClusterVersion string
		oldCondition         *hyperv1.NodePoolCondition
		expectedError        string
	}{

		{
			name:                 "If hostedCluster < 4.19 it should fail",
			hostedClusterVersion: "4.18.0",
			expectedError:        "capacityReservation is only supported on 4.19+ clusters",
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
					Platform: hyperv1.NodePoolPlatform{
						AWS: &hyperv1.AWSNodePoolPlatform{
							Placement: &hyperv1.PlacementOptions{
								CapacityReservation: &hyperv1.CapacityReservationOptions{
									ID: capacityReservationID,
								},
							},
						},
					},
				},
			}

			reconciler := &NodePoolReconciler{
				Client: fakeClient,
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
