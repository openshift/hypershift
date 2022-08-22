package hostedcluster

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strings"

	corev1 "k8s.io/api/core/v1"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

// Webhook implements a validating webhook for HostedCluster.
type Webhook struct{}

// SetupWebhookWithManager sets up HostedCluster webhooks.
func SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(&hyperv1.HostedCluster{}).
		WithValidator(&Webhook{}).
		Complete()
}

var _ webhook.CustomValidator = &Webhook{}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type.
func (webhook *Webhook) ValidateCreate(ctx context.Context, obj runtime.Object) error {
	hostedCluster, ok := obj.(*hyperv1.HostedCluster)
	if !ok {
		return apierrors.NewBadRequest(fmt.Sprintf("expected a HostedCluster but got a %T", obj))
	}

	return validateHostedClusterCreate(hostedCluster)
}

type cidrEntry struct {
	net  net.IPNet
	path field.Path
}

func cidrsOverlap(net1 *net.IPNet, net2 *net.IPNet) error {
	if net1.Contains(net2.IP) || net2.Contains(net1.IP) {
		return fmt.Errorf("%s and %s", net1.String(), net2.String())
	}
	return nil
}

func compareCIDREntries(ce []cidrEntry) field.ErrorList {
	var errs field.ErrorList

	for o := range ce {
		for i := o + 1; i < len(ce); i++ {
			if err := cidrsOverlap(&ce[o].net, &ce[i].net); err != nil {
				errs = append(errs, field.Invalid(&ce[o].path, ce[o].net.String(), fmt.Sprintf("%s and %s overlap: %s", ce[o].path.String(), ce[i].path.String(), err)))
			}
		}
	}
	return errs
}

func validateNetworkCIDRs(hc *hyperv1.HostedCluster) field.ErrorList {
	var errs field.ErrorList
	var cidrEntries []cidrEntry

	podCIDR := hc.Spec.Networking.PodCIDR
	serviceCIDR := hc.Spec.Networking.ServiceCIDR
	machineCIDR := hc.Spec.Networking.MachineCIDR

	// Validate CIDR format..
	_, serviceNet, err := net.ParseCIDR(serviceCIDR)
	if err != nil {
		errs = append(errs, field.Invalid(field.NewPath("spec.networking.serviceCIDR"), serviceCIDR, err.Error()))
	} else {
		ce := cidrEntry{*serviceNet, *field.NewPath("spec.networking.serviceCIDR")}
		cidrEntries = append(cidrEntries, ce)
	}

	_, podNet, err := net.ParseCIDR(podCIDR)
	if err != nil {
		errs = append(errs, field.Invalid(field.NewPath("spec.networking.podCIDR"), podCIDR, err.Error()))
	} else {
		ce := cidrEntry{*podNet, *field.NewPath("spec.networking.podCIDR")}
		cidrEntries = append(cidrEntries, ce)
	}

	_, machineNet, err := net.ParseCIDR(machineCIDR)
	if err != nil {
		errs = append(errs, field.Invalid(field.NewPath("spec.networking.machineCIDR"), machineCIDR, err.Error()))
	} else {
		ce := cidrEntry{*machineNet, *field.NewPath("spec.networking.machineCIDR")}
		cidrEntries = append(cidrEntries, ce)
	}

	// Bail if we can't parse.
	if len(errs) > 0 {
		return errs
	}

	return compareCIDREntries(cidrEntries)
}

func validateSliceNetworkCIDRs(hc *hyperv1.HostedCluster) field.ErrorList {
	var cidrEntries []cidrEntry

	for _, cidr := range hc.Spec.Networking.MachineNetwork {
		ce := cidrEntry{(net.IPNet)(cidr.CIDR), *field.NewPath("spec.networking.MachineNetwork")}
		cidrEntries = append(cidrEntries, ce)
	}
	for _, cidr := range hc.Spec.Networking.ServiceNetwork {
		ce := cidrEntry{(net.IPNet)(cidr.CIDR), *field.NewPath("spec.networking.ServiceNetwork")}
		cidrEntries = append(cidrEntries, ce)
	}
	for _, cidr := range hc.Spec.Networking.ClusterNetwork {
		ce := cidrEntry{(net.IPNet)(cidr.CIDR), *field.NewPath("spec.networking.ClusterNetwork")}
		cidrEntries = append(cidrEntries, ce)
	}

	return compareCIDREntries(cidrEntries)
}

