package etcd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/upsert"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	managedByValue                    = "control-plane-operator.hypershift.openshift.io"
	ovnPodNetworksAnnotationKey       = "k8s.ovn.org/pod-networks"
	ovnPrimaryRole                    = "primary"
	ovnUDNServiceNameLabelKey         = "k8s.ovn.org/service-name"
	ovnUDNEndpointSliceNetworkAnnoKey = "k8s.ovn.org/endpointslice-network"

	etcdClientServiceName    = "etcd-client"
	etcdDiscoveryServiceName = "etcd-discovery"
)

type podNetworksAnnotationEntry struct {
	Role      string `json:"role"`
	IPAddress string `json:"ip_address"`
}

type reconciler struct {
	client client.Client
	hcpKey types.NamespacedName
	upsert.CreateOrUpdateProvider
}

func (r *reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := log.FromContext(ctx).WithValues("controller", ControllerName, "hcp", r.hcpKey.String())

	hcp := &hyperv1.HostedControlPlane{}
	if err := r.client.Get(ctx, r.hcpKey, hcp); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	if hcp.Spec.Platform.Type != hyperv1.KubevirtPlatform {
		return ctrl.Result{}, nil
	}

	etcdPods, err := r.listEtcdPods(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}
	if len(etcdPods) == 0 {
		return ctrl.Result{}, nil
	}

	udnNetworkName, endpointsByFamily, ok, shouldRequeue := desiredEtcdEndpointsFromPods(etcdPods)
	if !ok {
		if shouldRequeue {
			logger.V(1).Info("etcd pods found but primary UDN network not detected yet, requeueing")
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}
		return ctrl.Result{}, nil
	}

	for _, svcName := range []string{etcdClientServiceName, etcdDiscoveryServiceName} {
		if err := r.reconcileUDNEndpointSlicesForService(ctx, svcName, udnNetworkName, endpointsByFamily); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *reconciler) listEtcdPods(ctx context.Context) ([]corev1.Pod, error) {
	pods := &corev1.PodList{}
	if err := r.client.List(ctx, pods, client.InNamespace(r.hcpKey.Namespace), client.MatchingLabels{"app": "etcd"}); err != nil {
		return nil, err
	}
	return pods.Items, nil
}

func (r *reconciler) reconcileUDNEndpointSlicesForService(ctx context.Context, serviceName, udnNetworkName string, endpointsByFamily map[discoveryv1.AddressType][]discoveryv1.Endpoint) error {
	svc := &corev1.Service{}
	if err := r.client.Get(ctx, types.NamespacedName{Namespace: r.hcpKey.Namespace, Name: serviceName}, svc); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	ports := serviceEndpointPorts(svc)

	for addressType, endpoints := range endpointsByFamily {
		// Only create slices that have endpoints for this family.
		if len(endpoints) == 0 {
			continue
		}

		name := fmt.Sprintf("%s-udn-%s", serviceName, strings.ToLower(string(addressType)))
		es := &discoveryv1.EndpointSlice{ObjectMeta: metav1.ObjectMeta{
			Namespace: r.hcpKey.Namespace,
			Name:      name,
		}}

		_, err := r.CreateOrUpdate(ctx, r.client, es, func() error {
			es.Labels = ensureStringMap(es.Labels)
			es.Annotations = ensureStringMap(es.Annotations)

			es.Labels[discoveryv1.LabelManagedBy] = managedByValue
			es.Labels[discoveryv1.LabelServiceName] = svc.Name
			es.Labels[ovnUDNServiceNameLabelKey] = svc.Name
			es.Annotations[ovnUDNEndpointSliceNetworkAnnoKey] = udnNetworkName

			es.AddressType = addressType
			es.Ports = ports
			es.Endpoints = endpoints
			return nil
		})
		if err != nil {
			return err
		}
	}

	return nil
}

func desiredEtcdEndpointsFromPods(pods []corev1.Pod) (udnNetworkName string, endpointsByFamily map[discoveryv1.AddressType][]discoveryv1.Endpoint, ok bool, shouldRequeue bool) {
	endpointsByFamily = map[discoveryv1.AddressType][]discoveryv1.Endpoint{
		discoveryv1.AddressTypeIPv4: {},
		discoveryv1.AddressTypeIPv6: {},
	}

	anyAnnotation := false
	anyParsed := false

	for i := range pods {
		pod := pods[i]
		networkName, ip, isPrimaryUDN, hasAnnotation, parsed := primaryUDNInfoFromPodNetworks(&pod)
		anyAnnotation = anyAnnotation || hasAnnotation
		anyParsed = anyParsed || parsed

		if !isPrimaryUDN || ip == "" {
			continue
		}
		if udnNetworkName == "" {
			udnNetworkName = networkName
		}

		addr, err := netip.ParseAddr(ip)
		if err != nil {
			continue
		}

		addressType := discoveryv1.AddressTypeIPv4
		if addr.Is6() {
			addressType = discoveryv1.AddressTypeIPv6
		}

		ep := discoveryv1.Endpoint{
			Addresses: []string{ip},
			Conditions: discoveryv1.EndpointConditions{
				Ready:   ptr.To(podReady(&pod)),
				Serving: ptr.To(podReady(&pod)),
			},
			TargetRef: &corev1.ObjectReference{
				APIVersion: "v1",
				Kind:       "Pod",
				Namespace:  pod.Namespace,
				Name:       pod.Name,
				UID:        pod.UID,
			},
		}
		if pod.Spec.Hostname != "" {
			ep.Hostname = ptr.To(pod.Spec.Hostname)
		}
		endpointsByFamily[addressType] = append(endpointsByFamily[addressType], ep)
	}

	if udnNetworkName != "" {
		return udnNetworkName, endpointsByFamily, true, false
	}

	// If we can parse pod-networks annotations and they don't indicate Primary UDN, we're in a non-UDN namespace.
	if anyAnnotation && anyParsed {
		return "", endpointsByFamily, false, false
	}

	// Pods exist but annotations aren't available yet; retry a few times to avoid races early in rollout.
	return "", endpointsByFamily, false, true
}

func primaryUDNInfoFromPodNetworks(pod *corev1.Pod) (networkName, ip string, isPrimaryUDN, hasAnnotation, parsed bool) {
	raw := ""
	if pod.Annotations != nil {
		raw = pod.Annotations[ovnPodNetworksAnnotationKey]
	}
	if raw == "" {
		return "", "", false, false, false
	}

	hasAnnotation = true
	m := map[string]podNetworksAnnotationEntry{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return "", "", false, true, false
	}
	parsed = true

	for networkKey, v := range m {
		if v.Role != ovnPrimaryRole {
			continue
		}
		if networkKey == "default" {
			continue
		}
		ip = strings.SplitN(v.IPAddress, "/", 2)[0]
		if ip == "" {
			continue
		}
		return strings.ReplaceAll(networkKey, "/", "_"), ip, true, true, true
	}
	return "", "", false, true, true
}

func podReady(pod *corev1.Pod) bool {
	for i := range pod.Status.Conditions {
		c := pod.Status.Conditions[i]
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

func serviceEndpointPorts(svc *corev1.Service) []discoveryv1.EndpointPort {
	out := make([]discoveryv1.EndpointPort, 0, len(svc.Spec.Ports))
	for i := range svc.Spec.Ports {
		p := svc.Spec.Ports[i]
		out = append(out, discoveryv1.EndpointPort{
			Name:     ptr.To(p.Name),
			Protocol: ptr.To(p.Protocol),
			Port:     ptr.To(p.Port),
		})
	}
	return out
}

func ensureStringMap(in map[string]string) map[string]string {
	if in == nil {
		return map[string]string{}
	}
	return in
}

func ReconcileUDNEndpointSlices(ctx context.Context, c client.Client, createOrUpdateProvider upsert.CreateOrUpdateProvider, namespace string) error {
	pods := &corev1.PodList{}
	if err := c.List(ctx, pods, client.InNamespace(namespace), client.MatchingLabels{"app": "etcd"}); err != nil {
		return err
	}
	if len(pods.Items) == 0 {
		return nil
	}

	udnNetworkName, endpointsByFamily, ok, _ := desiredEtcdEndpointsFromPods(pods.Items)
	if !ok {
		return nil
	}

	r := &reconciler{
		client:                 c,
		hcpKey:                 types.NamespacedName{Namespace: namespace},
		CreateOrUpdateProvider: createOrUpdateProvider,
	}

	for _, svcName := range []string{etcdClientServiceName, etcdDiscoveryServiceName} {
		if err := r.reconcileUDNEndpointSlicesForService(ctx, svcName, udnNetworkName, endpointsByFamily); err != nil {
			return err
		}
	}
	return nil
}

func isEtcdPodInNamespace(namespace string) predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		return obj.GetNamespace() == namespace && obj.GetLabels()["app"] == "etcd"
	})
}

func isEtcdServiceInNamespace(namespace string) predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		if obj.GetNamespace() != namespace {
			return false
		}
		switch obj.GetName() {
		case etcdClientServiceName, etcdDiscoveryServiceName:
			return true
		default:
			return false
		}
	})
}

func isEtcdEndpointSliceInNamespace(namespace string) predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		if obj.GetNamespace() != namespace {
			return false
		}
		labels := obj.GetLabels()
		if labels == nil {
			return false
		}
		switch labels[discoveryv1.LabelServiceName] {
		case etcdClientServiceName, etcdDiscoveryServiceName:
			return true
		default:
			return false
		}
	})
}
