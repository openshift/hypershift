package nodepool

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/releaseinfo"
	supportutil "github.com/openshift/hypershift/support/util"

	configv1 "github.com/openshift/api/config/v1"
	v1 "github.com/openshift/api/image/v1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/utils/ptr"

	capiaws "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

func TestAWSMachineTemplateSpec(t *testing.T) {
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
				var notReadyErr *NotReadyError
				if err == nil || !errors.As(err, &notReadyErr) {
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
		{
			name: "Windows ImageType without AMI specified should use Windows AMI mapping",
			cluster: hyperv1.HostedClusterSpec{Platform: hyperv1.PlatformSpec{AWS: &hyperv1.AWSPlatformSpec{
				Region: "us-east-1",
			}}},
			nodePool: hyperv1.NodePoolSpec{
				Platform: hyperv1.NodePoolPlatform{AWS: &hyperv1.AWSNodePoolPlatform{
					ImageType: hyperv1.ImageTypeWindows,
				}},
				Arch: hyperv1.ArchitectureAMD64,
			},
			expected: defaultAWSMachineTemplate(func(tmpl *capiaws.AWSMachineTemplate) {
				tmpl.Spec.Template.Spec.AMI.ID = ptr.To("ami-0abcdef1234567890")
			}),
		},
		{
			name: "Windows ImageType with AMI specified should use specified AMI",
			cluster: hyperv1.HostedClusterSpec{Platform: hyperv1.PlatformSpec{AWS: &hyperv1.AWSPlatformSpec{
				Region: "us-east-1",
			}}},
			nodePool: hyperv1.NodePoolSpec{
				Platform: hyperv1.NodePoolPlatform{AWS: &hyperv1.AWSNodePoolPlatform{
					AMI:       "ami-custom-windows",
					ImageType: hyperv1.ImageTypeWindows,
				}},
			},
			expected: defaultAWSMachineTemplate(func(tmpl *capiaws.AWSMachineTemplate) {
				tmpl.Spec.Template.Spec.AMI.ID = ptr.To("ami-custom-windows")
			}),
		},
		{
			name: "When spot is enabled via annotation, it should add managed tag",
			nodePool: hyperv1.NodePoolSpec{
				Platform: hyperv1.NodePoolPlatform{AWS: &hyperv1.AWSNodePoolPlatform{
					AMI: amiName,
				}},
			},
			nodePoolAnnotations: map[string]string{
				AnnotationEnableSpot: "true",
			},
			expected: defaultAWSMachineTemplate(func(tmpl *capiaws.AWSMachineTemplate) {
				tmpl.Spec.Template.Spec.AdditionalTags["aws-node-termination-handler/managed"] = ""
			}),
		},
		{
			name: "When spot is enabled via API, it should set SpotMarketOptions and add managed tag",
			nodePool: hyperv1.NodePoolSpec{
				Platform: hyperv1.NodePoolPlatform{AWS: &hyperv1.AWSNodePoolPlatform{
					AMI: amiName,
					Placement: &hyperv1.PlacementOptions{
						MarketType: hyperv1.MarketTypeSpot,
						Spot:       hyperv1.SpotOptions{},
					},
				}},
			},
			expected: defaultAWSMachineTemplate(func(tmpl *capiaws.AWSMachineTemplate) {
				tmpl.Spec.Template.Spec.SpotMarketOptions = &capiaws.SpotMarketOptions{}
				tmpl.Spec.Template.Spec.AdditionalTags["aws-node-termination-handler/managed"] = ""
			}),
		},
		{
			name: "When spot is enabled via API with MaxPrice, it should set SpotMarketOptions with MaxPrice",
			nodePool: hyperv1.NodePoolSpec{
				Platform: hyperv1.NodePoolPlatform{AWS: &hyperv1.AWSNodePoolPlatform{
					AMI: amiName,
					Placement: &hyperv1.PlacementOptions{
						MarketType: hyperv1.MarketTypeSpot,
						Spot: hyperv1.SpotOptions{
							MaxPrice: "0.50",
						},
					},
				}},
			},
			expected: defaultAWSMachineTemplate(func(tmpl *capiaws.AWSMachineTemplate) {
				tmpl.Spec.Template.Spec.SpotMarketOptions = &capiaws.SpotMarketOptions{
					MaxPrice: ptr.To("0.50"),
				}
				tmpl.Spec.Template.Spec.AdditionalTags["aws-node-termination-handler/managed"] = ""
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
			releaseImage := &releaseinfo.ReleaseImage{
				ImageStream: &v1.ImageStream{
					ObjectMeta: metav1.ObjectMeta{
						Name: "4.17.0",
					},
				},
				StreamMetadata: &releaseinfo.CoreOSStreamMetadata{
					Architectures: map[string]releaseinfo.CoreOSArchitecture{
						"x86_64": {
							RHCOS: releaseinfo.CoreRHCOSImage{
								AWSWinLi: releaseinfo.CoreAWSWinLi{
									Regions: map[string]releaseinfo.CoreAWSWinLiRegion{
										"us-east-1": {
											Release: "418.94.202410090804-0",
											Image:   "ami-0abcdef1234567890",
										},
									},
								},
							},
						},
					},
				},
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
				releaseImage,
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

func TestAWSMachineTemplate(t *testing.T) {
	// A simple name generator for predictable test outcomes.
	mockTemplateNameGenerator := func(spec any) (string, error) {
		specJSON, err := json.Marshal(spec)
		if err != nil {
			return "", err
		}
		return getName("test-prefix", supportutil.HashSimple(specJSON),
			validation.DNS1123SubdomainMaxLength), nil
	}

	namespace := "test-ns"

	testCases := []struct {
		name             string
		nodePool         *hyperv1.NodePool
		existingTemplate *capiaws.AWSMachineTemplate
		expectedName     string
		expectedTags     capiaws.Tags
	}{
		{
			name: "Migration: should avoid rollout on existing nodepools by reusing existing template name when nothing changes",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{Name: "stable-nodepool"},
				Spec: hyperv1.NodePoolSpec{
					Management: hyperv1.NodePoolManagement{UpgradeType: hyperv1.UpgradeTypeReplace},
					Platform: hyperv1.NodePoolPlatform{AWS: &hyperv1.AWSNodePoolPlatform{
						AMI:          amiName,
						InstanceType: "t3.large",
						ResourceTags: []hyperv1.AWSResourceTag{{Key: "version", Value: "stable"}},
					}},
				},
			},
			existingTemplate: defaultAWSMachineTemplate(func(template *capiaws.AWSMachineTemplate) {
				template.Name = "stable-name"
				template.Spec.Template.Spec.InstanceType = "t3.large"
				template.Spec.Template.Spec.AdditionalTags = capiaws.Tags{"version": "stable"}
			}),
			expectedName: "stable-name",
			expectedTags: capiaws.Tags{"version": "stable"},
		},
		{
			name: "should reuse existing template name when only tags change",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{Name: "test-nodepool"},
				Spec: hyperv1.NodePoolSpec{
					Management: hyperv1.NodePoolManagement{UpgradeType: hyperv1.UpgradeTypeReplace},
					Platform: hyperv1.NodePoolPlatform{AWS: &hyperv1.AWSNodePoolPlatform{
						AMI:          amiName,
						InstanceType: "t3.large",
						ResourceTags: []hyperv1.AWSResourceTag{{Key: "version", Value: "new"}}, // New tags
					}},
				},
			},
			existingTemplate: defaultAWSMachineTemplate(func(template *capiaws.AWSMachineTemplate) {
				template.Name = "old-name-from-spec-with-old-tags"
				template.Spec.Template.Spec.InstanceType = "t3.large"
				template.Spec.Template.Spec.AdditionalTags = capiaws.Tags{"version": "old"} // Old tags
			}),
			expectedName: "old-name-from-spec-with-old-tags", // Crucial: Expect the old name to be reused.
			expectedTags: capiaws.Tags{"version": "new"},
		},
		{
			name: "should create a new template name when instanceType changes",
			nodePool: &hyperv1.NodePool{ // Desired state has a new instance type.
				ObjectMeta: metav1.ObjectMeta{Name: "test-nodepool-structural"},
				Spec: hyperv1.NodePoolSpec{
					Management: hyperv1.NodePoolManagement{UpgradeType: hyperv1.UpgradeTypeReplace},
					Platform: hyperv1.NodePoolPlatform{AWS: &hyperv1.AWSNodePoolPlatform{
						AMI:          amiName,
						InstanceType: "m5.xlarge", // Structural change
						ResourceTags: []hyperv1.AWSResourceTag{{Key: "version", Value: "new"}},
					}},
				},
			},
			existingTemplate: defaultAWSMachineTemplate(func(template *capiaws.AWSMachineTemplate) {
				template.Name = "name-with-t3-large"
				template.Spec.Template.Spec.InstanceType = "t3.large"
			}),
			expectedName: func() string {
				// The new name is based on a spec with the new instance type and no tags.
				template := defaultAWSMachineTemplate(func(template *capiaws.AWSMachineTemplate) {
					template.Spec.Template.Spec.InstanceType = "m5.xlarge"
					template.Spec.Template.Spec.AdditionalTags = nil
				})
				name, _ := mockTemplateNameGenerator(template.Spec)
				return name
			}(),
			expectedTags: capiaws.Tags{"version": "new"},
		},
		{
			name: "should create new template when none exists",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{Name: "new-nodepool"},
				Spec: hyperv1.NodePoolSpec{
					Management: hyperv1.NodePoolManagement{UpgradeType: hyperv1.UpgradeTypeReplace},
					Platform: hyperv1.NodePoolPlatform{AWS: &hyperv1.AWSNodePoolPlatform{
						AMI:          amiName,
						InstanceType: "t3.medium",
						ResourceTags: []hyperv1.AWSResourceTag{{Key: "app", Value: "new"}},
					}},
				},
			},
			existingTemplate: nil, // No existing resources
			expectedName: func() string {
				template := defaultAWSMachineTemplate(func(template *capiaws.AWSMachineTemplate) {
					template.Spec.Template.Spec.InstanceType = "t3.medium"
					template.Spec.Template.Spec.AdditionalTags = nil
				})
				name, _ := mockTemplateNameGenerator(template.Spec)

				return name
			}(),
			expectedTags: capiaws.Tags{"app": "new"},
		},

		{
			name: "should find template via MachineSet when UpgradeType is InPlace",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{Name: "inplace-nodepool"},
				Spec: hyperv1.NodePoolSpec{
					Management: hyperv1.NodePoolManagement{UpgradeType: hyperv1.UpgradeTypeInPlace}, // Different UpgradeType
					Platform: hyperv1.NodePoolPlatform{AWS: &hyperv1.AWSNodePoolPlatform{
						AMI:          amiName,
						InstanceType: "t3.large",
						ResourceTags: []hyperv1.AWSResourceTag{{Key: "version", Value: "new"}},
					}},
				},
			},

			existingTemplate: defaultAWSMachineTemplate(func(template *capiaws.AWSMachineTemplate) {
				template.Name = "inplace-template-name"
				template.Spec.Template.Spec.InstanceType = "t3.large"
				template.Spec.Template.Spec.AdditionalTags = capiaws.Tags{"version": "old"}
			}),
			expectedName: "inplace-template-name", // Expect reuse
			expectedTags: capiaws.Tags{"version": "new"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup the fake client for this specific test case
			var existingObjs []client.Object
			if tc.existingTemplate != nil {
				tc.existingTemplate.Namespace = namespace
				existingObjs = append(existingObjs, tc.existingTemplate)

				// Automatically create the corresponding MachineDeployment or MachineSet.
				if tc.nodePool.Spec.Management.UpgradeType == hyperv1.UpgradeTypeReplace {
					md := &capiv1.MachineDeployment{
						ObjectMeta: metav1.ObjectMeta{Name: tc.nodePool.GetName(), Namespace: namespace},
						Spec: capiv1.MachineDeploymentSpec{Template: capiv1.MachineTemplateSpec{Spec: capiv1.MachineSpec{
							InfrastructureRef: corev1.ObjectReference{Name: tc.existingTemplate.Name},
						}}},
					}
					existingObjs = append(existingObjs, md)
				} else { // Handle InPlace (MachineSet)
					ms := &capiv1.MachineSet{
						ObjectMeta: metav1.ObjectMeta{Name: tc.nodePool.GetName(), Namespace: namespace},
						Spec: capiv1.MachineSetSpec{Template: capiv1.MachineTemplateSpec{Spec: capiv1.MachineSpec{
							InfrastructureRef: corev1.ObjectReference{Name: tc.existingTemplate.Name},
						}}},
					}
					existingObjs = append(existingObjs, ms)
				}
			}
			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(existingObjs...).Build()

			hc := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Namespace: namespace},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						AWS: &hyperv1.AWSPlatformSpec{},
					},
				},
				Status: hyperv1.HostedClusterStatus{Platform: &hyperv1.PlatformStatus{AWS: &hyperv1.AWSPlatformStatus{DefaultWorkerSecurityGroupID: "default"}}},
			}

			c := &CAPI{
				Token: &Token{
					ConfigGenerator: &ConfigGenerator{
						Client:                fakeClient,
						nodePool:              tc.nodePool,
						controlplaneNamespace: namespace,
						hostedCluster:         hc,
						rolloutConfig: &rolloutConfig{
							releaseImage: &releaseinfo.ReleaseImage{},
						},
					},
					cpoCapabilities: &CPOCapabilities{
						CreateDefaultAWSSecurityGroup: true,
					},
				},
				capiClusterName: infraName,
			}

			// Execute the function under test
			result, err := c.awsMachineTemplate(t.Context(), mockTemplateNameGenerator)

			// Assertions
			if err != nil {
				t.Fatalf("Function returned an unexpected error: %v", err)
			}
			if result.Name != tc.expectedName {
				t.Errorf("Wrong template name: \n- got:  %s\n- want: %s", result.Name, tc.expectedName)
			}

			// always add the cluster tag
			tc.expectedTags[awsClusterCloudProviderTagKey(infraName)] = infraLifecycleOwned

			if diff := cmp.Diff(tc.expectedTags, result.Spec.Template.Spec.AdditionalTags); diff != "" {
				t.Errorf("Final spec has incorrect tags (-want +got):\n%s", diff)
			}
		})
	}
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
									ID: &capacityReservationID,
								},
							},
						},
					},
				},
			}

			reconciler := &NodePoolReconciler{
				Client: fakeClient,
			}
			err := reconciler.validateAWSPlatformConfig(t.Context(), nodePool, hostedcluster, tc.oldCondition)
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

