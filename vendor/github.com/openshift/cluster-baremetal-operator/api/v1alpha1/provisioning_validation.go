/*

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	"bytes"
	"fmt"
	"net"
	"net/url"
	"strings"

	"k8s.io/apimachinery/pkg/util/errors"
	ctrl "sigs.k8s.io/controller-runtime"
)

type EnabledFeatures struct {
	ProvisioningNetwork map[ProvisioningNetwork]bool
}

var (
	log = ctrl.Log.WithName("provisioning_validation")
)

// ValidateBaremetalProvisioningConfig validates the contents of the provisioning resource
func (prov *Provisioning) ValidateBaremetalProvisioningConfig(enabledFeatures EnabledFeatures) error {
	provisioningNetworkMode := prov.getProvisioningNetworkMode()
	log.V(1).Info("provisioning network", "mode", provisioningNetworkMode)

	/*
	   Managed:
	   "ProvisioningIP"
	   "ProvisioningNetworkCIDR"
	   "ProvisioningDHCPRange"
	   "ProvisioningOSDownloadURL"

	   Unmanaged:
	   "ProvisioningIP"
	   "ProvisioningNetworkCIDR"
	   "ProvisioningOSDownloadURL"

	   Disabled:
	   "ProvisioningOSDownloadURL"

	   And optionally when Disabled, both:
	   "ProvisioningIP"
	   "ProvisioningNetworkCIDR"
	*/

	var errs []error

	if !enabledFeatures.ProvisioningNetwork[provisioningNetworkMode] {
		return errors.NewAggregate(append(errs, fmt.Errorf("ProvisioningNetwork %s is not supported", provisioningNetworkMode)))
	}

	// They all use provisioningOSDownloadURL
	if err := validateProvisioningOSDownloadURL(prov.Spec.ProvisioningOSDownloadURL); err != nil {
		errs = append(errs, err...)
	}

	if provisioningNetworkMode == ProvisioningNetworkDisabled {
		// Only check network settings in Disabled mode if it's set.
		if prov.Spec.ProvisioningNetworkCIDR == "" && prov.Spec.ProvisioningIP == "" {
			return errors.NewAggregate(errs)
		}
	}

	// Only force check of dhcpRange if in managed mode.
	dhcpRange := prov.Spec.ProvisioningDHCPRange
	if provisioningNetworkMode != ProvisioningNetworkManaged {
		dhcpRange = ""
	}

	if err := validateProvisioningNetworkSettings(prov.Spec.ProvisioningIP, prov.Spec.ProvisioningNetworkCIDR, dhcpRange, prov.getProvisioningNetworkMode()); err != nil {
		errs = append(errs, err...)
	}

	// We need to check this here because we've designed validateProvisioningNetworkSettings() to allow an empty DHCP Range.
	if provisioningNetworkMode == ProvisioningNetworkManaged {
		if prov.Spec.ProvisioningDHCPRange == "" {
			errs = append(errs, fmt.Errorf("provisioningDHCPRange is required in Managed mode but is not set"))
		}
	}

	return errors.NewAggregate(errs)
}

func (prov *Provisioning) getProvisioningNetworkMode() ProvisioningNetwork {
	provisioningNetworkMode := prov.Spec.ProvisioningNetwork
	if provisioningNetworkMode == "" {
		// Set it to the default Managed mode
		provisioningNetworkMode = ProvisioningNetworkManaged
		if prov.Spec.ProvisioningDHCPExternal {
			log.V(1).Info("provisioningDHCPExternal is deprecated and will be removed in the next release. Use provisioningNetwork instead.")
			provisioningNetworkMode = ProvisioningNetworkUnmanaged
		} else {
			log.V(1).Info("provisioningNetwork and provisioningDHCPExternal not set, defaulting to managed network")
		}
	}
	return provisioningNetworkMode
}

