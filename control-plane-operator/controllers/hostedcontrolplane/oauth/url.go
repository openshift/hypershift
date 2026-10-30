package oauth

import (
	"net"
	"net/url"
	"strconv"
	"strings"
)

// ExternalURL returns the external https URL of the OAuth server.
// The port is omitted when it is 443, the https default.
func ExternalURL(host string, port int32) string {
	// JoinHostPort brackets IPv6 literals, which url.URL requires.
	hostPort := net.JoinHostPort(host, strconv.Itoa(int(port)))
	if port == 443 {
		hostPort = strings.TrimSuffix(hostPort, ":443")
	}
	return (&url.URL{Scheme: "https", Host: hostPort}).String()
}
