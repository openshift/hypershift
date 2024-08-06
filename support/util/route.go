package util

import (
	"fmt"
	"hash/fnv"
	"strings"

	routev1 "github.com/openshift/api/route/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1alpha1"

	"k8s.io/apimachinery/pkg/util/validation"
)

const HCPRouteLabel = "hypershift.openshift.io/hosted-control-plane"
const InternalRouteLabel = "hypershift.openshift.io/internal-route"

// ShortenRouteHostnameIfNeeded will return a shortened hostname if the route hostname will exceed
// the allowed DNS name size. If the hostname is not too long, an empty string is returned so that
// the default can be used.
func ShortenRouteHostnameIfNeeded(name, namespace string, baseDomain string) string {
	if baseDomain == "" {
		return ""
	}
	if len(name)+len(namespace)+1 < validation.DNS1123LabelMaxLength {
		return ""
	} else {
		return fmt.Sprintf("%s.%s", strings.TrimSuffix(ShortenName(name+"-"+namespace, "", validation.DNS1123LabelMaxLength), "-"), baseDomain)
	}
}

// ShortenName returns a name given a base ("deployment-5") and a suffix ("deploy")
// It will first attempt to join them with a dash. If the resulting name is longer
// than maxLength: if the suffix is too long, it will truncate the base name and add
// an 8-character hash of the [base]-[suffix] string.  If the suffix is not too long,
// it will truncate the base, add the hash of the base and return [base]-[hash]-[suffix]
// Source: openshift/origin v3.9.0 pkg/api/apihelpers/namer.go
func ShortenName(base, suffix string, maxLength int) string {
	if maxLength <= 0 {
		return ""
	}
	name := fmt.Sprintf("%s-%s", base, suffix)
	if len(name) <= maxLength {
		return name
	}

	baseLength := maxLength - 10 /*length of -hash-*/ - len(suffix)

	// if the suffix is too long, ignore it
	if baseLength < 0 {
		prefix := base[0:min(len(base), max(0, maxLength-9))]
		// Calculate hash on initial base-suffix string
		shortName := fmt.Sprintf("%s-%s", prefix, hash(name))
		return shortName[:min(maxLength, len(shortName))]
	}

	prefix := base[0:baseLength]
	// Calculate hash on initial base-suffix string
	return fmt.Sprintf("%s-%s-%s", prefix, hash(base), suffix)
}

// max returns the greater of its 2 inputs
func max(a, b int) int {
	if b > a {
		return b
	}
	return a
}

// min returns the lesser of its 2 inputs
func min(a, b int) int {
	if b < a {
		return b
	}
	return a
}

// hash calculates the hexadecimal representation (8-chars)
// of the hash of the passed in string using the FNV-a algorithm
func hash(s string) string {
	hash := fnv.New32a()
	hash.Write([]byte(s))
	intHash := hash.Sum32()
	result := fmt.Sprintf("%08x", intHash)
	return result
}

func ReconcileExternalRoute(route *routev1.Route, hostname string, defaultIngressDomain string, serviceName string, labelHCPRoutes bool) error {
	if labelHCPRoutes {
		AddHCPRouteLabel(route)
	}
	if hostname != "" {
		route.Spec.Host = hostname
	} else {
		if route.Spec.Host == "" {
			route.Spec.Host = ShortenRouteHostnameIfNeeded(route.Name, route.Namespace, defaultIngressDomain)
		}
	}

	route.Spec.To = routev1.RouteTargetReference{
		Kind: "Service",
		Name: serviceName,
	}
	route.Spec.TLS = &routev1.TLSConfig{
		Termination:                   routev1.TLSTerminationPassthrough,
		InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyNone,
	}
	// remove annotation as external-dns will register the name if host is within the zone
	delete(route.Annotations, hyperv1.ExternalDNSHostnameAnnotation)
	return nil
}

func ReconcileInternalRoute(route *routev1.Route, hcName string, serviceName string) error {
	AddHCPRouteLabel(route)
	AddInternalRouteLabel(route)
	if route.Spec.Host == "" {
		route.Spec.Host = fmt.Sprintf("%s.apps.%s.hypershift.local", strings.TrimSuffix(route.Name, "-internal"), hcName)
	}
	route.Spec.To = routev1.RouteTargetReference{
		Kind: "Service",
		Name: serviceName,
	}
	route.Spec.TLS = &routev1.TLSConfig{
		Termination:                   routev1.TLSTerminationPassthrough,
		InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyNone,
	}
	return nil
}

func AddHCPRouteLabel(target crclient.Object) {
	labels := target.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels[HCPRouteLabel] = target.GetNamespace()
	target.SetLabels(labels)
}

func AddInternalRouteLabel(target crclient.Object) {
	labels := target.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels[InternalRouteLabel] = "true"
	target.SetLabels(labels)
}
