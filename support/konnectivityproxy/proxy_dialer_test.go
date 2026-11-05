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
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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
	closed bool
}

func (conn *proxyCloseTrackingConn) Close() error {
	conn.closed = true
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
	if mode := os.Getenv("HYPERSHIFT_PROXY_TEST_MODE"); mode != "" {
		runProxyDialContextChild(t, mode)
		return
	}
	for _, testCase := range []struct {
		name, scheme, host, user, mode string
		missingCA, wrongHost           bool
	}{
		{name: "When the HTTPS proxy has a trusted IP certificate, it should encrypt CONNECT and tunnel traffic", scheme: "https", host: "127.0.0.1", user: "synthetic-user:synthetic-password", mode: "success"},
		{name: "When the HTTPS proxy has a trusted DNS certificate, it should verify the proxy hostname", scheme: "https", host: "localhost", user: "synthetic-user:synthetic-password", mode: "success"},
		{name: "When the proxy CA is missing from system roots, it should fail closed", scheme: "https", host: "127.0.0.1", user: "synthetic-user:synthetic-password", mode: "untrusted", missingCA: true},
		{name: "When the trusted proxy certificate has the wrong hostname, it should fail closed", scheme: "https", host: "localhost", user: "synthetic-user:synthetic-password", mode: "hostname", wrongHost: true},
		{name: "When the proxy uses HTTP, it should preserve CONNECT authentication", scheme: "http", host: "127.0.0.1", user: "synthetic-user:synthetic-password", mode: "success"},
		{name: "When the HTTP proxy has no userinfo, it should omit authentication", scheme: "http", host: "127.0.0.1", mode: "success"},
		{name: "When the HTTP proxy has no password, it should preserve omitted authentication", scheme: "http", host: "127.0.0.1", user: "synthetic-user", mode: "success"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			certificate, caPEM := proxyTestCertificate(t, testCase.wrongHost)
			caDir := t.TempDir()
			caFile := filepath.Join(caDir, "additional-ca-bundle.pem")
			if err := os.WriteFile(caFile, caPEM, 0600); err != nil {
				t.Fatal(err)
			}
			rootDir := caDir
			if testCase.missingCA {
				rootDir = t.TempDir()
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
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestKonnectivityProxy_DialContext$")
			command.Env = []string{
				"HYPERSHIFT_PROXY_TEST_MODE=" + testCase.mode,
				"HYPERSHIFT_PROXY_TEST_CA=" + caFile,
				"HTTPS_PROXY=" + proxyURL.String(), "NO_PROXY=*",
				"SSL_CERT_DIR=" + rootDir, "SSL_CERT_FILE=" + filepath.Join(t.TempDir(), "missing.pem"),
			}
			output, childErr := command.CombinedOutput()
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
			if childErr != nil {
				t.Fatalf("proxy subprocess failed: %v\n%s", childErr, output)
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

func runProxyDialContextChild(t *testing.T, mode string) {
	t.Helper()
	caPEM, err := os.ReadFile(os.Getenv("HYPERSHIFT_PROXY_TEST_CA"))
	if err != nil {
		t.Fatalf("cannot load synthetic konnectivity CA: %v", err)
	}
	dialer := &konnectivityProxy{
		connectDirectlyToCloudAPIs: true,
		ca:                         caPEM, clientCert: []byte("unused synthetic client certificate"), clientKey: []byte("unused synthetic client key"),
	}
	dialer.log = logr.Discard()
	conn, err := dialer.DialContext(t.Context(), "tcp", proxyTestAuthority)
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
		dialer := &httpProxyDialer{proxyURL: proxyURL, forwardDial: func(network, address string) (net.Conn, error) {
			conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(t.Context(), network, address)
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
		if conn != nil || !errors.As(err, &unknownCA) || tracked == nil || !tracked.closed {
			t.Fatal("TLS verification failure did not close the underlying connection before returning")
		}
	})
	t.Run("When TCP dialing fails, it should return the error without retrying", func(t *testing.T) {
		failure := errors.New("synthetic dial failure")
		calls := 0
		dialer := &httpProxyDialer{proxyURL: &url.URL{Scheme: "https", Host: "localhost"}, forwardDial: func(network, address string) (net.Conn, error) {
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
