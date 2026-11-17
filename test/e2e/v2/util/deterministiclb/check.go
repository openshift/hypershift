//go:build e2ev2

// Package deterministiclb contains the Day 1 deterministic LoadBalancer IP
// checks shared by the v2 E2E spec and local development tools.
package deterministiclb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	cpomanifests "github.com/openshift/hypershift/pkg/manifests/cpo"
	"github.com/openshift/hypershift/support/netutil"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var lookupHost = net.DefaultResolver.LookupHost

// GuestClientFunc returns a client for the hosted cluster. The function is
// called again while polling so a newly provisioned cluster can be retried.
type GuestClientFunc func(ctx context.Context, hc *hyperv1.HostedCluster) (crclient.Client, error)

// Options configures the deterministic LoadBalancer IP checks.
type Options struct {
	Context context.Context
	Client  crclient.Client

	HostedCluster         *hyperv1.HostedCluster
	ControlPlaneNamespace string

	// ExpectedIP overrides the address derived from the provider annotations.
	// It is required for providers whose annotation carries an allocation ID
	// instead of an IP address (for example AWS EIPs).
	ExpectedIP string
	Timeout    time.Duration

	// SkipReconciliation disables the temporary Service label update used to
	// prove that the controller reconciles the primary KAS Service.
	SkipReconciliation bool

	// GuestClient is optional. When set, the package verifies the kube-apiserver
	// is reachable with the hosted cluster kubeconfig.
	GuestClient GuestClientFunc

	// Out receives PASS/FAIL/SKIP lines. Defaults to io.Discard.
	Out io.Writer
}

type reporter struct {
	out io.Writer
}

func (r *reporter) pass(format string, args ...interface{}) {
	fmt.Fprintf(r.out, "[PASS] "+format+"\n", args...)
}

func (r *reporter) fail(format string, args ...interface{}) {
	fmt.Fprintf(r.out, "[FAIL] "+format+"\n", args...)
}

func (r *reporter) skip(format string, args ...interface{}) {
	fmt.Fprintf(r.out, "[SKIP] "+format+"\n", args...)
}

// Check verifies the Day 1 deterministic LoadBalancer IP behavior for an
// existing HostedCluster. It never deletes or recreates the cluster. The only
// mutation is a temporary label on the primary kube-apiserver Service, which
// is restored before returning.
func Check(opts Options) error {
	if opts.Context == nil {
		opts.Context = context.Background()
	}
	if opts.Timeout == 0 {
		opts.Timeout = 15 * time.Minute
	}
	if opts.Client == nil {
		return errors.New("client is required")
	}
	if opts.HostedCluster == nil {
		return errors.New("hosted cluster is required")
	}
	if opts.ControlPlaneNamespace == "" {
		return errors.New("control plane namespace is required")
	}
	if opts.Out == nil {
		opts.Out = io.Discard
	}

	r := &reporter{out: opts.Out}
	hc := opts.HostedCluster
	strategy := netutil.ServicePublishingStrategyByTypeByHC(hc, hyperv1.APIServer)
	if strategy == nil || strategy.Type != hyperv1.LoadBalancer || strategy.LoadBalancer == nil {
		return errors.New("the APIServer service is not published through a LoadBalancer")
	}
	if len(strategy.LoadBalancer.ServiceAnnotations) == 0 {
		return errors.New("the APIServer LoadBalancer has no serviceAnnotations; create the HostedCluster with the deterministic LB IP fields")
	}

	if err := checkConditions(opts.Context, opts.Client, hc, opts.Timeout); err != nil {
		r.fail("HC conditions: %v", err)
		return err
	}
	r.pass("SupportedHostedCluster=True and DeterministicLBIPSupported=True")

	primary := primaryKASService(hc, opts.ControlPlaneNamespace)
	expectedIPs := requestedLBIPs(strategy.LoadBalancer.ServiceAnnotations)
	if opts.ExpectedIP != "" {
		ip := net.ParseIP(opts.ExpectedIP)
		if ip == nil {
			return fmt.Errorf("expected IP %q is not a valid IP", opts.ExpectedIP)
		}
		if !slices.Contains(expectedIPs, ip.String()) {
			expectedIPs = append(expectedIPs, ip.String())
		}
	}
	if len(expectedIPs) == 0 {
		r.skip("exact address assertion is not possible from the provider annotation; set an expected IP to assert it")
	}

	svc, err := waitForPrimaryService(opts.Context, opts.Client, primary, strategy.LoadBalancer, expectedIPs, opts.Timeout)
	if err != nil {
		r.fail("primary KAS Service: %v", err)
		return err
	}
	r.pass("primary KAS Service %s is a LoadBalancer with the requested annotations and class", svc.Name)
	if len(expectedIPs) > 0 {
		r.pass("primary KAS Service address matches one of %v", expectedIPs)
	} else {
		r.pass("primary KAS Service has an address (exact IP not asserted)")
	}

	if err := checkOtherLBServices(opts.Context, opts.Client, opts.ControlPlaneNamespace, primary.Name, strategy.LoadBalancer); err != nil {
		r.fail("other LoadBalancer Services: %v", err)
		return err
	}
	r.pass("no other LoadBalancer Service received the primary endpoint annotations")

	if opts.GuestClient == nil {
		r.skip("kube-apiserver reachability skipped: no guest client provided")
	} else {
		if err := checkKASReachable(opts.Context, opts.Client, opts.GuestClient, hc, opts.Timeout); err != nil {
			r.fail("KAS reachability: %v", err)
			return err
		}
		r.pass("kube-apiserver is reachable with the guest kubeconfig")
	}

	if opts.SkipReconciliation {
		r.skip("reconciliation check skipped")
		return nil
	}
	if err := checkReconciliation(opts.Context, opts.Client, hc, primary, strategy.LoadBalancer, expectedIPs); err != nil {
		r.fail("reconciliation check: %v", err)
		return err
	}
	r.pass("Service label reconciliation preserved the Service and the original HC state")
	return nil
}

