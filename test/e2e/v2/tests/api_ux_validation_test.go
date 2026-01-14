//go:build e2ev2

/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package tests

import (
	"context"
	"embed"
	"fmt"
	"net"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/ipnet"
	"github.com/openshift/hypershift/support/assets"
	"github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	configv1 "github.com/openshift/api/config/v1"

	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

//go:embed assets
var content embed.FS

var _ = Describe("API UX Validation", Label("API"), func() {
	var (
		ctx        context.Context
		mgmtClient crclient.Client
	)

	BeforeEach(func() {
		testCtx := internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
		ctx = testCtx
		mgmtClient = testCtx.MgmtClient
	})

	Context("HostedCluster creation", Label("HostedCluster"), func() {
		Context("Capabilities validation", Label("Capabilities"), func() {
			It("should accept when capabilities.disabled is set to a supported capability", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Capabilities = &hyperv1.Capabilities{
						Disabled: []hyperv1.OptionalCapability{
							hyperv1.ImageRegistryCapability,
						},
					}
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should accept when capabilities.disabled is set to openshift-samples", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Capabilities = &hyperv1.Capabilities{
						Disabled: []hyperv1.OptionalCapability{
							hyperv1.OpenShiftSamplesCapability,
						},
					}
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should reject when capabilities.disabled is set to an invalid capability", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Capabilities = &hyperv1.Capabilities{
						Disabled: []hyperv1.OptionalCapability{
							hyperv1.OptionalCapability("AnInvalidCapability"),
						},
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("Unsupported value: \"AnInvalidCapability\": supported values: \"ImageRegistry\", \"openshift-samples\", \"Insights\", \"baremetal\""))
			})

			It("should reject when capabilities.disabled is set to an unsupported capability", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Capabilities = &hyperv1.Capabilities{
						Disabled: []hyperv1.OptionalCapability{
							hyperv1.OptionalCapability("Storage"),
						},
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("Unsupported value: \"Storage\": supported values: \"ImageRegistry\", \"openshift-samples\", \"Insights\", \"baremetal\""))
			})

			It("should accept when capabilities.enabled is set to a supported capability", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Capabilities = &hyperv1.Capabilities{
						Enabled: []hyperv1.OptionalCapability{
							hyperv1.BaremetalCapability,
						},
					}
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should reject when capabilities.enabled is set to an invalid capability", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Capabilities = &hyperv1.Capabilities{
						Enabled: []hyperv1.OptionalCapability{
							hyperv1.OptionalCapability("AnInvalidCapability"),
						},
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("Unsupported value: \"AnInvalidCapability\": supported values: \"ImageRegistry\", \"openshift-samples\", \"Insights\", \"baremetal\""))
			})

			It("should reject when capabilities.enabled is set to an unsupported capability", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Capabilities = &hyperv1.Capabilities{
						Enabled: []hyperv1.OptionalCapability{
							hyperv1.OptionalCapability("Storage"),
						},
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("Unsupported value: \"Storage\": supported values: \"ImageRegistry\", \"openshift-samples\", \"Insights\", \"baremetal\""))
			})

			It("should reject when the same capability is added to both enabled and disabled", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Capabilities = &hyperv1.Capabilities{
						Enabled: []hyperv1.OptionalCapability{
							hyperv1.OptionalCapability("Insights"),
						},
						Disabled: []hyperv1.OptionalCapability{
							hyperv1.OptionalCapability("Insights"),
						},
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("Capabilities can not be both enabled and disabled at once."))
			})

			It("should reject when Ingress capability is disabled but Console capability is enabled", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Capabilities = &hyperv1.Capabilities{
						Enabled: []hyperv1.OptionalCapability{
							hyperv1.ConsoleCapability,
						},
						Disabled: []hyperv1.OptionalCapability{
							hyperv1.IngressCapability,
						},
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("Ingress capability can only be disabled if Console capability is also disabled"))
			})

			It("should accept when both Ingress and Console capabilities are disabled", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Capabilities = &hyperv1.Capabilities{
						Disabled: []hyperv1.OptionalCapability{
							hyperv1.IngressCapability,
							hyperv1.ConsoleCapability,
						},
					}
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should accept when neither Ingress nor Console capability is disabled", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Capabilities = &hyperv1.Capabilities{
						Disabled: []hyperv1.OptionalCapability{
							hyperv1.ImageRegistryCapability,
						},
					}
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should accept when Ingress capability is enabled but Console capability is disabled", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Capabilities = &hyperv1.Capabilities{
						Enabled: []hyperv1.OptionalCapability{
							hyperv1.IngressCapability,
						},
						Disabled: []hyperv1.OptionalCapability{
							hyperv1.ConsoleCapability,
						},
					}
				})
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Context("DNS configuration", Label("DNS"), func() {
			It("should reject when baseDomain has invalid chars", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.DNS.BaseDomain = "@foo"
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("baseDomain must be a valid domain name (e.g., example, example.com, sub.example.com)"))
			})

			It("should accept when baseDomain is a valid hierarchical domain with two levels", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.DNS.BaseDomain = "foo.bar"
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should accept when baseDomain is a valid hierarchical domain with 3 levels", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.DNS.BaseDomain = "123.foo.bar"
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should accept when baseDomain is a single subdomain", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.DNS.BaseDomain = "foo"
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should accept when baseDomain is empty", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.DNS.BaseDomain = ""
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should reject when baseDomainPrefix has invalid chars", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.DNS.BaseDomainPrefix = ptr.To("@foo")
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("baseDomainPrefix must be a valid domain name (e.g., example, example.com, sub.example.com)"))
			})

			It("should accept when baseDomainPrefix is a valid hierarchical domain with two levels", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.DNS.BaseDomainPrefix = ptr.To("foo.bar")
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should accept when baseDomainPrefix is a valid hierarchical domain with 3 levels", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.DNS.BaseDomainPrefix = ptr.To("123.foo.bar")
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should accept when baseDomainPrefix is a single subdomain", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.DNS.BaseDomainPrefix = ptr.To("foo")
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should accept when baseDomainPrefix is empty", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.DNS.BaseDomainPrefix = ptr.To("")
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should accept when publicZoneID and privateZoneID are empty", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.DNS.PublicZoneID = ""
					hc.Spec.DNS.PrivateZoneID = ""
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should accept when publicZoneID and privateZoneID are set", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.DNS.PublicZoneID = "123"
					hc.Spec.DNS.PrivateZoneID = "123"
				})
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Context("GCP platform validation", Label("GCP", "Platform"), func() {
			BeforeEach(func() {
				hasGCPField, err := util.HasFieldInCRDSchema(ctx, mgmtClient, "hostedclusters.hypershift.openshift.io", "spec.platform.gcp")
				Expect(err).NotTo(HaveOccurred())
				if !hasGCPField {
					Skip("GCP platform field is not available in the HostedCluster CRD schema")
				}
			})
			It("should reject when GCP project ID has invalid format", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Platform.Type = hyperv1.GCPPlatform
					hc.Spec.Platform.GCP = &hyperv1.GCPPlatformSpec{
						Project: "My-Project",
						Region:  "us-central1",
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("project in body should match"))
			})

			It("should reject when GCP region has invalid format", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Platform.Type = hyperv1.GCPPlatform
					hc.Spec.Platform.GCP = &hyperv1.GCPPlatformSpec{
						Project: "my-project",
						Region:  "us-central",
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("region in body should match"))
			})

			It("should accept when GCP project and region are valid", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Platform.Type = hyperv1.GCPPlatform
					hc.Spec.Platform.GCP = &hyperv1.GCPPlatformSpec{
						Project: "my-project-123",
						Region:  "europe-west2",
						NetworkConfig: hyperv1.GCPNetworkConfig{
							Network:                     hyperv1.GCPResourceReference{Name: "my-network"},
							PrivateServiceConnectSubnet: hyperv1.GCPResourceReference{Name: "my-psc-subnet"},
						},
						WorkloadIdentity: hyperv1.GCPWorkloadIdentityConfig{
							ProjectNumber: "123456789012",
							PoolID:        "my-wif-pool",
							ProviderID:    "my-wif-provider",
							ServiceAccountsEmails: hyperv1.GCPServiceAccountsEmails{
								NodePool:        "nodepool@my-project-123.iam.gserviceaccount.com",
								ControlPlane:    "controlplane@my-project-123.iam.gserviceaccount.com",
								CloudController: "cloudcontroller@my-project-123.iam.gserviceaccount.com",
							},
						},
					}
				})
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Context("GCP Workload Identity Federation validation", Label("GCP", "WIF"), func() {
			BeforeEach(func() {
				hasGCPField, err := util.HasFieldInCRDSchema(ctx, mgmtClient, "hostedclusters.hypershift.openshift.io", "spec.platform.gcp")
				Expect(err).NotTo(HaveOccurred())
				if !hasGCPField {
					Skip("GCP platform field is not available in the HostedCluster CRD schema")
				}
			})

			It("should accept when all WIF fields are valid", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Platform.Type = hyperv1.GCPPlatform
					hc.Spec.Platform.GCP = &hyperv1.GCPPlatformSpec{
						Project: "my-project-123",
						Region:  "us-central1",
						NetworkConfig: hyperv1.GCPNetworkConfig{
							Network: hyperv1.GCPResourceReference{
								Name: "my-network",
							},
							PrivateServiceConnectSubnet: hyperv1.GCPResourceReference{
								Name: "my-psc-subnet",
							},
						},
						WorkloadIdentity: hyperv1.GCPWorkloadIdentityConfig{
							ProjectNumber: "123456789012",
							PoolID:        "my-wif-pool",
							ProviderID:    "my-wif-provider",
							ServiceAccountsEmails: hyperv1.GCPServiceAccountsEmails{
								NodePool:        "nodepool@my-project-123.iam.gserviceaccount.com",
								ControlPlane:    "controlplane@my-project-123.iam.gserviceaccount.com",
								CloudController: "cloudcontroller@my-project-123.iam.gserviceaccount.com",
							},
						},
					}
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should reject when WIF projectNumber has invalid format (not numeric)", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Platform.Type = hyperv1.GCPPlatform
					hc.Spec.Platform.GCP = &hyperv1.GCPPlatformSpec{
						Project: "my-project-123",
						Region:  "us-central1",
						NetworkConfig: hyperv1.GCPNetworkConfig{
							Network:                     hyperv1.GCPResourceReference{Name: "my-network"},
							PrivateServiceConnectSubnet: hyperv1.GCPResourceReference{Name: "my-psc-subnet"},
						},
						WorkloadIdentity: hyperv1.GCPWorkloadIdentityConfig{
							ProjectNumber: "abc123",
							PoolID:        "my-wif-pool",
							ProviderID:    "my-wif-provider",
							ServiceAccountsEmails: hyperv1.GCPServiceAccountsEmails{
								NodePool:        "nodepool@my-project-123.iam.gserviceaccount.com",
								ControlPlane:    "controlplane@my-project-123.iam.gserviceaccount.com",
								CloudController: "cloudcontroller@my-project-123.iam.gserviceaccount.com",
							},
						},
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("projectNumber in body should match"))
			})

			It("should reject when WIF poolID starts with reserved 'gcp-' prefix", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Platform.Type = hyperv1.GCPPlatform
					hc.Spec.Platform.GCP = &hyperv1.GCPPlatformSpec{
						Project: "my-project-123",
						Region:  "us-central1",
						NetworkConfig: hyperv1.GCPNetworkConfig{
							Network:                     hyperv1.GCPResourceReference{Name: "my-network"},
							PrivateServiceConnectSubnet: hyperv1.GCPResourceReference{Name: "my-psc-subnet"},
						},
						WorkloadIdentity: hyperv1.GCPWorkloadIdentityConfig{
							ProjectNumber: "123456789012",
							PoolID:        "gcp-reserved",
							ProviderID:    "my-wif-provider",
							ServiceAccountsEmails: hyperv1.GCPServiceAccountsEmails{
								NodePool:        "nodepool@my-project-123.iam.gserviceaccount.com",
								ControlPlane:    "controlplane@my-project-123.iam.gserviceaccount.com",
								CloudController: "cloudcontroller@my-project-123.iam.gserviceaccount.com",
							},
						},
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("Pool ID cannot start with reserved prefix 'gcp-'"))
			})

			It("should reject when WIF poolID is too short", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Platform.Type = hyperv1.GCPPlatform
					hc.Spec.Platform.GCP = &hyperv1.GCPPlatformSpec{
						Project: "my-project-123",
						Region:  "us-central1",
						NetworkConfig: hyperv1.GCPNetworkConfig{
							Network:                     hyperv1.GCPResourceReference{Name: "my-network"},
							PrivateServiceConnectSubnet: hyperv1.GCPResourceReference{Name: "my-psc-subnet"},
						},
						WorkloadIdentity: hyperv1.GCPWorkloadIdentityConfig{
							ProjectNumber: "123456789012",
							PoolID:        "abc",
							ProviderID:    "my-wif-provider",
							ServiceAccountsEmails: hyperv1.GCPServiceAccountsEmails{
								NodePool:        "nodepool@my-project-123.iam.gserviceaccount.com",
								ControlPlane:    "controlplane@my-project-123.iam.gserviceaccount.com",
								CloudController: "cloudcontroller@my-project-123.iam.gserviceaccount.com",
							},
						},
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("poolID in body should be at least 4 chars long"))
			})

			It("should reject when WIF providerID starts with reserved 'gcp-' prefix", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Platform.Type = hyperv1.GCPPlatform
					hc.Spec.Platform.GCP = &hyperv1.GCPPlatformSpec{
						Project: "my-project-123",
						Region:  "us-central1",
						NetworkConfig: hyperv1.GCPNetworkConfig{
							Network:                     hyperv1.GCPResourceReference{Name: "my-network"},
							PrivateServiceConnectSubnet: hyperv1.GCPResourceReference{Name: "my-psc-subnet"},
						},
						WorkloadIdentity: hyperv1.GCPWorkloadIdentityConfig{
							ProjectNumber: "123456789012",
							PoolID:        "my-wif-pool",
							ProviderID:    "gcp-reserved",
							ServiceAccountsEmails: hyperv1.GCPServiceAccountsEmails{
								NodePool:        "nodepool@my-project-123.iam.gserviceaccount.com",
								ControlPlane:    "controlplane@my-project-123.iam.gserviceaccount.com",
								CloudController: "cloudcontroller@my-project-123.iam.gserviceaccount.com",
							},
						},
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("Provider ID cannot start with reserved prefix 'gcp-'"))
			})

			It("should reject when NodePool service account has invalid email format", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Platform.Type = hyperv1.GCPPlatform
					hc.Spec.Platform.GCP = &hyperv1.GCPPlatformSpec{
						Project: "my-project-123",
						Region:  "us-central1",
						NetworkConfig: hyperv1.GCPNetworkConfig{
							Network:                     hyperv1.GCPResourceReference{Name: "my-network"},
							PrivateServiceConnectSubnet: hyperv1.GCPResourceReference{Name: "my-psc-subnet"},
						},
						WorkloadIdentity: hyperv1.GCPWorkloadIdentityConfig{
							ProjectNumber: "123456789012",
							PoolID:        "my-wif-pool",
							ProviderID:    "my-wif-provider",
							ServiceAccountsEmails: hyperv1.GCPServiceAccountsEmails{
								NodePool:        "invalid-email",
								ControlPlane:    "controlplane@my-project-123.iam.gserviceaccount.com",
								CloudController: "cloudcontroller@my-project-123.iam.gserviceaccount.com",
							},
						},
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("nodePool in body"))
			})

			It("should reject when ControlPlane service account has invalid email format", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Platform.Type = hyperv1.GCPPlatform
					hc.Spec.Platform.GCP = &hyperv1.GCPPlatformSpec{
						Project: "my-project-123",
						Region:  "us-central1",
						NetworkConfig: hyperv1.GCPNetworkConfig{
							Network:                     hyperv1.GCPResourceReference{Name: "my-network"},
							PrivateServiceConnectSubnet: hyperv1.GCPResourceReference{Name: "my-psc-subnet"},
						},
						WorkloadIdentity: hyperv1.GCPWorkloadIdentityConfig{
							ProjectNumber: "123456789012",
							PoolID:        "my-wif-pool",
							ProviderID:    "my-wif-provider",
							ServiceAccountsEmails: hyperv1.GCPServiceAccountsEmails{
								NodePool:        "nodepool@my-project-123.iam.gserviceaccount.com",
								ControlPlane:    "not-an-email@format",
								CloudController: "cloudcontroller@my-project-123.iam.gserviceaccount.com",
							},
						},
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("controlPlane in body"))
			})

			It("should reject when CloudController service account has invalid email format", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Platform.Type = hyperv1.GCPPlatform
					hc.Spec.Platform.GCP = &hyperv1.GCPPlatformSpec{
						Project: "my-project-123",
						Region:  "us-central1",
						NetworkConfig: hyperv1.GCPNetworkConfig{
							Network:                     hyperv1.GCPResourceReference{Name: "my-network"},
							PrivateServiceConnectSubnet: hyperv1.GCPResourceReference{Name: "my-psc-subnet"},
						},
						WorkloadIdentity: hyperv1.GCPWorkloadIdentityConfig{
							ProjectNumber: "123456789012",
							PoolID:        "my-wif-pool",
							ProviderID:    "my-wif-provider",
							ServiceAccountsEmails: hyperv1.GCPServiceAccountsEmails{
								NodePool:        "nodepool@my-project-123.iam.gserviceaccount.com",
								ControlPlane:    "controlplane@my-project-123.iam.gserviceaccount.com",
								CloudController: "invalid-cloud-controller-email",
							},
						},
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("cloudController in body"))
			})

			It("should accept when GCP resource labels have valid values", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Platform.Type = hyperv1.GCPPlatform
					hc.Spec.Platform.GCP = &hyperv1.GCPPlatformSpec{
						Project: "my-project-123",
						Region:  "us-central1",
						NetworkConfig: hyperv1.GCPNetworkConfig{
							Network:                     hyperv1.GCPResourceReference{Name: "my-network"},
							PrivateServiceConnectSubnet: hyperv1.GCPResourceReference{Name: "my-psc-subnet"},
						},
						WorkloadIdentity: hyperv1.GCPWorkloadIdentityConfig{
							ProjectNumber: "123456789012",
							PoolID:        "my-wif-pool",
							ProviderID:    "my-wif-provider",
							ServiceAccountsEmails: hyperv1.GCPServiceAccountsEmails{
								NodePool:        "nodepool@my-project-123.iam.gserviceaccount.com",
								ControlPlane:    "controlplane@my-project-123.iam.gserviceaccount.com",
								CloudController: "cloudcontroller@my-project-123.iam.gserviceaccount.com",
							},
						},
						ResourceLabels: []hyperv1.GCPResourceLabel{
							{Key: "environment", Value: ptr.To("production")},
							{Key: "team", Value: ptr.To("platform")},
						},
					}
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should accept when GCP resource label has empty value (explicitly set)", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Platform.Type = hyperv1.GCPPlatform
					hc.Spec.Platform.GCP = &hyperv1.GCPPlatformSpec{
						Project: "my-project-123",
						Region:  "us-central1",
						NetworkConfig: hyperv1.GCPNetworkConfig{
							Network:                     hyperv1.GCPResourceReference{Name: "my-network"},
							PrivateServiceConnectSubnet: hyperv1.GCPResourceReference{Name: "my-psc-subnet"},
						},
						WorkloadIdentity: hyperv1.GCPWorkloadIdentityConfig{
							ProjectNumber: "123456789012",
							PoolID:        "my-wif-pool",
							ProviderID:    "my-wif-provider",
							ServiceAccountsEmails: hyperv1.GCPServiceAccountsEmails{
								NodePool:        "nodepool@my-project-123.iam.gserviceaccount.com",
								ControlPlane:    "controlplane@my-project-123.iam.gserviceaccount.com",
								CloudController: "cloudcontroller@my-project-123.iam.gserviceaccount.com",
							},
						},
						ResourceLabels: []hyperv1.GCPResourceLabel{
							{Key: "optional", Value: ptr.To("")},
						},
					}
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should accept when GCP resource label value is nil (not provided)", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Platform.Type = hyperv1.GCPPlatform
					hc.Spec.Platform.GCP = &hyperv1.GCPPlatformSpec{
						Project: "my-project-123",
						Region:  "us-central1",
						NetworkConfig: hyperv1.GCPNetworkConfig{
							Network:                     hyperv1.GCPResourceReference{Name: "my-network"},
							PrivateServiceConnectSubnet: hyperv1.GCPResourceReference{Name: "my-psc-subnet"},
						},
						WorkloadIdentity: hyperv1.GCPWorkloadIdentityConfig{
							ProjectNumber: "123456789012",
							PoolID:        "my-wif-pool",
							ProviderID:    "my-wif-provider",
							ServiceAccountsEmails: hyperv1.GCPServiceAccountsEmails{
								NodePool:        "nodepool@my-project-123.iam.gserviceaccount.com",
								ControlPlane:    "controlplane@my-project-123.iam.gserviceaccount.com",
								CloudController: "cloudcontroller@my-project-123.iam.gserviceaccount.com",
							},
						},
						ResourceLabels: []hyperv1.GCPResourceLabel{
							{Key: "test"},
						},
					}
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should reject when GCP resource label key starts with reserved 'goog' prefix", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Platform.Type = hyperv1.GCPPlatform
					hc.Spec.Platform.GCP = &hyperv1.GCPPlatformSpec{
						Project: "my-project-123",
						Region:  "us-central1",
						NetworkConfig: hyperv1.GCPNetworkConfig{
							Network:                     hyperv1.GCPResourceReference{Name: "my-network"},
							PrivateServiceConnectSubnet: hyperv1.GCPResourceReference{Name: "my-psc-subnet"},
						},
						WorkloadIdentity: hyperv1.GCPWorkloadIdentityConfig{
							ProjectNumber: "123456789012",
							PoolID:        "my-wif-pool",
							ProviderID:    "my-wif-provider",
							ServiceAccountsEmails: hyperv1.GCPServiceAccountsEmails{
								NodePool:        "nodepool@my-project-123.iam.gserviceaccount.com",
								ControlPlane:    "controlplane@my-project-123.iam.gserviceaccount.com",
								CloudController: "cloudcontroller@my-project-123.iam.gserviceaccount.com",
							},
						},
						ResourceLabels: []hyperv1.GCPResourceLabel{
							{Key: "goog-reserved", Value: ptr.To("value")},
						},
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("Label keys starting with the reserved 'goog' prefix are not allowed"))
			})

			It("should reject when exceeding maximum resource labels (60)", func() {
				labels := make([]hyperv1.GCPResourceLabel, 61)
				for i := 0; i < 61; i++ {
					labels[i] = hyperv1.GCPResourceLabel{
						Key:   fmt.Sprintf("label%d", i),
						Value: ptr.To("value"),
					}
				}
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Platform.Type = hyperv1.GCPPlatform
					hc.Spec.Platform.GCP = &hyperv1.GCPPlatformSpec{
						Project: "my-project-123",
						Region:  "us-central1",
						NetworkConfig: hyperv1.GCPNetworkConfig{
							Network:                     hyperv1.GCPResourceReference{Name: "my-network"},
							PrivateServiceConnectSubnet: hyperv1.GCPResourceReference{Name: "my-psc-subnet"},
						},
						WorkloadIdentity: hyperv1.GCPWorkloadIdentityConfig{
							ProjectNumber: "123456789012",
							PoolID:        "my-wif-pool",
							ProviderID:    "my-wif-provider",
							ServiceAccountsEmails: hyperv1.GCPServiceAccountsEmails{
								NodePool:        "nodepool@my-project-123.iam.gserviceaccount.com",
								ControlPlane:    "controlplane@my-project-123.iam.gserviceaccount.com",
								CloudController: "cloudcontroller@my-project-123.iam.gserviceaccount.com",
							},
						},
						ResourceLabels: labels,
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("must have at most 60 items"))
			})
		})

		Context("Cluster and Infra ID validation", Label("ClusterID", "InfraID"), func() {
			It("should reject when clusterID is not RFC4122 UUID", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.ClusterID = "foo"
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("clusterID must be an RFC4122 UUID value (xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx in hexadecimal digits)"))
			})

			It("should reject when infraID is not RFC4122 UUID", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.InfraID = "@"
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("infraID must consist of lowercase alphanumeric characters or '-', start and end with an alphanumeric character, and be between 1 and 253 characters"))
			})

			It("should accept when infraID and clusterID are valid", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.ClusterID = "123e4567-e89b-12d3-a456-426614174000"
					hc.Spec.InfraID = "infra-id"
				})
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Context("Labels validation", Label("Labels"), func() {
			It("should reject when labels have more than 20 entries", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					labels := map[string]string{}
					for i := 0; i < 25; i++ {
						key := fmt.Sprintf("test%d", i)
						labels[key] = key
					}
					hc.Spec.Labels = labels
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("must have at most 20 items"))
			})
		})

		Context("UpdateService validation", Label("UpdateService"), func() {
			It("should reject when updateService is not a complete URL", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.UpdateService = "foo"
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("updateService must be a valid absolute URL"))
			})

			It("should accept when updateService is a valid URL", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.UpdateService = "https://custom-updateservice.com"
				})
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Context("Availability policy validation", Label("Availability"), func() {
			It("should reject when controllerAvailabilityPolicy is not HighlyAvailable or SingleReplica", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.ControllerAvailabilityPolicy = "foo"
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("Unsupported value: \"foo\": supported values: \"HighlyAvailable\", \"SingleReplica\""))
			})

			It("should reject when infrastructureAvailabilityPolicy is not HighlyAvailable or SingleReplica", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.InfrastructureAvailabilityPolicy = "foo"
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("Unsupported value: \"foo\": supported values: \"HighlyAvailable\", \"SingleReplica\""))
			})
		})

		Context("Networking validation", Label("Networking"), func() {
			It("should reject when networkType is not one of OpenShiftSDN;Calico;OVNKubernetes;Other", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Networking = hyperv1.ClusterNetworking{
						NetworkType: "foo",
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("Unsupported value: \"foo\": supported values: \"OpenShiftSDN\", \"Calico\", \"OVNKubernetes\", \"Other\""))
			})

			It("should reject when the cidr is not valid", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Networking = hyperv1.ClusterNetworking{
						ClusterNetwork: []hyperv1.ClusterNetworkEntry{
							{
								CIDR: ipnet.IPNet{
									IP: net.IPv4(10, 128, 0, 0),
								},
								HostPrefix: 0,
							},
						},
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("cidr must be a valid IPv4 or IPv6 CIDR notation (e.g., 192.168.1.0/24 or 2001:db8::/64)"))
			})

			It("should reject when a cidr in clusterNetwork and serviceNetwork is duplicated", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Networking = hyperv1.ClusterNetworking{
						ClusterNetwork: []hyperv1.ClusterNetworkEntry{
							{
								CIDR: ipnet.IPNet{
									IP:   net.IPv4(10, 128, 0, 0),
									Mask: net.CIDRMask(32, 32),
								},
								HostPrefix: 0,
							},
						},
						ServiceNetwork: []hyperv1.ServiceNetworkEntry{
							{
								CIDR: ipnet.IPNet{
									IP:   net.IPv4(10, 128, 0, 0),
									Mask: net.CIDRMask(32, 32),
								},
							},
						},
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("CIDR ranges in machineNetwork, clusterNetwork, and serviceNetwork must be unique and non-overlapping"))
			})

			It("should reject when a cidr in machineNetwork and serviceNetwork is duplicated", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Networking = hyperv1.ClusterNetworking{
						MachineNetwork: []hyperv1.MachineNetworkEntry{
							{
								CIDR: ipnet.IPNet{
									IP:   net.IPv4(10, 128, 0, 0),
									Mask: net.CIDRMask(32, 32),
								},
							},
						},
						ServiceNetwork: []hyperv1.ServiceNetworkEntry{
							{
								CIDR: ipnet.IPNet{
									IP:   net.IPv4(10, 128, 0, 0),
									Mask: net.CIDRMask(32, 32),
								},
							},
						},
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("CIDR ranges in machineNetwork, clusterNetwork, and serviceNetwork must be unique and non-overlapping"))
			})

			It("should reject when a cidr in machineNetwork and ClusterNetwork is duplicated", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Networking = hyperv1.ClusterNetworking{
						MachineNetwork: []hyperv1.MachineNetworkEntry{
							{
								CIDR: ipnet.IPNet{
									IP:   net.IPv4(10, 128, 0, 0),
									Mask: net.CIDRMask(32, 32),
								},
							},
						},
						ClusterNetwork: []hyperv1.ClusterNetworkEntry{
							{
								CIDR: ipnet.IPNet{
									IP:   net.IPv4(10, 128, 0, 0),
									Mask: net.CIDRMask(32, 32),
								},
							},
						},
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("CIDR ranges in machineNetwork, clusterNetwork, and serviceNetwork must be unique and non-overlapping"))
			})
		})

		Context("Etcd configuration validation", Label("Etcd"), func() {
			It("should reject when managementType is managed with unmanaged configuration", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Etcd = hyperv1.EtcdSpec{
						ManagementType: hyperv1.Managed,
						Unmanaged: &hyperv1.UnmanagedEtcdSpec{
							Endpoint: "https://etcd.example.com:2379",
						},
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("Only managed configuration must be set when managementType is Managed"))
			})

			It("should reject when managementType is unmanaged with managed configuration", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Etcd = hyperv1.EtcdSpec{
						ManagementType: hyperv1.Unmanaged,
						Managed: &hyperv1.ManagedEtcdSpec{
							Storage: hyperv1.ManagedEtcdStorageSpec{
								Type: hyperv1.PersistentVolumeEtcdStorage,
							},
						},
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("Only unmanaged configuration must be set when managementType is Unmanaged"))
			})
		})

		Context("Service publishing strategies validation", Label("Services"), func() {
			It("should reject when servicePublishingStrategy is loadBalancer for kas and the hostname clashes with one of configuration.apiServer.servingCerts.namedCertificates", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Services = []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.APIServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.LoadBalancer,
								LoadBalancer: &hyperv1.LoadBalancerPublishingStrategy{
									Hostname: "kas.duplicated.hostname.com",
								},
							},
						},
						{
							Service: hyperv1.Ignition,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.NodePort,
								NodePort: &hyperv1.NodePortPublishingStrategy{
									Address: "127.0.0.1",
								},
							},
						},
						{
							Service: hyperv1.Konnectivity,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.NodePort,
								NodePort: &hyperv1.NodePortPublishingStrategy{
									Address: "fd2e:6f44:5dd8:c956::14",
								},
							},
						},
						{
							Service: hyperv1.OAuthServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.NodePort,
								NodePort: &hyperv1.NodePortPublishingStrategy{
									Address: "fd2e:6f44:5dd8:c956:0000:0000:0000:0014",
								},
							},
						},
					}
					hc.Spec.Configuration = &hyperv1.ClusterConfiguration{
						APIServer: &configv1.APIServerSpec{
							ServingCerts: configv1.APIServerServingCerts{
								NamedCertificates: []configv1.APIServerNamedServingCert{
									{
										Names: []string{
											"anything",
											"kas.duplicated.hostname.com",
										},
									},
								},
							},
						},
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("loadBalancer hostname cannot be in ClusterConfiguration.apiserver.servingCerts.namedCertificates"))
			})

			It("should accept when servicePublishingStrategy is nodePort and addresses valid hostname, IPv4 and IPv6", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Services = []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.APIServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.NodePort,
								NodePort: &hyperv1.NodePortPublishingStrategy{
									Address: "kas.example.com",
								},
							},
						},
						{
							Service: hyperv1.Ignition,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.NodePort,
								NodePort: &hyperv1.NodePortPublishingStrategy{
									Address: "127.0.0.1",
								},
							},
						},
						{
							Service: hyperv1.Konnectivity,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.NodePort,
								NodePort: &hyperv1.NodePortPublishingStrategy{
									Address: "fd2e:6f44:5dd8:c956::14",
								},
							},
						},
						{
							Service: hyperv1.OAuthServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.NodePort,
								NodePort: &hyperv1.NodePortPublishingStrategy{
									Address: "fd2e:6f44:5dd8:c956:0000:0000:0000:0014",
								},
							},
						},
					}
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should reject when servicePublishingStrategy is nodePort and addresses is invalid", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Services = []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.APIServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.NodePort,
								NodePort: &hyperv1.NodePortPublishingStrategy{
									Address: "@foo",
								},
							},
						},
						{
							Service: hyperv1.Ignition,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.NodePort,
								NodePort: &hyperv1.NodePortPublishingStrategy{
									Address: "127.0.0.1",
								},
							},
						},
						{
							Service: hyperv1.Konnectivity,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.NodePort,
								NodePort: &hyperv1.NodePortPublishingStrategy{
									Address: "fd2e:6f44:5dd8:c956::14",
								},
							},
						},
						{
							Service: hyperv1.OAuthServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.NodePort,
								NodePort: &hyperv1.NodePortPublishingStrategy{
									Address: "fd2e:6f44:5dd8:c956:0000:0000:0000:0014",
								},
							},
						},
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("address must be a valid hostname, IPv4, or IPv6 address"))
			})

			It("should reject when less than 4 services are set", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Services = []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.Ignition,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type:  hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{},
							},
						},
						{
							Service: hyperv1.Konnectivity,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type:  hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{},
							},
						},
						{
							Service: hyperv1.OAuthServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type:  hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{},
							},
						},
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("spec.services in body should have at least 4 items or 3 for IBMCloud"))
			})

			It("should reject when a type Route set with the nodePort configuration", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Services = []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.APIServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{
									Hostname: "api.example.com",
								},
							},
						},
						{
							Service: hyperv1.Ignition,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
								NodePort: &hyperv1.NodePortPublishingStrategy{
									Address: "ignition.example.com",
									Port:    3030,
								},
							},
						},
						{
							Service: hyperv1.Konnectivity,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type:  hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{},
							},
						},
						{
							Service: hyperv1.OAuthServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type:  hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{},
							},
						},
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("nodePort is required when type is NodePort, and forbidden otherwise"))
			})

			It("should reject when a type NodePort set with the route configuration", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Services = []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.APIServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{
									Hostname: "api.example.com",
								},
							},
						},
						{
							Service: hyperv1.Ignition,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.NodePort,
								Route: &hyperv1.RoutePublishingStrategy{
									Hostname: "ignition.example.com",
								},
							},
						},
						{
							Service: hyperv1.Konnectivity,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type:  hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{},
							},
						},
						{
							Service: hyperv1.OAuthServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type:  hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{},
							},
						},
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("only route is allowed when type is Route, and forbidden otherwise"))
			})
		})

		Context("ServiceAccount signing key and issuerURL validation", Label("ServiceAccount", "IssuerURL"), func() {
			It("should reject when issuerURL is not a valid URL", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.IssuerURL = "foo"
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("issuerURL must be a valid absolute URL"))
			})
		})

		Context("KubeAPIServerDNSName validation", Label("KubeAPIServerDNSName"), func() {
			It("should reject when kubeAPIServerDNSName has invalid chars", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.KubeAPIServerDNSName = "@foo"
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("kubeAPIServerDNSName must be a valid URL name (e.g., api.example.com)"))
			})

			It("should accept when kubeAPIServerDNSName is a valid hierarchical domain with two levels", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.KubeAPIServerDNSName = "foo.bar"
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should accept when kubeAPIServerDNSName is a valid hierarchical domain with 3 levels", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.KubeAPIServerDNSName = "123.foo.bar"
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should accept when kubeAPIServerDNSName is a single subdomain", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.KubeAPIServerDNSName = "foo"
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should accept when kubeAPIServerDNSName is empty", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.KubeAPIServerDNSName = ""
				})
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Context("Autoscaling scaleDown validation", Label("Autoscaling"), func() {
			It("should reject when scaling is ScaleUpOnly and scaleDown is set", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Autoscaling = hyperv1.ClusterAutoscaling{
						Scaling: hyperv1.ScaleUpOnly,
						ScaleDown: &hyperv1.ScaleDownConfig{
							DelayAfterAddSeconds: ptr.To(int32(300)),
						},
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("scaleDown can only be set when scaling is ScaleUpAndScaleDown"))
			})

			It("should accept when scaling is ScaleUpAndScaleDown and scaleDown is set", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Autoscaling = hyperv1.ClusterAutoscaling{
						Scaling: hyperv1.ScaleUpAndScaleDown,
						ScaleDown: &hyperv1.ScaleDownConfig{
							DelayAfterAddSeconds: ptr.To(int32(300)),
						},
					}
				})
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Context("BalancingIgnoredLabels validation", Label("Autoscaling", "Labels"), func() {
			It("should reject when balancingIgnoredLabels contains invalid label key with special characters", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Autoscaling = hyperv1.ClusterAutoscaling{
						BalancingIgnoredLabels: []string{
							"invalid@label",
						},
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("Each balancingIgnoredLabels item must be a valid label key"))
			})

			It("should reject when balancingIgnoredLabels contains invalid label key starting with dash", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Autoscaling = hyperv1.ClusterAutoscaling{
						BalancingIgnoredLabels: []string{
							"-invalid-label",
						},
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("Each balancingIgnoredLabels item must be a valid label key"))
			})

			It("should reject when balancingIgnoredLabels contains invalid label key ending with dash", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Autoscaling = hyperv1.ClusterAutoscaling{
						BalancingIgnoredLabels: []string{
							"invalid-label-",
						},
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("Each balancingIgnoredLabels item must be a valid label key"))
			})

			It("should accept when balancingIgnoredLabels contains valid label keys", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Autoscaling = hyperv1.ClusterAutoscaling{
						BalancingIgnoredLabels: []string{
							"valid-label",
							"valid.prefix.com/valid-suffix",
							"topology.ebs.csi.aws.com/zone",
						},
					}
				})
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Context("Azure authentication configuration validation", Label("Azure", "Authentication"), func() {
			It("should reject when azureAuthenticationConfigType is ManagedIdentities but managedIdentities field is missing", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Platform.Type = hyperv1.AzurePlatform
					hc.Spec.Platform.Azure = &hyperv1.AzurePlatformSpec{
						Location:          "eastus",
						ResourceGroupName: "test-rg",
						VnetID:            "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet",
						SubnetID:          "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet",
						SubscriptionID:    "12345678-1234-5678-9012-123456789012",
						SecurityGroupID:   "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/networkSecurityGroups/test-nsg",
						TenantID:          "87654321-4321-8765-2109-876543210987",
						AzureAuthenticationConfig: hyperv1.AzureAuthenticationConfiguration{
							AzureAuthenticationConfigType: "ManagedIdentities",
						},
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("managedIdentities is required when azureAuthenticationConfigType is ManagedIdentities, and forbidden otherwise"))
			})

			It("should reject when azureAuthenticationConfigType is WorkloadIdentities but workloadIdentities field is missing", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Platform.Type = hyperv1.AzurePlatform
					hc.Spec.Platform.Azure = &hyperv1.AzurePlatformSpec{
						Location:          "eastus",
						ResourceGroupName: "test-rg",
						VnetID:            "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet",
						SubnetID:          "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet",
						SubscriptionID:    "12345678-1234-5678-9012-123456789012",
						SecurityGroupID:   "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/networkSecurityGroups/test-nsg",
						TenantID:          "87654321-4321-8765-2109-876543210987",
						AzureAuthenticationConfig: hyperv1.AzureAuthenticationConfiguration{
							AzureAuthenticationConfigType: "WorkloadIdentities",
						},
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("workloadIdentities is required when azureAuthenticationConfigType is WorkloadIdentities, and forbidden otherwise"))
			})

			It("should reject when azureAuthenticationConfigType is ManagedIdentities but workloadIdentities field is present", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Platform.Type = hyperv1.AzurePlatform
					hc.Spec.Platform.Azure = &hyperv1.AzurePlatformSpec{
						Location:          "eastus",
						ResourceGroupName: "test-rg",
						VnetID:            "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet",
						SubnetID:          "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet",
						SubscriptionID:    "12345678-1234-5678-9012-123456789012",
						SecurityGroupID:   "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/networkSecurityGroups/test-nsg",
						TenantID:          "87654321-4321-8765-2109-876543210987",
						AzureAuthenticationConfig: hyperv1.AzureAuthenticationConfiguration{
							AzureAuthenticationConfigType: "ManagedIdentities",
							WorkloadIdentities: &hyperv1.AzureWorkloadIdentities{
								ImageRegistry:      hyperv1.WorkloadIdentity{ClientID: "12345678-1234-5678-9012-123456789012"},
								Ingress:            hyperv1.WorkloadIdentity{ClientID: "12345678-1234-5678-9012-123456789012"},
								File:               hyperv1.WorkloadIdentity{ClientID: "12345678-1234-5678-9012-123456789012"},
								Disk:               hyperv1.WorkloadIdentity{ClientID: "12345678-1234-5678-9012-123456789012"},
								NodePoolManagement: hyperv1.WorkloadIdentity{ClientID: "12345678-1234-5678-9012-123456789012"},
								CloudProvider:      hyperv1.WorkloadIdentity{ClientID: "12345678-1234-5678-9012-123456789012"},
							},
						},
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("managedIdentities is required when azureAuthenticationConfigType is ManagedIdentities, and forbidden otherwise"))
			})

			It("should reject when azureAuthenticationConfigType is WorkloadIdentities but managedIdentities field is present", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Platform.Type = hyperv1.AzurePlatform
					hc.Spec.Platform.Azure = &hyperv1.AzurePlatformSpec{
						Location:          "eastus",
						ResourceGroupName: "test-rg",
						VnetID:            "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet",
						SubnetID:          "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet",
						SubscriptionID:    "12345678-1234-5678-9012-123456789012",
						SecurityGroupID:   "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/networkSecurityGroups/test-nsg",
						TenantID:          "87654321-4321-8765-2109-876543210987",
						AzureAuthenticationConfig: hyperv1.AzureAuthenticationConfiguration{
							AzureAuthenticationConfigType: "WorkloadIdentities",
							ManagedIdentities: &hyperv1.AzureResourceManagedIdentities{
								ControlPlane: hyperv1.ControlPlaneManagedIdentities{
									ManagedIdentitiesKeyVault: hyperv1.ManagedAzureKeyVault{
										Name:     "test-kv",
										TenantID: "87654321-4321-8765-2109-876543210987",
									},
									CloudProvider: hyperv1.ManagedIdentity{
										ClientID:              "12345678-1234-5678-9012-123456789012",
										ObjectEncoding:        "utf-8",
										CredentialsSecretName: "cp-secret",
									},
									NodePoolManagement: hyperv1.ManagedIdentity{
										ClientID:              "12345678-1234-5678-9012-123456789012",
										ObjectEncoding:        "utf-8",
										CredentialsSecretName: "npm-secret",
									},
									ControlPlaneOperator: hyperv1.ManagedIdentity{
										ClientID:              "12345678-1234-5678-9012-123456789012",
										ObjectEncoding:        "utf-8",
										CredentialsSecretName: "cpo-secret",
									},
									ImageRegistry: hyperv1.ManagedIdentity{
										ClientID:              "12345678-1234-5678-9012-123456789012",
										ObjectEncoding:        "utf-8",
										CredentialsSecretName: "ir-secret",
									},
									Ingress: hyperv1.ManagedIdentity{
										ClientID:              "12345678-1234-5678-9012-123456789012",
										ObjectEncoding:        "utf-8",
										CredentialsSecretName: "ingress-secret",
									},
									Network: hyperv1.ManagedIdentity{
										ClientID:              "12345678-1234-5678-9012-123456789012",
										ObjectEncoding:        "utf-8",
										CredentialsSecretName: "network-secret",
									},
									Disk: hyperv1.ManagedIdentity{
										ClientID:              "12345678-1234-5678-9012-123456789012",
										ObjectEncoding:        "utf-8",
										CredentialsSecretName: "disk-secret",
									},
									File: hyperv1.ManagedIdentity{
										ClientID:              "12345678-1234-5678-9012-123456789012",
										ObjectEncoding:        "utf-8",
										CredentialsSecretName: "file-secret",
									},
								},
								DataPlane: hyperv1.DataPlaneManagedIdentities{
									ImageRegistryMSIClientID: "12345678-1234-5678-9012-123456789012",
									DiskMSIClientID:          "12345678-1234-5678-9012-123456789012",
									FileMSIClientID:          "12345678-1234-5678-9012-123456789012",
								},
							},
						},
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("workloadIdentities is required when azureAuthenticationConfigType is WorkloadIdentities, and forbidden otherwise"))
			})
		})

		Context("Operator configuration validation", Label("Operator", "Configuration"), func() {
			It("should accept when disableMultiNetwork is set to false", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.OperatorConfiguration = &hyperv1.OperatorConfiguration{
						ClusterNetworkOperator: &hyperv1.ClusterNetworkOperatorSpec{
							DisableMultiNetwork: ptr.To(false),
						},
					}
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should accept when disableMultiNetwork is true and networkType is Other", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Networking = hyperv1.ClusterNetworking{
						NetworkType: hyperv1.Other,
					}
					hc.Spec.OperatorConfiguration = &hyperv1.OperatorConfiguration{
						ClusterNetworkOperator: &hyperv1.ClusterNetworkOperatorSpec{
							DisableMultiNetwork: ptr.To(true),
						},
					}
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should reject when disableMultiNetwork is true and networkType is not Other", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Networking = hyperv1.ClusterNetworking{
						NetworkType: hyperv1.OVNKubernetes,
					}
					hc.Spec.OperatorConfiguration = &hyperv1.OperatorConfiguration{
						ClusterNetworkOperator: &hyperv1.ClusterNetworkOperatorSpec{
							DisableMultiNetwork: ptr.To(true),
						},
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("disableMultiNetwork can only be set to true when networkType is 'Other'"))
			})

			It("should accept when disableMultiNetwork is false and networkType is OVNKubernetes", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Networking = hyperv1.ClusterNetworking{
						NetworkType: hyperv1.OVNKubernetes,
					}
					hc.Spec.OperatorConfiguration = &hyperv1.OperatorConfiguration{
						ClusterNetworkOperator: &hyperv1.ClusterNetworkOperatorSpec{
							DisableMultiNetwork: ptr.To(false),
						},
					}
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should reject when ovnKubernetesConfig is set and networkType is not OVNKubernetes", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Networking = hyperv1.ClusterNetworking{
						NetworkType: hyperv1.OpenShiftSDN,
					}
					hc.Spec.OperatorConfiguration = &hyperv1.OperatorConfiguration{
						ClusterNetworkOperator: &hyperv1.ClusterNetworkOperatorSpec{
							OVNKubernetesConfig: &hyperv1.OVNKubernetesConfig{
								IPv4: &hyperv1.OVNIPv4Config{
									InternalJoinSubnet: "10.10.0.0/16",
								},
							},
						},
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("ovnKubernetesConfig is forbidden when networkType is not OVNKubernetes"))
			})

			It("should accept when ovnKubernetesConfig is set and networkType is OVNKubernetes", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Networking = hyperv1.ClusterNetworking{
						NetworkType: hyperv1.OVNKubernetes,
					}
					hc.Spec.OperatorConfiguration = &hyperv1.OperatorConfiguration{
						ClusterNetworkOperator: &hyperv1.ClusterNetworkOperatorSpec{
							OVNKubernetesConfig: &hyperv1.OVNKubernetesConfig{
								IPv4: &hyperv1.OVNIPv4Config{
									InternalJoinSubnet:          "10.10.0.0/16",
									InternalTransitSwitchSubnet: "10.20.0.0/16",
								},
							},
						},
					}
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should accept when ovnKubernetesConfig is not set and networkType is not OVNKubernetes", func() {
				err := testHostedClusterCreation(ctx, mgmtClient, "hostedcluster-base.yaml", func(hc *hyperv1.HostedCluster) {
					hc.Spec.Networking = hyperv1.ClusterNetworking{
						NetworkType: hyperv1.OpenShiftSDN,
					}
					hc.Spec.OperatorConfiguration = &hyperv1.OperatorConfiguration{
						ClusterNetworkOperator: &hyperv1.ClusterNetworkOperatorSpec{
							DisableMultiNetwork: ptr.To(false),
						},
					}
				})
				Expect(err).NotTo(HaveOccurred())
			})
		})
	})

	Context("NodePool creation", Label("NodePool"), func() {
		Context("Taint validation", Label("Taints"), func() {
			It("should reject when key prefix is not a valid subdomain", func() {
				err := testNodePoolCreation(ctx, mgmtClient, "nodepool-base.yaml", func(np *hyperv1.NodePool) {
					np.Spec.Taints = []hyperv1.Taint{{Key: "prefix@/suffix", Value: "value", Effect: "NoSchedule"}}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("key must be a qualified name with an optional subdomain prefix e.g. example.com/MyName"))
			})

			It("should reject when key suffix is not a valid qualified name", func() {
				err := testNodePoolCreation(ctx, mgmtClient, "nodepool-base.yaml", func(np *hyperv1.NodePool) {
					np.Spec.Taints = []hyperv1.Taint{{Key: "prefix/suffix@", Value: "value", Effect: "NoSchedule"}}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("key must be a qualified name with an optional subdomain prefix e.g. example.com/MyName"))
			})

			It("should reject when key is empty", func() {
				err := testNodePoolCreation(ctx, mgmtClient, "nodepool-base.yaml", func(np *hyperv1.NodePool) {
					np.Spec.Taints = []hyperv1.Taint{{Key: "", Value: "value", Effect: "NoSchedule"}}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("spec.taints[0].key in body should be at least 1 chars long"))
			})

			It("should accept when key is a valid qualified name with no prefix", func() {
				err := testNodePoolCreation(ctx, mgmtClient, "nodepool-base.yaml", func(np *hyperv1.NodePool) {
					np.Spec.Taints = []hyperv1.Taint{{Key: "valid-suffix", Value: "", Effect: "NoSchedule"}}
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should accept when key is a valid qualified name with a subdomain prefix", func() {
				err := testNodePoolCreation(ctx, mgmtClient, "nodepool-base.yaml", func(np *hyperv1.NodePool) {
					np.Spec.Taints = []hyperv1.Taint{{Key: "valid-prefix.com/valid-suffix", Value: "", Effect: "NoSchedule"}}
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should accept when key is a valid qualified name with a subdomain prefix and value is a valid qualified name", func() {
				err := testNodePoolCreation(ctx, mgmtClient, "nodepool-base.yaml", func(np *hyperv1.NodePool) {
					np.Spec.Taints = []hyperv1.Taint{{Key: "valid-prefix.com/valid-suffix", Value: "value", Effect: "NoSchedule"}}
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should reject when value contains strange chars", func() {
				err := testNodePoolCreation(ctx, mgmtClient, "nodepool-base.yaml", func(np *hyperv1.NodePool) {
					np.Spec.Taints = []hyperv1.Taint{{Key: "valid-prefix.com/valid-suffix", Value: "@", Effect: "NoSchedule"}}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("Value must start and end with alphanumeric characters and can only contain '-', '_', '.' in the middle"))
			})
		})

		Context("PausedUntil validation", Label("PausedUntil"), func() {
			It("should reject when pausedUntil is a random string", func() {
				err := testNodePoolCreation(ctx, mgmtClient, "nodepool-base.yaml", func(np *hyperv1.NodePool) {
					np.Spec.PausedUntil = ptr.To("fail")
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("PausedUntil must be a date in RFC3339 format or 'True', 'true', 'False' or 'false'"))
			})

			It("should reject when pausedUntil date is not RFC3339 format", func() {
				err := testNodePoolCreation(ctx, mgmtClient, "nodepool-base.yaml", func(np *hyperv1.NodePool) {
					np.Spec.PausedUntil = ptr.To("2022-01-01")
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("PausedUntil must be a date in RFC3339 format or 'True', 'true', 'False' or 'false'"))
			})

			It("should accept when pausedUntil is an allowed bool False", func() {
				err := testNodePoolCreation(ctx, mgmtClient, "nodepool-base.yaml", func(np *hyperv1.NodePool) {
					np.Spec.PausedUntil = ptr.To("False")
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should accept when pausedUntil is an allowed bool false", func() {
				err := testNodePoolCreation(ctx, mgmtClient, "nodepool-base.yaml", func(np *hyperv1.NodePool) {
					np.Spec.PausedUntil = ptr.To("false")
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should accept when pausedUntil is an allowed bool true", func() {
				err := testNodePoolCreation(ctx, mgmtClient, "nodepool-base.yaml", func(np *hyperv1.NodePool) {
					np.Spec.PausedUntil = ptr.To("true")
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should accept when pausedUntil is an allowed bool True", func() {
				err := testNodePoolCreation(ctx, mgmtClient, "nodepool-base.yaml", func(np *hyperv1.NodePool) {
					np.Spec.PausedUntil = ptr.To("True")
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should accept when pausedUntil date is RFC3339", func() {
				err := testNodePoolCreation(ctx, mgmtClient, "nodepool-base.yaml", func(np *hyperv1.NodePool) {
					np.Spec.PausedUntil = ptr.To("2022-01-01T00:00:00Z")
				})
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Context("Release image validation", Label("Release", "Image"), func() {
			It("should reject when image is bad format", func() {
				err := testNodePoolCreation(ctx, mgmtClient, "nodepool-base.yaml", func(np *hyperv1.NodePool) {
					np.Spec.Release.Image = "@"
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("Image must start with a word character (letters, digits, or underscores) and contain no white spaces"))
			})

			It("should reject when image is empty", func() {
				err := testNodePoolCreation(ctx, mgmtClient, "nodepool-base.yaml", func(np *hyperv1.NodePool) {
					np.Spec.Release.Image = ""
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("Image must start with a word character (letters, digits, or underscores) and contain no white spaces"))
			})

			It("should accept when image is valid", func() {
				err := testNodePoolCreation(ctx, mgmtClient, "nodepool-base.yaml", func(np *hyperv1.NodePool) {
					np.Spec.Release.Image = "quay.io/openshift-release-dev/ocp-release:4.17.0-rc.0-x86_64"
				})
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Context("Management validation", Label("Management"), func() {
			It("should reject when replace upgrade type is set with inPlace configuration", func() {
				err := testNodePoolCreation(ctx, mgmtClient, "nodepool-base.yaml", func(np *hyperv1.NodePool) {
					np.Spec.Management = hyperv1.NodePoolManagement{
						UpgradeType: hyperv1.UpgradeTypeReplace,
						InPlace: &hyperv1.InPlaceUpgrade{
							MaxUnavailable: ptr.To(intstr.FromInt32(1)),
						},
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("The 'inPlace' field can only be set when 'upgradeType' is 'InPlace'"))
			})

			It("should reject when strategy is onDelete with RollingUpdate configuration", func() {
				err := testNodePoolCreation(ctx, mgmtClient, "nodepool-base.yaml", func(np *hyperv1.NodePool) {
					np.Spec.Management = hyperv1.NodePoolManagement{
						UpgradeType: hyperv1.UpgradeTypeReplace,
						Replace: &hyperv1.ReplaceUpgrade{
							Strategy: hyperv1.UpgradeStrategyOnDelete,
							RollingUpdate: &hyperv1.RollingUpdate{
								MaxUnavailable: ptr.To(intstr.FromInt32(1)),
							},
						},
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("The 'rollingUpdate' field can only be set when 'strategy' is 'RollingUpdate'"))
			})
		})

		Context("AWS placement options validation", Label("AWS", "Placement"), func() {
			It("should reject when tenancy is 'host' and capacity reservation is specified", func() {
				err := testNodePoolCreation(ctx, mgmtClient, "nodepool-base.yaml", func(np *hyperv1.NodePool) {
					np.Spec.Platform.AWS.Placement = &hyperv1.PlacementOptions{
						Tenancy: "host",
						CapacityReservation: &hyperv1.CapacityReservationOptions{
							ID: ptr.To("cr-1234567890abcdef0"),
						},
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("AWS Capacity Reservations cannot be used with Dedicated Hosts (tenancy 'host')"))
			})

			It("should reject when capacity reservation ID is specified with preference 'None'", func() {
				err := testNodePoolCreation(ctx, mgmtClient, "nodepool-base.yaml", func(np *hyperv1.NodePool) {
					np.Spec.Platform.AWS.Placement = &hyperv1.PlacementOptions{
						CapacityReservation: &hyperv1.CapacityReservationOptions{
							ID:         ptr.To("cr-1234567890abcdef0"),
							Preference: hyperv1.CapacityReservationPreferenceNone,
						},
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("AWS Capacity Reservation preference 'None' or 'Open' is incompatible with specifying a Capacity Reservation ID"))
			})

			It("should reject when capacity reservation ID is specified with preference 'Open'", func() {
				err := testNodePoolCreation(ctx, mgmtClient, "nodepool-base.yaml", func(np *hyperv1.NodePool) {
					np.Spec.Platform.AWS.Placement = &hyperv1.PlacementOptions{
						CapacityReservation: &hyperv1.CapacityReservationOptions{
							ID:         ptr.To("cr-1234567890abcdef0"),
							Preference: hyperv1.CapacityReservationPreferenceOpen,
						},
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("AWS Capacity Reservation preference 'None' or 'Open' is incompatible with specifying a Capacity Reservation ID"))
			})

			It("should reject when capacity reservation ID has invalid format", func() {
				err := testNodePoolCreation(ctx, mgmtClient, "nodepool-base.yaml", func(np *hyperv1.NodePool) {
					np.Spec.Platform.AWS.Placement = &hyperv1.PlacementOptions{
						CapacityReservation: &hyperv1.CapacityReservationOptions{
							ID: ptr.To("invalid-id"),
						},
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("AWS Capacity Reservation ID must start with 'cr-' followed by 17 lowercase hexadecimal characters"))
			})

			It("should reject when marketType is 'CapacityBlocks' without capacity reservation ID", func() {
				err := testNodePoolCreation(ctx, mgmtClient, "nodepool-base.yaml", func(np *hyperv1.NodePool) {
					np.Spec.Platform.AWS.Placement = &hyperv1.PlacementOptions{
						CapacityReservation: &hyperv1.CapacityReservationOptions{
							MarketType: hyperv1.MarketTypeCapacityBlock,
							Preference: hyperv1.CapacityReservationPreferenceOpen,
						},
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("AWS Capacity Reservation market type 'CapacityBlocks' requires a Capacity Reservation ID"))
			})

			It("should accept when tenancy is 'default' with capacity reservation", func() {
				err := testNodePoolCreation(ctx, mgmtClient, "nodepool-base.yaml", func(np *hyperv1.NodePool) {
					np.Spec.Platform.AWS.Placement = &hyperv1.PlacementOptions{
						Tenancy: "default",
						CapacityReservation: &hyperv1.CapacityReservationOptions{
							ID: ptr.To("cr-1234567890abcdef0"),
						},
					}
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should accept when capacity reservation has preference 'Open' without ID", func() {
				err := testNodePoolCreation(ctx, mgmtClient, "nodepool-base.yaml", func(np *hyperv1.NodePool) {
					np.Spec.Platform.AWS.Placement = &hyperv1.PlacementOptions{
						CapacityReservation: &hyperv1.CapacityReservationOptions{
							Preference: hyperv1.CapacityReservationPreferenceOpen,
							MarketType: hyperv1.MarketTypeOnDemand,
						},
					}
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should accept when capacity reservation ID is specified with preference 'CapacityReservationsOnly'", func() {
				err := testNodePoolCreation(ctx, mgmtClient, "nodepool-base.yaml", func(np *hyperv1.NodePool) {
					np.Spec.Platform.AWS.Placement = &hyperv1.PlacementOptions{
						CapacityReservation: &hyperv1.CapacityReservationOptions{
							ID:         ptr.To("cr-1234567890abcdef0"),
							Preference: hyperv1.CapacityReservationPreferenceOnly,
							MarketType: hyperv1.MarketTypeCapacityBlock,
						},
					}
				})
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Context("Azure VM image configuration validation", Label("Azure", "VMImage"), func() {
			It("should accept when marketplace is fully populated with imageGeneration set", func() {
				err := testNodePoolCreation(ctx, mgmtClient, "nodepool-base.yaml", func(np *hyperv1.NodePool) {
					np.Spec.Platform.Type = hyperv1.AzurePlatform
					np.Spec.Platform.Azure = &hyperv1.AzureNodePoolPlatform{
						VMSize: "Standard_D4s_v5",
						Image: hyperv1.AzureVMImage{
							Type: hyperv1.AzureMarketplace,
							AzureMarketplace: &hyperv1.AzureMarketplaceImage{
								Publisher:       "azureopenshift",
								Offer:           "aro4",
								SKU:             "aro_417_rhel8_gen2",
								Version:         "417.94.20240701",
								ImageGeneration: ptr.To(hyperv1.Gen2),
							},
						},
						OSDisk: hyperv1.AzureNodePoolOSDisk{
							DiskStorageAccountType: hyperv1.DiskStorageAccountTypesPremiumLRS,
						},
						SubnetID: "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet",
					}
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should accept when marketplace is fully populated without imageGeneration", func() {
				err := testNodePoolCreation(ctx, mgmtClient, "nodepool-base.yaml", func(np *hyperv1.NodePool) {
					np.Spec.Platform.Type = hyperv1.AzurePlatform
					np.Spec.Platform.Azure = &hyperv1.AzureNodePoolPlatform{
						VMSize: "Standard_D4s_v5",
						Image: hyperv1.AzureVMImage{
							Type: hyperv1.AzureMarketplace,
							AzureMarketplace: &hyperv1.AzureMarketplaceImage{
								Publisher: "azureopenshift",
								Offer:     "aro4",
								SKU:       "aro_417_rhel8_gen2",
								Version:   "417.94.20240701",
							},
						},
						OSDisk: hyperv1.AzureNodePoolOSDisk{
							DiskStorageAccountType: hyperv1.DiskStorageAccountTypesPremiumLRS,
						},
						SubnetID: "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet",
					}
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should accept when type is AzureMarketplace with empty marketplace struct and imageGeneration is set", func() {
				err := testNodePoolCreation(ctx, mgmtClient, "nodepool-base.yaml", func(np *hyperv1.NodePool) {
					np.Spec.Platform.Type = hyperv1.AzurePlatform
					np.Spec.Platform.Azure = &hyperv1.AzureNodePoolPlatform{
						VMSize: "Standard_D4s_v5",
						Image: hyperv1.AzureVMImage{
							Type: hyperv1.AzureMarketplace,
							AzureMarketplace: &hyperv1.AzureMarketplaceImage{
								ImageGeneration: ptr.To(hyperv1.Gen2),
							},
						},
						OSDisk: hyperv1.AzureNodePoolOSDisk{
							DiskStorageAccountType: hyperv1.DiskStorageAccountTypesPremiumLRS,
						},
						SubnetID: "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet",
					}
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should accept when type is AzureMarketplace with nil marketplace struct", func() {
				err := testNodePoolCreation(ctx, mgmtClient, "nodepool-base.yaml", func(np *hyperv1.NodePool) {
					np.Spec.Platform.Type = hyperv1.AzurePlatform
					np.Spec.Platform.Azure = &hyperv1.AzureNodePoolPlatform{
						VMSize: "Standard_D4s_v5",
						Image: hyperv1.AzureVMImage{
							Type:             hyperv1.AzureMarketplace,
							AzureMarketplace: nil, // nil signals the controller to populate marketplace details from release payload
						},
						OSDisk: hyperv1.AzureNodePoolOSDisk{
							DiskStorageAccountType: hyperv1.DiskStorageAccountTypesPremiumLRS,
						},
						SubnetID: "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet",
					}
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should reject when marketplace has only publisher and offer but not sku and version", func() {
				err := testNodePoolCreation(ctx, mgmtClient, "nodepool-base.yaml", func(np *hyperv1.NodePool) {
					np.Spec.Platform.Type = hyperv1.AzurePlatform
					np.Spec.Platform.Azure = &hyperv1.AzureNodePoolPlatform{
						VMSize: "Standard_D4s_v5",
						Image: hyperv1.AzureVMImage{
							Type: hyperv1.AzureMarketplace,
							AzureMarketplace: &hyperv1.AzureMarketplaceImage{
								Publisher: "azureopenshift",
								Offer:     "aro4",
							},
						},
						OSDisk: hyperv1.AzureNodePoolOSDisk{
							DiskStorageAccountType: hyperv1.DiskStorageAccountTypesPremiumLRS,
						},
						SubnetID: "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet",
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("publisher, offer, sku and version must either be all set, or all omitted"))
			})

			It("should reject when marketplace has only sku without publisher, offer, and version", func() {
				err := testNodePoolCreation(ctx, mgmtClient, "nodepool-base.yaml", func(np *hyperv1.NodePool) {
					np.Spec.Platform.Type = hyperv1.AzurePlatform
					np.Spec.Platform.Azure = &hyperv1.AzureNodePoolPlatform{
						VMSize: "Standard_D4s_v5",
						Image: hyperv1.AzureVMImage{
							Type: hyperv1.AzureMarketplace,
							AzureMarketplace: &hyperv1.AzureMarketplaceImage{
								SKU: "aro_417_rhel8_gen2",
							},
						},
						OSDisk: hyperv1.AzureNodePoolOSDisk{
							DiskStorageAccountType: hyperv1.DiskStorageAccountTypesPremiumLRS,
						},
						SubnetID: "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet",
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("publisher, offer, sku and version must either be all set, or all omitted"))
			})

			It("should reject when marketplace has publisher, offer, sku but not version", func() {
				err := testNodePoolCreation(ctx, mgmtClient, "nodepool-base.yaml", func(np *hyperv1.NodePool) {
					np.Spec.Platform.Type = hyperv1.AzurePlatform
					np.Spec.Platform.Azure = &hyperv1.AzureNodePoolPlatform{
						VMSize: "Standard_D4s_v5",
						Image: hyperv1.AzureVMImage{
							Type: hyperv1.AzureMarketplace,
							AzureMarketplace: &hyperv1.AzureMarketplaceImage{
								Publisher: "azureopenshift",
								Offer:     "aro4",
								SKU:       "aro_417_rhel8_gen2",
							},
						},
						OSDisk: hyperv1.AzureNodePoolOSDisk{
							DiskStorageAccountType: hyperv1.DiskStorageAccountTypesPremiumLRS,
						},
						SubnetID: "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet",
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("publisher, offer, sku and version must either be all set, or all omitted"))
			})
		})

		Context("AutoScaling validation", Label("AutoScaling"), func() {
			It("should accept scale-from-zero (autoScaling.min=0) for AWS platform", func() {
				err := testNodePoolCreation(ctx, mgmtClient, "nodepool-base.yaml", func(np *hyperv1.NodePool) {
					np.Spec.Replicas = nil
					np.Spec.AutoScaling = &hyperv1.NodePoolAutoScaling{
						Min: ptr.To[int32](0),
						Max: 5,
					}
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should accept scale-from-zero (autoScaling.min=0) for Azure platform", func() {
				err := testNodePoolCreation(ctx, mgmtClient, "nodepool-base.yaml", func(np *hyperv1.NodePool) {
					np.Spec.Replicas = nil
					np.Spec.AutoScaling = &hyperv1.NodePoolAutoScaling{
						Min: ptr.To[int32](0),
						Max: 5,
					}
					np.Spec.Platform.Type = hyperv1.AzurePlatform
					np.Spec.Platform.Azure = &hyperv1.AzureNodePoolPlatform{
						VMSize: "Standard_D4s_v5",
						Image: hyperv1.AzureVMImage{
							Type: hyperv1.AzureMarketplace,
							AzureMarketplace: &hyperv1.AzureMarketplaceImage{
								ImageGeneration: ptr.To(hyperv1.Gen2),
							},
						},
						OSDisk: hyperv1.AzureNodePoolOSDisk{
							DiskStorageAccountType: hyperv1.DiskStorageAccountTypesPremiumLRS,
						},
						SubnetID: "/subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet",
					}
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should reject scale-from-zero (autoScaling.min=0) for unsupported platforms", func() {
				err := testNodePoolCreation(ctx, mgmtClient, "nodepool-base.yaml", func(np *hyperv1.NodePool) {
					np.Spec.Replicas = nil
					np.Spec.AutoScaling = &hyperv1.NodePoolAutoScaling{
						Min: ptr.To[int32](0),
						Max: 5,
					}
					np.Spec.Platform = hyperv1.NodePoolPlatform{
						Type: hyperv1.KubevirtPlatform,
					}
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("Scale-from-zero (autoScaling.min=0) is currently only supported for AWS and Azure platforms"))
			})
		})
	})
})

// testHostedClusterCreation is a helper function that encapsulates the common test pattern
// for HostedCluster validation tests. It loads the base template, applies mutations,
// handles GCP skip logic, and attempts to create the resource.
func testHostedClusterCreation(ctx context.Context, client crclient.Client, file string, mutate func(*hyperv1.HostedCluster)) error {
	hostedCluster := assets.ShouldHostedCluster(content.ReadFile, fmt.Sprintf("assets/%s", file))
	defer func() {
		_ = client.Delete(ctx, hostedCluster)
	}()
	mutate(hostedCluster)

	err := client.Create(ctx, hostedCluster)
	// Explicitly delete the resource after creation attempt, in addition to defer
	// This matches the original test behavior and ensures cleanup even if creation fails
	_ = client.Delete(ctx, hostedCluster)
	return err
}

// testNodePoolCreation is a helper function that encapsulates the common test pattern
// for NodePool validation tests. It loads the base template, applies mutations,
// and attempts to create the resource.
func testNodePoolCreation(ctx context.Context, client crclient.Client, file string, mutate func(*hyperv1.NodePool)) error {
	nodePool := assets.ShouldNodePool(content.ReadFile, fmt.Sprintf("assets/%s", file))
	defer func() {
		_ = client.Delete(ctx, nodePool)
	}()
	mutate(nodePool)

	err := client.Create(ctx, nodePool)
	// Explicitly delete the resource after creation attempt, in addition to defer
	// This matches the original test behavior and ensures cleanup even if creation fails
	_ = client.Delete(ctx, nodePool)
	return err
}