func validateProvisioningOSDownloadURL(uri string) []error {
	var errs []error

	if uri == "" {
		return errs
	}

	parsedURL, err := url.ParseRequestURI(uri)
	if err != nil {
		errs = append(errs, fmt.Errorf("the provisioningOSDownloadURL provided: %q is invalid", uri))
		// If it's not a valid URI lets just return.
		return errs
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		errs = append(errs, fmt.Errorf("unsupported scheme %q in provisioningOSDownloadURL %s", parsedURL.Scheme, uri))
		// Again it's not worth it if it's not http(s)
		return errs
	}
	var sha256Checksum string
	if sha256Checksums, ok := parsedURL.Query()["sha256"]; ok {
		sha256Checksum = sha256Checksums[0]
	}
	if sha256Checksum == "" {
		errs = append(errs, fmt.Errorf("the sha256 parameter in the provisioningOSDownloadURL %q is missing", uri))
	}
	if len(sha256Checksum) != 64 {
		errs = append(errs, fmt.Errorf("the sha256 parameter in the provisioningOSDownloadURL %q is invalid", uri))
	}
	if !strings.HasSuffix(parsedURL.Path, ".qcow2.gz") && !strings.HasSuffix(parsedURL.Path, ".qcow2.xz") {
		errs = append(errs, fmt.Errorf("the provisioningOSDownloadURL provided: %q is an OS image and must end in .qcow2.gz or .qcow2.xz", uri))
	}

	return errs
}

func validateProvisioningNetworkSettings(ip string, cidr string, dhcpRange string, provisioningNetworkMode ProvisioningNetwork) []error {
	// provisioningIP and networkCIDR are always set.  DHCP range is optional
	// depending on mode.
	var errs []error

	// Verify provisioning ip and get it into net format for future tests.
	provisioningIP := net.ParseIP(ip)
	if provisioningIP == nil {
		errs = append(errs, fmt.Errorf("could not parse provisioningIP %q", ip))
		return errs
	}

	// Verify Network CIDR
	_, provisioningCIDR, err := net.ParseCIDR(cidr)
	if err != nil {
		errs = append(errs, fmt.Errorf("could not parse provisioningNetworkCIDR %q", cidr))
		return errs
	}

	// We cannot have managed ipv6 provisioning networks larger than a /64 due
	// to a limitation in dnsmasq
	cidrSize, _ := provisioningCIDR.Mask.Size()
	if cidrSize < 64 && provisioningCIDR.IP.To4() == nil && provisioningCIDR.IP.To16() != nil && provisioningNetworkMode == ProvisioningNetworkManaged {
		errs = append(errs, fmt.Errorf("provisioningNetworkCIDR mask must be greater than or equal to 64 for managed IPv6 networks"))
	}

	// Ensure provisioning IP is in the network CIDR
	if !provisioningCIDR.Contains(provisioningIP) {
		errs = append(errs, fmt.Errorf("provisioningIP %q is not in the range defined by the provisioningNetworkCIDR %q", ip, cidr))
	}

	// DHCP Range might not be set in which case we're done here.
	if dhcpRange == "" {
		return errs
	}

	// We want to allow a space after the ',' if the user likes it.
	dhcpRange = strings.ReplaceAll(dhcpRange, ", ", ",")

	// Test DHCP Range.
	dhcpRangeSplit := strings.Split(dhcpRange, ",")
	if len(dhcpRangeSplit) != 2 {
		errs = append(errs, fmt.Errorf("%q is not a valid provisioningDHCPRange.  DHCP range format: start_ip,end_ip", dhcpRange))
		return errs
	}

	for _, ip := range dhcpRangeSplit {
		// Ensure IP is valid
		dhcpIP := net.ParseIP(ip)
		if dhcpIP == nil {
			errs = append(errs, fmt.Errorf("could not parse provisioningDHCPRange, %q is not a valid IP", ip))
			// Can't really do further tests without valid IPs
			return errs
		}

		// Validate IP is in the provisioning network
		if !provisioningCIDR.Contains(dhcpIP) {
			errs = append(errs, fmt.Errorf("invalid provisioningDHCPRange, IP %q is not part of the provisioningNetworkCIDR %q", dhcpIP, cidr))
		}
	}

	// Ensure provisioning IP is not in the DHCP range
	start := net.ParseIP(dhcpRangeSplit[0])
	end := net.ParseIP(dhcpRangeSplit[1])

	if start != nil && end != nil {
		if bytes.Compare(provisioningIP, start) >= 0 && bytes.Compare(provisioningIP, end) <= 0 {
			errs = append(errs, fmt.Errorf("invalid provisioningIP %q, value must be outside of the provisioningDHCPRange %q", provisioningIP, dhcpRange))
		}
	}

	return errs
}
