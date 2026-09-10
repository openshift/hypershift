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

package util

import (
	"context"
	"fmt"
	"net"
	"reflect"
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	cpomanifests "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/netutil"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// ValidateKubeAPIServerAllowedCIDRs verifies that HostedCluster API server
// allowed CIDRs are reconciled to the HCP and enforcing LoadBalancer service,
// and that reachability changes accordingly.
func ValidateKubeAPIServerAllowedCIDRs(ctx context.Context, mgmtClient crclient.Client, guestConfig *rest.Config, hc *hyperv1.HostedCluster) (retErr error) {
	var originalAPIServer *hyperv1.APIServerNetworking
	if hc.Spec.Networking.APIServer != nil {
		originalAPIServer = hc.Spec.Networking.APIServer.DeepCopy()
	}
	defer func() {
		restoreErr := UpdateObject(ctx, mgmtClient, hc, func(obj *hyperv1.HostedCluster) {
			if originalAPIServer != nil {
				obj.Spec.Networking.APIServer = originalAPIServer.DeepCopy()
			} else {
				obj.Spec.Networking.APIServer = nil
			}
		})
		if restoreErr != nil {
			retErr = combineErrors(retErr, fmt.Errorf("failed to restore HostedCluster API server CIDRs: %w", restoreErr))
		}

		// The AllowedCIDRs test uses cfg.Dial to create isolated transports, but
		// subsequent tests share the original guestConfig transport. Wait until
		// Azure LB propagation completes CIDR restoration before returning.
		if err := waitForKubeAPIServerReachability(ctx, guestConfig, true, 5*time.Minute, 10*time.Second, false); err != nil {
			retErr = combineErrors(retErr, fmt.Errorf("KAS should be reachable on original transport after CIDR cleanup: %w", err))
		}
	}()

	if err := ensureAPIServerAllowedCIDRs(ctx, mgmtClient, guestConfig, hc, []string{"0.0.0.0/32"}, false); err != nil {
		return err
	}
	return ensureAPIServerAllowedCIDRs(ctx, mgmtClient, guestConfig, hc, append([]string{"0.0.0.0/0"}, generateTestCIDRs250()...), true)
}

func ensureAPIServerAllowedCIDRs(ctx context.Context, mgmtClient crclient.Client, guestConfig *rest.Config, hc *hyperv1.HostedCluster, allowedCIDRs []string, shouldBeReachable bool) error {
	expectedCIDRs := make([]hyperv1.CIDRBlock, len(allowedCIDRs))
	for i, cidr := range allowedCIDRs {
		expectedCIDRs[i] = hyperv1.CIDRBlock(cidr)
	}

	if err := UpdateObject(ctx, mgmtClient, hc, func(obj *hyperv1.HostedCluster) {
		if obj.Spec.Networking.APIServer == nil {
			obj.Spec.Networking.APIServer = &hyperv1.APIServerNetworking{}
		}
		obj.Spec.Networking.APIServer.AllowedCIDRBlocks = expectedCIDRs
	}); err != nil {
		return fmt.Errorf("failed to update HostedCluster with allowed CIDRs: %w", err)
	}

	hcpNamespace := manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name)
	if err := EventuallyObject(ctx, "HostedControlPlane to reflect the updated AllowedCIDRBlocks",
		func(ctx context.Context) (*hyperv1.HostedControlPlane, error) {
			hcp := &hyperv1.HostedControlPlane{}
			err := mgmtClient.Get(ctx, crclient.ObjectKey{Namespace: hcpNamespace, Name: hc.Name}, hcp)
			if err != nil {
				return hcp, err
			}
			if hcp.Spec.Networking.APIServer == nil || !reflect.DeepEqual(hcp.Spec.Networking.APIServer.AllowedCIDRBlocks, expectedCIDRs) {
				// The HO may read a stale HC cache. Touch the HC to force a new
				// watch event and reconciliation with the current spec.
				_ = UpdateObject(ctx, mgmtClient, hc, func(obj *hyperv1.HostedCluster) {
					if obj.Annotations == nil {
						obj.Annotations = map[string]string{}
					}
					obj.Annotations["e2e.hypershift.openshift.io/cidr-trigger"] = time.Now().Format(time.RFC3339)
				})
			}
			return hcp, nil
		},
		[]e2eutil.Predicate[*hyperv1.HostedControlPlane]{
			func(hcp *hyperv1.HostedControlPlane) (bool, string, error) {
				if hcp.Spec.Networking.APIServer == nil {
					return false, "HCP APIServer networking should be set", nil
				}
				if !reflect.DeepEqual(hcp.Spec.Networking.APIServer.AllowedCIDRBlocks, expectedCIDRs) {
					return false, "HCP AllowedCIDRBlocks do not match the HostedCluster spec", nil
				}
				return true, "HCP AllowedCIDRBlocks match the HostedCluster spec", nil
			},
		},
		WithTimeout(3*time.Minute), WithInterval(5*time.Second),
	); err != nil {
		return err
	}

	// The target service depends on the API server publishing strategy:
	// Route: the router LB service carries the CIDRs; LoadBalancer: the KAS LB
	// service itself carries them.
	targetSvc := allowedCIDRsTargetService(hc, hcpNamespace)
	if targetSvc != nil {
		expectedSourceRanges := slices.Clone(allowedCIDRs)
		slices.Sort(expectedSourceRanges)
		GinkgoWriter.Printf("Waiting for service %s/%s LoadBalancerSourceRanges to match %d CIDRs\n", targetSvc.Namespace, targetSvc.Name, len(expectedSourceRanges))
		if err := EventuallyObject(ctx, fmt.Sprintf("service %s/%s LoadBalancerSourceRanges to match expected CIDRs", targetSvc.Namespace, targetSvc.Name),
			func(ctx context.Context) (*corev1.Service, error) {
				svc := &corev1.Service{}
				err := mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(targetSvc), svc)
				return svc, err
			},
			[]e2eutil.Predicate[*corev1.Service]{
				func(svc *corev1.Service) (bool, string, error) {
					actualSourceRanges := slices.Clone(svc.Spec.LoadBalancerSourceRanges)
					slices.Sort(actualSourceRanges)
					if reflect.DeepEqual(actualSourceRanges, expectedSourceRanges) {
						return true, "LoadBalancerSourceRanges match expected CIDRs", nil
					}
					return false, fmt.Sprintf("LoadBalancerSourceRanges are %v, want %v", actualSourceRanges, expectedSourceRanges), nil
				},
			},
			WithTimeout(3*time.Minute), WithInterval(5*time.Second),
		); err != nil {
			return err
		}
	} else {
		GinkgoWriter.Println("No downstream LB service identified for this cluster configuration; skipping LoadBalancerSourceRanges wait")
	}

	if err := waitForKubeAPIServerReachability(ctx, guestConfig, shouldBeReachable, 3*time.Minute, 5*time.Second, true); err != nil {
		return err
	}
	return nil
}

