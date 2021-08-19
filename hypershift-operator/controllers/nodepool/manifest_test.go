package nodepool

import (
	"github.com/google/go-cmp/cmp"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/equality"
	k8sutilspointer "k8s.io/utils/pointer"
	capiaws "sigs.k8s.io/cluster-api-provider-aws/api/v1alpha4"
	"testing"
)

const amiName = "ami"

var volume = hyperv1.Volume{
	Size: 16,
	Type: "io1",
	IOPS: 5000,
}

func TestAWSMachineTemplate(t *testing.T) {
	testCases := []struct {
		name     string
		nodePool hyperv1.NodePoolSpec
		expected *capiaws.AWSMachineTemplate
	}{
		{
			name: "ebs size",
			nodePool: hyperv1.NodePoolSpec{
				ClusterName: "",
				NodeCount:   nil,
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
			expected: withRootVolume(&volume),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, _ := AWSMachineTemplate("testi", amiName, &hyperv1.NodePool{Spec: tc.nodePool}, "test")
			if !equality.Semantic.DeepEqual(tc.expected.Spec, result.Spec) {
				t.Errorf(cmp.Diff(tc.expected.Spec, result.Spec))
			}
		})
	}
}

func withRootVolume(v *hyperv1.Volume) *capiaws.AWSMachineTemplate {
	template := defaultAWSMachineTemplate()
	template.Spec.Template.Spec.RootVolume = &capiaws.Volume{
		Size: v.Size,
		Type: capiaws.VolumeType(v.Type),
		IOPS: v.IOPS,
	}
	return template
}

func defaultAWSMachineTemplate() *capiaws.AWSMachineTemplate {
	return &capiaws.AWSMachineTemplate{
		Spec: capiaws.AWSMachineTemplateSpec{
			Template: capiaws.AWSMachineTemplateResource{
				Spec: capiaws.AWSMachineSpec{
					AMI: capiaws.AMIReference{
						ID: k8sutilspointer.StringPtr(amiName),
					},
					IAMInstanceProfile:       "testi-worker-profile",
					AdditionalSecurityGroups: []capiaws.AWSResourceReference{},
					Subnet:                   &capiaws.AWSResourceReference{},
					UncompressedUserData:     k8sutilspointer.BoolPtr(true),
					CloudInit: capiaws.CloudInit{
						InsecureSkipSecretsManager: true,
						SecureSecretsBackend:       "secrets-manager",
					},
				},
			},
		},
	}
}
