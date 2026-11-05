package konnectivityproxy

import (
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func managementTestIP(t *testing.T) string {
	t.Helper()
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range addresses {
		if network, ok := address.(*net.IPNet); ok && network.IP.To4() != nil && network.IP.IsGlobalUnicast() {
			return network.IP.String()
		}
	}
	t.Fatal("management socket tests require a local nonloopback IPv4 address")
	return ""
}

func exchangeManagementEcho(dialer *konnectivityProxy, address string) error {
	connection, err := dialer.dialDirectWithProxy("tcp", address)
	if err != nil {
		return err
	}
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	message := []byte("management-egress")
	if _, err := connection.Write(message); err != nil {
		return err
	}
	response := make([]byte, len(message))
	if _, err := io.ReadFull(connection, response); err != nil {
		return err
	}
	if string(response) != string(message) {
		return fmt.Errorf("unexpected echo payload")
	}
	return nil
}

func testManagementProxyRoutes(t *testing.T) {
	t.Helper()
	tests := []struct {
		name        string
		sequence    []bool
		unsetProxy  bool
		emptyBypass bool
		wildcard    bool
		changeEnv   bool
		concurrent  bool
	}{
		{name: "When NO_PROXY is empty, it should use the proxy", sequence: []bool{true}, emptyBypass: true},
		{name: "When HTTPS_PROXY is unset, it should dial both targets directly", sequence: []bool{false, true}, unsetProxy: true},
		{name: "When the destination matches NO_PROXY, it should use direct TCP", sequence: []bool{false}},
		{name: "When the destination does not match NO_PROXY, it should CONNECT", sequence: []bool{true}},
		{name: "When NO_PROXY is wildcard, it should use direct TCP for both targets", sequence: []bool{false, true}, wildcard: true},
		{name: "When targets alternate direct proxy direct, it should select each independently", sequence: []bool{false, true, false}},
		{name: "When targets alternate proxy direct proxy, it should select each independently", sequence: []bool{true, false, true}},
		{name: "When environment changes after direct first use, it should retain its snapshot", sequence: []bool{false, true, false}, changeEnv: true},
		{name: "When environment changes after proxy first use, it should retain its snapshot", sequence: []bool{true, false, true}, changeEnv: true},
		{name: "When proxy is initially unset, it should retain direct routing after environment changes", sequence: []bool{false, true}, unsetProxy: true, changeEnv: true},
		{name: "When mixed targets initialize and dial concurrently, it should keep routes independent", sequence: []bool{false, true, false, true}, concurrent: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var directCount, proxiedCount, connectCount atomic.Int32
			listenAddress := net.JoinHostPort(managementTestIP(t), "0")
			bypassTarget := startCountedTCPEchoServer(t, listenAddress, &directCount)
			proxyTarget := startCountedTCPEchoServer(t, listenAddress, &directCount)
			proxyBackend := startCountedTCPEchoServer(t, "127.0.0.1:0", &proxiedCount)
			proxyListener := startConnectProxy(t, &connectCount, proxyBackend.Addr().String())
			proxyURL := "http://" + proxyListener.Addr().String()
			t.Setenv("HTTPS_PROXY", proxyURL)
			if test.unsetProxy {
				t.Setenv("HTTPS_PROXY", "")
			}
			bypass := bypassTarget.Addr().String()
			if test.emptyBypass {
				bypass = ""
			}
			if test.wildcard {
				bypass = "*"
			}
			t.Setenv("NO_PROXY", bypass)
			t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
			t.Setenv("ALL_PROXY", "http://127.0.0.1:1")
			t.Setenv("https_proxy", "http://127.0.0.1:1")
			t.Setenv("no_proxy", "*")
			dialer := &konnectivityProxy{}
			var wantProxied, wantDirect int32
			rounds := 1
			if test.concurrent {
				rounds = 2
			}
			for range rounds {
				start := make(chan struct{})
				results := make(chan error, len(test.sequence))
				for index, useProxy := range test.sequence {
					address := bypassTarget.Addr().String()
					if useProxy {
						address = proxyTarget.Addr().String()
					}
					if !test.unsetProxy && !test.wildcard && (useProxy || test.emptyBypass) {
						wantProxied++
					} else {
						wantDirect++
					}
					if test.concurrent {
						go func() { <-start; results <- exchangeManagementEcho(dialer, address) }()
						continue
					}
					if err := exchangeManagementEcho(dialer, address); err != nil {
						t.Fatal(err)
					}
					if test.changeEnv && index == 0 {
						t.Setenv("NO_PROXY", "*")
						t.Setenv("HTTPS_PROXY", "")
						if test.unsetProxy {
							t.Setenv("HTTPS_PROXY", proxyURL)
						}
					}
					assertManagementRoutes(t, &directCount, &proxiedCount, &connectCount, wantDirect, wantProxied)
				}
				close(start)
				if test.concurrent {
					for range test.sequence {
						if err := <-results; err != nil {
							t.Error(err)
						}
					}
				}
				assertManagementRoutes(t, &directCount, &proxiedCount, &connectCount, wantDirect, wantProxied)
			}
			if test.changeEnv {
				if err := exchangeManagementEcho(&konnectivityProxy{}, proxyTarget.Addr().String()); err != nil {
					t.Fatal(err)
				}
				assertManagementRoutes(t, &directCount, &proxiedCount, &connectCount, wantDirect+1, wantProxied)
			}
		})
	}
}

