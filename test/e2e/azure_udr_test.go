//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/pkg/manifests"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	capiazure "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// udrSubnetIDEnvVar names a pre-provisioned subnet that already has egress (NAT Gateway, firewall,
// or a route to one). UserDefinedRouting creates no egress infrastructure, so nodes cannot pull
// images on a subnet without it, and the default e2e subnet relies on the Load Balancer outbound
// rule this test removes. The test is skipped unless this is set.
const udrSubnetIDEnvVar = "AZURE_UDR_SUBNET_ID"

// TestAzureUserDefinedRouting validates that a cluster created with
// spec.platform.azure.outboundType: UserDefinedRouting provisions no HyperShift-owned egress path.
// It verifies:
//   - outboundType propagates from the HostedCluster to the HostedControlPlane
//   - AzureCluster.spec.networkSpec.nodeOutboundLB is unset, so CAPZ does not reference a Load
//     Balancer backend pool that does not exist under UDR (an existing-but-absent pool reference
//     makes ARM reject worker NIC creation with InvalidResourceReference)
//   - the cloud provider config keeps disableOutboundSNAT: true, so a Service of type LoadBalancer
//     never becomes an egress path through an address the cluster owner does not control
//   - nodes reach Ready, which requires image pulls and therefore proves the customer-provided
//     egress on the subnet is the path actually in use
func TestAzureUserDefinedRouting(t *testing.T) {
	if globalOpts.Platform != hyperv1.AzurePlatform {
		t.Skip("Skipping test because it requires Azure platform")
	}

	subnetID := os.Getenv(udrSubnetIDEnvVar)
	if subnetID == "" {
		t.Skipf("Skipping test because %s is not set; UserDefinedRouting requires a subnet that already has egress", udrSubnetIDEnvVar)
	}

	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)
	clusterOpts.ControlPlaneAvailabilityPolicy = string(hyperv1.SingleReplica)
	clusterOpts.AzurePlatform.OutboundType = string(hyperv1.AzureOutboundTypeUserDefinedRouting)
	clusterOpts.AzurePlatform.SubnetID = subnetID

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		controlPlaneNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)

		t.Run("OutboundTypePropagatesToHostedControlPlane", func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(hostedCluster.Spec.Platform.Azure).ToNot(BeNil())
			g.Expect(hostedCluster.Spec.Platform.Azure.OutboundType).To(Equal(hyperv1.AzureOutboundTypeUserDefinedRouting))

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Namespace: controlPlaneNamespace, Name: hostedCluster.Name},
			}
			g.Expect(mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hcp), hcp)).To(Succeed())
			g.Expect(hcp.Spec.Platform.Azure).ToNot(BeNil())
			g.Expect(hcp.Spec.Platform.Azure.OutboundType).To(Equal(hyperv1.AzureOutboundTypeUserDefinedRouting))
		})

		t.Run("AzureClusterHasNoNodeOutboundLB", func(t *testing.T) {
			e2eutil.EventuallyObjects(t, ctx, "AzureCluster has no nodeOutboundLB",
				func(ctx context.Context) ([]*capiazure.AzureCluster, error) {
					list := &capiazure.AzureClusterList{}
					if err := mgtClient.List(ctx, list, crclient.InNamespace(controlPlaneNamespace)); err != nil {
						return nil, err
					}
					items := make([]*capiazure.AzureCluster, len(list.Items))
					for i := range list.Items {
						items[i] = &list.Items[i]
					}
					return items, nil
				},
				[]e2eutil.Predicate[[]*capiazure.AzureCluster]{
					func(items []*capiazure.AzureCluster) (done bool, reasons string, err error) {
						if len(items) == 0 {
							return false, "no AzureCluster found yet", nil
						}
						return true, fmt.Sprintf("found %d AzureCluster CR(s)", len(items)), nil
					},
				},
				[]e2eutil.Predicate[*capiazure.AzureCluster]{
					func(azureCluster *capiazure.AzureCluster) (done bool, reasons string, err error) {
						if azureCluster.Spec.NetworkSpec.NodeOutboundLB != nil {
							return false, fmt.Sprintf("AzureCluster %s has nodeOutboundLB set to %+v", azureCluster.Name, azureCluster.Spec.NetworkSpec.NodeOutboundLB), nil
						}
						return true, "no node outbound Load Balancer referenced", nil
					},
				},
				e2eutil.WithTimeout(10*time.Minute),
			)
		})

		t.Run("CloudProviderConfigDisablesOutboundSNAT", func(t *testing.T) {
			e2eutil.EventuallyObject(t, ctx, "azure-cloud-config disables outbound SNAT",
				func(ctx context.Context) (*corev1.ConfigMap, error) {
					cm := &corev1.ConfigMap{
						ObjectMeta: metav1.ObjectMeta{Namespace: controlPlaneNamespace, Name: "azure-cloud-config"},
					}
					err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(cm), cm)
					return cm, err
				},
				[]e2eutil.Predicate[*corev1.ConfigMap]{
					func(cm *corev1.ConfigMap) (done bool, reasons string, err error) {
						raw, ok := cm.Data["cloud.conf"]
						if !ok {
							return false, "cloud.conf key is not present yet", nil
						}
						cfg := struct {
							DisableOutboundSNAT bool `json:"disableOutboundSNAT"`
						}{}
						if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
							return false, "", fmt.Errorf("failed to parse cloud.conf: %w", err)
						}
						if !cfg.DisableOutboundSNAT {
							return false, "cloud.conf has disableOutboundSNAT: false, which would make Service load balancing rules an egress path", nil
						}
						return true, "cloud.conf has disableOutboundSNAT: true", nil
					},
				},
				e2eutil.WithTimeout(10*time.Minute),
			)
		})
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "azure-udr", globalOpts.ServiceAccountSigningKey)
}
