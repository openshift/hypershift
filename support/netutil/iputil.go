package netutil

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"time"
)

// IsIPv4CIDR checks if the input string is an IPv4 CIDR.
func IsIPv4CIDR(input string) (bool, error) {
	_, ipnet, err := net.ParseCIDR(input)
	if err != nil {
		return false, fmt.Errorf("error parsing input '%s': not a valid CIDR", input)
	}
	if ipnet.IP.To4() != nil {
		return true, nil
	}
	return false, nil
}

// IsIPv4Address checks if the input string is an IPv4 address.
func IsIPv4Address(input string) (bool, error) {
	ip := net.ParseIP(input)
	if ip == nil {
		return false, fmt.Errorf("error parsing input '%s': not a valid IP address", input)
	}
	if ip.To4() != nil {
		return true, nil
	}
	return false, nil
}

// FirstUsableIP returns the first usable IP in both, IPv4 and IPv6 stacks.
func FirstUsableIP(cidr string) (string, error) {
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", fmt.Errorf("error validating the incoming CIDR %s: %w", cidr, err)
	}
	ip := ipNet.IP
	ip[len(ipNet.IP)-1]++
	return ip.String(), nil
}

// ResolveDNSHostname receives a hostname string and tries to resolve it.
// Returns error if the host can't be resolved.
func ResolveDNSHostname(ctx context.Context, hostName string) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	ips, err := net.DefaultResolver.LookupIPAddr(timeoutCtx, hostName)
	if err == nil && len(ips) == 0 {
		err = fmt.Errorf("couldn't resolve %s", hostName)
	}

	return err
}

var (
	hasPortRegex = regexp.MustCompile(`:\d{1,5}$`)
)

func HostFromURL(addr string) (string, error) {
	parsedURL, err := url.Parse(addr)
	if err != nil {
		return "", fmt.Errorf("failed to parse URL(%s): %w", addr, err)
	}
	hostPort := parsedURL.Host
	if hostPort == "" {
		return "", fmt.Errorf("missing host/port name in URL(%s)", addr)
	}
	if !hasPortRegex.MatchString(hostPort) {
		return hostPort, nil
	}
	hostName, _, err := net.SplitHostPort(hostPort)
	if err != nil {
		return "", fmt.Errorf("failed to split host/port from (%s): %w", hostPort, err)
	}
	if hostName == "" {
		return "", fmt.Errorf("missing host name in URL(%s)", addr)
	}
	return hostName, nil

}
