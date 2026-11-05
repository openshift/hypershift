package konnectivityproxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/go-logr/logr"
)

const proxyTestAuthority = "transport-test.amazonaws.com:443"

type proxyRecordingConn struct {
	net.Conn
	wire bytes.Buffer
}

type proxyCloseTrackingConn struct {
	net.Conn
	closed atomic.Bool
}

type proxyDeadlineErrorConn struct {
	net.Conn
	calls, failOn int
}

func (conn *proxyDeadlineErrorConn) SetDeadline(deadline time.Time) error {
	conn.calls++
	if conn.calls == conn.failOn {
		return errors.New("synthetic deadline error")
	}
	return conn.Conn.SetDeadline(deadline)
}

func (conn *proxyCloseTrackingConn) Close() error {
	conn.closed.Store(true)
	return conn.Conn.Close()
}

func (conn *proxyRecordingConn) Read(buffer []byte) (int, error) {
	count, err := conn.Conn.Read(buffer)
	conn.wire.Write(buffer[:count])
	return count, err
}

func proxyTestCertificate(t *testing.T, wrongHost bool) (tls.Certificate, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "synthetic proxy"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:        true, BasicConstraintsValid: true,
		DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	if wrongHost {
		template.DNSNames = []string{"wrong.invalid"}
		template.IPAddresses = nil
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func serveProxyTestConn(ctx context.Context, raw net.Conn, certificate tls.Certificate, proxyURL *url.URL, expectedAuth string) (wire []byte, connected bool, serveErr error) {
	defer raw.Close()
	if err := raw.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return nil, false, err
	}
	recording := &proxyRecordingConn{Conn: raw}
	defer func() { wire = recording.wire.Bytes() }()
	var conn net.Conn = recording
	if proxyURL.Scheme == "https" {
		tlsConn := tls.Server(recording, &tls.Config{Certificates: []tls.Certificate{certificate}, ClientAuth: tls.RequestClientCert, MinVersion: tls.VersionTLS12})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_, closeErr := io.Copy(io.Discard, recording)
			if errors.Is(closeErr, syscall.ECONNRESET) {
				closeErr = nil
			}
			return nil, false, closeErr
		}
		state := tlsConn.ConnectionState()
		if len(state.PeerCertificates) != 0 || (proxyURL.Hostname() == "localhost" && state.ServerName != "localhost") {
			return nil, false, errors.New("unexpected client certificate or proxy SNI")
		}
		conn = tlsConn
	}
	request, err := http.ReadRequest(bufio.NewReader(conn))
	if err != nil {
		return nil, false, err
	}
	if request.Method != http.MethodConnect || request.Host != proxyTestAuthority || request.Header.Get("Proxy-Authorization") != expectedAuth {
		return nil, true, errors.New("unexpected CONNECT method, authority or authentication")
	}
	if _, err := io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return nil, true, err
	}
	_, err = io.Copy(conn, conn)
	return nil, true, err
}