func TestGetWindowsAMI(t *testing.T) {
	testCases := []struct {
		name          string
		region        string
		arch          string
		releaseImage  *releaseinfo.ReleaseImage
		expectedAMI   string
		expectedError string
	}{
		{
			name:          "nil release image",
			region:        "us-east-1",
			arch:          hyperv1.ArchitectureAMD64,
			releaseImage:  nil,
			expectedError: "release image is nil",
		},
		{
			name:   "nil stream metadata",
			region: "us-east-1",
			arch:   hyperv1.ArchitectureAMD64,
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &v1.ImageStream{
					ObjectMeta: metav1.ObjectMeta{
						Name: "4.17.0",
					},
				},
				StreamMetadata: nil,
			},
			expectedError: "release image stream metadata is nil",
		},
		{
			name:   "architecture not found",
			region: "us-east-1",
			arch:   hyperv1.ArchitectureAMD64,
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &v1.ImageStream{
					ObjectMeta: metav1.ObjectMeta{
						Name: "4.17.0",
					},
				},
				StreamMetadata: &releaseinfo.CoreOSStreamMetadata{
					Architectures: map[string]releaseinfo.CoreOSArchitecture{},
				},
			},
			expectedError: "couldn't find OS metadata for architecture \"amd64\"",
		},
		{
			name:   "no aws-winli regions data",
			region: "us-east-1",
			arch:   hyperv1.ArchitectureAMD64,
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &v1.ImageStream{
					ObjectMeta: metav1.ObjectMeta{
						Name: "4.17.0",
					},
				},
				StreamMetadata: &releaseinfo.CoreOSStreamMetadata{
					Architectures: map[string]releaseinfo.CoreOSArchitecture{
						"x86_64": {
							RHCOS: releaseinfo.CoreRHCOSImage{
								AWSWinLi: releaseinfo.CoreAWSWinLi{
									Regions: nil,
								},
							},
						},
					},
				},
			},
			expectedError: "no aws-winli regions data found in release image metadata",
		},
		{
			name:   "unsupported region",
			region: "unsupported-region",
			arch:   hyperv1.ArchitectureAMD64,
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &v1.ImageStream{
					ObjectMeta: metav1.ObjectMeta{
						Name: "4.17.0",
					},
				},
				StreamMetadata: &releaseinfo.CoreOSStreamMetadata{
					Architectures: map[string]releaseinfo.CoreOSArchitecture{
						"x86_64": {
							RHCOS: releaseinfo.CoreRHCOSImage{
								AWSWinLi: releaseinfo.CoreAWSWinLi{
									Regions: map[string]releaseinfo.CoreAWSWinLiRegion{
										"us-east-1": {
											Release: "418.94.202410090804-0",
											Image:   "ami-testimage",
										},
									},
								},
							},
						},
					},
				},
			},
			expectedError: "no Windows AMI found for region unsupported-region in release image metadata",
		},
		{
			name:   "empty AMI image",
			region: "us-east-1",
			arch:   hyperv1.ArchitectureAMD64,
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &v1.ImageStream{
					ObjectMeta: metav1.ObjectMeta{
						Name: "4.17.0",
					},
				},
				StreamMetadata: &releaseinfo.CoreOSStreamMetadata{
					Architectures: map[string]releaseinfo.CoreOSArchitecture{
						"x86_64": {
							RHCOS: releaseinfo.CoreRHCOSImage{
								AWSWinLi: releaseinfo.CoreAWSWinLi{
									Regions: map[string]releaseinfo.CoreAWSWinLiRegion{
										"us-east-1": {
											Release: "418.94.202410090804-0",
											Image:   "",
										},
									},
								},
							},
						},
					},
				},
			},
			expectedError: "windows AMI image is empty for region us-east-1 in release image metadata",
		},
		{
			name:   "successful Windows AMI lookup",
			region: "us-east-1",
			arch:   hyperv1.ArchitectureAMD64,
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &v1.ImageStream{
					ObjectMeta: metav1.ObjectMeta{
						Name: "4.17.0",
					},
				},
				StreamMetadata: &releaseinfo.CoreOSStreamMetadata{
					Architectures: map[string]releaseinfo.CoreOSArchitecture{
						"x86_64": {
							RHCOS: releaseinfo.CoreRHCOSImage{
								AWSWinLi: releaseinfo.CoreAWSWinLi{
									Regions: map[string]releaseinfo.CoreAWSWinLiRegion{
										"us-east-1": {
											Release: "418.94.202410090804-0",
											Image:   "ami-0abcdef1234567890",
										},
										"eu-west-1": {
											Release: "418.94.202410090804-0",
											Image:   "ami-0123456789abcdef0",
										},
									},
								},
							},
						},
					},
				},
			},
			expectedAMI: "ami-0abcdef1234567890",
		},
		{
			name:   "successful Windows AMI lookup for different region",
			region: "eu-west-1",
			arch:   hyperv1.ArchitectureAMD64,
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &v1.ImageStream{
					ObjectMeta: metav1.ObjectMeta{
						Name: "4.17.0",
					},
				},
				StreamMetadata: &releaseinfo.CoreOSStreamMetadata{
					Architectures: map[string]releaseinfo.CoreOSArchitecture{
						"x86_64": {
							RHCOS: releaseinfo.CoreRHCOSImage{
								AWSWinLi: releaseinfo.CoreAWSWinLi{
									Regions: map[string]releaseinfo.CoreAWSWinLiRegion{
										"us-east-1": {
											Release: "418.94.202410090804-0",
											Image:   "ami-0abcdef1234567890",
										},
										"eu-west-1": {
											Release: "418.94.202410090804-0",
											Image:   "ami-0123456789abcdef0",
										},
									},
								},
							},
						},
					},
				},
			},
			expectedAMI: "ami-0123456789abcdef0",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ami, err := getWindowsAMI(tc.region, tc.arch, tc.releaseImage)

			if tc.expectedError != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, but got nil", tc.expectedError)
				}
				if !strings.Contains(err.Error(), tc.expectedError) {
					t.Fatalf("expected error containing %q, but got %q", tc.expectedError, err.Error())
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if ami != tc.expectedAMI {
					t.Fatalf("expected AMI %q, but got %q", tc.expectedAMI, ami)
				}
			}
		})
	}
}

