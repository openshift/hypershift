// Package ipnet wraps net.IPNet to get CIDR serialization.
// derived from: https://github.com/openshift/installer/blob/e6ac416efbf6d8dcc5a36e1187a4e05bbe7c9319/pkg/ipnet/ipnet.go
package ipnet

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
)

var nullString = "null"
var nilString = "<nil>"
var nullBytes = []byte(nullString)

// IPNet wraps net.IPNet to get CIDR serialization.
//
// +kubebuilder:validation:Type=string
// +kubebuilder:validation:MaxLength=43
// +kubebuilder:validation:XValidation:rule=`self.matches('^((\\d{1,3}\\.){3}\\d{1,3}/\\d{1,2})$') || self.matches('^([0-9a-fA-F]{0,4}:){2,7}([0-9a-fA-F]{0,4})?/[0-9]{1,3}$')`,message="cidr must be a valid IPv4 or IPv6 CIDR notation (e.g., 192.168.1.0/24 or 2001:db8::/64)"
type IPNet net.IPNet

type IPNets []IPNet

func (ipnets IPNets) StringSlice() []string {
	out := make([]string, 0, len(ipnets))
	for _, n := range ipnets {
		out = append(out, n.String())
	}
	return out
}

func (ipnets IPNets) CSVString() string {
	return strings.Join(ipnets.StringSlice(), ",")
}

// String returns a CIDR serialization of the subnet, or an empty
// string if the subnet is nil.
func (ipnet *IPNet) String() string {
	if ipnet == nil {
		return ""
	}
	return (*net.IPNet)(ipnet).String()
}

// MarshalJSON interface for an IPNet
func (ipnet *IPNet) MarshalJSON() (data []byte, err error) {
	if ipnet == nil || len(ipnet.IP) == 0 {
		return nullBytes, nil
	}

	return json.Marshal(ipnet.String())
}

// UnmarshalJSON interface for an IPNet
func (ipnet *IPNet) UnmarshalJSON(b []byte) (err error) {
	if string(b) == nullString {
		ipnet.IP = net.IP{}
		ipnet.Mask = net.IPMask{}
		return nil
	}

	var cidr string
	err = json.Unmarshal(b, &cidr)
	if err != nil {
		return fmt.Errorf("could not unmarshal string: %w", err)
	}

	if cidr == nilString {
		ipnet.IP = net.IP{}
		ipnet.Mask = net.IPMask{}
		return nil
	}

	parsedIPNet, err := ParseCIDR(cidr)
	if err != nil {
		return fmt.Errorf("could not parse cidr %s: %w", cidr, err)
	}

	*ipnet = *parsedIPNet

	return nil
}

func (in *IPNet) DeepCopy() *IPNet {
	out := IPNet{
		IP:   append([]byte{}, in.IP...),
		Mask: append([]byte{}, in.Mask...),
	}
	return &out
}

func (in *IPNet) DeepCopyInto(out *IPNet) {
	clone := in.DeepCopy()
	*out = *clone
}

// ParseCIDR parses a CIDR from its string representation.
func ParseCIDR(s string) (*IPNet, error) {
	ip, cidr, err := net.ParseCIDR(s)
	if err != nil {
		return nil, err
	}

	// This check is needed in order to work around a strange quirk in the Go
	// standard library. All of the addresses returned by net.ParseCIDR() are
	// 16-byte addresses. This does _not_ imply that they are IPv6 addresses,
	// which is what some libraries (e.g. github.com/apparentlymart/go-cidr)
	// assume. By forcing the address to be the expected length, we can work
	// around these bugs.
	if ip.To4() != nil {
		ip = ip.To4()
	}

	return &IPNet{
		IP:   ip,
		Mask: cidr.Mask,
	}, nil
}

// MustParseCIDR parses a CIDR from its string representation. If the parse fails,
// the function will panic.
func MustParseCIDR(s string) *IPNet {
	cidr, err := ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return cidr
}