func TestKonnectivityProxy_DialContext(t *testing.T) {
	for _, testCase := range []struct {
		name, scheme, host, user, mode string
		missingCA, wrongHost           bool
	}{
		{name: "When the HTTPS proxy has a trusted IP certificate, it should encrypt CONNECT and tunnel traffic", scheme: "https", host: "127.0.0.1", user: "synthetic-user:synthetic-password", mode: "success"},
		{name: "When the HTTPS proxy has a trusted DNS certificate, it should verify the proxy hostname", scheme: "https", host: "localhost", user: "synthetic-user:synthetic-password", mode: "success"},
		{name: "When the proxy CA is missing from trusted roots, it should fail closed", scheme: "https", host: "127.0.0.1", user: "synthetic-user:synthetic-password", mode: "untrusted", missingCA: true},
		{name: "When the trusted proxy certificate has the wrong hostname, it should fail closed", scheme: "https", host: "localhost", user: "synthetic-user:synthetic-password", mode: "hostname", wrongHost: true},
		{name: "When the proxy uses HTTP, it should preserve CONNECT authentication", scheme: "http", host: "127.0.0.1", user: "synthetic-user:synthetic-password", mode: "success"},
		{name: "When the HTTP proxy has no userinfo, it should omit authentication", scheme: "http", host: "127.0.0.1", mode: "success"},
		{name: "When the HTTP proxy has no password, it should preserve omitted authentication", scheme: "http", host: "127.0.0.1", user: "synthetic-user", mode: "success"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			certificate, caPEM := proxyTestCertificate(t, testCase.wrongHost)
			roots := x509.NewCertPool()
			if !testCase.missingCA && !roots.AppendCertsFromPEM(caPEM) {
				t.Fatal("cannot load synthetic proxy CA")
			}
			listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { listener.Close() })
			proxyURL := &url.URL{Scheme: testCase.scheme, Host: net.JoinHostPort(testCase.host, strconv.Itoa(listener.Addr().(*net.TCPAddr).Port))}
			if testCase.user != "" {
				username, password, hasPassword := strings.Cut(testCase.user, ":")
				proxyURL.User = url.User(username)
				if hasPassword {
					proxyURL.User = url.UserPassword(username, password)
				}
			}
			expectedAuth := ""
			if strings.Contains(testCase.user, ":") {
				expectedAuth = "Basic " + base64.StdEncoding.EncodeToString([]byte(testCase.user))
			}
			var captures [][]byte
			var accepted, connected int
			var serverErr error
			done := make(chan struct{})
			go func() {
				defer close(done)
				for {
					raw, err := listener.Accept()
					if err != nil {
						return
					}
					accepted++
					wire, didConnect, err := serveProxyTestConn(t.Context(), raw, certificate, proxyURL, expectedAuth)
					captures = append(captures, wire)
					if didConnect {
						connected++
					}
					if err != nil && serverErr == nil {
						serverErr = err
					}
				}
			}()
			t.Cleanup(func() { listener.Close(); <-done })
			dialer := &konnectivityProxy{
				connectDirectlyToCloudAPIs: true,
				ca:                         caPEM, clientCert: []byte("unused synthetic client certificate"), clientKey: []byte("unused synthetic client key"),
			}
			dialer.log = logr.Discard()
			dialer.httpDialerOnce.Do(func() {
				proxyDialer := newHTTPDialer(proxyURL)
				proxyDialer.rootCAs = roots
				dialer.httpDialer = proxyDialer
			})
			runProxyDialContext(t, testCase.mode, dialer)
			listener.Close()
			<-done
			if accepted != 1 || len(captures) != 1 {
				t.Fatal("expected exactly one proxy connection with no retries")
			}
			if testCase.scheme == "https" {
				wire := captures[0]
				if len(wire) < 5 || wire[0] != 0x16 {
					t.Fatal("proxy connection did not begin with a TLS handshake record")
				}
				for _, forbidden := range []string{"CONNECT ", "Proxy-Authorization", "synthetic-user", "synthetic-password", strings.TrimPrefix(expectedAuth, "Basic ")} {
					if forbidden != "" && bytes.Contains(wire, []byte(forbidden)) {
						t.Fatal("plaintext request or authentication appeared on the proxy connection")
					}
				}
			}
			expectedConnects := 1
			if testCase.mode != "success" {
				expectedConnects = 0
			}
			if connected != expectedConnects || serverErr != nil {
				t.Fatalf("unexpected proxy activity: CONNECTs=%d server error=%v", connected, serverErr)
			}
		})
	}
}

func runProxyDialContext(t *testing.T, mode string, dialer *konnectivityProxy) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	conn, err := dialer.DialContext(ctx, "tcp", proxyTestAuthority)
	if mode != "success" {
		if conn != nil {
			conn.Close()
			t.Fatal("verification failure returned a connection")
		}
		var unknownCA x509.UnknownAuthorityError
		var wrongHost x509.HostnameError
		if (mode == "untrusted" && !errors.As(err, &unknownCA)) || (mode == "hostname" && !errors.As(err, &wrongHost)) {
			t.Fatalf("expected %s certificate verification error, got %T", mode, err)
		}
		return
	}
	if err != nil {
		t.Fatalf("verified proxy connection failed: %v", err)
	}
	defer conn.Close()
	if dialer.tlsConfig != nil {
		t.Fatal("management proxy initialized konnectivity mTLS configuration")
	}
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	payload := []byte("synthetic tunnel traffic")
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("tunnel write failed: %v", err)
	}
	echo := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, echo); err != nil || !bytes.Equal(echo, payload) {
		t.Fatalf("tunnel did not echo the synthetic payload (read error: %v)", err)
	}
}