func primaryKASService(hc *hyperv1.HostedCluster, controlPlaneNamespace string) *corev1.Service {
	if !netutil.IsPublicHC(hc) {
		return manifests.KubeAPIServerPrivateService(controlPlaneNamespace)
	}
	if hc.Spec.Platform.Type == hyperv1.AzurePlatform ||
		(hc.Spec.Platform.Type == hyperv1.KubevirtPlatform && hc.Annotations[hyperv1.ManagementPlatformAnnotation] == string(hyperv1.AzurePlatform)) {
		return cpomanifests.KubeAPIServerServiceAzureLB(controlPlaneNamespace)
	}
	return cpomanifests.KubeAPIServerService(controlPlaneNamespace)
}

func requestedLBIPs(annotations map[string]string) []string {
	keys := []string{
		"metallb.io/loadBalancerIPs",
		"metallb.universe.tf/loadBalancerIPs",
		"lbipam.cilium.io/ips",
		"cis.f5.com/ip",
		"service.beta.kubernetes.io/azure-load-balancer-ipv4",
		"service.beta.kubernetes.io/azure-load-balancer-ipv6",
		"loadbalancer.openstack.org/floating-ip",
	}
	var ips []string
	for _, key := range keys {
		for _, candidate := range strings.Split(annotations[key], ",") {
			candidate = strings.TrimSpace(candidate)
			if ip := net.ParseIP(candidate); ip != nil {
				ips = append(ips, ip.String())
			}
		}
	}
	return ips
}

func checkConditions(ctx context.Context, client crclient.Client, hc *hyperv1.HostedCluster, timeout time.Duration) error {
	return poll(ctx, timeout, func(ctx context.Context) (bool, error) {
		fresh := &hyperv1.HostedCluster{}
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(hc), fresh); err != nil {
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		supported := meta.FindStatusCondition(fresh.Status.Conditions, string(hyperv1.SupportedHostedCluster))
		if supported == nil {
			return false, nil
		}
		if supported.Status != metav1.ConditionTrue {
			return false, fmt.Errorf("SupportedHostedCluster is %s: %s: %s", supported.Status, supported.Reason, supported.Message)
		}
		det := meta.FindStatusCondition(fresh.Status.Conditions, string(hyperv1.DeterministicLBIPSupported))
		if det == nil {
			return false, nil
		}
		if det.Status != metav1.ConditionTrue {
			return false, fmt.Errorf("DeterministicLBIPSupported is %s: %s: %s", det.Status, det.Reason, det.Message)
		}
		return true, nil
	})
}

