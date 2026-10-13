//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hcpmanifests "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/support/azureutil"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	corev1 "k8s.io/api/core/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// TestAzureOAuthLoadBalancer validates that the OAuth server can be published via a LoadBalancer
// Service on a public self-managed Azure cluster. It verifies:
//   - The oauth-openshift Service is created with type LoadBalancer
//   - The Service gets an external IP allocated by Azure
//   - The OAuth token flow works through the LoadBalancer endpoint
func TestAzureOAuthLoadBalancer(t *testing.T) {
	if globalOpts.Platform != hyperv1.AzurePlatform {
		t.Skip("Skipping test because it requires Azure platform")
	}
	if azureutil.IsAroHCP() {
		t.Skip("OAuth LoadBalancer publishing strategy is not supported on ARO HCP")
	}

	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)
	clusterOpts.ControlPlaneAvailabilityPolicy = string(hyperv1.SingleReplica)
	clusterOpts.AzurePlatform.OAuthPublishingStrategy = "LoadBalancer"

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		controlPlaneNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)

		// Verify the oauth-openshift Service is type LoadBalancer and has an external IP
		t.Run("OAuthServiceIsLoadBalancerWithExternalIP", func(t *testing.T) {
			e2eutil.EventuallyObject(t, ctx, "oauth-openshift Service is LoadBalancer with external IP",
				func(ctx context.Context) (*corev1.Service, error) {
					svc := hcpmanifests.OauthServerService(controlPlaneNamespace)
					err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(svc), svc)
					return svc, err
				},
				[]e2eutil.Predicate[*corev1.Service]{
					func(svc *corev1.Service) (done bool, reasons string, err error) {
						if svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
							return false, fmt.Sprintf("expected Service type LoadBalancer, got %s", svc.Spec.Type), nil
						}
						return true, "oauth-openshift Service is type LoadBalancer", nil
					},
					func(svc *corev1.Service) (done bool, reasons string, err error) {
						if len(svc.Status.LoadBalancer.Ingress) == 0 {
							return false, "LoadBalancer has no ingress entries yet", nil
						}
						ingress := svc.Status.LoadBalancer.Ingress[0]
						if ingress.IP == "" && ingress.Hostname == "" {
							return false, "LoadBalancer ingress has no IP or hostname", nil
						}
						host := ingress.IP
						if host == "" {
							host = ingress.Hostname
						}
						return true, fmt.Sprintf("oauth-openshift LoadBalancer has external endpoint: %s", host), nil
					},
				},
				e2eutil.WithTimeout(10*time.Minute),
			)
		})

		// Verify the OAuth token flow works through the LoadBalancer
		t.Run("OAuthTokenFlowThroughLoadBalancer", func(t *testing.T) {
			e2eutil.EnsureOAuthWithIdentityProviderViaLoadBalancer(t, ctx, mgtClient, hostedCluster)
		})
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "azure-oauth-lb", globalOpts.ServiceAccountSigningKey)
}