func TestIsSpotEnabled(t *testing.T) {
	testCases := []struct {
		name     string
		nodePool *hyperv1.NodePool
		expected bool
	}{
		{
			name:     "When nodePool is nil, it should return false",
			nodePool: nil,
			expected: false,
		},
		{
			name: "When no spot config and no annotation, it should return false",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						AWS: &hyperv1.AWSNodePoolPlatform{},
					},
				},
			},
			expected: false,
		},
		{
			name: "When annotation is present, it should return true",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						AnnotationEnableSpot: "true",
					},
				},
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						AWS: &hyperv1.AWSNodePoolPlatform{},
					},
				},
			},
			expected: true,
		},
		{
			name: "When API marketType is Spot, it should return true",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						AWS: &hyperv1.AWSNodePoolPlatform{
							Placement: &hyperv1.PlacementOptions{
								MarketType: hyperv1.MarketTypeSpot,
								Spot:       hyperv1.SpotOptions{},
							},
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "When API marketType is Spot with MaxPrice, it should return true",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						AWS: &hyperv1.AWSNodePoolPlatform{
							Placement: &hyperv1.PlacementOptions{
								MarketType: hyperv1.MarketTypeSpot,
								Spot: hyperv1.SpotOptions{
									MaxPrice: "0.50",
								},
							},
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "When both annotation and API marketType Spot are present, it should return true",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						AnnotationEnableSpot: "true",
					},
				},
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						AWS: &hyperv1.AWSNodePoolPlatform{
							Placement: &hyperv1.PlacementOptions{
								MarketType: hyperv1.MarketTypeSpot,
								Spot:       hyperv1.SpotOptions{},
							},
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "When AWS platform is nil, it should return false",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{},
				},
			},
			expected: false,
		},
		{
			name: "When Placement is nil, it should return false",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						AWS: &hyperv1.AWSNodePoolPlatform{
							Placement: nil,
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "When marketType is OnDemand, it should return false",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						AWS: &hyperv1.AWSNodePoolPlatform{
							Placement: &hyperv1.PlacementOptions{
								MarketType: hyperv1.MarketTypeOnDemand,
							},
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "When marketType is CapacityBlocks, it should return false",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						AWS: &hyperv1.AWSNodePoolPlatform{
							Placement: &hyperv1.PlacementOptions{
								MarketType: hyperv1.MarketTypeCapacityBlock,
							},
						},
					},
				},
			},
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := isSpotEnabled(tc.nodePool)
			if result != tc.expected {
				t.Errorf("expected %v, got %v", tc.expected, result)
			}
		})
	}
}