func waitForPrimaryService(ctx context.Context, client crclient.Client, svcRef *corev1.Service, lb *hyperv1.LoadBalancerPublishingStrategy, expectedIPs []string, timeout time.Duration) (*corev1.Service, error) {
	var last *corev1.Service
	var lastAddresses []string
	var lastLookupErr error
	err := poll(ctx, timeout, func(ctx context.Context) (bool, error) {
		svc := &corev1.Service{}
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(svcRef), svc); err != nil {
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		if svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
			return false, fmt.Errorf("service %s has type %s, expected LoadBalancer", svc.Name, svc.Spec.Type)
		}
		for key, value := range lb.ServiceAnnotations {
			if svc.Annotations[key] != value {
				return false, fmt.Errorf("service %s does not have the requested annotation %s", svc.Name, key)
			}
		}
		if lb.LoadBalancerClass == "" {
			if svc.Spec.LoadBalancerClass != nil {
				return false, fmt.Errorf("service %s has loadBalancerClass %q, expected none", svc.Name, ptr.Deref(svc.Spec.LoadBalancerClass, ""))
			}
		} else if svc.Spec.LoadBalancerClass == nil || *svc.Spec.LoadBalancerClass != lb.LoadBalancerClass {
			return false, fmt.Errorf("service %s loadBalancerClass is %q, expected %q", svc.Name, ptr.Deref(svc.Spec.LoadBalancerClass, ""), lb.LoadBalancerClass)
		}

		last = svc
		lastAddresses = storedIngressIPs(svc.Status.LoadBalancer.Ingress)
		if len(storedIngressAddresses(svc.Status.LoadBalancer.Ingress)) == 0 {
			return false, nil
		}
		if len(expectedIPs) == 0 {
			return true, nil
		}

		addresses, err := resolveIngressAddresses(ctx, svc.Status.LoadBalancer.Ingress)
		if err != nil {
			lastLookupErr = err
			return false, nil //nolint:nilerr // retry while DNS recovers
		}
		lastLookupErr = nil
		if !slices.ContainsFunc(addresses, func(addr string) bool {
			return slices.Contains(expectedIPs, addr)
		}) {
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		if last == nil {
			return nil, err
		}
		detail := fmt.Sprintf("last observed ingress=%v expected IPs=%v", lastAddresses, expectedIPs)
		if lastLookupErr != nil {
			detail = fmt.Sprintf("%s last DNS error=%v", detail, lastLookupErr)
		}
		return last, fmt.Errorf("%w (%s)", err, detail)
	}
	return last, nil
}

// storedIngressAddresses returns the address fields published by the load
// balancer without resolving hostnames. It is used for stability comparisons,
// where a DNS failure must not look like a Service mutation.
func storedIngressAddresses(ingresses []corev1.LoadBalancerIngress) []string {
	var addresses []string
	for _, ingress := range ingresses {
		if ingress.IP != "" {
			addresses = append(addresses, ingress.IP)
		}
		if ingress.Hostname != "" {
			addresses = append(addresses, ingress.Hostname)
		}
	}
	return addresses
}

// storedIngressIPs returns only the published IPs for diagnostics. Hostnames
// are intentionally omitted from logs because they may identify internal
// infrastructure.
func storedIngressIPs(ingresses []corev1.LoadBalancerIngress) []string {
	var addresses []string
	for _, ingress := range ingresses {
		if ingress.IP != "" {
			addresses = append(addresses, ingress.IP)
		}
	}
	return addresses
}

// resolveIngressAddresses resolves hostnames with a bounded lookup so the
// expected-IP assertion reports DNS failures separately from Service changes.
func resolveIngressAddresses(ctx context.Context, ingresses []corev1.LoadBalancerIngress) ([]string, error) {
	var addresses []string
	var errs []error
	for _, ingress := range ingresses {
		if ingress.IP != "" {
			addresses = append(addresses, ingress.IP)
		}
		if ingress.Hostname == "" {
			continue
		}
		lookupCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		resolved, err := lookupHost(lookupCtx, ingress.Hostname)
		cancel()
		if err != nil {
			errs = append(errs, errors.New("failed to resolve load balancer hostname"))
			continue
		}
		addresses = append(addresses, resolved...)
	}
	slices.Sort(addresses)
	if len(errs) > 0 {
		return addresses, errors.Join(errs...)
	}
	return addresses, nil
}

func checkOtherLBServices(ctx context.Context, client crclient.Client, controlPlaneNamespace, primaryName string, lb *hyperv1.LoadBalancerPublishingStrategy) error {
	services := &corev1.ServiceList{}
	if err := client.List(ctx, services, crclient.InNamespace(controlPlaneNamespace)); err != nil {
		return err
	}
	for i := range services.Items {
		svc := &services.Items[i]
		if svc.Name == primaryName || svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
			continue
		}
		for key := range lb.ServiceAnnotations {
			if _, ok := svc.Annotations[key]; ok {
				return fmt.Errorf("service %s should not have the primary endpoint annotation %s", svc.Name, key)
			}
		}
	}
	return nil
}