func TestHttpProxyDialer_Dial(t *testing.T) {
	t.Run("When Dial has no caller context, it should apply the finite production setup bound", func(t *testing.T) {
		proxyURL := &url.URL{Scheme: "https", Host: "localhost"}
		dialer := newHTTPDialer(proxyURL)
		if dialer.rootCAs != nil {
			t.Fatal("production must use system roots by default")
		}
		failure := errors.New("synthetic forward failure")
		dialer.forwardDial = func(ctx context.Context, network, address string) (net.Conn, error) {
			deadline, ok := ctx.Deadline()
			remaining := time.Until(deadline)
			if !ok || remaining <= 59*time.Second || remaining > 60*time.Second {
				t.Error("default setup context must have a sixty-second bound")
			}
			return nil, failure
		}
		if conn, err := dialer.Dial("tcp", proxyTestAuthority); conn != nil || !errors.Is(err, failure) {
			t.Fatal("Dial did not preserve forward error")
		}
	})
	t.Run("When the TLS handshake fails, it should close the TCP connection before returning", func(t *testing.T) {
		certificate, _ := proxyTestCertificate(t, false)
		server := httptest.NewUnstartedServer(nil)
		server.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
		server.StartTLS()
		defer server.Close()
		proxyURL, err := url.Parse(server.URL)
		if err != nil {
			t.Fatal(err)
		}
		var tracked *proxyCloseTrackingConn
		dialer := &httpProxyDialer{proxyURL: proxyURL, forwardDial: func(ctx context.Context, network, address string) (net.Conn, error) {
			conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			tracked = &proxyCloseTrackingConn{Conn: conn}
			t.Cleanup(func() { conn.Close() })
			if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
				return nil, err
			}
			return tracked, nil
		}}
		conn, err := dialer.Dial("tcp", proxyTestAuthority)
		var unknownCA x509.UnknownAuthorityError
		if conn != nil || !errors.As(err, &unknownCA) || tracked == nil || !tracked.closed.Load() {
			t.Fatal("TLS verification failure did not close the underlying connection before returning")
		}
	})
	t.Run("When TCP dialing fails, it should return the error without retrying", func(t *testing.T) {
		failure := errors.New("synthetic dial failure")
		calls := 0
		dialer := &httpProxyDialer{proxyURL: &url.URL{Scheme: "https", Host: "localhost"}, forwardDial: func(ctx context.Context, network, address string) (net.Conn, error) {
			calls++
			if network != "tcp" || address != "localhost:443" {
				t.Error("unexpected forward dial address")
			}
			return nil, failure
		}}
		conn, err := dialer.Dial("tcp", proxyTestAuthority)
		if conn != nil || !errors.Is(err, failure) || calls != 1 {
			t.Fatal("TCP failure was not preserved")
		}
	})
}

func serveStalledProxy(ctx context.Context, server net.Conn, certificate tls.Certificate, scheme, stage string, reached chan<- struct{}, release <-chan struct{}) error {
	defer server.Close()
	if err := server.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	conn := server
	if scheme == "https" && stage != "TLS" {
		tlsConn := tls.Server(server, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return err
		}
		conn = tlsConn
	}
	if stage == "TLS" || stage == "CONNECT write" {
		close(reached)
		select {
		case <-release:
			return nil
		case <-time.After(5 * time.Second):
			return errors.New("test did not release stalled proxy")
		}
	}
	if _, err := http.ReadRequest(bufio.NewReader(conn)); err != nil {
		return err
	}
	close(reached)
	_, err := io.Copy(io.Discard, conn)
	return err
}

