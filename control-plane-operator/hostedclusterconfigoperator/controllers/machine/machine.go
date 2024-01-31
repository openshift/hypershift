package machine

import (
	"context"
	"fmt"
	"net/netip"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/types/hypershift/v1beta1"
	hcpmanifests "github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/support/config"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
	kubevirtv1 "kubevirt.io/api/core/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func (r *reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling")
	machine := &capiv1.Machine{}
	if err := r.client.Get(ctx, client.ObjectKey(req.NamespacedName), machine); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("not found", "Machine", r)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get Machine: %w", err)
	}

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
		if hcp.Spec.Platform.Kubevirt != nil && (hcp.Spec.Platform.Kubevirt.BaseDomainPassthrough != nil && *hcp.Spec.Platform.Kubevirt.BaseDomainPassthrough) {
			if err := r.reconcileKubevirtDefaultIngressEndpoints(ctx, hcp, machine); err != nil {
				return reconcile.Result{}, fmt.Errorf("failed reconciling kubevirt default ingress passthrough service endpoints: %w", err)
			}
		}
	}
	log.Info("Reconciled Machine")
	return reconcile.Result{}, nil
}

func (r *reconciler) reconcileKubevirtDefaultIngressEndpoints(ctx context.Context, hcp *hyperv1.HostedControlPlane, machine *capiv1.Machine) error {
	log := ctrl.LoggerFrom(ctx)
	var namespace string
	if hcp.Spec.Platform.Kubevirt.Credentials != nil {
		namespace = hcp.Spec.Platform.Kubevirt.Credentials.InfraNamespace
	} else {
		namespace = hcp.Namespace
	}

	// Manifests for infra/mgmt cluster passthrough service
	cpService := hcpmanifests.IngressDefaultIngressPassthroughService(namespace)

	cpService.Name = fmt.Sprintf("%s-%s",
		hcpmanifests.IngressDefaultIngressPassthroughServiceName,
		hcp.Spec.Platform.Kubevirt.GenerateID)

	if err := r.kubevirtInfraClient.Get(ctx, client.ObjectKeyFromObject(cpService), cpService); err != nil {
		return fmt.Errorf("failed to get default ingress passthrough Service: %w", err)
	}

	// If there is a selector endpoints should not be generated
	if len(cpService.Spec.Selector) > 0 {
		return nil
	}

	if len(cpService.Spec.Ports) == 0 {
		return fmt.Errorf("missing default ingress passthrough Service %s/%s ports", cpService.Namespace, cpService.Name)
	}

	ipv4MachineAddresses := []string{}
	ipv6MachineAddresses := []string{}
	ports := []discoveryv1.EndpointPort{}

	// If machine is back to not ready we should reset the endpointslice
	if machine.Status.GetTypedPhase() == capiv1.MachinePhaseRunning {
		for _, machineAddress := range machine.Status.Addresses {
			if machineAddress.Type == capiv1.MachineInternalIP {
				if netip.MustParseAddr(machineAddress.Address).Is4() {
					ipv4MachineAddresses = append(ipv4MachineAddresses, machineAddress.Address)
				} else {
					ipv6MachineAddresses = append(ipv6MachineAddresses, machineAddress.Address)
				}
			}
		}
		for _, port := range cpService.Spec.Ports {
			ports = append(ports, discoveryv1.EndpointPort{
				Protocol: &port.Protocol,
				Port:     pointer.Int32(int32(port.TargetPort.IntValue())),
			})
		}
	}

	ipAddressesByFamily := map[corev1.IPFamily][]string{
		corev1.IPv4Protocol: ipv4MachineAddresses,
		corev1.IPv6Protocol: ipv6MachineAddresses,
	}
	for _, ipFamily := range []corev1.IPFamily{corev1.IPv4Protocol, corev1.IPv6Protocol} {
		if !serviceHasIPFamily(cpService, ipFamily) {
			continue
		}
		err := r.reconcileKubevirtDefaultIngressEndpointsByIPFamily(ctx, machine, cpService, ipFamily, ipAddressesByFamily[ipFamily], ports)
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

func (r *reconciler) reconcileKubevirtDefaultIngressEndpointsByIPFamily(ctx context.Context, machine *capiv1.Machine, cpService *corev1.Service, ipFamily corev1.IPFamily, machineAddresses []string, ports []discoveryv1.EndpointPort) error {
	log := ctrl.LoggerFrom(ctx)
	endpointSlice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: cpService.Namespace,
			Name:      cpService.Name + "-" + machine.Name + "-" + strings.ToLower(string(ipFamily)),
		},
	}
	result, err := r.CreateOrUpdate(ctx, r.kubevirtInfraClient, endpointSlice, func() error {
		if len(endpointSlice.OwnerReferences) == 0 {
			// Machine infra ref is the KubevirtMachine wich has the same name
			// as the kubevirt VirtualMachine CRD, but the namespace ca
			// can be different if kubevirt infra cluster is external
			vmKey := client.ObjectKey{
				Namespace: cpService.Namespace,
				Name:      machine.Spec.InfrastructureRef.Name,
			}
			vm := kubevirtv1.VirtualMachine{}
			if err := r.kubevirtInfraClient.Get(ctx, vmKey, &vm); err != nil {
				return err
			}
			ownerRef := config.OwnerRefFrom(&vm)
			ownerRef.ApplyTo(endpointSlice)
		}

		if endpointSlice.Labels == nil {
			endpointSlice.Labels = map[string]string{}
		}
		endpointSlice.Labels["kubernetes.io/service-name"] = cpService.Name
		endpointSlice.Labels["endpointslice.kubernetes.io/managed-by"] = "control-plane-operator.hypershift.openshift.io"
		endpointSlice.AddressType = discoveryv1.AddressType(ipFamily)
		if len(machineAddresses) > 0 {
			endpointSlice.Endpoints = []discoveryv1.Endpoint{{Addresses: machineAddresses}}
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

func serviceHasIPFamily(service *corev1.Service, ipFamilyToFind corev1.IPFamily) bool {
	for _, ipFamily := range service.Spec.IPFamilies {
		if ipFamily == ipFamilyToFind {
			return true
		}
	}
	return false
}
