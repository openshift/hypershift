package nodepool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/releaseinfo"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	capipowervs "sigs.k8s.io/cluster-api-provider-ibmcloud/api/powervs/v1beta2"

	"github.com/coreos/stream-metadata-go/stream"
)

func testPowerVSMachineTemplateCompatibility(t *testing.T) {
	t.Helper()
	for _, testCase := range []struct {
		name    string
		fixture string
		image   *hyperv1.PowerVSResourceReference
	}{
		{name: "When PowerVS uses a release image reference, it should retain baseline serialization and template hash", fixture: "powervs-v0.13.1-reference.json"},
		{name: "When PowerVS uses an explicit image and s1122, it should retain baseline serialization and template hash", fixture: "powervs-v0.13.1-image.json", image: &hyperv1.PowerVSResourceReference{ID: ptr.To("image-id")}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", testCase.fixture))
			if err != nil {
				t.Fatal(err)
			}
			var baseline struct {
				Name string          `json:"name"`
				Spec json.RawMessage `json:"spec"`
			}
			if err := json.Unmarshal(data, &baseline); err != nil {
				t.Fatal(err)
			}
			cluster := &hyperv1.HostedCluster{Spec: hyperv1.HostedClusterSpec{Platform: hyperv1.PlatformSpec{Type: hyperv1.PowerVSPlatform, PowerVS: &hyperv1.PowerVSPlatformSpec{Region: "us-south", ServiceInstanceID: "service", Subnet: &hyperv1.PowerVSResourceReference{}}}}}
			nodePool := &hyperv1.NodePool{ObjectMeta: metav1.ObjectMeta{Name: "workers"}, Spec: hyperv1.NodePoolSpec{Platform: hyperv1.NodePoolPlatform{Type: hyperv1.PowerVSPlatform, PowerVS: &hyperv1.PowerVSNodePoolPlatform{SystemType: "s1122", ProcessorType: hyperv1.PowerVSNodePoolSharedProcType, Processors: intstr.FromString("0.5"), MemoryGiB: 32, Image: testCase.image}}}}
			releaseImage := &releaseinfo.ReleaseImage{StreamMetadata: &stream.Stream{Architectures: map[string]stream.Arch{"ppc64le": {Images: stream.Images{PowerVS: &stream.ReplicatedObject{Regions: map[string]stream.SingleObject{"us-south": {Release: "rhcos-4.18"}}}}}}}}
			capi := &CAPI{Token: &Token{ConfigGenerator: &ConfigGenerator{hostedCluster: cluster, nodePool: nodePool, controlplaneNamespace: "clusters", rolloutConfig: &rolloutConfig{releaseImage: releaseImage}}}}
			object, err := capi.machineTemplateBuilders(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			template := object.(*capipowervs.IBMPowerVSMachineTemplate)
			actual, err := json.Marshal(template.Spec)
			if err != nil {
				t.Fatal(err)
			}
			if string(actual) != string(baseline.Spec) {
				t.Fatalf("serialized template changed: %s, expected %s", actual, baseline.Spec)
			}
			if template.Name != baseline.Name {
				t.Fatalf("rollout-causing name change: %s, expected %s", template.Name, baseline.Name)
			}
			var roundTrip capipowervs.IBMPowerVSMachineTemplateSpec
			if err := json.Unmarshal(baseline.Spec, &roundTrip); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(roundTrip)
			if err != nil || string(encoded) != string(baseline.Spec) {
				t.Fatalf("baseline round trip changed: %s: %v", encoded, err)
			}
		})
	}
}

func TestGetPowerVSImage(t *testing.T) {
	testCases := []struct {
		name            string
		region          string
		streamName      string
		releaseImage    *releaseinfo.ReleaseImage
		expectedError   string
		expectedRelease string
		expectedRegion  string
	}{
		{
			name:   "When PowerVS images is nil, it should return error",
			region: "us-south",
			releaseImage: &releaseinfo.ReleaseImage{
				StreamMetadata: &stream.Stream{
					Architectures: map[string]stream.Arch{
						"ppc64le": {
							Images: stream.Images{},
						},
					},
				},
			},
			expectedError: "release image metadata has no PowerVS images",
		},
		{
			name:   "When architecture is not found, it should return error",
			region: "us-south",
			releaseImage: &releaseinfo.ReleaseImage{
				StreamMetadata: &stream.Stream{
					Architectures: map[string]stream.Arch{},
				},
			},
			expectedError: "couldn't find OS metadata for architecture",
		},
		{
			name:       "When named stream is used with multi-stream ReleaseImage it should resolve from the named stream",
			region:     "us-south",
			streamName: "rhel-9",
			releaseImage: &releaseinfo.ReleaseImage{
				StreamMetadata: &stream.Stream{
					Architectures: map[string]stream.Arch{
						"ppc64le": {
							Images: stream.Images{
								PowerVS: &stream.ReplicatedObject{
									Regions: map[string]stream.SingleObject{
										"us-south": {
											Release: "default-4.18.0-ppc64le",
											Object:  "default-object",
											Bucket:  "default-bucket",
										},
									},
								},
							},
						},
					},
				},
				OSStreams: map[string]*stream.Stream{
					"rhel-9": {
						Architectures: map[string]stream.Arch{
							"ppc64le": {
								Images: stream.Images{
									PowerVS: &stream.ReplicatedObject{
										Regions: map[string]stream.SingleObject{
											"us-south": {
												Release: "rhel9-4.18.0-ppc64le",
												Object:  "rhel9-object",
												Bucket:  "rhel9-bucket",
											},
										},
									},
								},
							},
						},
					},
				},
			},
			expectedRelease: "rhel9-4-18-0-ppc64le",
			expectedRegion:  "us-south",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			img, cosRegion, err := getPowerVSImage(tc.region, tc.releaseImage, tc.streamName)
			if tc.expectedError != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tc.expectedError))
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(img).ToNot(BeNil())
			g.Expect(img.Release).To(Equal(tc.expectedRelease))
			g.Expect(cosRegion).To(Equal(tc.expectedRegion))
		})
	}
}