func testManagementProxySelector(t *testing.T) {
	t.Helper()
	tests := []struct {
		name      string
		noProxy   string
		address   string
		wantProxy bool
	}{
		{name: "When the hostname matches, it should bypass", noProxy: "login.microsoftonline.com", address: "login.microsoftonline.com:443"},
		{name: "When the hostname differs, it should proxy", noProxy: "other.microsoftonline.com", address: "login.microsoftonline.com:443", wantProxy: true},
		{name: "When the domain matches its apex, it should bypass", noProxy: "microsoftonline.com", address: "microsoftonline.com:443"},
		{name: "When the domain matches a subdomain, it should bypass", noProxy: "microsoftonline.com", address: "login.microsoftonline.com:443"},
		{name: "When a suffix lacks a label boundary, it should proxy", noProxy: "microsoftonline.com", address: "notmicrosoftonline.com:443", wantProxy: true},
		{name: "When a leading-dot domain matches a subdomain, it should bypass", noProxy: ".microsoftonline.com", address: "login.microsoftonline.com:443"},
		{name: "When a leading-dot domain sees its apex, it should proxy", noProxy: ".microsoftonline.com", address: "microsoftonline.com:443", wantProxy: true},
		{name: "When hostname case differs, it should bypass", noProxy: "LOGIN.microsoftonline.com", address: "login.microsoftonline.com:443"},
		{name: "When an IPv4 address matches, it should bypass", noProxy: "192.0.2.10", address: "192.0.2.10:443"},
		{name: "When an IPv4 address differs, it should proxy", noProxy: "192.0.2.10", address: "192.0.2.11:443", wantProxy: true},
		{name: "When an IPv4 CIDR contains the target, it should bypass", noProxy: "192.0.2.0/24", address: "192.0.2.10:443"},
		{name: "When an IPv4 CIDR excludes the target, it should proxy", noProxy: "192.0.2.0/24", address: "198.51.100.10:443", wantProxy: true},
		{name: "When the hostname port matches, it should bypass", noProxy: "login.microsoftonline.com:443", address: "login.microsoftonline.com:443"},
		{name: "When the hostname port differs, it should proxy", noProxy: "login.microsoftonline.com:443", address: "login.microsoftonline.com:8443", wantProxy: true},
		{name: "When the IPv4 port matches, it should bypass", noProxy: "192.0.2.10:443", address: "192.0.2.10:443"},
		{name: "When the IPv4 port differs, it should proxy", noProxy: "192.0.2.10:443", address: "192.0.2.10:8443", wantProxy: true},
		{name: "When wildcard bypass matches a hostname, it should bypass", noProxy: "*", address: "ec2.amazonaws.com:443"},
		{name: "When wildcard bypass matches an IP, it should bypass", noProxy: "*", address: "198.51.100.10:443"},
		{name: "When an IPv6 address matches, it should bypass", noProxy: "2001:db8::10", address: "[2001:db8::10]:443"},
		{name: "When an IPv6 address differs, it should proxy", noProxy: "2001:db8::10", address: "[2001:db8::11]:443", wantProxy: true},
		{name: "When a bracketed IPv6 port matches, it should bypass", noProxy: "[2001:db8::10]:443", address: "[2001:db8::10]:443"},
		{name: "When a bracketed IPv6 port differs, it should proxy", noProxy: "[2001:db8::10]:443", address: "[2001:db8::10]:8443", wantProxy: true},
		{name: "When an IPv6 CIDR contains the target, it should bypass", noProxy: "2001:db8::/32", address: "[2001:db8::10]:443"},
		{name: "When an IPv6 CIDR excludes the target, it should proxy", noProxy: "2001:db8::/32", address: "[2001:db9::10]:443", wantProxy: true},
		{name: "When bypass is empty, it should proxy", address: "login.microsoftonline.com:443", wantProxy: true},
		{name: "When localhost is requested, it should bypass", address: "localhost:443"},
		{name: "When IPv4 loopback is requested, it should bypass", address: "127.0.0.1:443"},
		{name: "When IPv6 loopback is requested, it should bypass", address: "[::1]:443"},
	}
	echo := startTCPEchoServer(t)
	var connects atomic.Int32
	proxyListener := startConnectProxy(t, &connects)
	proxyURL := "http://" + proxyListener.Addr().String()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("HTTPS_PROXY", proxyURL)
			t.Setenv("NO_PROXY", test.noProxy)
			t.Setenv("https_proxy", "http://127.0.0.1:1")
			t.Setenv("no_proxy", "*")
			dialer := &konnectivityProxy{}
			if err := exchangeManagementEcho(dialer, echo.Addr().String()); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HTTPS_PROXY", "")
			t.Setenv("NO_PROXY", "*")
			selected, err := dialer.httpProxyFunc(&url.URL{Scheme: "https", Host: test.address})
			if err != nil {
				t.Fatal(err)
			}
			if (selected != nil) != test.wantProxy {
				t.Fatalf("selected proxy = %t, want %t", selected != nil, test.wantProxy)
			}
			if selected != nil && selected.String() != proxyURL {
				t.Error("selector did not preserve the configured management proxy")
			}
		})
	}
}

