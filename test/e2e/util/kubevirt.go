package util

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"

	routev1 "github.com/openshift/api/route/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
)

func WaitForKubeVirtMachines(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster, count int32) {
	g := NewWithT(t)
	start := time.Now()

	t.Logf("Waiting for %d kubevirt machines to come online", count)

	namespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name).Name

	err := wait.PollUntil(5*time.Second, func() (done bool, err error) {
		var kvMachineList capikubevirt.KubevirtMachineList

		err = client.List(ctx, &kvMachineList, crclient.InNamespace(namespace))
		if err != nil {
			t.Errorf("Failed to list KubeVirtMachines: %v", err)
			return false, nil
		}

		readyCount := 0
		for _, machine := range kvMachineList.Items {
			if machine.Status.Ready {
				readyCount++
			}
		}
		if int32(readyCount) < count {
			return false, nil
		}

		return true, nil
	}, ctx.Done())
	g.Expect(err).NotTo(HaveOccurred(), "timeout waiting for kubevirt machines to become ready")

	t.Logf("KubeVirtMachines are ready in %s", time.Since(start).Round(time.Second))
}

func WaitForKubeVirtCluster(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	g := NewWithT(t)
	start := time.Now()

	namespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name).Name

	t.Logf("Waiting for kubevirt cluster to come online")
	err := wait.PollUntil(5*time.Second, func() (done bool, err error) {
		var kvClusterList capikubevirt.KubevirtClusterList

		err = client.List(ctx, &kvClusterList, crclient.InNamespace(namespace))
		if err != nil {
			t.Errorf("Failed to list KubeVirtClusters: %v", err)
			return false, nil
		}

		if len(kvClusterList.Items) == 0 {
			// waiting on kubevirt cluster to be posted
			return false, nil
		}

		return kvClusterList.Items[0].Status.Ready, nil
	}, ctx.Done())
	g.Expect(err).NotTo(HaveOccurred(), "timeout waiting for kubevirt cluster to become ready")

	t.Logf("KubeVirtCluster is ready in %s", time.Since(start).Round(time.Second))
}

func CreateKubeVirtClusterWildcardRoute(t *testing.T, ctx context.Context, client crclient.Client, guestClient crclient.Client, hostedCluster *hyperv1.HostedCluster, baseDomain string) {

	g := NewWithT(t)

	// manifests for default ingress nodeport on guest cluster
	defaultIngressNodePortService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "router-nodeport-default",
			Namespace: "openshift-ingress",
		},
	}

	detectedHTTPSNodePort := int32(0)
	err := guestClient.Get(ctx, crclient.ObjectKeyFromObject(defaultIngressNodePortService), defaultIngressNodePortService)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get guest's default ingress NodePort service.")
	for _, port := range defaultIngressNodePortService.Spec.Ports {
		if port.Port == 443 {
			detectedHTTPSNodePort = port.NodePort
			break
		}
	}
	g.Expect(detectedHTTPSNodePort).NotTo(Equal(0), "failed to detect port for default ingress router's https node port service")

	hcpNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name).Name

	// Manifests for clusterIP on management cluster
	cpService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default-ingress",
			Namespace: hcpNamespace,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name:       "https-443",
					Protocol:   corev1.ProtocolTCP,
					Port:       443,
					TargetPort: intstr.FromInt(int(detectedHTTPSNodePort)),
				},
			},
			Selector: map[string]string{
				"kubevirt.io": "virt-launcher",
			},
			Type: corev1.ServiceTypeClusterIP,
		},
	}
	// Manifests for route
	cpRoute := &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default-ingress",
			Namespace: hcpNamespace,
		},
		Spec: routev1.RouteSpec{
			Host:           fmt.Sprintf("data.apps.%s.%s", hostedCluster.Name, baseDomain),
			WildcardPolicy: routev1.WildcardPolicySubdomain,
			TLS: &routev1.TLSConfig{
				Termination: routev1.TLSTerminationPassthrough,
			},
			Port: &routev1.RoutePort{
				TargetPort: intstr.FromString("https-443"),
			},
			To: routev1.RouteTargetReference{
				Kind: "Service",
				Name: cpService.Name,
			},
		},
	}

	t.Logf("Created mgmt service for default tenant cluster ingress")
	err = client.Create(ctx, cpService)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create guest clusters default apps service on mgmt cluster")

	t.Logf("Created mgmt route for default tenant cluster ingress")
	err = client.Create(ctx, cpRoute)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create guest clusters default apps route on mgmt cluster")
}
