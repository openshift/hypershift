package kubernetesdefaultproxy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/go-logr/logr"
)

func TestValidate(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		input string
		want  string
	}{
		{input: "fd00::1:6443", want: "[fd00::1]:6443"},
		{input: "2001:db8::2:3128", want: "[2001:db8::2]:3128"},
		{input: "::ffff:192.0.2.1:443", want: "[::ffff:192.0.2.1]:443"},
		{input: "fe80::1%eth0:6443", want: "[fe80::1%eth0]:6443"},
		{input: "127.0.0.1:6443", want: "127.0.0.1:6443"},
		{input: "127.0.0.1:+80", want: "127.0.0.1:+80"},
		{input: "[::1]:-0", want: "[::1]:-0"},
		{input: "api.openshift.test:https", want: "api.openshift.test:https"},
		{input: "[FD00::0001]:06443", want: "[FD00::0001]:06443"},
		{input: "[::ffff:192.0.2.1]:https", want: "[::ffff:192.0.2.1]:https"},
		{input: "[fe80::1%eth0]:6443", want: "[fe80::1%eth0]:6443"},
		{input: "[fe80::1%25eth0]:https", want: "[fe80::1%25eth0]:https"},
		{input: ":0", want: ":0"},
		{input: "0.0.0.0:0", want: "0.0.0.0:0"},
		{input: "[::]:0", want: "[::]:0"},
		{input: "::1:00080", want: "[::1]:00080"},
		{input: "::1:65535", want: "[::1]:65535"},
		{input: "fd00::1"},
		{input: "::1"},
		{input: "127.0.0.1"},
		{input: "api.openshift.test"},
		{input: "[::1]"},
		{input: "[::1]:"},
		{input: "::1:"},
		{input: "[[::1]]:6443"},
		{input: "[::1:6443"},
		{input: "::1]:6443"},
		{input: "[not:ipv6]:6443"},
		{input: "api:extra:6443"},
		{input: "::1:https"},
		{input: "::1:-1"},
		{input: "127.0.0.1:-1"},
		{input: "[::1]:-1"},
		{input: "::1:+80"},
		{input: "::1:65536"},
		{input: "127.0.0.1:65536"},
		{input: "127.0.0.1:invalid-service"},
		{input: "http://[::1]:6443"},
		{input: "https://127.0.0.1:6443"},
		{input: "secret-user:secret-password@proxy:3128"},
		{input: "proxy:3128/path"},
		{input: "proxy:3128?query"},
		{input: "proxy:3128#fragment"},
		{input: "proxy\n:3128"},
	}
	for _, flag := range []string{"listen-addr", "proxy-addr", "apiserver-addr"} {
		for _, testCase := range testCases {
			t.Run(fmt.Sprintf("When --%s is %q, it should validate its TCP authority", flag, testCase.input), func(t *testing.T) {
				t.Parallel()
				proxy := &server{listenAddr: "127.0.0.1:0", proxyAddr: "127.0.0.1:3128", apiServerAddr: "api.openshift.test:6443"}
				fields := map[string]*string{"listen-addr": &proxy.listenAddr, "proxy-addr": &proxy.proxyAddr, "apiserver-addr": &proxy.apiServerAddr}
				*fields[flag] = testCase.input
				before := *proxy
				err := proxy.validate(t.Context())
				if testCase.want == "" {
					if err == nil || !strings.Contains(err.Error(), "--"+flag) {
						t.Fatalf("expected flag-specific validation error, got %v", err)
					}
					if strings.Contains(err.Error(), testCase.input) {
						t.Fatalf("validation error exposes rejected input: %v", err)
					}
					if proxy.listenAddr != before.listenAddr || proxy.proxyAddr != before.proxyAddr || proxy.apiServerAddr != before.apiServerAddr {
						t.Fatal("failed validation mutated configuration")
					}
					return
				}
				if err != nil || *fields[flag] != testCase.want {
					t.Fatalf("got %q, error %v; want %q", *fields[flag], err, testCase.want)
				}
				if err := proxy.validate(t.Context()); err != nil || *fields[flag] != testCase.want {
					t.Fatalf("validation is not idempotent: %q, %v", *fields[flag], err)
				}
			})
		}
	}
	t.Run("When required fields are missing, it should aggregate all mandatory errors", func(t *testing.T) {
		proxy := &server{}
		err := proxy.validate(t.Context())
		if err == nil {
			t.Fatal("expected validation error")
		}
		for _, flag := range []string{"listen-addr", "proxy-addr", "apiserver-addr"} {
			if !strings.Contains(err.Error(), "--"+flag+" is mandatory") {
				t.Fatalf("missing mandatory error for %s: %v", flag, err)
			}
		}
	})
	t.Run("When one field is invalid, it should not partially normalize other fields", func(t *testing.T) {
		proxy := &server{listenAddr: "fd00::1:6443", proxyAddr: "http://secret@proxy:3128"}
		before := *proxy
		err := proxy.validate(t.Context())
		if err == nil || !strings.Contains(err.Error(), "--proxy-addr") || !strings.Contains(err.Error(), "--apiserver-addr is mandatory") {
			t.Fatalf("expected aggregated errors, got %v", err)
		}
		if proxy.listenAddr != before.listenAddr || proxy.proxyAddr != before.proxyAddr || proxy.apiServerAddr != before.apiServerAddr {
			t.Fatal("failed validation partially normalized configuration")
		}
	})
	t.Run("When a legacy listen address is normalized, it should be accepted by ListenConfig", func(t *testing.T) {
		proxy := &server{listenAddr: "::1:0", proxyAddr: "proxy.openshift.test:3128", apiServerAddr: "fd00::1:6443"}
		if err := proxy.validate(t.Context()); err != nil {
			t.Fatal(err)
		}
		if _, err := net.ResolveTCPAddr("tcp6", proxy.listenAddr); err != nil {
			t.Fatalf("normalized listener cannot be parsed: %v", err)
		}
		listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp6", proxy.listenAddr)
		if errors.Is(err, syscall.EAFNOSUPPORT) || errors.Is(err, syscall.EADDRNOTAVAIL) || errors.Is(err, syscall.EPROTONOSUPPORT) {
			t.Skipf("IPv6 loopback unavailable: %v", err)
		}
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
	})
}

