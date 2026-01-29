package konnectivityhttpsproxy

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

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
