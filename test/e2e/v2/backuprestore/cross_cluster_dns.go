//go:build e2ev2 && backuprestore

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

package backuprestore

import (
	"context"
	"fmt"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	externalDNSNamespace      = "hypershift"
	externalDNSDeploymentName = "external-dns"
)

// PublicServiceRouteEndpoint identifies the configured Route name and hostname for a published service.
type PublicServiceRouteEndpoint struct {
	Service   hyperv1.ServiceType
	DNSName   string
	RouteName string
}

// ScaleDownExternalDNS scales the ExternalDNS Deployment to zero and waits for its pods to stop.
func ScaleDownExternalDNS(testCtx *internal.TestContext) error {
	return scaleExternalDNS(testCtx, 0)
}

// ScaleUpExternalDNS scales the ExternalDNS Deployment to one and waits for its pod to become ready.
func ScaleUpExternalDNS(testCtx *internal.TestContext) error {
	return scaleExternalDNS(testCtx, 1)
}

func scaleExternalDNS(testCtx *internal.TestContext, replicas int32) error {
	deployment := &appsv1.Deployment{}
	key := crclient.ObjectKey{Namespace: externalDNSNamespace, Name: externalDNSDeploymentName}
	if err := testCtx.MgmtClient.Get(testCtx.Context, key, deployment); err != nil {
		return fmt.Errorf("failed to get ExternalDNS Deployment %s/%s: %w", key.Namespace, key.Name, err)
	}
	if deployment.Spec.Selector == nil {
		return fmt.Errorf("ExternalDNS Deployment %s/%s has no pod selector", key.Namespace, key.Name)
	}

	if deployment.Spec.Replicas == nil || *deployment.Spec.Replicas != replicas {
		deployment.Spec.Replicas = &replicas
		if err := testCtx.MgmtClient.Update(testCtx.Context, deployment); err != nil {
			return fmt.Errorf("failed to scale ExternalDNS Deployment %s/%s to %d replicas: %w", key.Namespace, key.Name, replicas, err)
		}
	}

	selector, err := metav1.LabelSelectorAsSelector(deployment.Spec.Selector)
	if err != nil {
		return fmt.Errorf("failed to build ExternalDNS pod selector: %w", err)
	}

	err = wait.PollUntilContextTimeout(testCtx.Context, 5*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
		pods := &corev1.PodList{}
		if err := testCtx.MgmtClient.List(ctx, pods, crclient.InNamespace(key.Namespace), crclient.MatchingLabelsSelector{Selector: selector}); err != nil {
			return false, fmt.Errorf("failed to list ExternalDNS pods: %w", err)
		}
		if len(pods.Items) != int(replicas) {
			return false, nil
		}
		for _, pod := range pods.Items {
			if pod.DeletionTimestamp != nil || pod.Status.Phase != corev1.PodRunning {
				return false, nil
			}
			ready := false
			for _, condition := range pod.Status.Conditions {
				if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
					ready = true
					break
				}
			}
			if !ready {
				return false, nil
			}
		}
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("ExternalDNS pods did not reach %d ready replicas: %w", replicas, err)
	}

	return nil
}

// PublicServiceRouteEndpoints returns the configured Route names and hostnames for public services.
func PublicServiceRouteEndpoints(hostedCluster *hyperv1.HostedCluster) ([]PublicServiceRouteEndpoint, error) {
	if hostedCluster == nil {
		return nil, fmt.Errorf("HostedCluster is nil")
	}

	routeNames := map[hyperv1.ServiceType]string{
		hyperv1.APIServer:    "kube-apiserver",
		hyperv1.OAuthServer:  "oauth",
		hyperv1.Konnectivity: "konnectivity-server",
		hyperv1.Ignition:     "ignition-server",
	}
	required := map[hyperv1.ServiceType]bool{
		hyperv1.APIServer:   false,
		hyperv1.OAuthServer: false,
	}

	endpoints := make([]PublicServiceRouteEndpoint, 0, len(hostedCluster.Spec.Services))
	for _, service := range hostedCluster.Spec.Services {
		if service.ServicePublishingStrategy.Type != hyperv1.Route || service.Route == nil || service.Route.Hostname == "" {
			continue
		}
		routeName, ok := routeNames[service.Service]
		if !ok {
			continue
		}
		required[service.Service] = true
		endpoints = append(endpoints, PublicServiceRouteEndpoint{
			Service:   service.Service,
			DNSName:   service.Route.Hostname,
			RouteName: routeName,
		})
	}

	for service, found := range required {
		if !found {
			return nil, fmt.Errorf("HostedCluster %s/%s must configure %s with a Route hostname for cross-cluster Route admission verification", hostedCluster.Namespace, hostedCluster.Name, service)
		}
	}
	return endpoints, nil
}
