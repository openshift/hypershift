//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/support/azureutil"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// TestAzurePrivateTopology validates the full Azure Private Link Service lifecycle
// when a HostedCluster is created with endpointAccess: Private. It verifies:
//   - KAS Service gets the internal LB annotation
//   - AzurePrivateLinkService CR is created in the HCP namespace
//   - PLS alias is populated in the CR status
//   - Private Endpoint IP is populated in the CR status
//   - Private DNS Zone ID is populated in the CR status
//   - The HostedCluster reaches the Available condition (via the framework's ValidatePrivateCluster)
//
// Required environment variables:
//   - AZURE_PRIVATE_NAT_SUBNET_ID: Azure resource ID of the subnet used for PLS NAT IP allocation
//   - AZURE_PRIVATE_ALLOWED_SUBSCRIPTIONS: Comma-separated list of Azure subscription IDs permitted to create Private Endpoints
func TestAzurePrivateTopology(t *testing.T) {
	if globalOpts.Platform != hyperv1.AzurePlatform {
		t.Skip("Skipping test because it requires Azure platform")
	}
	if azureutil.IsAroHCP() {
		t.Skip("Azure private topology is not supported on ARO HCP")
	}

	natSubnetID := os.Getenv("AZURE_PRIVATE_NAT_SUBNET_ID")
	allowedSubscriptionsRaw := os.Getenv("AZURE_PRIVATE_ALLOWED_SUBSCRIPTIONS")
	if natSubnetID == "" || allowedSubscriptionsRaw == "" {
		t.Skip("Skipping Azure Private Topology test: AZURE_PRIVATE_NAT_SUBNET_ID and AZURE_PRIVATE_ALLOWED_SUBSCRIPTIONS environment variables are required")
	}
	allowedSubscriptions := make([]string, 0)
	for _, sub := range strings.Split(allowedSubscriptionsRaw, ",") {
		sub = strings.TrimSpace(sub)
		if sub != "" {
			allowedSubscriptions = append(allowedSubscriptions, sub)
		}
	}
	if len(allowedSubscriptions) == 0 {
		t.Skip("Skipping Azure Private Topology test: no valid subscription IDs parsed from AZURE_PRIVATE_ALLOWED_SUBSCRIPTIONS")
	}

	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)
	clusterOpts.ControlPlaneAvailabilityPolicy = string(hyperv1.SingleReplica)

	// Configure Azure private endpoint access
	clusterOpts.AzurePlatform.EndpointAccess = string(hyperv1.AzureEndpointAccessPrivate)
	clusterOpts.AzurePlatform.PrivateConnectivityNATSubnetID = natSubnetID
	clusterOpts.AzurePlatform.PrivateConnectivityAllowedSubscriptions = allowedSubscriptions

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		controlPlaneNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)

		// Verify KAS Service has internal LB annotation
		t.Run("KASServiceHasInternalLBAnnotation", func(t *testing.T) {
			e2eutil.EventuallyObject(t, ctx, "KAS Service has Azure internal LB annotation",
				func(ctx context.Context) (*corev1.Service, error) {
					svc := &corev1.Service{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: controlPlaneNamespace,
							Name:      "kube-apiserver-private",
						},
					}
					err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(svc), svc)
					return svc, err
				},
				[]e2eutil.Predicate[*corev1.Service]{
					func(svc *corev1.Service) (done bool, reasons string, err error) {
						val, ok := svc.Annotations["service.beta.kubernetes.io/azure-load-balancer-internal"]
						if !ok || val != "true" {
							return false, fmt.Sprintf("expected annotation 'service.beta.kubernetes.io/azure-load-balancer-internal' to be 'true', got %q (present: %v)", val, ok), nil
						}
						return true, "KAS Service has internal LB annotation", nil
					},
				},
				e2eutil.WithTimeout(5*time.Minute),
			)
		})

		// Verify AzurePrivateLinkService CR is created and PLS alias is populated
		t.Run("AzurePrivateLinkServiceCRCreatedWithPLSAlias", func(t *testing.T) {
			e2eutil.EventuallyObjects(t, ctx, "AzurePrivateLinkService CR is created with PLS alias",
				func(ctx context.Context) ([]*hyperv1.AzurePrivateLinkService, error) {
					plsList := &hyperv1.AzurePrivateLinkServiceList{}
					err := mgtClient.List(ctx, plsList, crclient.InNamespace(controlPlaneNamespace))
					if err != nil {
						return nil, err
					}
					items := make([]*hyperv1.AzurePrivateLinkService, len(plsList.Items))
					for i := range plsList.Items {
						items[i] = &plsList.Items[i]
					}
					return items, nil
				},
				[]e2eutil.Predicate[[]*hyperv1.AzurePrivateLinkService]{
					func(items []*hyperv1.AzurePrivateLinkService) (done bool, reasons string, err error) {
						if len(items) == 0 {
							return false, "no AzurePrivateLinkService CRs found in HCP namespace", nil
						}
						return true, fmt.Sprintf("found %d AzurePrivateLinkService CR(s)", len(items)), nil
					},
				},
				[]e2eutil.Predicate[*hyperv1.AzurePrivateLinkService]{
					func(pls *hyperv1.AzurePrivateLinkService) (done bool, reasons string, err error) {
						alias := pls.Status.PrivateLinkServiceAlias
						if alias == "" {
							return false, "PLS alias is empty", nil
						}
						return true, fmt.Sprintf("PLS alias is %q", alias), nil
					},
				},
				e2eutil.WithTimeout(15*time.Minute),
				e2eutil.WithInterval(15*time.Second),
			)
		})

		// Verify Private Endpoint IP is populated in status
		t.Run("PrivateEndpointIPPopulated", func(t *testing.T) {
			e2eutil.EventuallyObjects(t, ctx, "AzurePrivateLinkService has Private Endpoint IP",
				func(ctx context.Context) ([]*hyperv1.AzurePrivateLinkService, error) {
					plsList := &hyperv1.AzurePrivateLinkServiceList{}
					err := mgtClient.List(ctx, plsList, crclient.InNamespace(controlPlaneNamespace))
					if err != nil {
						return nil, err
					}
					items := make([]*hyperv1.AzurePrivateLinkService, len(plsList.Items))
					for i := range plsList.Items {
						items[i] = &plsList.Items[i]
					}
					return items, nil
				},
				[]e2eutil.Predicate[[]*hyperv1.AzurePrivateLinkService]{
					func(items []*hyperv1.AzurePrivateLinkService) (done bool, reasons string, err error) {
						if len(items) == 0 {
							return false, "no AzurePrivateLinkService CRs found", nil
						}
						return true, fmt.Sprintf("found %d AzurePrivateLinkService CR(s)", len(items)), nil
					},
				},
				[]e2eutil.Predicate[*hyperv1.AzurePrivateLinkService]{
					func(pls *hyperv1.AzurePrivateLinkService) (done bool, reasons string, err error) {
						ip := pls.Status.PrivateEndpointIP
						if ip == "" {
							return false, "Private Endpoint IP is empty", nil
						}
						return true, fmt.Sprintf("Private Endpoint IP is %q", ip), nil
					},
				},
				e2eutil.WithTimeout(15*time.Minute),
				e2eutil.WithInterval(15*time.Second),
			)
		})

		// Verify Private DNS Zone ID is populated in status
		t.Run("PrivateDNSZoneIDPopulated", func(t *testing.T) {
			e2eutil.EventuallyObjects(t, ctx, "AzurePrivateLinkService has Private DNS Zone ID",
				func(ctx context.Context) ([]*hyperv1.AzurePrivateLinkService, error) {
					plsList := &hyperv1.AzurePrivateLinkServiceList{}
					err := mgtClient.List(ctx, plsList, crclient.InNamespace(controlPlaneNamespace))
					if err != nil {
						return nil, err
					}
					items := make([]*hyperv1.AzurePrivateLinkService, len(plsList.Items))
					for i := range plsList.Items {
						items[i] = &plsList.Items[i]
					}
					return items, nil
				},
				[]e2eutil.Predicate[[]*hyperv1.AzurePrivateLinkService]{
					func(items []*hyperv1.AzurePrivateLinkService) (done bool, reasons string, err error) {
						if len(items) == 0 {
							return false, "no AzurePrivateLinkService CRs found", nil
						}
						return true, fmt.Sprintf("found %d AzurePrivateLinkService CR(s)", len(items)), nil
					},
				},
				[]e2eutil.Predicate[*hyperv1.AzurePrivateLinkService]{
					func(pls *hyperv1.AzurePrivateLinkService) (done bool, reasons string, err error) {
						zoneID := pls.Status.PrivateDNSZoneID
						if zoneID == "" {
							return false, "Private DNS Zone ID is empty", nil
						}
						return true, fmt.Sprintf("Private DNS Zone ID is %q", zoneID), nil
					},
				},
				e2eutil.WithTimeout(15*time.Minute),
				e2eutil.WithInterval(15*time.Second),
			)
		})
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "azure-private-topology", globalOpts.ServiceAccountSigningKey)
}