func TestNewStartCommand(t *testing.T) {
	t.Parallel()
	command := NewStartCommand()
	command.SilenceErrors = true
	command.SilenceUsage = true
	command.SetArgs([]string{"--listen-addr=127.0.0.1:0", "--proxy-addr=http://secret@proxy:3128", "--apiserver-addr=fd00::1:6443"})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := command.ExecuteContext(ctx)
	if err == nil || !strings.Contains(err.Error(), "--proxy-addr") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("expected sanitized validation error before run, got %v", err)
	}
}

func TestHandleConnection(t *testing.T) {
	t.Parallel()
	for _, network := range []string{"tcp4", "tcp6"} {
		t.Run("When a legacy IPv6 target uses "+network+", it should dial the backend and send bracketed CONNECT authorities", func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			address := "127.0.0.1:0"
			if network == "tcp6" {
				address = "[::1]:0"
			}
			listener, err := (&net.ListenConfig{}).Listen(ctx, network, address)
			if network == "tcp6" && (errors.Is(err, syscall.EAFNOSUPPORT) || errors.Is(err, syscall.EADDRNOTAVAIL) || errors.Is(err, syscall.EPROTONOSUPPORT)) {
				t.Skipf("IPv6 loopback unavailable: %v", err)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			proxyAddress := listener.Addr().String()
			if network == "tcp6" {
				host, port, err := net.SplitHostPort(proxyAddress)
				if err != nil {
					t.Fatal(err)
				}
				proxyAddress = host + ":" + port
			}
			proxy := &server{listenAddr: "fd00::1:6443", proxyAddr: proxyAddress, apiServerAddr: "2001:db8::2:6443", log: logr.Discard()}
			if err := proxy.validate(ctx); err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			go func() {
				backend, err := listener.Accept()
				if err != nil {
					result <- err
					return
				}
				defer backend.Close()
				deadline, _ := ctx.Deadline()
				if err := backend.SetDeadline(deadline); err != nil {
					result <- err
					return
				}
				reader := bufio.NewReader(backend)
				request, err := http.ReadRequest(reader)
				if err != nil {
					result <- err
					return
				}
				if request.Method != http.MethodConnect || request.RequestURI != "[2001:db8::2]:6443" || request.Host != "[2001:db8::2]:6443" {
					result <- fmt.Errorf("unexpected CONNECT wire request: method=%q target=%q Host=%q", request.Method, request.RequestURI, request.Host)
					return
				}
				if _, err := io.WriteString(backend, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
					result <- err
					return
				}
				payload := make([]byte, 4)
				if _, err := io.ReadFull(reader, payload); err != nil {
					result <- err
					return
				}
				if string(payload) != "ping" {
					result <- fmt.Errorf("unexpected tunnel payload %q", payload)
					return
				}
				_, err = backend.Write(payload)
				result <- err
			}()
			client, connection := net.Pipe()
			defer client.Close()
			deadline, _ := ctx.Deadline()
			if err := client.SetDeadline(deadline); err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() {
				proxy.handleConnection(ctx, connection)
				close(done)
			}()
			if _, err := io.WriteString(client, "ping"); err != nil {
				t.Fatal(err)
			}
			payload := make([]byte, 4)
			if _, err := io.ReadFull(client, payload); err != nil || string(payload) != "ping" {
				t.Fatalf("tunnel echo = %q, error %v", payload, err)
			}
			select {
			case err := <-result:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if err := client.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-done:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		})
	}
}