func validateHostedClusterCreate(hc *hyperv1.HostedCluster) error {
	errs := validateNetworkCIDRs(hc)
	errs = append(errs, validateSliceNetworkCIDRs(hc)...)

	return errs.ToAggregate()
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type.
func (webhook *Webhook) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) error {
	newHC, ok := newObj.(*hyperv1.HostedCluster)
	if !ok {
		return apierrors.NewBadRequest(fmt.Sprintf("expected a HostedCluster but got a %T", newObj))
	}

	oldHC, ok := oldObj.(*hyperv1.HostedCluster)
	if !ok {
		return apierrors.NewBadRequest(fmt.Sprintf("expected a HostedCluster but got a %T", oldObj))
	}

	return validateHostedClusterUpdate(newHC, oldHC)
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type.
func (webhook *Webhook) ValidateDelete(_ context.Context, obj runtime.Object) error {
	return nil
}

// filterMutableHostedClusterSpecFields zeros out non-immutable entries so that they are
// "equal" when we do the comparison below.
func filterMutableHostedClusterSpecFields(spec *hyperv1.HostedClusterSpec) {
	spec.Release.Image = ""
	spec.Configuration = nil
	spec.AdditionalTrustBundle = nil
	spec.SecretEncryption = nil
	spec.PausedUntil = nil
	for i, svc := range spec.Services {
		if svc.Type == hyperv1.NodePort && svc.NodePort != nil {
			spec.Services[i].NodePort.Address = ""
			spec.Services[i].NodePort.Port = 0
		}
	}
	if spec.Platform.Type == hyperv1.AWSPlatform && spec.Platform.AWS != nil {
		spec.Platform.AWS.ResourceTags = nil
		// This is to enable reconcileDeprecatedAWSRoles.
		spec.Platform.AWS.RolesRef = hyperv1.AWSRolesRef{}
		spec.Platform.AWS.Roles = []hyperv1.AWSRoleCredentials{}
		spec.Platform.AWS.NodePoolManagementCreds = corev1.LocalObjectReference{}
		spec.Platform.AWS.ControlPlaneOperatorCreds = corev1.LocalObjectReference{}
		spec.Platform.AWS.KubeCloudControllerCreds = corev1.LocalObjectReference{}
	}

	// This is to enable reconcileDeprecatedNetworkSettings
	// reset everything except network type and apiserver settings
	spec.Networking = hyperv1.ClusterNetworking{
		NetworkType: spec.Networking.NetworkType,
		APIServer:   spec.Networking.APIServer,
	}
}

// validateStructDeepEqual walks through a struct and compares each entry.  If it comes across a substruct it
// recursively calls itself.  Returns a list of immutable field errors generated by any field being changed.
func validateStructDeepEqual(x reflect.Value, y reflect.Value, path *field.Path, errs field.ErrorList) field.ErrorList {
	for i := 0; i < x.NumField(); i++ {
		v1 := x.Field(i)
		v2 := y.Field(i)
		jsonId := x.Type().Field(i).Tag.Get("json")
		sep := strings.Split(jsonId, ",")
		if len(sep) > 1 {
			jsonId = sep[0]
		}

		if v1.Kind() == reflect.Pointer {
			// If this is a pointer to a struct, dereference before continuing.
			if v1.Elem().Kind() == reflect.Struct {
				v1 = v1.Elem()
				v2 = v2.Elem()
			}
		}
		if v1.Kind() == reflect.Struct {
			errs = validateStructDeepEqual(v1, v2, path.Child(jsonId), errs)
		} else {
			if v1.CanInterface() {
				// Slices are actually tricky to compare and determine what has actually changed.  Only do the comparisons
				// If they are the same length, otherwise we'll just have to rely on DeepEqual().
				if v1.Kind() == reflect.Slice && v1.Len() > 0 && v1.Len() == v2.Len() && v1.Index(0).Kind() == reflect.Struct {
					for i := 0; i < v1.Len(); i++ {
						errs = validateStructDeepEqual(v1.Index(i), v2.Index(i), path.Child(jsonId), errs)
					}
				} else {
					// Using DeepEqual() here because it takes care of all the type checking/comparison magic.
					if !equality.Semantic.DeepEqual(v1.Interface(), v2.Interface()) {
						errs = append(errs, field.Invalid(path.Child(jsonId), v1.Interface(), "Attempted to change an immutable field"))
					}
				}
			}
		}
	}
	return errs
}

// validateStructEqual uses introspection to walk through the fields of a struct and check
// for differences.  Any differences are flagged as an invalid change to an immutable field.
func validateStructEqual(x any, y any, path *field.Path) field.ErrorList {
	var errs field.ErrorList

	if x == nil || y == nil {
		errs = append(errs, field.InternalError(path, errors.New("nil struct")))
		return errs
	}
	v1 := reflect.ValueOf(x)
	v2 := reflect.ValueOf(y)
	if v1.Type() != v2.Type() {
		errs = append(errs, field.InternalError(path, errors.New("comparing structs of different type")))
		return errs
	}
	if v1.Kind() != reflect.Struct {
		errs = append(errs, field.InternalError(path, errors.New("comparing non structs")))
		return errs
	}
	return validateStructDeepEqual(v1, v2, path, errs)
}

func validateHostedClusterUpdate(new *hyperv1.HostedCluster, old *hyperv1.HostedCluster) error {
	filterMutableHostedClusterSpecFields(&new.Spec)
	filterMutableHostedClusterSpecFields(&old.Spec)

	// Only allow these to be set from empty.  Once set they should not be changed.
	if old.Spec.InfraID == "" {
		new.Spec.InfraID = ""
	}
	if old.Spec.ClusterID == "" {
		new.Spec.ClusterID = ""
	}

	// We default the port in Azure management cluster, so we allow setting it from being unset, but no updates.
	if new.Spec.Networking.APIServer != nil && (old.Spec.Networking.APIServer == nil || old.Spec.Networking.APIServer.Port == nil) {
		if old.Spec.Networking.APIServer == nil {
			old.Spec.Networking.APIServer = &hyperv1.APIServerNetworking{}
		}
		old.Spec.Networking.APIServer.Port = new.Spec.Networking.APIServer.Port
	}

	errs := validateStructEqual(new.Spec, old.Spec, field.NewPath("HostedCluster.spec"))

	return errs.ToAggregate()
}