func TestResolveAWSAMI(t *testing.T) {
	releaseImageWithMetadata := &releaseinfo.ReleaseImage{
		ImageStream: &v1.ImageStream{
			ObjectMeta: metav1.ObjectMeta{Name: "4.17.0"},
		},
		StreamMetadata: &releaseinfo.CoreOSStreamMetadata{
			Architectures: map[string]releaseinfo.CoreOSArchitecture{
				"x86_64": {
					RHCOS: releaseinfo.CoreRHCOSImage{
						AWSWinLi: releaseinfo.CoreAWSWinLi{
							Regions: map[string]releaseinfo.CoreAWSWinLiRegion{
								"us-east-1": {
									Release: "418.94.202410090804-0",
									Image:   "ami-windows-us-east-1",
								},
							},
						},
					},
				},
			},
		},
	}

	testCases := []struct {
		name          string
		hostedCluster *hyperv1.HostedCluster
		nodePool      *hyperv1.NodePool
		releaseImage  *releaseinfo.ReleaseImage
		expectedAMI   string
		expectError   bool
	}{
		{
			name: "When nodePool has explicit AMI, it should return that AMI directly",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{AWS: &hyperv1.AWSPlatformSpec{Region: "us-east-1"}},
				},
			},
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{AWS: &hyperv1.AWSNodePoolPlatform{AMI: "ami-explicit"}},
				},
			},
			releaseImage: releaseImageWithMetadata,
			expectedAMI:  "ami-explicit",
		},
		{
			name: "When nodePool has Windows ImageType, it should resolve Windows AMI from metadata",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{AWS: &hyperv1.AWSPlatformSpec{Region: "us-east-1"}},
				},
			},
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Arch:     hyperv1.ArchitectureAMD64,
					Platform: hyperv1.NodePoolPlatform{AWS: &hyperv1.AWSNodePoolPlatform{ImageType: hyperv1.ImageTypeWindows}},
				},
			},
			releaseImage: releaseImageWithMetadata,
			expectedAMI:  "ami-windows-us-east-1",
		},
		{
			name: "When nodePool has Windows ImageType with unsupported region, it should return error",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{AWS: &hyperv1.AWSPlatformSpec{Region: "ap-southeast-99"}},
				},
			},
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Arch:     hyperv1.ArchitectureAMD64,
					Platform: hyperv1.NodePoolPlatform{AWS: &hyperv1.AWSNodePoolPlatform{ImageType: hyperv1.ImageTypeWindows}},
				},
			},
			releaseImage: releaseImageWithMetadata,
			expectError:  true,
		},
		{
			name: "When nodePool has no AMI and default Linux type with nil stream metadata, it should return error",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{AWS: &hyperv1.AWSPlatformSpec{Region: "us-east-1"}},
				},
			},
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Arch:     hyperv1.ArchitectureAMD64,
					Platform: hyperv1.NodePoolPlatform{AWS: &hyperv1.AWSNodePoolPlatform{}},
				},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream:    &v1.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "4.17.0"}},
				StreamMetadata: nil,
			},
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			ami, err := resolveAWSAMI(tc.hostedCluster, tc.nodePool, tc.releaseImage)
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(ami).To(Equal(tc.expectedAMI))
			}
		})
	}
}

