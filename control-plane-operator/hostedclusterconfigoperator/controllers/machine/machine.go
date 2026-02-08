package machine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hcpmanifests "github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/support/capabilities"
	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kubevirtv1 "kubevirt.io/api/core/v1"
)

const (
	managedByValue = "control-plane-operator.hypershift.openshift.io"

	// OVN-Kubernetes UDN EndpointSlice plumbing.
	ovnUDNServiceNameLabelKey         = "k8s.ovn.org/service-name"
	ovnUDNEndpointSliceNetworkAnnoKey = "k8s.ovn.org/endpointslice-network"
	ovnPodNetworksAnnotationKey       = "k8s.ovn.org/pod-networks"
	ovnPrimaryRole                    = "primary"
)

func (r *reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling")

	hcp := &hyperv1.HostedControlPlane{}
	if err := r.client.Get(ctx, r.hcpKey, hcp); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("not found", "HostedControlPlane", r.hcpKey)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get HostedControlPlane: %w", err)
	}
	switch hcp.Spec.Platform.Type {
	case hyperv1.KubevirtPlatform:
		if hcp.Spec.Platform.Kubevirt != nil {
			kubevirtPassthroughServices, err := r.findKubevirtPassthroughServices(ctx, hcp)
			if err != nil {
				return reconcile.Result{}, fmt.Errorf("failed finding kubevirt passthrough services: %w", err)
			}
			for _, kubevirtPassthroughService := range kubevirtPassthroughServices {
				if err := r.reconcileKubevirtPassthroughService(ctx, hcp, req.NamespacedName, &kubevirtPassthroughService); err != nil {
					return reconcile.Result{}, fmt.Errorf("failed reconciling kubevirt infra passthrough service endpoint slices: %w", err)
				}
			}
			if err := r.removeOrphanKubevirtPassthroughEndpointSlices(ctx, hcp); err != nil {
				return reconcile.Result{}, fmt.Errorf("failed removing orphan kubevirt passthrough endpoint slices: %w", err)
			}
		}
	}
	log.Info("Reconciled Machine")
	return reconcile.Result{}, nil
}

func (r *reconciler) findKubevirtPassthroughServices(ctx context.Context, hcp *hyperv1.HostedControlPlane) ([]corev1.Service, error) {
	kubevirtPassthroughServices := []corev1.Service{}

	// Add ingress passthrough service only if IngressCapability is enabled
	if capabilities.IsIngressCapabilityEnabled(hcp.Spec.Capabilities) {
		// Manifests for infra/mgmt cluster passthrough service
		cpService := hcpmanifests.IngressDefaultIngressPassthroughService(kubevirtInfraNamespace(hcp))

		cpService.Name = fmt.Sprintf("%s-%s",
			hcpmanifests.IngressDefaultIngressPassthroughServiceName,
			hcp.Spec.Platform.Kubevirt.GenerateID)

		err := r.kubevirtInfraClient.Get(ctx, client.ObjectKeyFromObject(cpService), cpService)
		if err != nil && !apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("failed to get default ingress passthrough Service: %w", err)
		}
		if !apierrors.IsNotFound(err) {
			kubevirtPassthroughServices = append(kubevirtPassthroughServices, *cpService)
		}
	}

	kccmServiceList := &corev1.ServiceList{}
	if err := r.kubevirtInfraClient.List(ctx, kccmServiceList, client.InNamespace(kubevirtInfraNamespace(hcp)),
		client.HasLabels{tenantServiceNameLabelKey},
		client.MatchingLabels{clusterNameLabelKey: hcp.Labels[clusterNameLabelKey]},
	); err != nil {
		return nil, fmt.Errorf("failed listing kccm services: %w", err)
	}
	kubevirtPassthroughServices = append(kubevirtPassthroughServices, kccmServiceList.Items...)
	return kubevirtPassthroughServices, nil
}

