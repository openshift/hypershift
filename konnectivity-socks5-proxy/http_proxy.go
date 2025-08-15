package konnectivitysocks5proxy

import (
	"bufio"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	"golang.org/x/net/proxy"
)

func init() {
	// The proxy package only interprets the ALL_PROXY variable. This is only used
	// for cloud provider endpoints, so use the https proxy.
	if err := os.Setenv("ALL_PROXY", os.Getenv("HTTPS_PROXY")); err != nil {
		// Can't really handle this error in init, but at least avoid silent failure
		panic("Failed to set ALL_PROXY environment variable: " + err.Error())
	}
	// The proxy itself might be using either http or https though, so we have to
	// register both dialers.
	proxy.RegisterDialerType("http", newHttpDialer)
	proxy.RegisterDialerType("https", newHttpDialer)
}

func newHttpDialer(proxyURL *url.URL, forwardDialer proxy.Dialer) (proxy.Dialer, error) {
	return &httpProxyDialer{proxyURL: proxyURL, forwardDial: forwardDialer.Dial}, nil
}

// Everything below is a copied from https://github.com/fasthttp/websocket/blob/2f8e79d2aac1e8e5a06518870e872b15608cea90/proxy.go
// as the golang.org/x/net/proxy package only supports socks5 proxies, but does allow registering additional protocols.
type httpProxyDialer struct {
	proxyURL    *url.URL
	forwardDial func(network, addr string) (net.Conn, error)
}

func (hpd *httpProxyDialer) Dial(network string, addr string) (net.Conn, error) {
	hostPort, _ := hostPortNoPort(hpd.proxyURL)
	conn, err := hpd.forwardDial(network, hostPort)
	if err != nil {
		return nil, err
	}

	connectHeader := make(http.Header)
	if user := hpd.proxyURL.User; user != nil {
		proxyUser := user.Username()
		if proxyPassword, passwordSet := user.Password(); passwordSet {
			credential := base64.StdEncoding.EncodeToString([]byte(proxyUser + ":" + proxyPassword))
			connectHeader.Set("Proxy-Authorization", "Basic "+credential)
		}
	}

	connectReq := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: addr},
		Host:   addr,
		Header: connectHeader,
	}

	if err := connectReq.Write(conn); err != nil {
		if closeErr := conn.Close(); closeErr != nil {
			return nil, errors.Join(err, fmt.Errorf("failed to close connection: %w", closeErr))
		}
		return nil, err
	}

	// Read response. It's OK to use and discard buffered reader here because
	// the remote server does not speak until spoken to.
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, connectReq)
	if err != nil {
		if closeErr := conn.Close(); closeErr != nil {
			return nil, errors.Join(err, fmt.Errorf("failed to close connection: %w", closeErr))
		}
		return nil, err
	}

	if resp.StatusCode != 200 {
		f := strings.SplitN(resp.Status, " ", 2)
		statusErr := errors.New(f[1])
		if closeErr := conn.Close(); closeErr != nil {
			return nil, errors.Join(statusErr, fmt.Errorf("failed to close connection: %w", closeErr))
		}
		return nil, statusErr
	}
	return conn, nil
}

func hostPortNoPort(u *url.URL) (hostPort, hostNoPort string) {
	hostPort = u.Host
	hostNoPort = u.Host
	if i := strings.LastIndex(u.Host, ":"); i > strings.LastIndex(u.Host, "]") {
		hostNoPort = hostNoPort[:i]
	} else {
		switch u.Scheme {
		case "wss":
			hostPort += ":443"
		case "https":
			hostPort += ":443"
		default:
			hostPort += ":80"
		}
	}
	return hostPort, hostNoPort
}