func TestBuildAWSSubnet(t *testing.T) {
	testCases := []struct {
		name           string
		nodePool       *hyperv1.NodePool
		expectedSubnet *capiaws.AWSResourceReference
	}{
		{
			name: "When subnet has only ID, it should return subnet with ID set",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						AWS: &hyperv1.AWSNodePoolPlatform{
							Subnet: hyperv1.AWSResourceReference{
								ID: ptr.To("subnet-abc123"),
							},
						},
					},
				},
			},
			expectedSubnet: &capiaws.AWSResourceReference{
				ID: ptr.To("subnet-abc123"),
			},
		},
		{
			name: "When subnet has filters, it should copy filters to CAPI format",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						AWS: &hyperv1.AWSNodePoolPlatform{
							Subnet: hyperv1.AWSResourceReference{
								Filters: []hyperv1.Filter{
									{Name: "tag:Name", Values: []string{"my-subnet"}},
									{Name: "vpc-id", Values: []string{"vpc-123"}},
								},
							},
						},
					},
				},
			},
			expectedSubnet: &capiaws.AWSResourceReference{
				Filters: []capiaws.Filter{
					{Name: "tag:Name", Values: []string{"my-subnet"}},
					{Name: "vpc-id", Values: []string{"vpc-123"}},
				},
			},
		},
		{
			name: "When subnet has no ID and no filters, it should return empty subnet reference",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						AWS: &hyperv1.AWSNodePoolPlatform{
							Subnet: hyperv1.AWSResourceReference{},
						},
					},
				},
			},
			expectedSubnet: &capiaws.AWSResourceReference{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			subnet := buildAWSSubnet(tc.nodePool)
			g.Expect(subnet).To(Equal(tc.expectedSubnet))
		})
	}
}