func (r *reconciler) reconcileKubevirtPassthroughService(ctx context.Context, hcp *hyperv1.HostedControlPlane, machineKey types.NamespacedName, cpService *corev1.Service) error {
	log := ctrl.LoggerFrom(ctx)

	// If there is a selector endpoints should not be generated
	if len(cpService.Spec.Selector) > 0 {
		return nil
	}

	if len(cpService.Spec.Ports) == 0 {
		return fmt.Errorf("missing default ingress passthrough Service %s/%s ports", cpService.Namespace, cpService.Name)
	}

	machine := &capiv1.Machine{}
	if err := r.client.Get(ctx, client.ObjectKey(machineKey), machine); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("not found", "Machine", r)
			return nil
		}
		return fmt.Errorf("failed to get Machine: %w", err)
	}

	ipv4MachineAddresses := []string{}
	ipv6MachineAddresses := []string{}
	ports := []discoveryv1.EndpointPort{}

	for _, machineAddress := range machine.Status.Addresses {
		if machineAddress.Type == capiv1.MachineInternalIP {
			parsedAddr, err := netip.ParseAddr(machineAddress.Address)
			if err != nil {
				return fmt.Errorf("parsing machine address (%s) in machine %s: %w", machineAddress.Address, machine.Name, err)
			}
			if parsedAddr.Is4() {
				ipv4MachineAddresses = append(ipv4MachineAddresses, machineAddress.Address)
			} else {
				ipv6MachineAddresses = append(ipv6MachineAddresses, machineAddress.Address)
			}
		}
	}
	for _, port := range cpService.Spec.Ports {
		ports = append(ports, discoveryv1.EndpointPort{
			Name:     &port.Name,
			Protocol: &port.Protocol,
			Port:     ptr.To(int32(port.TargetPort.IntValue())),
		})
	}

	ipAddressesByFamily := map[corev1.IPFamily][]string{
		corev1.IPv4Protocol: ipv4MachineAddresses,
		corev1.IPv6Protocol: ipv6MachineAddresses,
	}
	for _, ipFamily := range []corev1.IPFamily{corev1.IPv4Protocol, corev1.IPv6Protocol} {
		if !serviceHasIPFamily(cpService, ipFamily) {
			continue
		}
		err := r.reconcileKubevirtPassthroughServiceEndpointsByIPFamily(ctx, machine, cpService, ipFamily, ipAddressesByFamily[ipFamily], ports)
		if err != nil {
			if apierrors.IsNotFound(err) {
				log.Info(fmt.Sprintf("waiting for kubevirt VM to be created before processing default ingress %s endpoints", ipFamily))
				return nil
			} else {
				return fmt.Errorf("failed to reconcile kubevirt default ingress %s endpoints: %w", ipFamily, err)
			}
		}
	}
	return nil
}

func (r *reconciler) reconcileKubevirtPassthroughServiceEndpointsByIPFamily(ctx context.Context, machine *capiv1.Machine, cpService *corev1.Service, ipFamily corev1.IPFamily, machineAddresses []string, ports []discoveryv1.EndpointPort) error {
	log := ctrl.LoggerFrom(ctx)
	endpointSlice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: cpService.Namespace,
			Name:      cpService.Name + "-" + machine.Name + "-" + strings.ToLower(string(ipFamily)),
		},
	}
	result, err := r.CreateOrUpdate(ctx, r.kubevirtInfraClient, endpointSlice, func() error {
		if len(endpointSlice.OwnerReferences) == 0 {
			// Machine infra ref is the KubevirtMachine which has the same name
			// as the kubevirt VirtualMachine CRD, but the namespace ca
			// can be different if kubevirt infra cluster is external
			vmKey := client.ObjectKey{
				Namespace: cpService.Namespace,
				Name:      machine.Spec.InfrastructureRef.Name,
			}
			vm := &kubevirtv1.VirtualMachine{}
			if err := r.kubevirtInfraClient.Get(ctx, vmKey, vm); err != nil {
				return err
			}
			ownerRef := config.OwnerRefFrom(vm)
			ownerRef.ApplyTo(endpointSlice)
		}

		if endpointSlice.Labels == nil {
			endpointSlice.Labels = map[string]string{}
		}
		endpointSlice.Labels[discoveryv1.LabelManagedBy] = managedByValue

		udnNetworkName, isPrimaryUDN := r.primaryUDNNetworkName(ctx, cpService.Namespace)

		if isPrimaryUDN {
			if endpointSlice.Annotations == nil {
				endpointSlice.Annotations = map[string]string{}
			}
			endpointSlice.Labels[ovnUDNServiceNameLabelKey] = cpService.Name
			endpointSlice.Annotations[ovnUDNEndpointSliceNetworkAnnoKey] = udnNetworkName
			delete(endpointSlice.Labels, discoveryv1.LabelServiceName) // remove kubernetes.io/service-name
		} else {
			endpointSlice.Labels[discoveryv1.LabelServiceName] = cpService.Name
			delete(endpointSlice.Labels, ovnUDNServiceNameLabelKey)
			if endpointSlice.Annotations != nil {
				delete(endpointSlice.Annotations, ovnUDNEndpointSliceNetworkAnnoKey)
			}
		}

		endpointSlice.AddressType = discoveryv1.AddressType(ipFamily)
		if len(machineAddresses) > 0 {
			endpointSlice.Endpoints = []discoveryv1.Endpoint{{
				Addresses:  machineAddresses,
				Conditions: machinePhaseToEndpointConditions(machine),
			}}
		} else {
			endpointSlice.Endpoints = []discoveryv1.Endpoint{}
		}
		endpointSlice.Ports = ports
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile kubevirt default ingress %s endpoints: %w", ipFamily, err)
	}
	log.Info(fmt.Sprintf("Reconciled kubevirt default ingress %s endpoints", ipFamily), "result", result)
	return nil
}