func waitForKubeAPIServerReachability(ctx context.Context, guestConfig *rest.Config, shouldBeReachable bool, timeout, interval time.Duration, freshTransport bool) error {
	err := wait.PollUntilContextTimeout(ctx, interval, timeout, true, func(ctx context.Context) (bool, error) {
		cfg := guestConfig
		if freshTransport {
			cfg = rest.CopyConfig(guestConfig)
			cfg.Dial = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext
		}
		client, err := kubeclient.NewForConfig(cfg)
		if err != nil {
			GinkgoWriter.Printf("failed to create kubeclient: %v\n", err)
			return false, nil
		}
		_, err = client.ServerVersion()
		if shouldBeReachable {
			if err == nil {
				return true, nil
			}
		} else if err != nil {
			// An error is the expected result when checking that the API is unreachable.
			return true, nil //nolint:nilerr
		}
		return false, nil
	})
	if err != nil {
		if shouldBeReachable {
			return fmt.Errorf("failed waiting for kube-apiserver to be reachable: %w", err)
		}
		return fmt.Errorf("failed waiting for kube-apiserver to be unreachable: %w", err)
	}
	return nil
}

func allowedCIDRsTargetService(hc *hyperv1.HostedCluster, hcpNamespace string) *corev1.Service {
	if !netutil.IsPublicHC(hc) {
		return nil
	}
	strategy := netutil.ServicePublishingStrategyByTypeByHC(hc, hyperv1.APIServer)
	if strategy == nil {
		return nil
	}
	switch strategy.Type {
	case hyperv1.Route:
		if azureutil.IsAroHCP() {
			return nil
		}
		return cpomanifests.RouterPublicService(hcpNamespace)
	case hyperv1.LoadBalancer:
		if hc.Spec.Platform.Type == hyperv1.AzurePlatform ||
			(hc.Annotations != nil && hc.Annotations[hyperv1.ManagementPlatformAnnotation] == string(hyperv1.AzurePlatform)) {
			return cpomanifests.KubeAPIServerServiceAzureLB(hcpNamespace)
		}
		return cpomanifests.KubeAPIServerService(hcpNamespace)
	default:
		return nil
	}
}

func generateTestCIDRs250() []string {
	cidrs := make([]string, 0, 250)
	for i := 1; i <= 250; i++ {
		cidrs = append(cidrs, fmt.Sprintf("250.250.250.%d/32", i))
	}
	return cidrs
}

func combineErrors(first, second error) error {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}
	return fmt.Errorf("%w; %w", first, second)
}