func checkKASReachable(ctx context.Context, client crclient.Client, guestClient GuestClientFunc, hc *hyperv1.HostedCluster, timeout time.Duration) error {
	return poll(ctx, timeout, func(ctx context.Context) (bool, error) {
		fresh := &hyperv1.HostedCluster{}
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(hc), fresh); err != nil {
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		guest, err := guestClient(ctx, fresh)
		if err != nil {
			return false, nil //nolint:nilerr // retry while the guest kubeconfig is being provisioned
		}
		namespaces := &corev1.NamespaceList{}
		if err := guest.List(ctx, namespaces); err != nil {
			return false, nil //nolint:nilerr // retry while the kube-apiserver becomes reachable
		}
		return true, nil
	})
}

func checkReconciliation(ctx context.Context, client crclient.Client, hc *hyperv1.HostedCluster, primary *corev1.Service, lb *hyperv1.LoadBalancerPublishingStrategy, expectedIPs []string) (err error) {
	labelKey := hyperv1.ControlPlaneComponentLabel

	before := &corev1.Service{}
	if err := client.Get(ctx, crclient.ObjectKeyFromObject(primary), before); err != nil {
		return fmt.Errorf("failed to read the primary KAS Service before reconciliation: %w", err)
	}
	originalLabelValue, hasLabel := before.Labels[labelKey]
	if !hasLabel {
		return fmt.Errorf("service %s has no %s label to trigger reconciliation", before.Name, labelKey)
	}

	patched := before.DeepCopy()
	delete(patched.Labels, labelKey)
	if err := client.Patch(ctx, patched, crclient.MergeFrom(before)); err != nil {
		return fmt.Errorf("failed to remove the %s label from the primary KAS Service: %w", labelKey, err)
	}

	defer func() {
		restoreCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		restoreErr := restoreServiceLabel(restoreCtx, client, before, labelKey, originalLabelValue)
		if restoreErr != nil {
			err = errors.Join(err, fmt.Errorf("failed to restore the %s label: %w", labelKey, restoreErr))
		}
	}()

	if err := poll(ctx, 2*time.Minute, func(ctx context.Context) (bool, error) {
		current := &corev1.Service{}
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(primary), current); err != nil {
			return false, err
		}
		return current.Labels[labelKey] == originalLabelValue, nil
	}); err != nil {
		return fmt.Errorf("the controller did not restore the %s label: %w", labelKey, err)
	}

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if err := verifyServiceUnchanged(ctx, client, primary, before, lb); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	if err := verifyServiceUnchanged(ctx, client, primary, before, lb); err != nil {
		return err
	}
	if err := verifyExpectedIP(ctx, client, primary, expectedIPs, 2*time.Minute); err != nil {
		return fmt.Errorf("expected IP changed during reconciliation: %w", err)
	}
	if err := checkConditions(ctx, client, hc, 2*time.Minute); err != nil {
		return fmt.Errorf("conditions changed during reconciliation: %w", err)
	}
	return nil
}