type podNetworksAnnotationEntry struct {
	Role string `json:"role"`
}

// primaryUDNNetworkName determines if the namespace is using a Primary UDN network and returns the OVN-K
// endpointslice-network value (<namespace>_<udnName>) when it is.
//
// Note: HCCO runs with namespaced RBAC, so it cannot reliably read Namespace labels (cluster-scoped).
// Instead, we infer Primary UDN by inspecting the OVN pod-networks annotation on virt-launcher pods.
func (r *reconciler) primaryUDNNetworkName(ctx context.Context, namespace string) (string, bool) {
	derived, ok := r.detectPrimaryUDNNetworkNameFromVirtLauncher(ctx, namespace)
	return derived, ok
}

// detectPrimaryUDNNetworkNameFromVirtLauncher derives the OVN-K primary network name used in
// k8s.ovn.org/endpointslice-network, from any virt-launcher pod's k8s.ovn.org/pod-networks annotation.
// The annotation JSON key is typically "<namespace>/<udnName>" which OVN-K expects as "<namespace>_<udnName>".
func (r *reconciler) detectPrimaryUDNNetworkNameFromVirtLauncher(ctx context.Context, namespace string) (string, bool) {
	podList := &corev1.PodList{}
	if err := r.kubevirtInfraClient.List(ctx, podList, client.InNamespace(namespace), client.MatchingLabels{"kubevirt.io": "virt-launcher"}); err != nil {
		return "", false
	}
	for i := range podList.Items {
		raw := podList.Items[i].Annotations[ovnPodNetworksAnnotationKey]
		if raw == "" {
			continue
		}
		var m map[string]podNetworksAnnotationEntry
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			continue
		}
		for networkKey, v := range m {
			if v.Role == ovnPrimaryRole && networkKey != "default" {
				return strings.ReplaceAll(networkKey, "/", "_"), true
			}
		}
	}
	return "", false
}

func serviceHasIPFamily(service *corev1.Service, ipFamilyToFind corev1.IPFamily) bool {
	for _, ipFamily := range service.Spec.IPFamilies {
		if ipFamily == ipFamilyToFind {
			return true
		}
	}
	return false
}

func kubevirtInfraNamespace(hcp *hyperv1.HostedControlPlane) string {
	if hcp.Spec.Platform.Kubevirt.Credentials != nil {
		return hcp.Spec.Platform.Kubevirt.Credentials.InfraNamespace
	}
	return hcp.Namespace
}

func (r *reconciler) removeOrphanKubevirtPassthroughEndpointSlices(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	namespace := kubevirtInfraNamespace(hcp)
	endpointSliceList := discoveryv1.EndpointSliceList{}
	managedByControlPlaneOperator := client.MatchingLabels{discoveryv1.LabelManagedBy: managedByValue}
	if err := r.kubevirtInfraClient.List(ctx, &endpointSliceList, client.InNamespace(namespace), managedByControlPlaneOperator); err != nil {
		return fmt.Errorf("failed finding endpoint slices managed by control plane operator: %w", err)
	}

	for _, endpointSlice := range endpointSliceList.Items {
		serviceName := endpointSlice.Labels[discoveryv1.LabelServiceName]
		if serviceName == "" {
			// UDN EndpointSlices use a different service name label.
			serviceName = endpointSlice.Labels[ovnUDNServiceNameLabelKey]
		}
		if serviceName == "" {
			continue
		}
		err := r.kubevirtInfraClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: serviceName}, &corev1.Service{})
		if err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed looking for service referencing endpoint slices managed by control plane operator: %w", err)
		}
		if apierrors.IsNotFound(err) {
			if err := r.kubevirtInfraClient.Delete(ctx, &endpointSlice); err != nil {
				return fmt.Errorf("failed deleting orphan kubevirt passthrough endpoint slice: %w", err)
			}
		}
	}
	return nil
}

func machinePhaseToEndpointConditions(machine *capiv1.Machine) discoveryv1.EndpointConditions {
	if machine.Status.GetTypedPhase() == capiv1.MachinePhaseRunning {
		return discoveryv1.EndpointConditions{
			Ready:       ptr.To(true),
			Serving:     ptr.To(true),
			Terminating: ptr.To(false),
		}
	}
	return discoveryv1.EndpointConditions{
		Ready:       ptr.To(false),
		Serving:     ptr.To(false),
		Terminating: ptr.To(false),
	}
}
