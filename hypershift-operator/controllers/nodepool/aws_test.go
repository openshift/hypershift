package nodepool

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/equality"
	k8sutilspointer "k8s.io/utils/pointer"
	capiaws "sigs.k8s.io/cluster-api-provider-aws/api/v1beta1"
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
	testCases := []struct {
		name     string
		cluster  hyperv1.HostedClusterSpec
		nodePool hyperv1.NodePoolSpec
		expected *capiaws.AWSMachineTemplate
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
			}}},

			expected: defaultAWSMachineTemplate(func(tmpl *capiaws.AWSMachineTemplate) {
				tmpl.Spec.Template.Spec.AdditionalTags["cluster-only"] = "value"
				tmpl.Spec.Template.Spec.AdditionalTags["cluster-and-nodepool"] = "cluster"
				tmpl.Spec.Template.Spec.AdditionalTags["nodepool-only"] = "value"
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
			result := awsMachineTemplateSpec(infraName, amiName, &hyperv1.HostedCluster{Spec: tc.cluster}, &hyperv1.NodePool{Spec: tc.nodePool})
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
					AdditionalSecurityGroups: []capiaws.AWSResourceReference{},
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