func runProxySetupStall(t *testing.T, scheme, stage, trigger string) {
	t.Helper()
	client, server := net.Pipe()
	tracked := &proxyCloseTrackingConn{Conn: client}
	t.Cleanup(func() { client.Close(); server.Close() })
	certificate, caPEM := proxyTestCertificate(t, false)
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		t.Fatal("cannot load proxy root")
	}
	reached := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	serverDone := make(chan error, 1)
	serverExited := make(chan struct{})
	if stage != "TCP" {
		t.Cleanup(func() {
			client.Close()
			server.Close()
			select {
			case <-serverExited:
			case <-time.After(5 * time.Second):
				t.Error("server goroutine did not finish")
			}
		})
		go func() {
			defer close(serverExited)
			serverDone <- serveStalledProxy(t.Context(), server, certificate, scheme, stage, reached, release)
		}()
	}
	dialer := &httpProxyDialer{
		proxyURL: &url.URL{Scheme: scheme, Host: "localhost"}, rootCAs: roots,
		forwardDial: func(ctx context.Context, network, address string) (net.Conn, error) {
			if stage == "TCP" {
				close(reached)
				<-ctx.Done()
				return nil, ctx.Err()
			}
			return tracked, nil
		},
	}
	ctx, cancel := context.WithCancel(t.Context())
	switch trigger {
	case "caller deadline":
		cancel()
		ctx, cancel = context.WithTimeout(t.Context(), 200*time.Millisecond)
	case "setup timeout":
		dialer.setupTimeout = 200 * time.Millisecond
	}
	defer cancel()
	consumer := &konnectivityProxy{connectDirectlyToCloudAPIs: true}
	consumer.log = logr.Discard()
	consumer.httpDialerOnce.Do(func() { consumer.httpDialer = dialer })
	finished := make(chan error, 1)
	dialDone := make(chan struct{})
	t.Cleanup(func() {
		cancel()
		client.Close()
		server.Close()
		select {
		case <-dialDone:
		case <-time.After(5 * time.Second):
			t.Error("dial goroutine did not finish")
		}
	})
	go func() {
		defer close(dialDone)
		conn, err := consumer.DialContext(ctx, "tcp", proxyTestAuthority)
		if conn != nil {
			conn.Close()
			finished <- errors.New("stalled setup returned a connection")
			return
		}
		finished <- err
	}()
	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		t.Fatal("setup did not reach stall")
	}
	if trigger == "caller cancellation" {
		cancel()
	}
	select {
	case err := <-finished:
		var timeout net.Error
		if trigger == "caller cancellation" {
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("expected cancellation, got %v", err)
			}
		} else if !errors.Is(err, context.DeadlineExceeded) && !(errors.As(err, &timeout) && timeout.Timeout()) {
			t.Fatalf("expected timeout, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("setup ignored cancellation or timeout")
	}
	if stage != "TCP" && !tracked.closed.Load() {
		t.Fatal("failed setup did not close raw connection")
	}
	if stage == "CONNECT response" {
		select {
		case err := <-serverDone:
			if err != nil {
				t.Fatalf("failed setup did not close connection: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("server remained connected after setup failure")
		}
	}
}

func runProxySetupSuccess(t *testing.T, scheme string) {
	t.Helper()
	certificate, caPEM := proxyTestCertificate(t, false)
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		t.Fatal("cannot load proxy root")
	}
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	proxyURL := &url.URL{Scheme: scheme, Host: listener.Addr().String()}
	serverDone := make(chan error, 1)
	serverExited := make(chan struct{})
	t.Cleanup(func() {
		listener.Close()
		select {
		case <-serverExited:
		case <-time.After(6 * time.Second):
			t.Error("server goroutine did not finish")
		}
	})
	go func() {
		defer close(serverExited)
		raw, err := listener.Accept()
		if err != nil {
			serverDone <- err
			return
		}
		_, _, err = serveProxyTestConn(t.Context(), raw, certificate, proxyURL, "")
		serverDone <- err
	}()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	dialer := newHTTPDialer(proxyURL)
	dialer.rootCAs = roots
	dialer.setupTimeout = 100 * time.Millisecond
	conn, err := dialer.DialContext(ctx, "tcp", proxyTestAuthority)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cancel()
	<-time.After(150 * time.Millisecond)
	payload := []byte("tunnel remains usable after setup cancellation")
	finished := make(chan error, 1)
	go func() {
		if _, err := conn.Write(payload); err != nil {
			finished <- err
			return
		}
		echo := make([]byte, len(payload))
		_, err := io.ReadFull(conn, echo)
		if err == nil && !bytes.Equal(echo, payload) {
			err = errors.New("tunnel payload mismatch")
		}
		finished <- err
	}()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("established tunnel stalled")
	}
	conn.Close()
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not finish")
	}
}

