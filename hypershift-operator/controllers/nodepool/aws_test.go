package nodepool

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"k8s.io/apimachinery/pkg/api/equality"
	k8sutilspointer "k8s.io/utils/pointer"
	capiaws "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
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
			ID: k8sutilspointer.String("default"),
		},
	}
	testCases := []struct {
		name          string
		cluster       hyperv1.HostedClusterSpec
		clusterStatus hyperv1.HostedClusterStatus
		nodePool      hyperv1.NodePoolSpec
		expected      *capiaws.AWSMachineTemplate
		checkError    func(*testing.T, error)
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
						RootVolume:     &volume,
						SecurityGroups: defaultSG,
					},
				},
				Release: hyperv1.Release{},
			},

			expected: defaultAWSMachineTemplate(withRootVolume(&volume)),
		},
		{
			name: "Tags from nodepool get copied",
			nodePool: hyperv1.NodePoolSpec{Platform: hyperv1.NodePoolPlatform{AWS: &hyperv1.AWSNodePoolPlatform{
				SecurityGroups: defaultSG,
				ResourceTags: []hyperv1.AWSResourceTag{
					{Key: "key", Value: "value"},
				},
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
			nodePool: hyperv1.NodePoolSpec{Platform: hyperv1.NodePoolPlatform{AWS: &hyperv1.AWSNodePoolPlatform{SecurityGroups: defaultSG}}},

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
				SecurityGroups: defaultSG,
				ResourceTags: []hyperv1.AWSResourceTag{
					{Key: "nodepool-only", Value: "value"},
					{Key: "cluster-and-nodepool", Value: "nodepool"},
				},
			}}},

			expected: defaultAWSMachineTemplate(func(tmpl *capiaws.AWSMachineTemplate) {
				tmpl.Spec.Template.Spec.AdditionalTags["cluster-only"] = "value"
				tmpl.Spec.Template.Spec.AdditionalTags["cluster-and-nodepool"] = "cluster"
				tmpl.Spec.Template.Spec.AdditionalTags["nodepool-only"] = "value"
			}),
		},
		{
			name:          "Cluster default sg is used when none specified",
			clusterStatus: hyperv1.HostedClusterStatus{Platform: &hyperv1.PlatformStatus{AWS: &hyperv1.AWSPlatformStatus{DefaultWorkerSecurityGroupID: "cluster-default"}}},
			expected: defaultAWSMachineTemplate(func(tmpl *capiaws.AWSMachineTemplate) {
				tmpl.Spec.Template.Spec.AdditionalSecurityGroups = []capiaws.AWSResourceReference{{ID: k8sutilspointer.String("cluster-default")}}
			}),
		},
		{
			name:          "NodePool sg is preferred to cluster default",
			clusterStatus: hyperv1.HostedClusterStatus{Platform: &hyperv1.PlatformStatus{AWS: &hyperv1.AWSPlatformStatus{DefaultWorkerSecurityGroupID: "cluster-default"}}},
			nodePool: hyperv1.NodePoolSpec{Platform: hyperv1.NodePoolPlatform{AWS: &hyperv1.AWSNodePoolPlatform{
				SecurityGroups: []hyperv1.AWSResourceReference{{ID: k8sutilspointer.String("nodepool-specific")}},
			}}},
			expected: defaultAWSMachineTemplate(func(tmpl *capiaws.AWSMachineTemplate) {
				tmpl.Spec.Template.Spec.AdditionalSecurityGroups = []capiaws.AWSResourceReference{{ID: k8sutilspointer.String("nodepool-specific")}}
			}),
		},
		{
			name: "NotReady error is returned if no sg specified and no cluster sg is available",
			checkError: func(t *testing.T, err error) {
				_, isNotReady := err.(*NotReadyError)
				if err == nil || !isNotReady {
					t.Errorf("did not get expected NotReady error")
				}
			},
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
			result, err := awsMachineTemplateSpec(infraName, amiName, &hyperv1.HostedCluster{Spec: tc.cluster, Status: tc.clusterStatus}, &hyperv1.NodePool{Spec: tc.nodePool}, true)
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
				t.Errorf(cmp.Diff(tc.expected.Spec, result))
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
						ID: k8sutilspointer.StringPtr(amiName),
					},
					AdditionalTags: capiaws.Tags{
						awsClusterCloudProviderTagKey(infraName): infraLifecycleOwned,
					},
					IAMInstanceProfile:       infraName + "-worker-profile",
					AdditionalSecurityGroups: []capiaws.AWSResourceReference{{ID: k8sutilspointer.String("default")}},
					Subnet:                   &capiaws.AWSResourceReference{},
					UncompressedUserData:     k8sutilspointer.BoolPtr(true),
					CloudInit: capiaws.CloudInit{
						InsecureSkipSecretsManager: true,
						SecureSecretsBackend:       "secrets-manager",
					},
					RootVolume: &capiaws.Volume{Size: 16},
				},
			},
		},
	}

	for _, m := range modify {
		m(template)
	}

	return template
}