func testManagementProxyInvalidConfig(t *testing.T) {
	t.Helper()
	echo := startTCPEchoServer(t)
	t.Setenv("HTTPS_PROXY", "http://test-user:test-password@proxy.invalid:%zz")
	t.Setenv("NO_PROXY", "")
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reader.Close(); writer.Close() })
	originalStderr := os.Stderr
	os.Stderr = writer
	t.Cleanup(func() { os.Stderr = originalStderr })
	dialErr := exchangeManagementEcho(&konnectivityProxy{}, echo.Addr().String())
	os.Stderr = originalStderr
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if dialErr != nil {
		t.Fatal("invalid proxy configuration should fall back to direct dialing")
	}
	if strings.Contains(string(output), "test-user") || strings.Contains(string(output), "test-password") {
		t.Error("proxy diagnostic exposed userinfo")
	}
}

func assertManagementRoutes(t *testing.T, direct, proxied, connects *atomic.Int32, wantDirect, wantProxied int32) {
	t.Helper()
	if got := direct.Load(); got != wantDirect {
		t.Errorf("direct target accepts = %d, want %d", got, wantDirect)
	}
	if got := proxied.Load(); got != wantProxied {
		t.Errorf("proxied target accepts = %d, want %d", got, wantProxied)
	}
	if got := connects.Load(); got != wantProxied {
		t.Errorf("CONNECT count = %d, want %d", got, wantProxied)
	}
}
