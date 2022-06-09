package util

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"

	routev1 "github.com/openshift/api/route/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
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

func createKubeVirtClusterWildcardRoute(t *testing.T, ctx context.Context, client crclient.Client, guestClient crclient.Client, hostedCluster *hyperv1.HostedCluster, baseDomain string) {

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

func renderEchoPod(namespace string) *corev1.Pod {
	echoPod := &corev1.Pod{}
	echoPod.Name = "http-echo"
	echoPod.Namespace = namespace
	echoPod.ObjectMeta.Labels = map[string]string{
		"app": "http-echo",
	}
	echoPod.Spec.Containers = []corev1.Container{
		{
			Name:  "echo-pod",
			Image: "quay.io/dvossel/http-echo:v0.2.4",
			Args: []string{
				"-text=echo",
			},
		},
	}
	return echoPod
}

func renderEchoNodePortService(nodePort int, namespace string) *corev1.Service {

	service := &corev1.Service{}
	service.Name = "echo-service"
	service.Namespace = namespace
	service.Spec = corev1.ServiceSpec{
		Type: "NodePort",
		Selector: map[string]string{
			"app": "http-echo",
		},
		Ports: []corev1.ServicePort{
			{
				// default port for the echo container
				Port:     5678,
				NodePort: int32(nodePort),
			},
		},
	}

	return service
}

func renderCurlJob(namespace string, addresses []string, hostNet bool, onGuestCluster bool) *batchv1.Job {
	template := corev1.PodSpec{
		HostNetwork:   hostNet,
		Containers:    []corev1.Container{},
		RestartPolicy: "Never",
	}

	if onGuestCluster {
		// This makes sure we schedule the curl pod on a node separate from the echo pod.
		// This is important because we've had connectivity issues that are covered up
		// when cross guest node connectivity is not exercised.
		template.Affinity = &corev1.Affinity{
			PodAntiAffinity: &corev1.PodAntiAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "app",
									Values:   []string{"http-echo"},
									Operator: metav1.LabelSelectorOpIn,
								},
							},
						},
						TopologyKey: "kubernetes.io/hostname",
					},
				},
			},
		}
	} else {
		nodePoolNameLabelKey := "hypershift.kubevirt.io/node-pool-name"

		// this makes sure we schedule the curl pod on an infra node that
		// is not running a KubeVirt VMI. This is necessary because we have
		// had connectivity issues which are hidden when the endpoint attempting
		// to contact the guest is on the same infra node the guest VM exists on.
		template.Affinity = &corev1.Affinity{
			PodAntiAffinity: &corev1.PodAntiAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      nodePoolNameLabelKey,
									Operator: metav1.LabelSelectorOpExists,
								},
							},
						},
						TopologyKey: "kubernetes.io/hostname",
					},
				},
			},
		}
	}

	for i, addr := range addresses {
		template.Containers = append(template.Containers, corev1.Container{
			Name:  fmt.Sprintf("curl-%d", i),
			Image: "fedora:35",
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("100Mi"),
					corev1.ResourceCPU:    resource.MustParse("10m"),
				},
			},
			Command: []string{
				"curl",
				addr,
			},
		})
	}

	backoff := int32(4)
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      "nodeport-curl-test-hostnetwork",
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoff,
			Template: corev1.PodTemplateSpec{
				Spec: template,
			},
		},
	}
	return job
}

func hasJobSucceeded(t *testing.T, ctx context.Context, job *batchv1.Job, guestClient crclient.Client) bool {

	updatedJob := &batchv1.Job{}
	err := guestClient.Get(ctx, crclient.ObjectKeyFromObject(job), updatedJob)
	if err != nil {
		t.Errorf("Failed to get job: %v", err)
		return false
	}

	for _, condition := range updatedJob.Status.Conditions {
		if condition.Type == batchv1.JobComplete && condition.Status == corev1.ConditionTrue {
			t.Logf("Guest NodePort connectivity test passed from guest host network")
			return true
		}
	}
	return false
}

func verifyGuestNodePortConnectivity(t *testing.T, ctx context.Context, client crclient.Client, guestClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {

	g := NewWithT(t)

	nodePort := 32700

	guestNamespace := "default"
	infraNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name).Name

	guestNodes := &corev1.NodeList{}
	err := guestClient.List(ctx, guestNodes)
	g.Expect(err).NotTo(HaveOccurred(), "failed to list guest nodes")

	curlAddresses := []string{}
	for _, node := range guestNodes.Items {
		ip := ""
		for _, addr := range node.Status.Addresses {
			if addr.Type == corev1.NodeInternalIP {
				ip = addr.Address
			}
		}
		g.Expect(ip).NotTo(Equal(""), fmt.Sprintf("no internal ip found for guest node %s", node.Name))
		curlAddresses = append(curlAddresses, fmt.Sprintf("%s:%d", ip, nodePort))
	}

	// This echo pod simply exists to return a successful 200ok
	echoPod := renderEchoPod(guestNamespace)

	// This nodeport service routes to the echo pod.
	service := renderEchoNodePortService(nodePort, guestNamespace)

	// This job executes a curl command that ensures every guest cluster
	// node ip can be used successfully to connect to the echo pod through
	// the nodeport service.
	guestCurlJob := renderCurlJob(guestNamespace, curlAddresses, true, true)

	// This job executes a curl command that ensure every guest cluster
	// node ip can be used successfully from the infra cluster pod network.
	infraCurlJob := renderCurlJob(infraNamespace, curlAddresses, false, false)

	err = guestClient.Create(ctx, echoPod)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create echo pod")
	defer guestClient.Delete(ctx, echoPod)

	err = guestClient.Create(ctx, service)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create echo nodeport")
	defer guestClient.Delete(ctx, service)

	err = guestClient.Create(ctx, guestCurlJob)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create guest echo curl test job")
	defer guestClient.Delete(ctx, guestCurlJob)

	err = client.Create(ctx, infraCurlJob)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create infra echo curl test job")
	defer client.Delete(ctx, infraCurlJob)

	t.Logf("waiting on guest cluster pod network to nodeport service connectivity")
	err = wait.PollImmediateWithContext(ctx, 10*time.Second, 5*time.Minute, func(ctx context.Context) (done bool, err error) {
		return hasJobSucceeded(t, ctx, guestCurlJob, guestClient), nil
	})
	g.Expect(err).NotTo(HaveOccurred(), "curl pod failed connectivity tests for NodePort from guest host network")
	t.Logf("Success: guest cluster pod network to nodeport service connectivity")

	t.Logf("waiting on infra cluster host network to guest cluster nodeport service connectivity")
	err = wait.PollImmediateWithContext(ctx, 10*time.Second, 5*time.Minute, func(ctx context.Context) (done bool, err error) {

		return hasJobSucceeded(t, ctx, infraCurlJob, client), nil
	})
	g.Expect(err).NotTo(HaveOccurred(), "curl pod failed connectivity tests for NodePort from infra node pod network")

	t.Logf("Success: infra cluster pod network to nodeport service connectivity")
}
