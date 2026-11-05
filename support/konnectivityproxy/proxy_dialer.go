package konnectivityproxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

const proxySetupTimeout = 60 * time.Second

func newHTTPDialer(proxyURL *url.URL) *httpProxyDialer {
	return &httpProxyDialer{proxyURL: proxyURL, forwardDial: proxy.Direct.DialContext}
}

// Everything below is a copied from https://github.com/fasthttp/websocket/blob/2f8e79d2aac1e8e5a06518870e872b15608cea90/proxy.go
// as the golang.org/x/net/proxy package only supports socks5 proxies, but does allow registering additional protocols.
type httpProxyDialer struct {
	proxyURL     *url.URL
	forwardDial  func(ctx context.Context, network, addr string) (net.Conn, error)
	rootCAs      *x509.CertPool
	setupTimeout time.Duration
}

func (hpd *httpProxyDialer) Dial(network string, addr string) (net.Conn, error) {
	return hpd.DialContext(context.Background(), network, addr)
}

func (hpd *httpProxyDialer) DialContext(ctx context.Context, network string, addr string) (result net.Conn, err error) {
	timeout := hpd.setupTimeout
	if timeout == 0 {
		timeout = proxySetupTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	hostPort, _ := hostPortNoPort(hpd.proxyURL)
	raw, err := hpd.forwardDial(ctx, network, hostPort)
	if err != nil {
		return nil, err
	}
	conn := raw
	canceled := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		raw.Close()
		close(canceled)
	})
	defer func() {
		if !stop() {
			<-canceled
		}
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		if err == nil {
			err = conn.SetDeadline(time.Time{})
		}
		if err != nil {
			raw.Close()
			result = nil
		}
	}()
	deadline, _ := ctx.Deadline()
	if err := raw.SetDeadline(deadline); err != nil {
		return nil, err
	}

	if hpd.proxyURL.Scheme == "https" {
		tlsConn := tls.Client(conn, &tls.Config{
			ServerName: hpd.proxyURL.Hostname(),
			MinVersion: tls.VersionTLS12,
			RootCAs:    hpd.rootCAs,
		})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return nil, err
		}
		conn = tlsConn
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
		return nil, err
	}

	// Read response. It's OK to use and discard buffered reader here because
	// the remote server does not speak until spoken to.
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, connectReq)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(resp.Status)
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