func TestHttpProxyDialer_DialContext(t *testing.T) {
	for _, scheme := range []string{"http", "https"} {
		for _, stage := range []string{"TCP", "TLS", "CONNECT write", "CONNECT response"} {
			if scheme == "http" && stage == "TLS" {
				continue
			}
			for _, trigger := range []string{"caller deadline", "caller cancellation", "setup timeout"} {
				t.Run("When "+scheme+" "+stage+" stalls with "+trigger+", it should close setup promptly", func(t *testing.T) {
					runProxySetupStall(t, scheme, stage, trigger)
				})
			}
		}
		t.Run("When "+scheme+" CONNECT succeeds, it should detach cancellation and clear the setup deadline", func(t *testing.T) {
			runProxySetupSuccess(t, scheme)
		})
	}
	for _, testCase := range []struct {
		name, response string
		failDeadlineOn int
	}{
		{name: "When setting the setup deadline fails, it should close the connection", failDeadlineOn: 1},
		{name: "When clearing the setup deadline fails, it should close instead of returning a tunnel", response: "HTTP/1.1 200 Connection Established\r\n\r\n", failDeadlineOn: 2},
		{name: "When CONNECT returns a rejection without a reason phrase, it should close without panicking", response: "HTTP/1.1 407\r\nContent-Length: 0\r\n\r\n"},
		{name: "When CONNECT returns a malformed response, it should close the connection", response: "malformed\r\n\r\n"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			client, server := net.Pipe()
			tracked := &proxyCloseTrackingConn{Conn: &proxyDeadlineErrorConn{Conn: client, failOn: testCase.failDeadlineOn}}
			serverDone := make(chan struct{})
			t.Cleanup(func() {
				client.Close()
				server.Close()
				select {
				case <-serverDone:
				case <-time.After(5 * time.Second):
					t.Error("server goroutine did not finish")
				}
			})
			go func() {
				defer close(serverDone)
				defer server.Close()
				if err := server.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
					return
				}
				if _, err := http.ReadRequest(bufio.NewReader(server)); err != nil {
					return
				}
				if _, err := io.WriteString(server, testCase.response); err != nil {
					return
				}
				if _, err := io.Copy(io.Discard, server); err != nil {
					return
				}
			}()
			dialer := &httpProxyDialer{
				proxyURL:    &url.URL{Scheme: "http", Host: "localhost"},
				forwardDial: func(ctx context.Context, network, address string) (net.Conn, error) { return tracked, nil },
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			conn, err := dialer.DialContext(ctx, "tcp", proxyTestAuthority)
			if conn != nil || err == nil || !tracked.closed.Load() {
				t.Fatalf("setup failure must close and return no connection: %v", err)
			}
		})
	}
}

func TestHostPortNoPort(t *testing.T) {
	for _, testCase := range []struct{ name, input, address, host string }{
		{"When HTTPS omits the port, it should default to 443", "https://localhost", "localhost:443", "localhost"},
		{"When HTTP omits the port, it should default to 80", "http://localhost", "localhost:80", "localhost"},
		{"When HTTPS specifies a port, it should preserve it", "https://localhost:8443", "localhost:8443", "localhost"},
		{"When HTTP specifies a port, it should preserve it", "http://localhost:8080", "localhost:8080", "localhost"},
		{"When HTTPS uses IPv6, it should preserve brackets and default port", "https://[::1]", "[::1]:443", "[::1]"},
		{"When HTTP uses IPv6 and a port, it should preserve them", "http://[::1]:8080", "[::1]:8080", "[::1]"},
		{"When WSS omits the port, it should preserve the 443 default", "wss://localhost", "localhost:443", "localhost"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			proxyURL, err := url.Parse(testCase.input)
			if err != nil {
				t.Fatal(err)
			}
			address, host := hostPortNoPort(proxyURL)
			if address != testCase.address || host != testCase.host {
				t.Fatal("proxy host or port changed")
			}
		})
	}
}
