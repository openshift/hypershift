package releaseinfo

import (
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/releaseinfo/fixtures"
)

func TestDeserializeImageStream(t *testing.T) {
	for _, imageStream := range [][]byte{fixtures.ImageReferencesJSON_4_8, fixtures.ImageReferencesJSON_4_10} {
		if _, err := DeserializeImageStream(imageStream); err != nil {
			t.Fatal(err)
		}
	}
}

func testConfigMap(dataFields map[string]string) []byte {
	cm := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test\ndata:\n"
	for k, v := range dataFields {
		cm += fmt.Sprintf("  %s: %s\n", k, v)
	}
	return []byte(cm)
}

func TestDeserializeImageMetadata(t *testing.T) {
	tests := []struct {
		name           string
		data           []byte
		expectHasArch  bool
		expectOSStream bool
		expectError    bool
	}{
		{
			name:          "When parsing a single-stream 4.8 ConfigMap it should populate Architectures and leave OSStreams nil",
			data:          fixtures.CoreOSBootImagesYAML_4_8,
			expectHasArch: true,
		},
		{
			name:          "When parsing a single-stream 4.10 ConfigMap it should populate Architectures and leave OSStreams nil",
			data:          fixtures.CoreOSBootImagesYAML_4_10,
			expectHasArch: true,
		},
		{
			name:           "When parsing a multi-stream 5.0 ConfigMap it should populate both Architectures and OSStreams",
			data:           fixtures.CoreOSBootImagesYAML_5_0,
			expectHasArch:  true,
			expectOSStream: true,
		},
		{
			name: "When ConfigMap has only streams key it should return nil default and populate OSStreams",
			data: testConfigMap(map[string]string{
				"streams": `'{"rhel-9":{"stream":"rhcos-4.21","architectures":{"x86_64":{"artifacts":{},"images":{}}}}}'`,
			}),
			expectHasArch:  false,
			expectOSStream: true,
		},
		{
			name:        "When ConfigMap is missing both stream and streams keys it should return an error",
			data:        testConfigMap(map[string]string{"releaseVersion": `"5.0.0"`}),
			expectError: true,
		},
		{
			name:        "When stream JSON is invalid it should return an error",
			data:        testConfigMap(map[string]string{"stream": `"not valid json {"`}),
			expectError: true,
		},
		{
			name:        "When streams JSON is invalid it should return an error",
			data:        testConfigMap(map[string]string{"streams": `"not valid json {"`}),
			expectError: true,
		},
		{
			name:        "When input is empty it should return an error",
			data:        []byte{},
			expectError: true,
		},
		{
			name:        "When input is not valid YAML it should return an error",
			data:        []byte(`{not yaml at all`),
			expectError: true,
		},
		{
			name:        "When streams map is empty it should return an error",
			data:        testConfigMap(map[string]string{"streams": `"{}"`}),
			expectError: true,
		},
		{
			name: "When streams is valid but stream JSON is invalid it should return an error",
			data: testConfigMap(map[string]string{
				"streams": `'{"rhel-9":{"stream":"rhcos-4.21","architectures":{"x86_64":{"artifacts":{},"images":{}}}}}'`,
				"stream":  `"not valid json {"`,
			}),
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			result, osStreams, err := DeserializeImageMetadata(tt.data)
			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).ToNot(HaveOccurred())

			if tt.expectHasArch {
				_, hasX86 := result.Architectures["x86_64"]
				g.Expect(hasX86).To(BeTrue())
			}

			if tt.expectOSStream {
				g.Expect(osStreams).ToNot(BeNil())
				g.Expect(len(osStreams)).To(BeNumerically(">", 0))
			} else {
				g.Expect(osStreams).To(BeNil())
			}
		})
	}
}

func TestDeserializeImageMetadataStreamsOnly(t *testing.T) {
	g := NewWithT(t)

	data := testConfigMap(map[string]string{
		"streams": `'{"rhel-9":{"stream":"rhcos-4.21","architectures":{"x86_64":{"artifacts":{},"images":{"aws":{"regions":{"us-east-1":{"release":"9.8","image":"ami-fallback"}}}}}}}}'`,
	})

	defaultStream, osStreams, err := DeserializeImageMetadata(data)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(osStreams).ToNot(BeNil())

	t.Run("When ConfigMap has only streams key it should return nil default stream", func(t *testing.T) {
		g := NewWithT(t)
		g.Expect(defaultStream).To(BeNil())
	})

	t.Run("When ConfigMap has only streams key consumers should use StreamForName to pick a stream", func(t *testing.T) {
		g := NewWithT(t)
		rhel9, ok := osStreams[StreamRHEL9]
		g.Expect(ok).To(BeTrue())
		ami := rhel9.Architectures["x86_64"].Images.Aws.Regions["us-east-1"].Image
		g.Expect(ami).To(Equal("ami-fallback"))
	})
}

