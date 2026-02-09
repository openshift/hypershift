package router

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

// adaptDeployment adds the Azure DNS HTTP CONNECT proxy sidecar when Swift networking is enabled
func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	hcp := cpContext.HCP

	// Add Azure DNS proxy sidecar when Swift networking is enabled
	// The proxy will be used by Azure KMS and other containers via HTTP_PROXY environment variable
	if swiftPodNetworkInstance := hcp.Annotations[hyperv1.SwiftPodNetworkInstanceAnnotation]; swiftPodNetworkInstance != "" {
		// Add Swift pod network instance label to router pod
		if deployment.Spec.Template.Labels == nil {
			deployment.Spec.Template.Labels = map[string]string{}
		}
		deployment.Spec.Template.Labels["kubernetes.azure.com/pod-network-instance"] = swiftPodNetworkInstance

		// Add Azure DNS proxy sidecar
		addAzureDNSProxySidecar(deployment)
	}

	return nil
}

// addAzureDNSProxySidecar adds the HTTP CONNECT proxy sidecar to the router deployment
// This proxy resolves Azure domains via Azure DNS (168.63.129.16) and enables access to Private Link endpoints
func addAzureDNSProxySidecar(deployment *appsv1.Deployment) {
	sidecar := corev1.Container{
		Name:  "azure-dns-proxy",
		Image: "azure-dns-proxy",
		Args: []string{
			"--listen-addr=0.0.0.0:8888",
			"--request-timeout=30s",
			"--connect-timeout=10s",
			"--tunnel-idle-timeout=5m",
		},
		Ports: []corev1.ContainerPort{
			{
				Name:          "proxy",
				ContainerPort: 8888,
				Protocol:      corev1.ProtocolTCP,
			},
		},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10m"),
				corev1.ResourceMemory: resource.MustParse("20Mi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100m"),
				corev1.ResourceMemory: resource.MustParse("50Mi"),
			},
		},
		LivenessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path:   "/",
					Port:   intstr.FromInt(8888),
					Scheme: corev1.URISchemeHTTP,
				},
			},
			InitialDelaySeconds: 10,
			PeriodSeconds:       10,
			TimeoutSeconds:      1,
			SuccessThreshold:    1,
			FailureThreshold:    3,
		},
		ReadinessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path:   "/",
					Port:   intstr.FromInt(8888),
					Scheme: corev1.URISchemeHTTP,
				},
			},
			InitialDelaySeconds: 5,
			PeriodSeconds:       5,
			TimeoutSeconds:      1,
			SuccessThreshold:    1,
			FailureThreshold:    3,
		},
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: ptr.To(false),
			Capabilities: &corev1.Capabilities{
				Drop: []corev1.Capability{"ALL"},
			},
			RunAsNonRoot: ptr.To(true),
		},
	}

	deployment.Spec.Template.Spec.Containers = append(deployment.Spec.Template.Spec.Containers, sidecar)
}
