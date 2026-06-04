package konnectivityhttpsproxy

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/elazarl/goproxy"
	"golang.org/x/net/http/httpproxy"
)

func TestShouldDialDirectFunc(t *testing.T) {
	u := func(s string) *url.URL {
		u, err := url.Parse("https://" + s)
		if err != nil {
			panic(err)
		}
		return u
	}
	withoutScheme := func(s string) *url.URL {
		u, err := url.Parse("https://" + s)
		if err != nil {
			panic(err)
		}
		u.Scheme = ""
		return u
	}
	tests := []struct {
		name                       string
		connectDirectlyToCloudAPIs bool
		requestURL                 *url.URL
		proxyNotConfigured         bool
		expectedDialDirectly       bool
	}{
		{
			name:                       "cloud API",
			connectDirectlyToCloudAPIs: true,
			requestURL:                 u("cloudapi.com"),
			expectedDialDirectly:       true,
		},
		{
			name:                       "not cloud API, proxy configured",
			connectDirectlyToCloudAPIs: true,
			requestURL:                 u("example.com"),
			expectedDialDirectly:       false,
		},
		{
			name:                       "not cloud API, matches noProxy",
			connectDirectlyToCloudAPIs: true,
			requestURL:                 u("dontproxy.com"),
			expectedDialDirectly:       true,
		},
		{
			name:                       "not cloud API, no proxy configured",
			connectDirectlyToCloudAPIs: true,
			requestURL:                 u("example.com"),
			proxyNotConfigured:         true,
			expectedDialDirectly:       true,
		},
		{
			name:                       "not cloud API, url has no scheme",
			connectDirectlyToCloudAPIs: true,
			requestURL:                 withoutScheme("example.com"),
			expectedDialDirectly:       false,
		},
		{
			name:                       "do not connect directly to cloud APIs",
			connectDirectlyToCloudAPIs: false,
			requestURL:                 u("cloudapi.com"),
			expectedDialDirectly:       false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			isCloudAPI := func(s string) bool {
				return strings.HasSuffix(s, "cloudapi.com")
			}
			userProxyConfig := &httpproxy.Config{
				HTTPProxy:  "http://proxy.example.com:3128",
				HTTPSProxy: "https://proxy.example.com:3128",
				NoProxy:    "dontproxy.com",
			}
			proxyConfigFunc := userProxyConfig.ProxyFunc()
			userProxyFunc := func(u *url.URL) (*url.URL, error) {
				if tc.proxyNotConfigured {
					return nil, nil
				}
				return proxyConfigFunc(u)
			}
			g := NewGomegaWithT(t)
			f := shouldDialDirectFunc(tc.connectDirectlyToCloudAPIs, isCloudAPI, userProxyFunc)
			result, err := f(tc.requestURL)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(result).To(Equal(tc.expectedDialDirectly))
		})
	}
}

func TestDialDirectFunc(t *testing.T) {
	dialErr := errors.New("dial failed")

	tests := []struct {
		name      string
		dialCtx   func(ctx context.Context, network, addr string) (net.Conn, error)
		addr      func(t *testing.T) string
		expectErr error
	}{
		{
			name:    "When dialing with a valid listener it should connect successfully",
			dialCtx: (&net.Dialer{}).DialContext,
			addr: func(t *testing.T) string {
				listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatalf("failed to create listener: %v", err)
				}
				t.Cleanup(func() { listener.Close() })
				return listener.Addr().String()
			},
		},
		{
			name: "When the transport DialContext fails it should return an error",
			dialCtx: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return nil, dialErr
			},
			addr:      func(t *testing.T) string { return "127.0.0.1:1" },
			expectErr: dialErr,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			httpProxy := goproxy.NewProxyHttpServer()
			httpProxy.Tr = &http.Transport{
				DialContext: tc.dialCtx,
			}

			dialFn := dialDirectFunc(httpProxy)
			conn, err := dialFn(t.Context(), "tcp", tc.addr(t))

			if tc.expectErr != nil {
				g.Expect(err).To(MatchError(tc.expectErr))
				g.Expect(conn).To(BeNil())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(conn).NotTo(BeNil())
				conn.Close()
			}
		})
	}
}

func TestConnectDialFunc(t *testing.T) {
	lookupErr := errors.New("lookup failed")

	tests := []struct {
		name                string
		shouldDialDirect    bool
		shouldDialDirectErr error
		expectDialDirect    bool
		expectDialProxy     bool
		expectErr           error
	}{
		{
			name:             "When shouldDialDirect returns true it should dial directly with request context",
			shouldDialDirect: true,
			expectDialDirect: true,
		},
		{
			name:            "When shouldDialDirect returns false it should dial through proxy",
			expectDialProxy: true,
		},
		{
			name:                "When shouldDialDirect returns an error it should propagate the error",
			shouldDialDirectErr: lookupErr,
			expectErr:           lookupErr,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			type contextKey string
			reqCtx := context.WithValue(t.Context(), contextKey("test"), "value")
			req, err := http.NewRequestWithContext(reqCtx, http.MethodConnect, "https://example.com:443", nil)
			g.Expect(err).NotTo(HaveOccurred())

			directCalled := false
			proxyCalled := false
			var capturedCtx any

			dialDirectly := func(ctx context.Context, network, addr string) (net.Conn, error) {
				directCalled = true
				capturedCtx = ctx
				return nil, nil
			}
			dialThroughProxy := func(network, addr string) (net.Conn, error) {
				proxyCalled = true
				return nil, nil
			}
			shouldDialDirect := func(u *url.URL) (bool, error) {
				return tc.shouldDialDirect, tc.shouldDialDirectErr
			}

			f := connectDialFunc(shouldDialDirect, dialDirectly, dialThroughProxy)
			conn, err := f(req, "tcp", "example.com:443")

			if tc.expectErr != nil {
				g.Expect(err).To(MatchError(tc.expectErr))
				g.Expect(conn).To(BeNil())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
			g.Expect(directCalled).To(Equal(tc.expectDialDirect))
			g.Expect(proxyCalled).To(Equal(tc.expectDialProxy))
			if tc.expectDialDirect {
				g.Expect(capturedCtx).To(Equal(reqCtx))
				g.Expect(capturedCtx.(context.Context).Value(contextKey("test"))).To(Equal("value"))
			}
		})
	}
}

func TestAddBasicAuthHeader(t *testing.T) {
	userInfo := url.UserPassword("user", "password")
	tests := []struct {
		name           string
		userInfo       *url.Userinfo
		expectedHeader string
	}{
		{
			name:           "no userinfo",
			userInfo:       nil,
			expectedHeader: "",
		},
		{
			name:           "userinfo present",
			userInfo:       userInfo,
			expectedHeader: fmt.Sprintf("Basic %s", base64.StdEncoding.EncodeToString([]byte(userInfo.String()))),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			f := addBasicAuthHeader(tc.userInfo)
			req := &http.Request{
				Header: http.Header{},
			}
			f(req)
			value := req.Header.Get("Proxy-Authorization")
			g.Expect(value).To(Equal(tc.expectedHeader))
		})
	}
}