func TestBuildAWSRootVolume(t *testing.T) {
	testCases := []struct {
		name           string
		nodePool       *hyperv1.NodePool
		expectedVolume *capiaws.Volume
	}{
		{
			name: "When RootVolume is nil, it should return default volume with default size",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						AWS: &hyperv1.AWSNodePoolPlatform{
							RootVolume: nil,
						},
					},
				},
			},
			expectedVolume: &capiaws.Volume{
				Size: EC2VolumeDefaultSize,
			},
		},
		{
			name: "When RootVolume has custom type and size, it should use them",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						AWS: &hyperv1.AWSNodePoolPlatform{
							RootVolume: &hyperv1.Volume{
								Type: "io1",
								Size: 100,
								IOPS: 5000,
							},
						},
					},
				},
			},
			expectedVolume: &capiaws.Volume{
				Type: capiaws.VolumeType("io1"),
				Size: 100,
				IOPS: 5000,
			},
		},
		{
			name: "When RootVolume has empty type, it should use default type",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						AWS: &hyperv1.AWSNodePoolPlatform{
							RootVolume: &hyperv1.Volume{
								Type: "",
								Size: 50,
							},
						},
					},
				},
			},
			expectedVolume: &capiaws.Volume{
				Type: capiaws.VolumeType(EC2VolumeDefaultType),
				Size: 50,
			},
		},
		{
			name: "When RootVolume has zero size, it should keep default size",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						AWS: &hyperv1.AWSNodePoolPlatform{
							RootVolume: &hyperv1.Volume{
								Type: "gp3",
								Size: 0,
							},
						},
					},
				},
			},
			expectedVolume: &capiaws.Volume{
				Type: capiaws.VolumeType("gp3"),
				Size: EC2VolumeDefaultSize,
			},
		},
		{
			name: "When RootVolume has encryption settings, it should propagate them",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						AWS: &hyperv1.AWSNodePoolPlatform{
							RootVolume: &hyperv1.Volume{
								Type:          "gp3",
								Size:          64,
								Encrypted:     ptr.To(true),
								EncryptionKey: "arn:aws:kms:us-east-1:123:key/abc",
							},
						},
					},
				},
			},
			expectedVolume: &capiaws.Volume{
				Type:          capiaws.VolumeType("gp3"),
				Size:          64,
				Encrypted:     ptr.To(true),
				EncryptionKey: "arn:aws:kms:us-east-1:123:key/abc",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			volume := buildAWSRootVolume(tc.nodePool)
			g.Expect(volume).To(Equal(tc.expectedVolume))
		})
	}
}