func verifyServiceUnchanged(ctx context.Context, client crclient.Client, ref *corev1.Service, before *corev1.Service, lb *hyperv1.LoadBalancerPublishingStrategy) error {
	svc := &corev1.Service{}
	if err := client.Get(ctx, crclient.ObjectKeyFromObject(ref), svc); err != nil {
		return err
	}
	if svc.UID != before.UID {
		return fmt.Errorf("service %s was recreated (UID changed)", svc.Name)
	}
	for key, value := range lb.ServiceAnnotations {
		if svc.Annotations[key] != value {
			return fmt.Errorf("service %s lost or changed annotation %s", svc.Name, key)
		}
	}
	if lb.LoadBalancerClass == "" {
		if svc.Spec.LoadBalancerClass != nil {
			return fmt.Errorf("service %s unexpectedly gained loadBalancerClass", svc.Name)
		}
	} else if svc.Spec.LoadBalancerClass == nil || *svc.Spec.LoadBalancerClass != lb.LoadBalancerClass {
		return fmt.Errorf("service %s loadBalancerClass changed", svc.Name)
	}
	if len(svc.Status.LoadBalancer.Ingress) == 0 {
		return fmt.Errorf("service %s lost its load balancer address", svc.Name)
	}
	if len(before.Status.LoadBalancer.Ingress) == 0 {
		return fmt.Errorf("service %s had no load balancer address before reconciliation", before.Name)
	}
	beforeAddresses := storedIngressAddresses(before.Status.LoadBalancer.Ingress)
	gotAddresses := storedIngressAddresses(svc.Status.LoadBalancer.Ingress)
	slices.Sort(beforeAddresses)
	slices.Sort(gotAddresses)
	if !slices.Equal(beforeAddresses, gotAddresses) {
		return fmt.Errorf("service %s stored ingress fields changed", svc.Name)
	}
	return nil
}

func verifyExpectedIP(ctx context.Context, client crclient.Client, ref *corev1.Service, expectedIPs []string, timeout time.Duration) error {
	if len(expectedIPs) == 0 {
		return nil
	}
	var lastAddresses []string
	var lastLookupErr error
	if err := poll(ctx, timeout, func(ctx context.Context) (bool, error) {
		current := &corev1.Service{}
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(ref), current); err != nil {
			return false, err
		}
		lastAddresses, lastLookupErr = resolveIngressAddresses(ctx, current.Status.LoadBalancer.Ingress)
		if lastLookupErr != nil {
			return false, nil //nolint:nilerr // retry while DNS recovers
		}
		return slices.ContainsFunc(lastAddresses, func(addr string) bool {
			return slices.Contains(expectedIPs, addr)
		}), nil
	}); err != nil {
		if lastLookupErr != nil {
			return fmt.Errorf("expected IP %v was not observed; last resolved addresses %v: %w", expectedIPs, lastAddresses, errors.Join(lastLookupErr, err))
		}
		return fmt.Errorf("expected IP %v was not observed; last resolved addresses %v: %w", expectedIPs, lastAddresses, err)
	}
	return nil
}

func restoreServiceLabel(ctx context.Context, client crclient.Client, ref *corev1.Service, key, value string) error {
	current := &corev1.Service{}
	if err := client.Get(ctx, crclient.ObjectKeyFromObject(ref), current); err != nil {
		return err
	}
	if current.Labels[key] == value {
		return nil
	}
	patchBase := current.DeepCopy()
	if current.Labels == nil {
		current.Labels = map[string]string{}
	}
	current.Labels[key] = value
	return client.Patch(ctx, current, crclient.MergeFrom(patchBase))
}

func poll(ctx context.Context, timeout time.Duration, condition func(context.Context) (bool, error)) error {
	pollCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return wait.PollUntilContextTimeout(pollCtx, 10*time.Second, timeout, true, condition)
}
