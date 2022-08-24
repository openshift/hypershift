package hostedcluster

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

// Webhook implements a validating webhook for HostedCluster.
type Webhook struct {
	serviceNetworkCidrEntries []cidrEntry
}

type cidrEntry struct {
	net  net.IPNet
	path field.Path
}

// SetupWebhookWithManager sets up HostedCluster webhooks.
func SetupWebhookWithManager(ctx context.Context, mgr ctrl.Manager, log logr.Logger) error {
	var webhook Webhook
	var err error

	webhook.serviceNetworkCidrEntries, err = getNetworkServiceCIDRs(ctx, mgr.GetAPIReader())
	if err != nil {
		log.Info("Failed to get network service CIDRs: %w", err)
	}
	return ctrl.NewWebhookManagedBy(mgr).
		For(&hyperv1.HostedCluster{}).
		WithValidator(&webhook).
		Complete()
}

var _ webhook.CustomValidator = &Webhook{}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type.
func (webhook *Webhook) ValidateCreate(ctx context.Context, obj runtime.Object) error {
	hostedCluster, ok := obj.(*hyperv1.HostedCluster)
	if !ok {
		return apierrors.NewBadRequest(fmt.Sprintf("expected a HostedCluster but got a %T", obj))
	}

	return validateHostedClusterCreate(ctx, webhook.serviceNetworkCidrEntries, hostedCluster)
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

	return validateHostedClusterUpdate(ctx, webhook.serviceNetworkCidrEntries, newHC, oldHC)
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type.
func (webhook *Webhook) ValidateDelete(_ context.Context, obj runtime.Object) error {
	return nil
}

func getNetworkServiceCIDRs(ctx context.Context, c client.Reader) ([]cidrEntry, error) {
	var cidrEntries []cidrEntry

	network := &configv1.Network{}
	err := c.Get(ctx, client.ObjectKey{
		Name: "cluster",
	}, network)

	if err != nil {
		return nil, fmt.Errorf("unable to get network.spec.serviceNetwork: %s", err)
	}

	for _, cidr := range network.Spec.ServiceNetwork {
		_, serviceNet, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, err
		} else {
			ce := cidrEntry{*serviceNet, *field.NewPath("network.spec.serviceNetwork")}
			cidrEntries = append(cidrEntries, ce)
		}
	}
	return cidrEntries, nil
}

func cidrsOverlap(net1 *net.IPNet, net2 *net.IPNet) error {
	if net1.Contains(net2.IP) || net2.Contains(net1.IP) {
		return fmt.Errorf("%s and %s", net1.String(), net2.String())
	}
	return nil
}