func TestDeserializeImageMetadataMultiStreamContent(t *testing.T) {
	defaultStream, osStreams, err := DeserializeImageMetadata(fixtures.CoreOSBootImagesYAML_5_0)
	if err != nil {
		t.Fatalf("failed to parse 5.0 fixture: %v", err)
	}

	tests := []struct {
		name   string
		assert func(g Gomega)
	}{
		{
			name: "When parsing a 5.0 ConfigMap it should have rhel-9 and rhel-10 in OSStreams",
			assert: func(g Gomega) {
				g.Expect(osStreams).To(HaveKey("rhel-9"))
				g.Expect(osStreams).To(HaveKey("rhel-10"))
			},
		},
		{
			name: "When looking up rhel-9 stream it should have distinct AWS AMIs from rhel-10",
			assert: func(g Gomega) {
				rhel9AMI := osStreams["rhel-9"].Architectures["x86_64"].Images.Aws.Regions["us-east-1"].Image
				rhel10AMI := osStreams["rhel-10"].Architectures["x86_64"].Images.Aws.Regions["us-east-1"].Image
				g.Expect(rhel9AMI).To(Equal("ami-06a6b025350ff1e23"))
				g.Expect(rhel10AMI).To(Equal("ami-04b3d999e39d62c5b"))
				g.Expect(rhel9AMI).ToNot(Equal(rhel10AMI))
			},
		},
		{
			name: "When looking up rhel-9 stream it should have distinct GCP images from rhel-10",
			assert: func(g Gomega) {
				rhel9GCP := osStreams["rhel-9"].Architectures["x86_64"].Images.Gcp.Name
				rhel10GCP := osStreams["rhel-10"].Architectures["x86_64"].Images.Gcp.Name
				g.Expect(rhel9GCP).To(Equal("rhcos-9-8-20260403-0-gcp-x86-64"))
				g.Expect(rhel10GCP).To(Equal("rhcos-10-2-20260405-0-gcp-x86-64"))
			},
		},
		{
			name: "When looking up rhel-9 stream it should have distinct KubeVirt images from rhel-10",
			assert: func(g Gomega) {
				rhel9KV := osStreams["rhel-9"].Architectures["x86_64"].Images.KubeVirt.DigestRef
				rhel10KV := osStreams["rhel-10"].Architectures["x86_64"].Images.KubeVirt.DigestRef
				g.Expect(rhel9KV).ToNot(BeEmpty())
				g.Expect(rhel10KV).ToNot(BeEmpty())
				g.Expect(rhel9KV).ToNot(Equal(rhel10KV))
			},
		},
		{
			name: "When looking up streams both rhel-9 and rhel-10 should have ppc64le architecture",
			assert: func(g Gomega) {
				_, rhel9HasPPC := osStreams["rhel-9"].Architectures["ppc64le"]
				_, rhel10HasPPC := osStreams["rhel-10"].Architectures["ppc64le"]
				g.Expect(rhel9HasPPC).To(BeTrue())
				g.Expect(rhel10HasPPC).To(BeTrue())
			},
		},
		{
			name: "When looking up a non-existent stream it should not exist in OSStreams",
			assert: func(g Gomega) {
				_, exists := osStreams["rhel-8"]
				g.Expect(exists).To(BeFalse())
			},
		},
		{
			name: "When the default stream is parsed it should match the rhel-9 stream content",
			assert: func(g Gomega) {
				defaultAMI := defaultStream.Architectures["x86_64"].Images.Aws.Regions["us-east-1"].Image
				rhel9AMI := osStreams["rhel-9"].Architectures["x86_64"].Images.Aws.Regions["us-east-1"].Image
				g.Expect(defaultAMI).To(Equal(rhel9AMI))
			},
		},
		{
			name: "When looking up stream names it should report correct coreos stream identifiers",
			assert: func(g Gomega) {
				g.Expect(osStreams["rhel-9"].Stream).To(Equal("rhcos-4.21"))
				g.Expect(osStreams["rhel-10"].Stream).To(Equal("rhcos-4.22"))
			},
		},
		{
			name: "When looking up aarch64 architecture it should have distinct AMIs between streams",
			assert: func(g Gomega) {
				rhel9ARM := osStreams["rhel-9"].Architectures["aarch64"].Images.Aws.Regions["us-east-1"].Image
				rhel10ARM := osStreams["rhel-10"].Architectures["aarch64"].Images.Aws.Regions["us-east-1"].Image
				g.Expect(rhel9ARM).To(Equal("ami-0e73a95fb409d8abd"))
				g.Expect(rhel10ARM).To(Equal("ami-0d7237e6b04d9a9e1"))
				g.Expect(rhel9ARM).ToNot(Equal(rhel10ARM))
			},
		},
		{
			name: "When looking up Azure marketplace data rhel-10 should have no no-purchase-plan entries",
			assert: func(g Gomega) {
				rhel9Ext := osStreams["rhel-9"].Architectures["x86_64"].RHELCoreOSExtensions
				rhel10Ext := osStreams["rhel-10"].Architectures["x86_64"].RHELCoreOSExtensions
				g.Expect(rhel9Ext).ToNot(BeNil())
				g.Expect(rhel9Ext.Marketplace).ToNot(BeNil())
				g.Expect(rhel9Ext.Marketplace.Azure).ToNot(BeNil())
				g.Expect(rhel9Ext.Marketplace.Azure.NoPurchasePlan).ToNot(BeNil())
				g.Expect(rhel9Ext.Marketplace.Azure.NoPurchasePlan.Gen2).ToNot(BeNil())
				if rhel10Ext != nil && rhel10Ext.Marketplace != nil && rhel10Ext.Marketplace.Azure != nil && rhel10Ext.Marketplace.Azure.NoPurchasePlan != nil {
					g.Expect(rhel10Ext.Marketplace.Azure.NoPurchasePlan.Gen1).To(BeNil())
					g.Expect(rhel10Ext.Marketplace.Azure.NoPurchasePlan.Gen2).To(BeNil())
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.assert(NewWithT(t))
		})
	}
}