func TestBuildAWSSecurityGroups(t *testing.T) {
	testCases := []struct {
		name           string
		nodePool       *hyperv1.NodePool
		hostedCluster  *hyperv1.HostedCluster
		defaultSG      bool
		expectedSGs    []capiaws.AWSResourceReference
		expectError    bool
		expectNotReady bool
	}{
		{
			name: "When nodePool has security groups and defaultSG is true, it should include both",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						AWS: &hyperv1.AWSNodePoolPlatform{
							SecurityGroups: []hyperv1.AWSResourceReference{
								{ID: ptr.To("sg-custom")},
							},
						},
					},
				},
			},
			hostedCluster: &hyperv1.HostedCluster{
				Status: hyperv1.HostedClusterStatus{
					Platform: &hyperv1.PlatformStatus{
						AWS: &hyperv1.AWSPlatformStatus{
							DefaultWorkerSecurityGroupID: "sg-default",
						},
					},
				},
			},
			defaultSG: true,
			expectedSGs: []capiaws.AWSResourceReference{
				{ID: ptr.To("sg-custom")},
				{ID: ptr.To("sg-default")},
			},
		},
		{
			name: "When nodePool has no security groups and defaultSG is true, it should use only default",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						AWS: &hyperv1.AWSNodePoolPlatform{},
					},
				},
			},
			hostedCluster: &hyperv1.HostedCluster{
				Status: hyperv1.HostedClusterStatus{
					Platform: &hyperv1.PlatformStatus{
						AWS: &hyperv1.AWSPlatformStatus{
							DefaultWorkerSecurityGroupID: "sg-default",
						},
					},
				},
			},
			defaultSG: true,
			expectedSGs: []capiaws.AWSResourceReference{
				{ID: ptr.To("sg-default")},
			},
		},
		{
			name: "When defaultSG is true but no default SG available, it should return NotReadyError",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						AWS: &hyperv1.AWSNodePoolPlatform{},
					},
				},
			},
			hostedCluster: &hyperv1.HostedCluster{
				Status: hyperv1.HostedClusterStatus{
					Platform: &hyperv1.PlatformStatus{
						AWS: &hyperv1.AWSPlatformStatus{
							DefaultWorkerSecurityGroupID: "",
						},
					},
				},
			},
			defaultSG:      true,
			expectError:    true,
			expectNotReady: true,
		},
		{
			name: "When defaultSG is false, it should only return nodePool security groups",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						AWS: &hyperv1.AWSNodePoolPlatform{
							SecurityGroups: []hyperv1.AWSResourceReference{
								{ID: ptr.To("sg-1")},
								{ID: ptr.To("sg-2")},
							},
						},
					},
				},
			},
			hostedCluster: &hyperv1.HostedCluster{},
			defaultSG:     false,
			expectedSGs: []capiaws.AWSResourceReference{
				{ID: ptr.To("sg-1")},
				{ID: ptr.To("sg-2")},
			},
		},
		{
			name: "When security group has filters, it should copy filters to CAPI format",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						AWS: &hyperv1.AWSNodePoolPlatform{
							SecurityGroups: []hyperv1.AWSResourceReference{
								{
									Filters: []hyperv1.Filter{
										{Name: "tag:Role", Values: []string{"worker"}},
									},
								},
							},
						},
					},
				},
			},
			hostedCluster: &hyperv1.HostedCluster{},
			defaultSG:     false,
			expectedSGs: []capiaws.AWSResourceReference{
				{
					Filters: []capiaws.Filter{
						{Name: "tag:Role", Values: []string{"worker"}},
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			sgs, err := buildAWSSecurityGroups(tc.nodePool, tc.hostedCluster, tc.defaultSG)
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
				if tc.expectNotReady {
					var notReadyErr *NotReadyError
					g.Expect(errors.As(err, &notReadyErr)).To(BeTrue())
				}
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(sgs).To(Equal(tc.expectedSGs))
			}
		})
	}
}