func validateNoCIDROverlap(ce []cidrEntry) field.ErrorList {
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

// Validate the old single string format for each CIDR.
func validateNetworkCIDRs(hc *hyperv1.HostedCluster) field.ErrorList {
	var errs field.ErrorList
	var cidrEntries []cidrEntry

	podCIDR := hc.Spec.Networking.PodCIDR
	serviceCIDR := hc.Spec.Networking.ServiceCIDR
	machineCIDR := hc.Spec.Networking.MachineCIDR

	// If these are unset we should ignore them.  They're using the
	// new API which is a slice of network addresses for each.
	if podCIDR == "" && serviceCIDR == "" && machineCIDR == "" {
		return errs
	}

	// Validate CIDR format.
	_, serviceNet, err := net.ParseCIDR(serviceCIDR)
	if err != nil {
		errs = append(errs, field.Invalid(field.NewPath("hostedcluster.spec.networking.serviceCIDR"), serviceCIDR, err.Error()))
	} else {
		ce := cidrEntry{*serviceNet, *field.NewPath("hostedcluster.spec.networking.serviceCIDR")}
		cidrEntries = append(cidrEntries, ce)
	}

	_, podNet, err := net.ParseCIDR(podCIDR)
	if err != nil {
		errs = append(errs, field.Invalid(field.NewPath("hostedcluster.spec.networking.podCIDR"), podCIDR, err.Error()))
	} else {
		ce := cidrEntry{*podNet, *field.NewPath("hostedcluster.spec.networking.podCIDR")}
		cidrEntries = append(cidrEntries, ce)
	}

	_, machineNet, err := net.ParseCIDR(machineCIDR)
	if err != nil {
		errs = append(errs, field.Invalid(field.NewPath("hostedcluster.spec.networking.machineCIDR"), machineCIDR, err.Error()))
	} else {
		ce := cidrEntry{*machineNet, *field.NewPath("hostedcluster.spec.networking.machineCIDR")}
		cidrEntries = append(cidrEntries, ce)
	}

	// Bail if we can't parse.
	if len(errs) > 0 {
		return errs
	}

	return validateNoCIDROverlap(cidrEntries)
}

// Validate the new slice of network CIDRs.
func validateSliceNetworkCIDRs(hc *hyperv1.HostedCluster) field.ErrorList {
	var cidrEntries []cidrEntry

	for _, cidr := range hc.Spec.Networking.MachineNetwork {
		ce := cidrEntry{(net.IPNet)(cidr.CIDR), *field.NewPath("hostedcluster.spec.networking.machineNetwork")}
		cidrEntries = append(cidrEntries, ce)
	}
	for _, cidr := range hc.Spec.Networking.ServiceNetwork {
		ce := cidrEntry{(net.IPNet)(cidr.CIDR), *field.NewPath("hostedcluster.spec.networking.serviceNetwork")}
		cidrEntries = append(cidrEntries, ce)
	}
	for _, cidr := range hc.Spec.Networking.ClusterNetwork {
		ce := cidrEntry{(net.IPNet)(cidr.CIDR), *field.NewPath("hostedcluster.spec.networking.clusterNetwork")}
		cidrEntries = append(cidrEntries, ce)
	}

	return validateNoCIDROverlap(cidrEntries)
}

// This validates the old network settings do not collide with the cluster network settings.
func validateServiceNetworkNoOverlap(serviceNetworkCidrEntries []cidrEntry, hc *hyperv1.HostedCluster) field.ErrorList {
	var errs field.ErrorList
	var cidrEntries []cidrEntry

	serviceCIDR := hc.Spec.Networking.ServiceCIDR
	if serviceCIDR == "" {
		return errs
	}

	_, serviceNet, err := net.ParseCIDR(serviceCIDR)
	if err != nil {
		errs = append(errs, field.Invalid(field.NewPath("hostedcluster.spec.networking.serviceCIDR"), serviceCIDR, err.Error()))
		return errs
	} else {
		ce := cidrEntry{*serviceNet, *field.NewPath("hostedcluster.spec.networking.serviceCIDR")}
		cidrEntries = append(cidrEntries, ce)
	}

	cidrEntries = append(cidrEntries, serviceNetworkCidrEntries...)

	return validateNoCIDROverlap(cidrEntries)
}

func validateSliceServiceNetworkNoOverlap(serviceNetworkCidrEntries []cidrEntry, hc *hyperv1.HostedCluster) field.ErrorList {
	var cidrEntries []cidrEntry

	for _, cidr := range hc.Spec.Networking.ServiceNetwork {
		ce := cidrEntry{(net.IPNet)(cidr.CIDR), *field.NewPath("hostedcluster.spec.networking.serviceNetwork")}
		cidrEntries = append(cidrEntries, ce)
	}

	cidrEntries = append(cidrEntries, serviceNetworkCidrEntries...)

	return validateNoCIDROverlap(cidrEntries)
}

func validateHostedClusterCreate(ctx context.Context, serviceNetworkCidrEntries []cidrEntry, hc *hyperv1.HostedCluster) error {
	errs := validateNetworkCIDRs(hc)
	errs = append(errs, validateSliceNetworkCIDRs(hc)...)
	errs = append(errs, validateSliceServiceNetworkNoOverlap(serviceNetworkCidrEntries, hc)...)

	return errs.ToAggregate()
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

func validateEndpointAccess(new *hyperv1.PlatformSpec, old *hyperv1.PlatformSpec) field.ErrorList {
	var errs field.ErrorList

	if old.Type != hyperv1.AWSPlatform || new.Type != hyperv1.AWSPlatform || old.AWS == nil || new.AWS == nil {
		return nil
	}
	if old.AWS.EndpointAccess == new.AWS.EndpointAccess {
		return nil
	}
	if old.AWS.EndpointAccess == hyperv1.Public || new.AWS.EndpointAccess == hyperv1.Public {
		errs = append(errs, field.InternalError(field.NewPath("hostedcluster.AWS.EndpointAccess"), fmt.Errorf("transitioning from EndpointAccess %s to %s is not allowed", old.AWS.EndpointAccess, new.AWS.EndpointAccess)))
		return errs
	}
	// Clear EndpointAccess for further validation
	old.AWS.EndpointAccess = ""
	new.AWS.EndpointAccess = ""

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

func validateHostedClusterUpdate(ctx context.Context, serviceNetworkCidrEntries []cidrEntry, new *hyperv1.HostedCluster, old *hyperv1.HostedCluster) error {
	errs := validateNetworkCIDRs(new)
	errs = append(errs, validateServiceNetworkNoOverlap(serviceNetworkCidrEntries, new)...)

	// The rest of this deals with checking for immutable fields.
	// Note that we zero various values in here.
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

	errs = append(errs, validateEndpointAccess(&new.Spec.Platform, &old.Spec.Platform)...)

	errs = append(errs, validateStructEqual(new.Spec, old.Spec, field.NewPath("hostedcluster.spec"))...)
	return errs.ToAggregate()
}