func TestApplyAWSPlacementOptions(t *testing.T) {
	capacityReservationID := "cr-0123456789abcdef0"

	testCases := []struct {
		name                             string
		nodePool                         *hyperv1.NodePool
		expectedSpotMarketOptions        *capiaws.SpotMarketOptions
		expectedMarketType               capiaws.MarketType
		expectedTenancy                  string
		expectedCapacityReservationID    *string
		expectedCapReservationPreference capiaws.CapacityReservationPreference
	}{
		{
			name: "When placement is nil, it should not modify spec",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						AWS: &hyperv1.AWSNodePoolPlatform{
							Placement: nil,
						},
					},
				},
			},
		},
		{
			name: "When marketType is Spot with no MaxPrice, it should set empty SpotMarketOptions",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						AWS: &hyperv1.AWSNodePoolPlatform{
							Placement: &hyperv1.PlacementOptions{
								MarketType: hyperv1.MarketTypeSpot,
								Spot:       hyperv1.SpotOptions{},
							},
						},
					},
				},
			},
			expectedSpotMarketOptions: &capiaws.SpotMarketOptions{},
		},
		{
			name: "When marketType is Spot with MaxPrice, it should set SpotMarketOptions with MaxPrice",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						AWS: &hyperv1.AWSNodePoolPlatform{
							Placement: &hyperv1.PlacementOptions{
								MarketType: hyperv1.MarketTypeSpot,
								Spot: hyperv1.SpotOptions{
									MaxPrice: "1.50",
								},
							},
						},
					},
				},
			},
			expectedSpotMarketOptions: &capiaws.SpotMarketOptions{
				MaxPrice: ptr.To("1.50"),
			},
		},
		{
			name: "When marketType is CapacityBlock, it should set MarketType to CapacityBlock",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						AWS: &hyperv1.AWSNodePoolPlatform{
							Placement: &hyperv1.PlacementOptions{
								MarketType: hyperv1.MarketTypeCapacityBlock,
							},
						},
					},
				},
			},
			expectedMarketType: capiaws.MarketTypeCapacityBlock,
		},
		{
			name: "When marketType is OnDemand, it should set MarketType to OnDemand",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						AWS: &hyperv1.AWSNodePoolPlatform{
							Placement: &hyperv1.PlacementOptions{
								MarketType: hyperv1.MarketTypeOnDemand,
							},
						},
					},
				},
			},
			expectedMarketType: capiaws.MarketTypeOnDemand,
		},
		{
			name: "When tenancy is dedicated, it should set tenancy on spec",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						AWS: &hyperv1.AWSNodePoolPlatform{
							Placement: &hyperv1.PlacementOptions{
								Tenancy: "dedicated",
							},
						},
					},
				},
			},
			expectedTenancy: "dedicated",
		},
		{
			name: "When capacityReservation has ID and preference, it should set both on spec",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						AWS: &hyperv1.AWSNodePoolPlatform{
							Placement: &hyperv1.PlacementOptions{
								CapacityReservation: &hyperv1.CapacityReservationOptions{
									ID:         &capacityReservationID,
									Preference: hyperv1.CapacityReservationPreferenceOnly,
								},
							},
						},
					},
				},
			},
			expectedCapacityReservationID:    &capacityReservationID,
			expectedCapReservationPreference: capiaws.CapacityReservationPreference(hyperv1.CapacityReservationPreferenceOnly),
			expectedMarketType:               capiaws.MarketTypeCapacityBlock,
		},
		{
			name: "When deprecated capacityReservation.MarketType is CapacityBlock and no top-level marketType, it should use deprecated value",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						AWS: &hyperv1.AWSNodePoolPlatform{
							Placement: &hyperv1.PlacementOptions{
								CapacityReservation: &hyperv1.CapacityReservationOptions{
									MarketType: hyperv1.MarketTypeCapacityBlock,
								},
							},
						},
					},
				},
			},
			expectedMarketType: capiaws.MarketTypeCapacityBlock,
		},
		{
			name: "When tenancy is host with capacityReservation ID but no marketType, it should not default to CapacityBlock",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						AWS: &hyperv1.AWSNodePoolPlatform{
							Placement: &hyperv1.PlacementOptions{
								Tenancy: "host",
								CapacityReservation: &hyperv1.CapacityReservationOptions{
									ID: &capacityReservationID,
								},
							},
						},
					},
				},
			},
			expectedTenancy:               "host",
			expectedMarketType:            "",
			expectedCapacityReservationID: &capacityReservationID,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			spec := &capiaws.AWSMachineTemplateSpec{}
			applyAWSPlacementOptions(tc.nodePool, spec)

			g.Expect(spec.Template.Spec.SpotMarketOptions).To(Equal(tc.expectedSpotMarketOptions))
			g.Expect(spec.Template.Spec.MarketType).To(Equal(tc.expectedMarketType))
			g.Expect(spec.Template.Spec.Tenancy).To(Equal(tc.expectedTenancy))
			g.Expect(spec.Template.Spec.CapacityReservationID).To(Equal(tc.expectedCapacityReservationID))
			g.Expect(spec.Template.Spec.CapacityReservationPreference).To(Equal(tc.expectedCapReservationPreference))
		})
	}
}
